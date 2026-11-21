package auth

import (
	"context"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/pkg/apperr"
)

// 上下文键。
const (
	CtxUserKey      = "tmp.current_user"
	CtxRequestIDKey = "tmp.request_id"
)

// UserLoader 由 repo 层实现，用于按 ID 加载用户以校验 token_version。
type UserLoader interface {
	GetByID(ctx context.Context, id int64) (*model.PlatformUser, error)
}

// Middleware JWT 认证中间件依赖。
type Middleware struct {
	Manager *Manager
	Users   UserLoader
}

// NewMiddleware 构造认证中间件。
func NewMiddleware(m *Manager, users UserLoader) *Middleware {
	return &Middleware{Manager: m, Users: users}
}

// Authenticate 校验 Bearer token：签名、过期时间、token_version 三重校验。
//
// token_version 与平台用户记录比对，改密/禁用后旧 token 立即失效（不使用内存黑名单）。
func (mw *Middleware) Authenticate() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := bearerToken(c)
		if raw == "" {
			abortUnauthorized(c, "缺少认证信息")
			return
		}
		claims, err := mw.Manager.Parse(raw)
		if err != nil {
			abortUnauthorized(c, "登录已过期，请重新登录")
			return
		}
		uid := claims.UserID()
		if uid <= 0 {
			abortUnauthorized(c, "登录已过期，请重新登录")
			return
		}
		user, err := mw.Users.GetByID(c.Request.Context(), uid)
		if err != nil || user == nil {
			abortUnauthorized(c, "账号不存在")
			return
		}
		if !user.IsActive() {
			abortUnauthorized(c, "账号已被禁用")
			return
		}
		if claims.Ver != user.TokenVersion {
			abortUnauthorized(c, "登录状态已失效，请重新登录")
			return
		}
		c.Set(CtxUserKey, user)
		c.Next()
	}
}

// Require 声明某端点所需的操作权限（每个端点独立声明）。
func Require(action Action) gin.HandlerFunc {
	return func(c *gin.Context) {
		u := CurrentUser(c)
		if u == nil {
			abortUnauthorized(c, "缺少认证信息")
			return
		}
		if !Allows(u.Role, action) {
			// 标记越权，供审计中间件记录失败结果。
			c.Set("tmp.denied_action", string(action))
			c.AbortWithStatusJSON(403, gin.H{
				"code":    apperr.CodeForbidden,
				"message": "无权限执行该操作",
				"data":    nil,
			})
			return
		}
		c.Next()
	}
}

// MustChangePasswordAllowList 强制改密期间仍可访问的路径白名单。
//
// 只保留「查自己 / 改密码 / 退出登录 / 探针」四类，其余业务接口一律拒绝，
// 避免用户跳过强制改密后继续使用平台。
var MustChangePasswordAllowList = map[string]bool{
	"/api/v1/auth/me":       true,
	"/api/v1/auth/password": true,
	"/api/v1/auth/logout":   true,
	"/healthz":              true,
	"/readyz":               true,
}

// MustChangePasswordGuard 强制改密守卫，必须挂在 Authenticate 之后。
//
// platform_users.must_change_password=1 时，除白名单外的请求统一返回
// HTTP 403 + {"code":40302,"message":"请先修改初始密码"}。
func MustChangePasswordGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		u := CurrentUser(c)
		if u == nil || u.MustChangePassword != 1 {
			c.Next()
			return
		}
		if MustChangePasswordAllowList[c.Request.URL.Path] {
			c.Next()
			return
		}
		c.AbortWithStatusJSON(403, gin.H{
			"code":    apperr.CodeMustChangePassword,
			"message": "请先修改初始密码",
			"data":    nil,
		})
	}
}

// CurrentUser 从上下文取当前登录用户。
func CurrentUser(c *gin.Context) *model.PlatformUser {
	if v, ok := c.Get(CtxUserKey); ok {
		if u, ok2 := v.(*model.PlatformUser); ok2 {
			return u
		}
	}
	return nil
}

// bearerToken 从 Authorization 头或 token 查询参数提取 token。
func bearerToken(c *gin.Context) string {
	h := c.GetHeader("Authorization")
	if h != "" {
		parts := strings.SplitN(h, " ", 2)
		if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
			return strings.TrimSpace(parts[1])
		}
	}
	// 下载类接口允许通过 query 传递 token（浏览器直接打开下载链接的场景）。
	if t := c.Query("token"); t != "" {
		return t
	}
	return ""
}

// abortUnauthorized 统一输出 40101。
func abortUnauthorized(c *gin.Context, msg string) {
	c.AbortWithStatusJSON(401, gin.H{
		"code":    apperr.CodeUnauthorized,
		"message": msg,
		"data":    nil,
	})
}
