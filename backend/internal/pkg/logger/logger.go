// Package logger 提供基于标准库 log/slog 的结构化日志封装，并内置脱敏能力。
package logger

import (
	"context"
	"log/slog"
	"os"
	"regexp"
	"strings"
)

// ctxKey 上下文键类型，避免与外部包冲突。
type ctxKey string

const loggerKey ctxKey = "tmp.logger"

// 敏感字段名关键字：命中即脱敏为 ***。
var sensitiveKeys = []string{"password", "passwd", "pwd", "secret", "token", "key", "credential", "authorization"}

// 连接串中 user:password@ 形态的脱敏正则。
var dsnCredRe = regexp.MustCompile(`([A-Za-z0-9_\-\.]+):([^@/\s]+)@`)

// Init 初始化全局 logger。
func Init(level string, appVersion string) *slog.Logger {
	var lv slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: lv,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{Key: "ts", Value: a.Value}
			}
			return a
		},
	}).WithAttrs([]slog.Attr{slog.String("app", "tmp"), slog.String("version", appVersion)})
	l := slog.New(h)
	slog.SetDefault(l)
	return l
}

// With 把 logger 放入 context。
func With(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, l)
}

// From 从 context 取 logger，缺失时返回默认 logger。
func From(ctx context.Context) *slog.Logger {
	if v, ok := ctx.Value(loggerKey).(*slog.Logger); ok && v != nil {
		return v
	}
	return slog.Default()
}

// Sanitize 对字符串做敏感信息脱敏（连接串中的账号密码）。
func Sanitize(s string) string {
	return dsnCredRe.ReplaceAllString(s, "$1:***@")
}

// SanitizeMap 对 map 做敏感字段脱敏，返回新 map（不修改入参）。
func SanitizeMap(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if isSensitiveKey(k) {
			out[k] = "***"
			continue
		}
		switch vv := v.(type) {
		case map[string]any:
			out[k] = SanitizeMap(vv)
		case string:
			out[k] = Sanitize(vv)
		default:
			out[k] = v
		}
	}
	return out
}

// isSensitiveKey 判断字段名是否敏感。
func isSensitiveKey(k string) bool {
	lk := strings.ToLower(k)
	for _, s := range sensitiveKeys {
		if strings.Contains(lk, s) {
			return true
		}
	}
	return false
}
