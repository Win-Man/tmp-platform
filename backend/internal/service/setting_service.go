package service

import (
	"context"
	"strconv"
	"time"

	"github.com/tmp-platform/backend/internal/config"
	"github.com/tmp-platform/backend/internal/inspect"
	"github.com/tmp-platform/backend/internal/pkg/apperr"
	"github.com/tmp-platform/backend/internal/repo"
)

// CollectionSettings 采集与平台参数配置（GET/PUT /settings/collection）。
type CollectionSettings struct {
	IntervalSec        int     `json:"intervalSec"`
	TimeoutSec         int     `json:"timeoutSec"`
	Concurrency        int     `json:"concurrency"`
	Retry              int     `json:"retry"`
	JWTTokenTTLHours   int     `json:"jwtTtlHours"`
	IdleTimeoutMin     int     `json:"idleTimeoutMin"`
	RetentionJobsDays  int     `json:"retentionJobsDays"`
	RetentionAuditDays int     `json:"retentionAuditDays"`
	ThresholdCPUPct    float64 `json:"thresholdCpuPct"`
	ThresholdDiskPct   float64 `json:"thresholdDiskPct"`
	ClusterErrorStreak int     `json:"clusterErrorStreak"`
	MockMode           bool    `json:"mockMode"`
	// MockForced 为 true 表示由环境变量 TMP_MOCK 强制，界面不可切换。
	MockForced bool `json:"mockForced"`
}

// SettingService 系统设置服务。
type SettingService struct {
	repo *repo.SettingRepo
	mode *ModeManager
	cfg  *config.Config
}

// NewSettingService 构造设置服务。
func NewSettingService(r *repo.SettingRepo, mode *ModeManager, cfg *config.Config) *SettingService {
	return &SettingService{repo: r, mode: mode, cfg: cfg}
}

// GetCollection 读取采集配置（缺失键回落默认值）。
func (s *SettingService) GetCollection(ctx context.Context) (*CollectionSettings, error) {
	all, err := s.repo.All(ctx)
	if err != nil {
		return nil, apperr.Internal("读取配置失败")
	}
	get := func(key string) string {
		if v, ok := all[key]; ok {
			return v
		}
		return repo.DefaultSettings[key]
	}
	out := &CollectionSettings{
		IntervalSec:        atoiDefault(get(repo.SettingCollectIntervalSec), 300),
		TimeoutSec:         atoiDefault(get(repo.SettingCollectTimeoutSec), 10),
		Concurrency:        atoiDefault(get(repo.SettingCollectConcurrency), 4),
		Retry:              atoiDefault(get(repo.SettingCollectRetry), 1),
		JWTTokenTTLHours:   atoiDefault(get(repo.SettingJWTTokenTTLHours), 8),
		IdleTimeoutMin:     atoiDefault(get(repo.SettingAuthIdleTimeoutMin), 30),
		RetentionJobsDays:  atoiDefault(get(repo.SettingRetentionJobsDays), 30),
		RetentionAuditDays: atoiDefault(get(repo.SettingRetentionAuditDays), 180),
		ThresholdCPUPct:    floatDefault(get(repo.SettingThresholdCPUPct), 80),
		ThresholdDiskPct:   floatDefault(get(repo.SettingThresholdDiskPct), 85),
		ClusterErrorStreak: atoiDefault(get(repo.SettingClusterErrorStreak), 3),
		MockMode:           s.mode.IsMock(),
		MockForced:         s.mode.ForcedMock(),
	}
	return out, nil
}

// UpdateCollection 修改采集配置（仅 admin，立即生效）。
func (s *SettingService) UpdateCollection(ctx context.Context, in CollectionSettings, by *int64) (*CollectionSettings, error) {
	if in.IntervalSec < 30 || in.IntervalSec > 86400 {
		return nil, apperr.BadRequest("采集周期需在 30~86400 秒之间")
	}
	if in.TimeoutSec < 3 || in.TimeoutSec > 60 {
		return nil, apperr.BadRequest("单集群超时需在 3~60 秒之间")
	}
	if in.Concurrency < 1 || in.Concurrency > 32 {
		return nil, apperr.BadRequest("采集并发数需在 1~32 之间")
	}
	if in.Retry < 0 || in.Retry > 5 {
		return nil, apperr.BadRequest("失败重试次数需在 0~5 之间")
	}
	if in.ThresholdCPUPct <= 0 || in.ThresholdCPUPct > 100 {
		return nil, apperr.BadRequest("CPU 阈值需在 0~100 之间")
	}
	if in.ThresholdDiskPct <= 0 || in.ThresholdDiskPct > 100 {
		return nil, apperr.BadRequest("磁盘阈值需在 0~100 之间")
	}
	if in.ClusterErrorStreak < 1 || in.ClusterErrorStreak > 20 {
		return nil, apperr.BadRequest("异常判定阈值需在 1~20 之间")
	}
	kv := map[string]string{
		repo.SettingCollectIntervalSec: strconv.Itoa(in.IntervalSec),
		repo.SettingCollectTimeoutSec:  strconv.Itoa(in.TimeoutSec),
		repo.SettingCollectConcurrency: strconv.Itoa(in.Concurrency),
		repo.SettingCollectRetry:       strconv.Itoa(in.Retry),
		repo.SettingJWTTokenTTLHours:   strconv.Itoa(in.JWTTokenTTLHours),
		repo.SettingAuthIdleTimeoutMin: strconv.Itoa(in.IdleTimeoutMin),
		repo.SettingRetentionJobsDays:  strconv.Itoa(in.RetentionJobsDays),
		repo.SettingRetentionAuditDays: strconv.Itoa(in.RetentionAuditDays),
		repo.SettingThresholdCPUPct:    strconv.FormatFloat(in.ThresholdCPUPct, 'f', 1, 64),
		repo.SettingThresholdDiskPct:   strconv.FormatFloat(in.ThresholdDiskPct, 'f', 1, 64),
		repo.SettingClusterErrorStreak: strconv.Itoa(in.ClusterErrorStreak),
	}
	if err := s.repo.SetMany(ctx, kv, by); err != nil {
		return nil, apperr.Internal("保存配置失败")
	}
	return s.GetCollection(ctx)
}

// Thresholds 返回巡检阈值。
func (s *SettingService) Thresholds(ctx context.Context) inspect.Thresholds {
	cfg, err := s.GetCollection(ctx)
	if err != nil {
		return inspect.DefaultThresholds()
	}
	return inspect.Thresholds{CPUPct: cfg.ThresholdCPUPct, DiskPct: cfg.ThresholdDiskPct}
}

// ErrorStreak 返回集群异常判定阈值。
func (s *SettingService) ErrorStreak(ctx context.Context) int {
	cfg, err := s.GetCollection(ctx)
	if err != nil || cfg.ClusterErrorStreak <= 0 {
		return 3
	}
	return cfg.ClusterErrorStreak
}

// CollectTimeout 返回单集群采集超时。
func (s *SettingService) CollectTimeout(ctx context.Context) time.Duration {
	cfg, err := s.GetCollection(ctx)
	if err != nil || cfg.TimeoutSec <= 0 {
		return 10 * time.Second
	}
	return time.Duration(cfg.TimeoutSec) * time.Second
}

// ListRaw 返回全部设置原始 K/V（关于页与调试用）。
func (s *SettingService) ListRaw(ctx context.Context) (map[string]string, error) {
	all, err := s.repo.All(ctx)
	if err != nil {
		return nil, apperr.Internal("读取配置失败")
	}
	return all, nil
}

// DemoModeStatus 演示模式状态。
type DemoModeStatus struct {
	Enabled      bool     `json:"enabled"`
	Forced       bool     `json:"forced"`
	DataSource   string   `json:"dataSource"`
	MockClusters []string `json:"mockClusters"`
}

// GetDemoMode 查询当前模式。
func (s *SettingService) GetDemoMode(ctx context.Context) *DemoModeStatus {
	return &DemoModeStatus{
		Enabled:      s.mode.IsMock(),
		Forced:       s.mode.ForcedMock(),
		DataSource:   s.mode.DataSource(),
		MockClusters: mockClusterNames,
	}
}

// SetDemoMode 切换演示模式（仅 admin，二次确认由前端负责）。
func (s *SettingService) SetDemoMode(ctx context.Context, enabled bool, by *int64) (*DemoModeStatus, error) {
	if !s.mode.SetMock(enabled) {
		return nil, apperr.Forbidden("演示模式已由环境变量 TMP_MOCK 强制启用，无法切换")
	}
	val := "false"
	if enabled {
		val = "true"
	}
	if err := s.repo.Set(ctx, repo.SettingModeMock, val, by); err != nil {
		return nil, apperr.Internal("保存模式配置失败")
	}
	return s.GetDemoMode(ctx), nil
}

// CompatibilityRow 版本兼容矩阵行。
type CompatibilityRow struct {
	Version        string `json:"version"`
	BasicInfo      bool   `json:"basicInfo"`
	TiDBUser       bool   `json:"tidbUser"`
	ResourceGroup  bool   `json:"resourceGroup"`
	Session        bool   `json:"session"`
	SessionKill    bool   `json:"sessionKill"`
	Variables      bool   `json:"variables"`
	VariableUpdate bool   `json:"variableUpdate"`
	Note           string `json:"note"`
}

// AboutInfo 关于页信息。
type AboutInfo struct {
	Version       string             `json:"version"`
	BuildTime     string             `json:"buildTime"`
	Commit        string             `json:"commit"`
	Database      string             `json:"database"`
	GoVersion     string             `json:"goVersion"`
	MockMode      bool               `json:"mockMode"`
	Compatibility []CompatibilityRow `json:"compatibility"`
	Settings      map[string]string  `json:"settings"`
}

// About 返回系统信息与兼容矩阵。
func (s *SettingService) About(ctx context.Context) (*AboutInfo, error) {
	all, _ := s.repo.All(ctx)
	return &AboutInfo{
		Version:   config.Version,
		BuildTime: config.BuildTime,
		Commit:    config.Commit,
		Database:  "SQLite（单文件 WAL，无外部依赖）",
		GoVersion: "go1.22",
		MockMode:  s.mode.IsMock(),
		Compatibility: []CompatibilityRow{
			{Version: "v6.5", BasicInfo: true, TiDBUser: true, ResourceGroup: false, Session: true, SessionKill: true, Variables: true, VariableUpdate: true, Note: "RG 相关菜单隐藏"},
			{Version: "v7.1", BasicInfo: true, TiDBUser: true, ResourceGroup: false, Session: true, SessionKill: true, Variables: true, VariableUpdate: true, Note: "RG 相关菜单隐藏"},
			{Version: "v7.5", BasicInfo: true, TiDBUser: true, ResourceGroup: true, Session: true, SessionKill: true, Variables: true, VariableUpdate: true, Note: "完整功能"},
			{Version: "v8.5", BasicInfo: true, TiDBUser: true, ResourceGroup: true, Session: true, SessionKill: true, Variables: true, VariableUpdate: true, Note: "完整功能；新增变量做兼容降级"},
		},
		Settings: all,
	}, nil
}

// atoiDefault 字符串转整型，失败返回默认值。
func atoiDefault(s string, def int) int {
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return def
}

// floatDefault 字符串转浮点，失败返回默认值。
func floatDefault(s string, def float64) float64 {
	if v, err := strconv.ParseFloat(s, 64); err == nil {
		return v
	}
	return def
}
