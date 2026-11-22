// Command tmp 启动 TiDB 管理平台（TMP）：单二进制同时提供 REST API 与内嵌前端。
//
// 装配顺序：配置 → 日志 → 元数据库（迁移 + 内置数据）→ 主密钥 → 仓储层 →
// 领域服务 → 任务引擎（含启动恢复）→ HTTP 路由 → 优雅退出。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/tmp-platform/backend/internal/api"
	"github.com/tmp-platform/backend/internal/auth"
	"github.com/tmp-platform/backend/internal/config"
	"github.com/tmp-platform/backend/internal/crypto"
	"github.com/tmp-platform/backend/internal/db"
	"github.com/tmp-platform/backend/internal/job"
	"github.com/tmp-platform/backend/internal/pkg/logger"
	"github.com/tmp-platform/backend/internal/repo"
	"github.com/tmp-platform/backend/internal/service"
	"github.com/tmp-platform/backend/web"
)

// 默认参数。
const (
	// defaultWorkers 任务引擎 worker 数。
	defaultWorkers = 4
	// defaultJWTTH 默认 token 有效期（小时），可被 settings.jwt_ttl_hours 覆盖。
	defaultJWTTTLHours = 8
	// shutdownTimeout 优雅退出等待时间。
	shutdownTimeout = 15 * time.Second
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "TMP 启动失败: %v\n", err)
		os.Exit(1)
	}
}

// run 完成全部装配并阻塞至收到退出信号。
func run() error {
	cfg := config.Load()
	log := logger.Init(cfg.LogLevel, config.Version)
	slog.SetDefault(log)

	log.Info("TMP 启动中",
		"version", config.Version, "addr", cfg.Addr,
		"db", cfg.DBPath, "dataDir", cfg.DataDir,
		"mockForced", cfg.Mock, "ginMode", cfg.GinMode)

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return fmt.Errorf("创建数据目录失败: %w", err)
	}

	// ---------- 元数据库 ----------
	gdb, err := db.Open(cfg.DBPath, log)
	if err != nil {
		return fmt.Errorf("打开元数据库失败: %w", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return fmt.Errorf("获取数据库连接失败: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()

	bootCtx, cancelBoot := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelBoot()

	if err := db.Migrate(gdb); err != nil {
		return fmt.Errorf("执行数据库迁移失败: %w", err)
	}
	if err := db.Seed(bootCtx, gdb); err != nil {
		return fmt.Errorf("灌入内置数据失败: %w", err)
	}
	log.Info("元数据库就绪", "tables", len(db.Models()))

	// ---------- 主密钥 / JWT ----------
	cipher, generated, err := crypto.Load(cfg.Secret, cfg.DataDir)
	if err != nil {
		return fmt.Errorf("加载主密钥失败: %w", err)
	}
	if generated {
		log.Warn("已生成新的主密钥文件，请妥善备份；若丢失，已加密凭据将无法解密",
			"path", filepath.Join(cfg.DataDir, "secret.key"))
	}
	jwtSecret, err := resolveJWTSecret(cfg)
	if err != nil {
		return err
	}

	// ---------- 仓储层 ----------
	userRepo := repo.NewUserRepo(gdb)
	auditRepo := repo.NewAuditRepo(gdb)
	settingRepo := repo.NewSettingRepo(gdb)
	clusterRepo := repo.NewClusterRepo(gdb)
	instanceRepo := repo.NewInstanceRepo(gdb)
	hostRepo := repo.NewHostRepo(gdb)
	credRepo := repo.NewCredentialRepo(gdb)
	jobRepo := repo.NewJobRepo(gdb)
	tidbRepo := repo.NewTiDBObjRepo(gdb)
	inspectRepo := repo.NewInspectRepo(gdb)

	jwtMgr := auth.NewManager(jwtSecret, jwtTTL(bootCtx, settingRepo, log))

	// ---------- 领域服务 ----------
	mode := service.NewModeManager(cfg.Mock)
	if err := mode.LoadFromSettings(bootCtx, settingRepo); err != nil {
		log.Warn("读取采集模式配置失败，沿用默认值", "error", err)
	}
	log.Info("采集模式已确定", "mock", mode.IsMock(), "forced", mode.ForcedMock())

	settingSvc := service.NewSettingService(settingRepo, mode, cfg)
	auditSvc := service.NewAuditService(auditRepo)
	authSvc := service.NewAuthService(userRepo, settingRepo, jwtMgr)
	userSvc := service.NewUserService(userRepo)
	clusterSvc := service.NewClusterService(service.ClusterDeps{
		Clusters: clusterRepo, Instances: instanceRepo, Hosts: hostRepo,
		Creds: credRepo, Jobs: jobRepo, Audits: auditRepo,
		Settings: settingSvc, Mode: mode, Cipher: cipher, TiDBObj: tidbRepo,
		// 连接测试凭证与 JWT 同源派生（TMP_SECRET 或 secret.key），重启后旧凭证仍可校验。
		ProbeTokens: service.NewProbeTokenSigner(jwtSecret),
	})
	hostSvc := service.NewHostService(hostRepo, instanceRepo, clusterRepo)
	tidbObjSvc := service.NewTiDBObjService(clusterRepo, credRepo, tidbRepo, cipher, mode)
	inspectSvc := service.NewInspectService(service.InspectDeps{
		Repo: inspectRepo, Clusters: clusterRepo, Creds: credRepo,
		Cipher: cipher, Settings: settingSvc, Mode: mode,
	})
	credSvc := service.NewCredentialService(credRepo, clusterRepo, cipher, mode)
	jobSvc := service.NewJobService(jobRepo)
	overviewSvc := service.NewOverviewService(clusterRepo, hostRepo, jobRepo, inspectRepo, mode)

	// ---------- 任务引擎 ----------
	engine := job.NewEngine(job.Deps{
		Repo: jobRepo, Clusters: clusterRepo, Instances: instanceRepo, Hosts: hostRepo,
		Creds: credRepo, TiDBObj: tidbRepo, Settings: settingRepo, Inspect: inspectRepo,
		Cipher: cipher, Collector: mode.Current, IsMock: mode.IsMock, Log: log,
	}, defaultWorkers)
	// 端口后注入，打破 service ↔ job 的构造期循环依赖。
	// 统一走 EngineBindable 列表 + 自检：漏掉任何一个持有 engine 的服务，
	// 都会让对应动作在运行时拿到 nil engine（Kill 会话曾因此 500）。
	engine.SetSink(clusterSvc)
	engine.SetInspector(inspectSvc)
	for _, svc := range []service.EngineBindable{
		clusterSvc, inspectSvc, hostSvc, tidbObjSvc, jobSvc,
	} {
		svc.SetEngine(engine)
		if !svc.EngineBound() {
			return fmt.Errorf("服务 %T 注入任务引擎失败", svc)
		}
	}

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()
	engine.Start(runCtx)

	// 启动恢复三件事：running→interrupted、pending 重新入队、clusters.collecting 归零。
	if err := job.Recover(runCtx, jobRepo, clusterRepo, engine, log); err != nil {
		log.Warn("任务恢复未完全成功", "error", err)
	}

	// ---------- 首启初始化 ----------
	if created, err := authSvc.EnsureDefaultAdmin(bootCtx); err != nil {
		return fmt.Errorf("初始化默认管理员失败: %w", err)
	} else if created {
		log.Warn("已创建默认管理员账号，请首次登录后立即修改密码",
			"username", service.DefaultAdminUsername)
	}
	if ids, err := clusterSvc.SeedMockClusters(bootCtx); err != nil {
		log.Warn("演示集群初始化失败", "error", err)
	} else if len(ids) > 0 {
		log.Info("演示集群就绪", "clusters", len(ids))
	}

	// ---------- HTTP ----------
	handlers := &api.Handlers{
		Config: cfg, Mode: mode, Auth: authSvc, Users: userSvc,
		Clusters: clusterSvc, Hosts: hostSvc, TiDBObj: tidbObjSvc,
		Inspect: inspectSvc, Jobs: jobSvc, Settings: settingSvc,
		Creds: credSvc, Overview: overviewSvc, Audit: auditSvc,
		MockMode: mode.IsMock(),
	}
	router := api.NewRouter(api.RouterDeps{
		Handlers: handlers,
		Auth:     auth.NewMiddleware(jwtMgr, userRepo),
		Logger:   log,
		Assets:   web.Assets(),
		Ping:     func() error { return db.Ping(gdb) },
	})

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           router,
		ReadHeaderTimeout: 15 * time.Second,
		// 连通性测试接口最长 15s，读写超时留出余量。
		ReadTimeout:  60 * time.Second,
		WriteTimeout: 120 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("HTTP 服务已监听", "addr", cfg.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	// ---------- 退出信号 ----------
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigCh:
		log.Info("收到退出信号，开始优雅退出", "signal", sig.String())
	case err, ok := <-errCh:
		if ok && err != nil {
			cancelRun()
			return fmt.Errorf("HTTP 服务异常退出: %w", err)
		}
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancelShutdown()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Warn("HTTP 服务关闭超时", "error", err)
	}
	cancelRun()
	engine.Stop()
	log.Info("TMP 已退出")
	return nil
}

// resolveJWTSecret 解析 JWT 签名密钥：优先 TMP_SECRET，否则复用 data/secret.key 内容。
func resolveJWTSecret(cfg *config.Config) (string, error) {
	if strings.TrimSpace(cfg.Secret) != "" {
		return cfg.Secret, nil
	}
	keyPath := filepath.Join(cfg.DataDir, "secret.key")
	data, err := os.ReadFile(keyPath)
	if err != nil {
		return "", fmt.Errorf("读取 JWT 密钥失败: %w", err)
	}
	if len(data) == 0 {
		return "", errors.New("JWT 密钥文件为空")
	}
	return string(data), nil
}

// jwtTTL 从 settings 读取 token 有效期，缺省 8 小时。
func jwtTTL(ctx context.Context, settings *repo.SettingRepo, log *slog.Logger) time.Duration {
	raw, err := settings.Get(ctx, repo.SettingJWTTokenTTLHours)
	if err != nil {
		return defaultJWTTTLHours * time.Hour
	}
	hours, convErr := strconv.Atoi(strings.TrimSpace(raw))
	if convErr != nil || hours <= 0 || hours > 24*30 {
		log.Warn("jwt_ttl_hours 配置非法，使用默认值", "value", raw)
		return defaultJWTTTLHours * time.Hour
	}
	return time.Duration(hours) * time.Hour
}
