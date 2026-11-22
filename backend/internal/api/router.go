package api

import (
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/tmp-platform/backend/internal/auth"
	"github.com/tmp-platform/backend/internal/config"
	"github.com/tmp-platform/backend/internal/pkg/apperr"
)

// RouterDeps 路由装配依赖。
type RouterDeps struct {
	Handlers *Handlers
	Auth     *auth.Middleware
	Logger   *slog.Logger
	// Assets 为内嵌的前端构建产物（backend/web/dist）。
	Assets fs.FS
	// Ping 为元数据库探活函数（/readyz 使用），可为 nil。
	Ping func() error
}

// NewRouter 构建完整的 Gin 引擎：探针 + /api/v1 业务分组 + SPA 静态回退。
//
// 权限声明原则：每个端点在路由层显式声明所需 Action（不依赖前端隐藏菜单）。
func NewRouter(d RouterDeps) *gin.Engine {
	gin.SetMode(ginMode())
	r := gin.New()
	r.Use(RequestIDMiddleware(), LoggerWith(d.Logger), RecoveryMiddleware(d.Logger), SecurityHeadersMiddleware())

	// 探针：不鉴权、不落访问日志噪声之外的信息。
	r.GET("/healthz", func(c *gin.Context) {
		OK(c, gin.H{"status": "ok", "version": config.Version})
	})
	r.GET("/readyz", func(c *gin.Context) {
		if d.Ping != nil {
			if err := d.Ping(); err != nil {
				c.JSON(http.StatusServiceUnavailable, Response{
					Code: apperr.CodeInternal, Message: "元数据库不可用", Data: gin.H{"db": "down"},
				})
				return
			}
		}
		OK(c, gin.H{"status": "ready", "db": "up"})
	})

	h := d.Handlers
	v1 := r.Group("/api/v1")
	// 接口数据（集群状态、采集快照）不可缓存，否则刷新按钮拿到的仍是旧值。
	v1.Use(NoCacheMiddleware())

	// ---- 公开接口：初始化状态、初始化、登录 ----
	v1.GET("/setup/status", h.SetupStatus)
	v1.POST("/setup/init", h.SetupInit)
	v1.POST("/auth/login", h.Login)

	// ---- 以下全部需要 Bearer token ----
	// 认证之后追加强制改密守卫：must_change_password=1 时仅放行改密白名单。
	authed := v1.Group("", d.Auth.Authenticate(), auth.MustChangePasswordGuard())
	{
		authed.GET("/auth/me", h.Me)
		authed.POST("/auth/logout", h.Logout)
		authed.PUT("/auth/password", h.ChangePassword)

		// 平台用户（仅 admin）
		authed.GET("/users", auth.Require(auth.ActionPlatformUserView), h.ListUsers)
		authed.POST("/users", auth.Require(auth.ActionPlatformUserManage), h.CreateUser)
		authed.GET("/users/:id", auth.Require(auth.ActionPlatformUserView), h.GetUser)
		authed.PUT("/users/:id", auth.Require(auth.ActionPlatformUserManage), h.UpdateUser)
		authed.PATCH("/users/:id/status", auth.Require(auth.ActionPlatformUserManage), h.SetUserStatus)
		authed.POST("/users/:id/reset-password", auth.Require(auth.ActionPlatformUserManage), h.ResetUserPassword)
		authed.GET("/roles/matrix", auth.Require(auth.ActionRoleView), h.RoleMatrix)

		// 总览
		ov := authed.Group("/overview", auth.Require(auth.ActionOverviewView))
		{
			ov.GET("/summary", h.OverviewSummary)
			ov.GET("/health-distribution", h.OverviewHealth)
			ov.GET("/recent-jobs", h.OverviewRecentJobs)
			ov.GET("/latest-inspection", h.OverviewLatestInspection)
			ov.GET("/collect-status", h.OverviewCollectStatus)
		}

		// 集群：静态子路径先注册，避免与 :id 冲突。
		authed.GET("/clusters", auth.Require(auth.ActionClusterView), h.ListClusters)
		authed.GET("/clusters/options", auth.Require(auth.ActionClusterView), h.ClusterOptions)
		authed.POST("/clusters/test-connection", auth.Require(auth.ActionClusterTakeover), h.TestConnection)
		authed.POST("/clusters/discover-pd", auth.Require(auth.ActionClusterTakeover), h.DiscoverPD)
		authed.POST("/clusters", auth.Require(auth.ActionClusterTakeover), h.CreateCluster)
		authed.POST("/clusters/batch-refresh", auth.Require(auth.ActionClusterRefresh), h.BatchRefreshClusters)
		authed.GET("/clusters/:id", auth.Require(auth.ActionClusterView), h.GetCluster)
		authed.PUT("/clusters/:id", auth.Require(auth.ActionClusterEdit), h.UpdateCluster)
		authed.DELETE("/clusters/:id", auth.Require(auth.ActionClusterDelete), h.DeleteCluster)
		authed.POST("/clusters/:id/refresh", auth.Require(auth.ActionClusterRefresh), h.RefreshCluster)
		authed.GET("/clusters/:id/overview", auth.Require(auth.ActionClusterView), h.ClusterOverview)
		authed.GET("/clusters/:id/topology", auth.Require(auth.ActionClusterView), h.ClusterTopology)
		authed.GET("/clusters/:id/instances", auth.Require(auth.ActionClusterView), h.ClusterInstances)
		authed.GET("/clusters/:id/components", auth.Require(auth.ActionClusterView), h.ClusterComponents)
		authed.GET("/clusters/:id/capabilities", auth.Require(auth.ActionClusterView), h.ClusterCapabilities)
		authed.GET("/clusters/:id/changes", auth.Require(auth.ActionClusterView), h.ClusterChanges)
		authed.GET("/clusters/:id/jobs", auth.Require(auth.ActionJobView), h.ClusterJobs)
		authed.GET("/clusters/:id/metrics", auth.Require(auth.ActionClusterView), h.ClusterMetrics)
		authed.GET("/clusters/:id/alerts", auth.Require(auth.ActionClusterView), h.ClusterAlerts)

		// TiDB 对象：用户 / Resource Group / 会话 / 参数 / 慢查询
		authed.GET("/clusters/:id/tidb-users", auth.Require(auth.ActionTiDBUserView), h.ListTiDBUsers)
		authed.POST("/clusters/:id/tidb-users", auth.Require(auth.ActionTiDBUserCreate), h.CreateTiDBUser)
		authed.DELETE("/clusters/:id/tidb-users/:uid", auth.Require(auth.ActionTiDBUserDelete), h.DeleteTiDBUser)
		authed.PUT("/clusters/:id/tidb-users/:uid/password", auth.Require(auth.ActionTiDBUserPassword), h.ResetTiDBUserPassword)
		authed.GET("/clusters/:id/tidb-users/:uid/grants", auth.Require(auth.ActionTiDBUserView), h.GetTiDBUserGrants)
		authed.POST("/clusters/:id/tidb-users/:uid/grants", auth.Require(auth.ActionTiDBUserGrant), h.ChangeTiDBUserGrants)
		authed.DELETE("/clusters/:id/tidb-users/:uid/grants", auth.Require(auth.ActionTiDBUserGrant), h.RevokeTiDBUserGrants)

		authed.GET("/clusters/:id/resource-groups", auth.Require(auth.ActionRGView), h.ListResourceGroups)
		authed.POST("/clusters/:id/resource-groups", auth.Require(auth.ActionRGManage), h.CreateResourceGroup)
		authed.PUT("/clusters/:id/resource-groups/:name", auth.Require(auth.ActionRGManage), h.AlterResourceGroup)
		authed.DELETE("/clusters/:id/resource-groups/:name", auth.Require(auth.ActionRGManage), h.DeleteResourceGroup)
		authed.POST("/clusters/:id/resource-groups/bind", auth.Require(auth.ActionRGBind), h.BindResourceGroup)
		authed.POST("/clusters/:id/resource-groups/unbind", auth.Require(auth.ActionRGBind), h.UnbindResourceGroup)

		authed.GET("/clusters/:id/sessions", auth.Require(auth.ActionSessionView), h.ListSessions)
		authed.POST("/clusters/:id/sessions/kill", auth.Require(auth.ActionSessionKill), h.KillSessions)
		authed.GET("/clusters/:id/slow-queries", auth.Require(auth.ActionSessionView), h.SlowQueries)

		authed.GET("/clusters/:id/variables", auth.Require(auth.ActionVariableView), h.ListVariables)
		authed.GET("/clusters/:id/variables/export", auth.Require(auth.ActionVariableView), h.ExportVariables)
		authed.PUT("/clusters/:id/variables", auth.Require(auth.ActionVariableUpdate), h.UpdateVariable)

		// 主机
		authed.GET("/hosts", auth.Require(auth.ActionHostView), h.ListHosts)
		authed.GET("/hosts/grouped", auth.Require(auth.ActionHostView), h.GroupedHosts)
		authed.POST("/hosts/ssh-check", auth.Require(auth.ActionHostSSHCheck), h.SSHCheck)
		authed.GET("/hosts/:id", auth.Require(auth.ActionHostView), h.GetHost)
		authed.GET("/hosts/:id/metrics", auth.Require(auth.ActionHostView), h.HostMetrics)
		authed.GET("/hosts/:id/ssh-checks", auth.Require(auth.ActionHostView), h.HostSSHChecks)

		// 巡检
		authed.GET("/inspection/templates", auth.Require(auth.ActionInspectView), h.ListInspectTemplates)
		authed.GET("/inspection/templates/:id", auth.Require(auth.ActionInspectView), h.GetInspectTemplate)
		authed.GET("/inspection/items", auth.Require(auth.ActionInspectView), h.ListInspectItems)
		authed.POST("/inspection/runs", auth.Require(auth.ActionInspectRun), h.StartInspectionRuns)
		authed.GET("/inspection/runs/:id", auth.Require(auth.ActionInspectView), h.GetInspectionRun)
		authed.GET("/inspection/reports", auth.Require(auth.ActionReportView), h.ListInspectionReports)
		authed.GET("/inspection/reports/:id", auth.Require(auth.ActionReportView), h.GetInspectionReport)
		authed.GET("/inspection/reports/:id/download", auth.Require(auth.ActionReportDownload), h.DownloadInspectionReport)
		authed.GET("/inspection/schedules", auth.Require(auth.ActionInspectView), h.ListSchedules)
		authed.POST("/inspection/schedules", auth.Require(auth.ActionInspectRun), h.CreateSchedule)

		// 任务中心
		authed.GET("/jobs", auth.Require(auth.ActionJobView), h.ListJobs)
		authed.GET("/jobs/running-count", auth.Require(auth.ActionJobView), h.RunningJobCount)
		authed.GET("/jobs/:id", auth.Require(auth.ActionJobView), h.GetJob)
		authed.GET("/jobs/:id/steps", auth.Require(auth.ActionJobView), h.JobSteps)
		authed.GET("/jobs/:id/logs", auth.Require(auth.ActionJobView), h.JobLogs)
		authed.POST("/jobs/:id/retry", auth.Require(auth.ActionJobRetry), h.RetryJob)
		authed.POST("/jobs/:id/cancel", auth.Require(auth.ActionJobCancel), h.CancelJob)

		// 设置 / 凭据 / 审计 / 枚举
		authed.GET("/settings/collection", auth.Require(auth.ActionSettingView), h.GetCollectionSettings)
		authed.PUT("/settings/collection", auth.Require(auth.ActionSettingManage), h.UpdateCollectionSettings)
		authed.GET("/settings/credentials", auth.Require(auth.ActionCredentialView), h.ListCredentials)
		authed.PUT("/settings/credentials/:clusterId", auth.Require(auth.ActionCredentialManage), h.UpdateCredential)
		authed.POST("/settings/credentials/:clusterId/test", auth.Require(auth.ActionCredentialManage), h.TestCredential)
		authed.GET("/settings/demo-mode", auth.Require(auth.ActionSettingView), h.GetDemoMode)
		authed.PUT("/settings/demo-mode", auth.Require(auth.ActionDemoToggle), h.SetDemoMode)
		authed.GET("/settings/about", auth.Require(auth.ActionSettingView), h.About)
		authed.GET("/audit-logs", auth.Require(auth.ActionAuditView), h.ListAuditLogs)
		authed.GET("/audit-logs/export", auth.Require(auth.ActionAuditView), h.ExportAuditLogs)
		authed.GET("/meta/enums", auth.Require(auth.ActionSettingView), h.Enums)
	}

	mountSPA(r, d.Assets)
	return r
}

// ginMode 解析 TMP_GIN_MODE（未设置时使用 release，避免调试输出泄漏）。
func ginMode() string {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TMP_GIN_MODE"))) {
	case "debug":
		return gin.DebugMode
	case "test":
		return gin.TestMode
	default:
		return gin.ReleaseMode
	}
}

// mountSPA 挂载前端单页应用：静态资源命中即返回，路由路径回退 index.html（深链不 404）。
//
// 回退规则（防止把缺失的静态资源伪装成 200 HTML）：
//  1. /api/、/healthz、/readyz 前缀 → 40401 JSON；
//  2. /assets/ 下的构建产物未命中 → 404（带哈希的资源不可能由前端路由生成）；
//  3. 其余带扩展名的路径（如 /favicon.ico、/manifest.json）未命中 → 404；
//  4. 只有「无扩展名的前端路由路径」才回退 index.html。
func mountSPA(r *gin.Engine, assets fs.FS) {
	if assets == nil {
		r.NoRoute(apiNotFound)
		return
	}
	index, indexErr := fs.ReadFile(assets, "index.html")
	fileServer := http.FileServer(http.FS(assets))
	r.NoRoute(func(c *gin.Context) {
		reqPath := c.Request.URL.Path
		// /api/ 与探针属于接口域，绝不回退 index.html。
		if reqPath == "/healthz" || reqPath == "/readyz" || strings.HasPrefix(reqPath, "/api/") {
			apiNotFound(c)
			return
		}
		clean := strings.TrimPrefix(path.Clean(reqPath), "/")
		if clean != "" && clean != "." {
			if f, err := assets.Open(clean); err == nil {
				_ = f.Close()
				if strings.HasPrefix(reqPath, "/assets/") {
					// 构建产物带哈希，可长期缓存。
					c.Header("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(c.Writer, c.Request)
				return
			}
			// /assets/ 下的构建产物未命中：带哈希的资源不可能由前端路由生成，必须 404，
			// 不能回退 index.html（否则浏览器会把 HTML 当 JS 解析）。
			if strings.HasPrefix(reqPath, "/assets/") {
				apiNotFound(c)
				return
			}
		}
		// 带扩展名但未命中：这是缺失的静态资源，不是前端路由。
		if path.Ext(path.Base(reqPath)) != "" {
			apiNotFound(c)
			return
		}
		if indexErr != nil {
			c.Data(http.StatusOK, "text/plain; charset=utf-8",
				[]byte("前端资源未构建，请先执行 make frontend-build"))
			return
		}
		c.Header("Cache-Control", "no-cache")
		c.Data(http.StatusOK, "text/html; charset=utf-8", index)
	})
}

// apiNotFound 统一 40401 响应。
func apiNotFound(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusNotFound, Response{
		Code: apperr.CodeNotFound, Message: "接口不存在", Data: nil,
	})
}
