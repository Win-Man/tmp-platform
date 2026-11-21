package service

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/tmp-platform/backend/internal/auth"
	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/pkg/apperr"
	"github.com/tmp-platform/backend/internal/repo"
)

// usernameRe 用户名允许字符。
var usernameRe = regexp.MustCompile(`^[a-z0-9_]{3,32}$`)

// validUsername 校验用户名格式（统一小写存储）。
func validUsername(name string) bool { return usernameRe.MatchString(name) }

// UserService 平台用户管理服务。
type UserService struct {
	users *repo.UserRepo
}

// NewUserService 构造用户服务。
func NewUserService(r *repo.UserRepo) *UserService { return &UserService{users: r} }

// List 分页查询平台用户。
func (s *UserService) List(ctx context.Context, f repo.UserFilter) (*repo.ListResult[UserProfile], error) {
	list, total, err := s.users.List(ctx, f)
	if err != nil {
		return nil, apperr.Internal("查询用户列表失败")
	}
	views := make([]UserProfile, 0, len(list))
	for i := range list {
		views = append(views, *ToUserProfile(&list[i]))
	}
	res := repo.NewListResult(views, total, f.Page)
	return &res, nil
}

// GetByID 按 ID 查询。
func (s *UserService) GetByID(ctx context.Context, id int64) (*model.PlatformUser, error) {
	u, err := s.users.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, apperr.NotFound("用户不存在")
		}
		return nil, apperr.Internal("查询用户失败")
	}
	return u, nil
}

// CreateUserInput 新建用户入参。
type CreateUserInput struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Password    string `json:"password"`
	Role        string `json:"role"`
}

// Create 新建平台用户。
func (s *UserService) Create(ctx context.Context, in CreateUserInput) (*UserProfile, error) {
	username := strings.ToLower(strings.TrimSpace(in.Username))
	if !validUsername(username) {
		return nil, apperr.BadRequest("用户名需为 3~32 位小写字母、数字或下划线")
	}
	if !validRole(in.Role) {
		return nil, apperr.BadRequest("角色取值非法")
	}
	if err := auth.ValidatePasswordPolicy(in.Password); err != nil {
		return nil, apperr.BadRequest(err.Error())
	}
	if _, err := s.users.GetByUsername(ctx, username); err == nil {
		return nil, apperr.BadRequest("用户名已存在")
	} else if !errors.Is(err, repo.ErrNotFound) {
		return nil, apperr.Internal("查询用户失败")
	}
	hash, err := auth.HashPassword(in.Password)
	if err != nil {
		return nil, apperr.Internal("密码处理失败")
	}
	display := strings.TrimSpace(in.DisplayName)
	if display == "" {
		display = username
	}
	u := &model.PlatformUser{
		Username:     username,
		DisplayName:  display,
		PasswordHash: hash,
		Role:         in.Role,
		Status:       "active",
		// 管理员代设的初始密码属于「已知密码」，与默认 admin 弱口令同类风险：
		// 置位后由 MustChangePasswordGuard 强制该用户首次登录先改密。
		MustChangePassword: 1,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, apperr.Internal("创建用户失败")
	}
	return ToUserProfile(u), nil
}

// UpdateUserInput 编辑用户入参（仅显示名与角色可改）。
type UpdateUserInput struct {
	DisplayName string `json:"displayName"`
	Role        string `json:"role"`
}

// Update 编辑用户；最后一个 admin 不可降级。
func (s *UserService) Update(ctx context.Context, id int64, in UpdateUserInput) (*UserProfile, error) {
	u, err := s.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if !validRole(in.Role) {
		return nil, apperr.BadRequest("角色取值非法")
	}
	if u.Role == auth.RoleAdmin && in.Role != auth.RoleAdmin {
		n, cerr := s.users.CountByRole(ctx, auth.RoleAdmin, u.ID)
		if cerr != nil {
			return nil, apperr.Internal("校验管理员数量失败")
		}
		if n == 0 {
			return nil, apperr.BadRequest("系统至少需保留一个启用的管理员")
		}
	}
	display := strings.TrimSpace(in.DisplayName)
	if display == "" {
		display = u.DisplayName
	}
	fields := map[string]any{"display_name": display, "role": in.Role}
	// 角色变更使旧 token 失效，避免权限漂移。
	if in.Role != u.Role {
		fields["token_version"] = u.TokenVersion + 1
	}
	if err := s.users.UpdateFields(ctx, id, fields); err != nil {
		return nil, apperr.Internal("更新用户失败")
	}
	u.DisplayName = display
	u.Role = in.Role
	return ToUserProfile(u), nil
}

// SetStatus 启用 / 禁用用户；禁用后 token 立即失效。
func (s *UserService) SetStatus(ctx context.Context, id int64, status string) (*UserProfile, error) {
	if status != "active" && status != "disabled" {
		return nil, apperr.BadRequest("状态取值非法")
	}
	u, err := s.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if status == "disabled" {
		if u.Role == auth.RoleAdmin {
			n, cerr := s.users.CountByRole(ctx, auth.RoleAdmin, u.ID)
			if cerr != nil {
				return nil, apperr.Internal("校验管理员数量失败")
			}
			if n == 0 {
				return nil, apperr.BadRequest("系统至少需保留一个启用的管理员")
			}
		}
		fields := map[string]any{"status": status, "token_version": u.TokenVersion + 1}
		if err := s.users.UpdateFields(ctx, id, fields); err != nil {
			return nil, apperr.Internal("更新用户状态失败")
		}
		u.Status = status
		return ToUserProfile(u), nil
	}
	if err := s.users.UpdateFields(ctx, id, map[string]any{"status": status}); err != nil {
		return nil, apperr.Internal("更新用户状态失败")
	}
	u.Status = status
	return ToUserProfile(u), nil
}

// ResetPassword 重置指定用户密码；成功后 token_version+1。
func (s *UserService) ResetPassword(ctx context.Context, id int64, password string) error {
	u, err := s.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := auth.ValidatePasswordPolicy(password); err != nil {
		return apperr.BadRequest(err.Error())
	}
	hash, herr := auth.HashPassword(password)
	if herr != nil {
		return apperr.Internal("密码处理失败")
	}
	if uerr := s.users.UpdateFields(ctx, id, map[string]any{
		"password_hash":        hash,
		"must_change_password": 1,
		"token_version":        u.TokenVersion + 1,
		"failed_login_count":   0,
		"locked_until":         nil,
	}); uerr != nil {
		return apperr.Internal("重置密码失败")
	}
	return nil
}

// validRole 校验角色取值。
func validRole(r string) bool {
	return r == auth.RoleAdmin || r == auth.RoleOperator || r == auth.RoleViewer
}

// RoleMatrix 返回角色 × 操作权限矩阵。
func RoleMatrix() []auth.ActionDef { return auth.Matrix() }
