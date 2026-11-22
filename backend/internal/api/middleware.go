package api

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tmp-platform/backend/internal/auth"
	"github.com/tmp-platform/backend/internal/pkg/apperr"
	"github.com/tmp-platform/backend/internal/pkg/logger"
)

// RequestIDMiddleware 注入 / 回写 X-Request-Id，并放入 context 供日志与审计串联。
func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader("X-Request-Id")
		if rid == "" {
			rid = newRequestID()
		}
		c.Set(auth.CtxRequestIDKey, rid)
		c.Header("X-Request-Id", rid)
		c.Next()
	}
}

// newRequestID 生成 16 字节随机 ID。
func newRequestID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// LoggerWith 结构化访问日志中间件；密码类字段不落日志。
func LoggerWith(base *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		rid := RequestID(c)
		l := base.With("request_id", rid)
		// 把 logger 注入请求上下文，service 层可直接取用。
		ctx := logger.With(c.Request.Context(), l)
		c.Request = c.Request.WithContext(ctx)

		c.Next()

		duration := time.Since(start)
		status := c.Writer.Status()
		attrs := []any{
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", status,
			"duration_ms", duration.Milliseconds(),
			"ip", c.ClientIP(),
		}
		if u := auth.CurrentUser(c); u != nil {
			attrs = append(attrs, "user", u.Username, "role", u.Role)
		}
		if denied, ok := c.Get("tmp.denied_action"); ok {
			attrs = append(attrs, "denied_action", denied)
		}
		switch {
		case status >= 500:
			l.Error("http", attrs...)
		case status >= 400:
			l.Warn("http", attrs...)
		default:
			l.Info("http", attrs...)
		}
	}
}

// RecoveryMiddleware 兜底 panic，返回 50001 且不暴露堆栈。
func RecoveryMiddleware(log *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("请求处理 panic",
					"panic", fmt.Sprint(rec),
					"path", c.Request.URL.Path,
					"stack", string(debug.Stack()))
				c.AbortWithStatusJSON(http.StatusInternalServerError, Response{
					Code:    apperr.CodeInternal,
					Message: "服务内部错误，请稍后重试",
					Data:    nil,
				})
			}
		}()
		c.Next()
	}
}

// SecurityHeadersMiddleware 添加基础安全响应头。
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	}
}

// NoCacheMiddleware 禁止浏览器缓存接口响应。
//
// 集群状态、采集快照这类数据随时可能变化，一旦被缓存（含代理复用），
// 用户会看到「点了刷新还是旧数据」，必须整页 F5 才能更新。
func NoCacheMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store, no-cache, must-revalidate")
		c.Header("Pragma", "no-cache")
		c.Header("Expires", "0")
		c.Next()
	}
}

// RequestID 从上下文取请求 ID。
func RequestID(c *gin.Context) string {
	if v, ok := c.Get(auth.CtxRequestIDKey); ok {
		if s, ok2 := v.(string); ok2 {
			return s
		}
	}
	return ""
}
