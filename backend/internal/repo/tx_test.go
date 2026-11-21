package repo

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tmp-platform/backend/internal/model"
)

// txTestDB 在 t.TempDir 下建立独立 SQLite 库。
//
// 注意：本包不能 import internal/db（db → repo 会造成测试导入环），
// 因此这里直接复用生产 DSN 的三项 pragma（WAL / foreign_keys / busy_timeout），
// 只迁移本次并发测试用到的 settings 表。
func txTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "qa.db")
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开临时库失败: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("获取连接池失败: %v", err)
	}
	// 与生产一致：SQLite 单写者，限制连接数。
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)
	if err := gdb.AutoMigrate(&model.Setting{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	if err := gdb.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS uk_settings_key ON settings(key)`).Error; err != nil {
		t.Fatalf("创建唯一索引失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return gdb
}

// runConcurrentWrites 启动 n 个 goroutine 并发写同一库。
//
// 每个 goroutine 在自己的事务里做「先读后写 + 删除 + 插入」，即 Tx() 注释要求的
// 幂等操作，从而复现 deferred 事务的锁升级竞争（SQLITE_BUSY 的典型触发路径）。
func runConcurrentWrites(t *testing.T, gdb *gorm.DB, n int) {
	t.Helper()
	ctx := context.Background()

	var wg sync.WaitGroup
	errCh := make(chan error, n)
	start := make(chan struct{})

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			key := fmt.Sprintf("k%d", idx)
			err := Tx(ctx, gdb, func(tx *gorm.DB) error {
				// 先读后写：deferred 事务在此处尝试把读锁升级为写锁。
				var count int64
				if err := tx.Model(&model.Setting{}).Where("key = ?", key).Count(&count).Error; err != nil {
					return err
				}
				if err := tx.Where("key = ?", key).Delete(&model.Setting{}).Error; err != nil {
					return err
				}
				return tx.Create(&model.Setting{
					Key:       key,
					Value:     fmt.Sprintf("v%d-%d", idx, count),
					UpdatedAt: time.Now(),
				}).Error
			})
			if err != nil {
				errCh <- fmt.Errorf("goroutine %d: %w", idx, err)
			}
		}(i)
	}

	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("并发写出现失败（SQLITE_BUSY 未被消除）: %v", err)
		}
	}
}

// TestTx_ConcurrentWrites8 8 goroutine 并发写同一库不得出现 SQLITE_BUSY。
func TestTx_ConcurrentWrites8(t *testing.T) {
	gdb := txTestDB(t)
	runConcurrentWrites(t, gdb, 8)

	var count int64
	if err := gdb.Model(&model.Setting{}).Count(&count).Error; err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if count != 8 {
		t.Fatalf("写入后记录数 = %d, want 8", count)
	}
}

// TestTx_ConcurrentWrites16 16 goroutine 压测。
func TestTx_ConcurrentWrites16(t *testing.T) {
	gdb := txTestDB(t)
	runConcurrentWrites(t, gdb, 16)
}

// TestTx_ConcurrentWrites32 32 goroutine 压测（明显高于引擎 4 worker 的常规压力）。
func TestTx_ConcurrentWrites32(t *testing.T) {
	gdb := txTestDB(t)
	runConcurrentWrites(t, gdb, 32)
}

// TestTx_ConcurrentWrites8_Repeat 重复 5 轮 8 并发，用于观察抖动。
func TestTx_ConcurrentWrites8_Repeat(t *testing.T) {
	for round := 0; round < 5; round++ {
		gdb := txTestDB(t)
		runConcurrentWrites(t, gdb, 8)
	}
}

// TestTx_PropagatesNonBusyError 非 busy 错误必须原样返回，不被吞掉或无限重试。
func TestTx_PropagatesNonBusyError(t *testing.T) {
	gdb := txTestDB(t)
	sentinel := fmt.Errorf("业务错误，不应重试")
	err := Tx(context.Background(), gdb, func(tx *gorm.DB) error {
		return sentinel
	})
	if err == nil {
		t.Fatal("业务错误必须向上返回")
	}
	if err.Error() != sentinel.Error() {
		t.Fatalf("错误应原样返回，实际 %v", err)
	}
}

// TestIsBusyErr 锁冲突识别。
func TestIsBusyErr(t *testing.T) {
	cases := map[string]bool{
		"database is locked":       true,
		"SQLITE_BUSY":              true,
		"database table is locked": true,
		"UNIQUE constraint failed": false,
		"no such table: clusters":  false,
	}
	for msg, want := range cases {
		if got := isBusyErr(fmt.Errorf("%s", msg)); got != want {
			t.Fatalf("isBusyErr(%q) = %v, want %v", msg, got, want)
		}
	}
	if isBusyErr(nil) {
		t.Fatal("isBusyErr(nil) 应为 false")
	}
}
