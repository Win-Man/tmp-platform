// Package retry 提供指数退避重试与网络类错误判定。
package retry

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"
)

// ErrCanceled 上下文取消。
var ErrCanceled = errors.New("retry: 已取消")

// Do 执行 fn，失败时按指数退避重试，仅对可重试错误重试。
//
// attempts 为总尝试次数（>=1）；baseDelay 为首次退避时长，之后翻倍。
func Do(ctx context.Context, attempts int, baseDelay time.Duration, fn func() error) error {
	if attempts < 1 {
		attempts = 1
	}
	if baseDelay <= 0 {
		baseDelay = 2 * time.Second
	}
	var lastErr error
	delay := baseDelay
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			return ErrCanceled
		}
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if !IsRetryable(lastErr) {
			return lastErr
		}
		if i == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ErrCanceled
		case <-time.After(delay):
		}
		delay *= 2
	}
	return lastErr
}

// IsRetryable 判断错误是否属于「网络类」可重试错误；认证类错误不重试。
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	// 认证 / 权限类错误不重试，重试也无意义。
	nonRetryable := []string{
		"access denied", "authentication", "auth failed", "no password",
		"permission denied", "unknown user", "invalid password", "无法认证",
	}
	for _, s := range nonRetryable {
		if strings.Contains(msg, s) {
			return false
		}
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	retryable := []string{
		"timeout", "timed out", "connection refused", "connection reset",
		"broken pipe", "no route to host", "i/o timeout", "network is unreachable",
		"eof", "temporarily unavailable", "too many connections",
	}
	for _, s := range retryable {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}
