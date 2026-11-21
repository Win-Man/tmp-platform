package repo

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/tmp-platform/backend/internal/model"
)

// ErrNotFound 记录不存在。
var ErrNotFound = errors.New("记录不存在")

// UserRepo 平台用户数据访问。
type UserRepo struct {
	db *gorm.DB
}

// NewUserRepo 构造仓库。
func NewUserRepo(db *gorm.DB) *UserRepo { return &UserRepo{db: db} }

// GetByID 按 ID 查询（认证中间件每次请求都会调用）。
func (r *UserRepo) GetByID(ctx context.Context, id int64) (*model.PlatformUser, error) {
	var u model.PlatformUser
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// GetByUsername 按用户名（小写）查询。
func (r *UserRepo) GetByUsername(ctx context.Context, username string) (*model.PlatformUser, error) {
	var u model.PlatformUser
	err := r.db.WithContext(ctx).Where("username = ?", strings.ToLower(strings.TrimSpace(username))).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// Count 统计用户总数。
func (r *UserRepo) Count(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.PlatformUser{}).Count(&n).Error
	return n, err
}

// CountByRole 统计指定角色且处于启用状态的人数。
func (r *UserRepo) CountByRole(ctx context.Context, role string, excludeID int64) (int64, error) {
	var n int64
	q := r.db.WithContext(ctx).Model(&model.PlatformUser{}).Where("role = ? AND status = ?", role, "active")
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	err := q.Count(&n).Error
	return n, err
}

// UserFilter 用户列表筛选。
type UserFilter struct {
	Keyword string
	Page    Page
}

// List 分页查询用户。
func (r *UserRepo) List(ctx context.Context, f UserFilter) ([]model.PlatformUser, int64, error) {
	f.Page.Normalize()
	q := r.db.WithContext(ctx).Model(&model.PlatformUser{}).Order("id ASC")
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		like := likePattern(kw)
		q = q.Where("username LIKE ? ESCAPE '\\' OR display_name LIKE ? ESCAPE '\\'", like, like)
	}
	list, total, err := ApplyPagination[model.PlatformUser](q, f.Page, &model.PlatformUser{})
	if err != nil {
		return nil, 0, err
	}
	return list, total, nil
}

// Create 新建用户。
func (r *UserRepo) Create(ctx context.Context, u *model.PlatformUser) error {
	return r.db.WithContext(ctx).Create(u).Error
}

// UpdateFields 更新指定字段。
func (r *UserRepo) UpdateFields(ctx context.Context, id int64, fields map[string]any) error {
	fields["updated_at"] = time.Now()
	return r.db.WithContext(ctx).Model(&model.PlatformUser{}).Where("id = ?", id).Updates(fields).Error
}

// UpdateLoginSuccess 记录登录成功并清零失败计数。
func (r *UserRepo) UpdateLoginSuccess(ctx context.Context, id int64, ip string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&model.PlatformUser{}).Where("id = ?", id).Updates(map[string]any{
		"last_login_at":      now,
		"last_login_ip":      ip,
		"failed_login_count": 0,
		"locked_until":       nil,
		"updated_at":         now,
	}).Error
}

// IncrementFailedLogin 增加失败次数并在达到阈值时锁定账号。
//
// 返回最新的失败次数与锁定截止时间。
func (r *UserRepo) IncrementFailedLogin(ctx context.Context, id int64, threshold int, lockDuration time.Duration) (int, *time.Time, error) {
	var u model.PlatformUser
	if err := r.db.WithContext(ctx).Where("id = ?", id).First(&u).Error; err != nil {
		return 0, nil, err
	}
	fails := u.FailedLoginCount + 1
	updates := map[string]any{
		"failed_login_count": fails,
		"updated_at":         time.Now(),
	}
	var lockedUntil *time.Time
	if fails >= threshold {
		t := time.Now().Add(lockDuration)
		lockedUntil = &t
		updates["locked_until"] = t
	}
	if err := r.db.WithContext(ctx).Model(&model.PlatformUser{}).Where("id = ?", id).Updates(updates).Error; err != nil {
		return fails, nil, err
	}
	return fails, lockedUntil, nil
}

// ResetFailedLogin 重置失败计数与锁定状态。
func (r *UserRepo) ResetFailedLogin(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Model(&model.PlatformUser{}).Where("id = ?", id).
		Updates(map[string]any{"failed_login_count": 0, "locked_until": nil, "updated_at": time.Now()}).Error
}

// AuditRepo 审计日志数据访问；本仓库刻意不提供 Update/Delete 方法。
type AuditRepo struct {
	db *gorm.DB
}

// NewAuditRepo 构造仓库。
func NewAuditRepo(db *gorm.DB) *AuditRepo { return &AuditRepo{db: db} }

// Create 写入审计记录。
func (r *AuditRepo) Create(ctx context.Context, a *model.AuditLog) error {
	return r.db.WithContext(ctx).Create(a).Error
}

// AuditFilter 审计筛选条件。
type AuditFilter struct {
	Username     string
	Action       string
	ResourceType string
	ResourceID   string
	Result       string
	From         *time.Time
	To           *time.Time
	Page         Page
}

// buildAuditQuery 构造审计查询条件。
func (r *AuditRepo) buildAuditQuery(ctx context.Context, f AuditFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.AuditLog{})
	if f.Username != "" {
		q = q.Where("username LIKE ? ESCAPE '\\'", likePattern(f.Username))
	}
	if f.Action != "" {
		q = q.Where("action = ?", f.Action)
	}
	if f.ResourceType != "" {
		q = q.Where("resource_type = ?", f.ResourceType)
	}
	if f.ResourceID != "" {
		q = q.Where("resource_id = ?", f.ResourceID)
	}
	if f.Result != "" {
		q = q.Where("result = ?", f.Result)
	}
	if f.From != nil {
		q = q.Where("created_at >= ?", *f.From)
	}
	if f.To != nil {
		q = q.Where("created_at <= ?", *f.To)
	}
	return q
}

// List 分页查询审计日志（时间倒序）。
func (r *AuditRepo) List(ctx context.Context, f AuditFilter) ([]model.AuditLog, int64, error) {
	f.Page.Normalize()
	q := r.buildAuditQuery(ctx, f).Order("id DESC")
	list, total, err := ApplyPagination[model.AuditLog](q, f.Page, &model.AuditLog{})
	return list, total, err
}

// ExportAll 导出全部匹配记录（CSV 用，上限 10000 行）。
func (r *AuditRepo) ExportAll(ctx context.Context, f AuditFilter) ([]model.AuditLog, error) {
	var list []model.AuditLog
	err := r.buildAuditQuery(ctx, f).Order("id DESC").Limit(10000).Find(&list).Error
	return list, err
}

// CountSince 统计某时间之后的记录数（总览采集状态用）。
func (r *AuditRepo) CountSince(ctx context.Context, since time.Time) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.AuditLog{}).Where("created_at >= ?", since).Count(&n).Error
	return n, err
}

// DeleteBefore 清理过期审计（保留期策略，仅清理任务/审计，不清理报告）。
func (r *AuditRepo) DeleteBefore(ctx context.Context, before time.Time) (int64, error) {
	res := r.db.WithContext(ctx).Where("created_at < ?", before).Delete(&model.AuditLog{})
	return res.RowsAffected, res.Error
}
