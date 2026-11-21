package repo

import (
	"context"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

// writeMu 串行化进程内的 SQLite 写事务（吞吐优化，不承担正确性）。
//
// 正确性由 DSN 的 _txlock=immediate 保证：写事务在 BEGIN 时即取写锁，
// busy_timeout 因此对锁等待生效，不会出现 deferred 事务「锁升级失败」。
// writeMu 的作用是减少同进程内并发事务互相等待的时间片浪费，提高吞吐。
var writeMu sync.Mutex

// busyRetries busy 重试次数。
const busyRetries = 5

// busyBackoff busy 重试基础退避（线性退避：第 n 次重试等待 n*50ms）。
const busyBackoff = 50 * time.Millisecond

// Tx 在写锁 + busy 重试保护下执行一个事务。
//
// 退避策略为线性退避（attempt*50ms，最多 5 次），配合 _txlock=immediate 的
// busy_timeout 等待，足以覆盖采集任务并发落库的场景。
// fn 必须可重入（重试时会重复执行），因此内部只做「删除 + 插入 / 赋值」这类幂等操作。
func Tx(ctx context.Context, db *gorm.DB, fn func(tx *gorm.DB) error) error {
	ctx = context.WithoutCancel(ctx)
	writeMu.Lock()
	defer writeMu.Unlock()

	var lastErr error
	for attempt := 0; attempt < busyRetries; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * busyBackoff):
			}
		}
		err := db.WithContext(ctx).Transaction(fn)
		if err == nil {
			return nil
		}
		if !isBusyErr(err) {
			return err
		}
		lastErr = err
	}
	return lastErr
}

// isBusyErr 判断是否为 SQLite 写锁冲突。
func isBusyErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "database is locked") ||
		strings.Contains(msg, "sqlite_busy") ||
		strings.Contains(msg, "database table is locked")
}
