package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/pkg/logger"
	"github.com/tmp-platform/backend/internal/repo"
)

// 审计动作常量（PRD 8.4 全部写操作 + 登录/登出/导出/下载）。
const (
	AuditLogin           = "auth.login"
	AuditLogout          = "auth.logout"
	AuditPasswordChange  = "auth.password.change"
	AuditUserCreate      = "user.create"
	AuditUserUpdate      = "user.update"
	AuditUserStatus      = "user.status"
	AuditUserResetPwd    = "user.reset_password"
	AuditClusterTakeover = "cluster.takeover"
	AuditClusterUpdate   = "cluster.update"
	AuditClusterDelete   = "cluster.delete"
	AuditClusterRefresh  = "cluster.refresh"
	AuditTiDBUserCreate  = "tidb_user.create"
	AuditTiDBUserDelete  = "tidb_user.delete"
	AuditTiDBUserPwd     = "tidb_user.reset_password"
	AuditTiDBUserGrant   = "tidb_user.grant"
	AuditRGBind          = "resource_group.bind"
	AuditRGUnbind        = "resource_group.unbind"
	AuditRGCreate        = "resource_group.create"
	AuditRGAlter         = "resource_group.alter"
	AuditRGDrop          = "resource_group.drop"
	AuditSessionKill     = "session.kill"
	AuditVariableUpdate  = "variable.update"
	AuditInspectRun      = "inspection.run"
	AuditReportDownload  = "inspection.download"
	AuditReportExport    = "inspection.export"
	AuditJobRetry        = "job.retry"
	AuditJobCancel       = "job.cancel"
	AuditHostSSHCheck    = "host.ssh_check"
	AuditCredential      = "credential.update"
	AuditSettingUpdate   = "setting.update"
	AuditDemoMode        = "setting.demo_mode"
	AuditAuditExport     = "audit.export"
	AuditVariableExport  = "variable.export"
)

// AuditInput 审计写入入参。
type AuditInput struct {
	// User 当前登录用户，可为 nil（登录失败等场景）。
	User *model.PlatformUser
	// ActorName 未登录场景下的操作者名（如登录失败时输入的用户名）。
	ActorName    string
	Action       string
	ResourceType string
	ResourceID   string
	ResourceName string
	// Result success|failed。
	Result string
	// Detail 附加信息，内部会脱敏。
	Detail    map[string]any
	IP        string
	UserAgent string
	RequestID string
}

// AuditService 审计服务。
type AuditService struct {
	repo *repo.AuditRepo
}

// NewAuditService 构造审计服务。
func NewAuditService(r *repo.AuditRepo) *AuditService { return &AuditService{repo: r} }

// Record 写入一条审计；失败仅记录日志，不影响主流程。
func (s *AuditService) Record(ctx context.Context, in AuditInput) {
	if in.Action == "" {
		return
	}
	entry := &model.AuditLog{
		Action:       in.Action,
		ResourceType: in.ResourceType,
		ResourceID:   in.ResourceID,
		ResourceName: in.ResourceName,
		Result:       in.Result,
		IP:           in.IP,
		UserAgent:    in.UserAgent,
		RequestID:    in.RequestID,
		CreatedAt:    time.Now(),
	}
	if entry.Result == "" {
		entry.Result = "success"
	}
	if in.User != nil {
		uid := in.User.ID
		entry.UserID = &uid
		entry.Username = in.User.Username
		entry.Role = in.User.Role
	} else if in.ActorName != "" {
		entry.Username = in.ActorName
	}
	if len(in.Detail) > 0 {
		// 密码/密钥/token 类字段一律脱敏为 ***。
		safe := logger.SanitizeMap(in.Detail)
		if b, err := json.Marshal(safe); err == nil {
			entry.Detail = string(b)
		}
	}
	if err := s.repo.Create(ctx, entry); err != nil {
		logger.From(ctx).Warn("写入审计失败", "action", in.Action, "error", err)
	}
}

// AuditFilter 审计查询筛选（对 API 层暴露）。
type AuditFilter = repo.AuditFilter

// List 查询审计日志。
func (s *AuditService) List(ctx context.Context, f repo.AuditFilter) ([]model.AuditLog, int64, error) {
	return s.repo.List(ctx, f)
}

// Export 导出审计日志。
func (s *AuditService) Export(ctx context.Context, f repo.AuditFilter) ([]model.AuditLog, error) {
	return s.repo.ExportAll(ctx, f)
}
