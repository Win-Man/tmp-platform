package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/tmp-platform/backend/internal/auth"
	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/pkg/apperr"
	"github.com/tmp-platform/backend/internal/repo"
)

// 登录失败限制。
const (
	// MaxFailedLogins 连续失败次数上限。
	MaxFailedLogins = 5
	// LoginLockDuration 锁定时长。
	LoginLockDuration = 10 * time.Minute
)

// DefaultAdminUsername / DefaultAdminPassword 首启自动创建的管理员账号。
//
// 与架构 D 决策一致：开箱即用，但 must_change_password=1 强制首次登录改密。
const (
	DefaultAdminUsername = "admin"
	DefaultAdminPassword = "admin123"
)

// AuthService 认证服务。
type AuthService struct {
	users    *repo.UserRepo
	settings *repo.SettingRepo
	jwt      *auth.Manager
}

// NewAuthService 构造认证服务。
func NewAuthService(users *repo.UserRepo, settings *repo.SettingRepo, jwtMgr *auth.Manager) *AuthService {
	return &AuthService{users: users, settings: settings, jwt: jwtMgr}
}

// LoginInput 登录入参。
type LoginInput struct {
	Username  string
	Password  string
	IP        string
	UserAgent string
	RequestID string
}

// LoginResult 登录结果。
type LoginResult struct {
	Token     string       `json:"token"`
	ExpiresAt time.Time    `json:"expiresAt"`
	User      *UserProfile `json:"user"`
}

// Login 用户名密码登录。
//
// 安全要点：用户不存在与密码错误返回同一错误文案；连续 5 次失败锁定 10 分钟。
func (s *AuthService) Login(ctx context.Context, in LoginInput) (*LoginResult, error) {
	username := strings.ToLower(strings.TrimSpace(in.Username))
	if username == "" || in.Password == "" {
		return nil, apperr.BadRequest("请输入用户名和密码")
	}
	u, err := s.users.GetByUsername(ctx, username)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, apperr.Unauthorized("用户名或密码错误")
		}
		return nil, apperr.Internal("查询账号失败")
	}

	// 锁定检查
	if u.LockedUntil != nil && u.LockedUntil.After(time.Now()) {
		remain := int(time.Until(*u.LockedUntil).Seconds())
		if remain < 0 {
			remain = 0
		}
		return nil, apperr.TooMany("账号已锁定，请稍后重试").
			WithData(map[string]any{"lockedUntil": u.LockedUntil, "remainSeconds": remain})
	}

	if !u.IsActive() {
		return nil, apperr.Unauthorized("账号已被禁用")
	}

	if !auth.VerifyPassword(u.PasswordHash, in.Password) {
		fails, lockedUntil, ierr := s.users.IncrementFailedLogin(ctx, u.ID, MaxFailedLogins, LoginLockDuration)
		if ierr != nil {
			return nil, apperr.Internal("登录处理失败")
		}
		if lockedUntil != nil {
			return nil, apperr.TooMany("连续登录失败次数过多，账号已锁定 10 分钟").
				WithData(map[string]any{"lockedUntil": lockedUntil, "failedCount": fails})
		}
		return nil, apperr.Unauthorized("用户名或密码错误")
	}

	if err := s.users.UpdateLoginSuccess(ctx, u.ID, in.IP); err != nil {
		return nil, apperr.Internal("登录处理失败")
	}
	token, exp, err := s.jwt.Issue(u.ID, u.Username, u.TokenVersion)
	if err != nil {
		return nil, apperr.Internal("签发令牌失败")
	}
	now := time.Now()
	u.LastLoginAt = &now
	u.LastLoginIP = in.IP
	u.FailedLoginCount = 0
	u.LockedUntil = nil

	return &LoginResult{Token: token, ExpiresAt: exp, User: ToUserProfile(u)}, nil
}

// UserProfile 对外用户视图（不含任何密码/令牌字段）。
type UserProfile struct {
	ID                 int64      `json:"id"`
	Username           string     `json:"username"`
	DisplayName        string     `json:"displayName"`
	Role               string     `json:"role"`
	Status             string     `json:"status"`
	MustChangePassword bool       `json:"mustChangePassword"`
	LastLoginAt        *time.Time `json:"lastLoginAt"`
	LastLoginIP        string     `json:"lastLoginIp"`
	CreatedAt          time.Time  `json:"createdAt"`
}

// ToUserProfile 转换为对外视图。
func ToUserProfile(u *model.PlatformUser) *UserProfile {
	if u == nil {
		return nil
	}
	return &UserProfile{
		ID:                 u.ID,
		Username:           u.Username,
		DisplayName:        u.DisplayName,
		Role:               u.Role,
		Status:             u.Status,
		MustChangePassword: u.MustChangePassword == 1,
		LastLoginAt:        u.LastLoginAt,
		LastLoginIP:        u.LastLoginIP,
		CreatedAt:          u.CreatedAt,
	}
}

// MeResult 当前用户信息 + 权限集合。
type MeResult struct {
	User        *UserProfile `json:"user"`
	Permissions []string     `json:"permissions"`
	Role        string       `json:"role"`
}

// Me 返回当前用户信息与权限集合。
func (s *AuthService) Me(u *model.PlatformUser) *MeResult {
	perms := auth.Permissions(u.Role)
	list := make([]string, 0, len(perms))
	for _, p := range perms {
		list = append(list, string(p))
	}
	return &MeResult{User: ToUserProfile(u), Permissions: list, Role: u.Role}
}

// SetupStatus 初始化状态。
type SetupStatus struct {
	Initialized bool  `json:"initialized"`
	UserCount   int64 `json:"userCount"`
	MockMode    bool  `json:"mockMode"`
}

// SetupStatus 查询是否已完成初始化。
func (s *AuthService) SetupStatus(ctx context.Context) (*SetupStatus, error) {
	n, err := s.users.Count(ctx)
	if err != nil {
		return nil, apperr.Internal("查询初始化状态失败")
	}
	return &SetupStatus{Initialized: n > 0, UserCount: n}, nil
}

// SetupInitInput 初始化管理员入参。
type SetupInitInput struct {
	Username    string
	DisplayName string
	Password    string
}

// SetupInit 创建首个管理员。
func (s *AuthService) SetupInit(ctx context.Context, in SetupInitInput) (*UserProfile, error) {
	n, err := s.users.Count(ctx)
	if err != nil {
		return nil, apperr.Internal("查询初始化状态失败")
	}
	if n > 0 {
		return nil, apperr.Conflict("平台已完成初始化，请直接登录")
	}
	username := strings.ToLower(strings.TrimSpace(in.Username))
	if !validUsername(username) {
		return nil, apperr.BadRequest("用户名需为 3~32 位字母、数字或下划线")
	}
	if err := auth.ValidatePasswordPolicy(in.Password); err != nil {
		return nil, apperr.BadRequest(err.Error())
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
		Role:         auth.RoleAdmin,
		Status:       "active",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := s.users.Create(ctx, u); err != nil {
		return nil, apperr.Internal("创建管理员失败")
	}
	_ = s.settings.Set(ctx, repo.SettingSetupCompleted, "true", nil)
	return ToUserProfile(u), nil
}

// ChangePassword 修改自己的密码；成功后 token_version+1 使其他会话立即失效。
func (s *AuthService) ChangePassword(ctx context.Context, u *model.PlatformUser, oldPassword, newPassword string) error {
	if !auth.VerifyPassword(u.PasswordHash, oldPassword) {
		return apperr.BadRequest("原密码不正确")
	}
	if err := auth.ValidatePasswordPolicy(newPassword); err != nil {
		return apperr.BadRequest(err.Error())
	}
	if oldPassword == newPassword {
		return apperr.BadRequest("新密码不能与原密码相同")
	}
	hash, err := auth.HashPassword(newPassword)
	if err != nil {
		return apperr.Internal("密码处理失败")
	}
	return s.users.UpdateFields(ctx, u.ID, map[string]any{
		"password_hash":        hash,
		"must_change_password": 0,
		"token_version":        u.TokenVersion + 1,
	})
}

// IssueTokenFor 为新密码场景重新签发 token（改密后前端需更新 token）。
func (s *AuthService) IssueTokenFor(ctx context.Context, userID int64) (string, time.Time, error) {
	u, err := s.users.GetByID(ctx, userID)
	if err != nil {
		return "", time.Time{}, apperr.NotFound("账号不存在")
	}
	return s.jwt.Issue(u.ID, u.Username, u.TokenVersion)
}

// EnsureDefaultAdmin 首启自动创建 admin/admin123（must_change_password=1）。
//
// 仅在库中没有任何平台用户时执行，重复启动不会重复创建。
func (s *AuthService) EnsureDefaultAdmin(ctx context.Context) (created bool, err error) {
	n, cerr := s.users.Count(ctx)
	if cerr != nil {
		return false, cerr
	}
	if n > 0 {
		return false, nil
	}
	hash, herr := auth.HashPassword(DefaultAdminPassword)
	if herr != nil {
		return false, herr
	}
	u := &model.PlatformUser{
		Username:           DefaultAdminUsername,
		DisplayName:        "平台管理员",
		PasswordHash:       hash,
		Role:               auth.RoleAdmin,
		Status:             "active",
		MustChangePassword: 1,
		CreatedAt:          time.Now(),
		UpdatedAt:          time.Now(),
	}
	if cerr := s.users.Create(ctx, u); cerr != nil {
		return false, cerr
	}
	_ = s.settings.Set(ctx, repo.SettingSetupCompleted, "true", nil)
	return true, nil
}
