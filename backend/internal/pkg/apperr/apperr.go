// Package apperr 定义带错误码的领域错误，供 service 层返回并由 api 层统一转换为
// {code,message,data} 响应。错误码定义严格对齐 ARCHITECTURE §5.2。
package apperr

import (
	"errors"
	"fmt"
	"net/http"
)

// 统一错误码常量（与 ARCHITECTURE §5.2 一一对应）。
const (
	CodeOK           = 0
	CodeBadRequest   = 40001
	CodeUnauthorized = 40101
	CodeForbidden    = 40301
	// CodeMustChangePassword 首次登录未改初始密码，仅放行改密相关接口。
	CodeMustChangePassword = 40302
	CodeNotFound           = 40401
	CodeConflict           = 40901
	CodeTooManyRequest     = 42901
	CodeInternal           = 50001
	CodeCollectFailed      = 50201
)

// Error 带 HTTP 状态码与业务码的错误。
type Error struct {
	Code    int    `json:"code"`
	HTTP    int    `json:"-"`
	Message string `json:"message"`
	// Data 附加数据，如 {"lockedUntil": "..."} 或 {"channel":"ssh","reason":"..."}。
	Data any `json:"data,omitempty"`
}

// Error 实现 error 接口。
func (e *Error) Error() string { return fmt.Sprintf("%d: %s", e.Code, e.Message) }

// New 构造一个错误。
func New(code, httpStatus int, message string) *Error {
	return &Error{Code: code, HTTP: httpStatus, Message: message}
}

// WithData 附加数据并返回自身，便于链式构造。
func (e *Error) WithData(data any) *Error {
	cp := *e
	cp.Data = data
	return &cp
}

// BadRequest 参数错误。
func BadRequest(msg string) *Error { return New(CodeBadRequest, http.StatusBadRequest, msg) }

// Unauthorized 未认证。
func Unauthorized(msg string) *Error {
	return New(CodeUnauthorized, http.StatusUnauthorized, msg)
}

// Forbidden 无权限。
func Forbidden(msg string) *Error { return New(CodeForbidden, http.StatusForbidden, msg) }

// MustChangePassword 未完成初始密码修改。
func MustChangePassword(msg string) *Error {
	return New(CodeMustChangePassword, http.StatusForbidden, msg)
}

// NotFound 资源不存在。
func NotFound(msg string) *Error { return New(CodeNotFound, http.StatusNotFound, msg) }

// Conflict 版本不支持 / 状态冲突。
func Conflict(msg string) *Error { return New(CodeConflict, http.StatusConflict, msg) }

// TooMany 限流或锁定。
func TooMany(msg string) *Error { return New(CodeTooManyRequest, http.StatusTooManyRequests, msg) }

// Internal 内部错误。
func Internal(msg string) *Error { return New(CodeInternal, http.StatusInternalServerError, msg) }

// CollectFailed 采集失败。
func CollectFailed(msg string, data any) *Error {
	return New(CodeCollectFailed, http.StatusBadGateway, msg).WithData(data)
}

// From 把任意 error 归一为 *Error；未知错误按内部错误处理（不暴露细节）。
func From(err error) *Error {
	if err == nil {
		return nil
	}
	var ae *Error
	if errors.As(err, &ae) {
		return ae
	}
	return Internal(err.Error())
}
