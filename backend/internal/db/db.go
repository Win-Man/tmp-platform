// Package db 负责打开 SQLite 元数据库、执行迁移与内置数据灌入。
package db

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Open 打开 SQLite 元数据库。
//
// DSN 必须携带 WAL / foreign_keys / busy_timeout 三项 pragma，缺一不可；
// 另外固定 _txlock=immediate：写事务在 BEGIN 时即取写锁，busy_timeout 才能生效，
// 避免「先读后写」的 deferred 事务在锁升级阶段直接 SQLITE_BUSY。
func Open(path string, log *slog.Logger) (*gorm.DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, fmt.Errorf("创建元数据库目录失败: %w", err)
	}
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate"

	gormLogLevel := logger.Warn
	if log != nil {
		// 生产环境不打印 SQL，避免泄露查询参数。
		gormLogLevel = logger.Warn
	}
	gormLogger := logger.New(logWriter(log), logger.Config{
		SlowThreshold:             500 * time.Millisecond,
		LogLevel:                  gormLogLevel,
		IgnoreRecordNotFoundError: true, // First() 未命中的 record not found 不是错误
		Colorful:                  false,
	})
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger:                                   gormLogger,
		DisableForeignKeyConstraintWhenMigrating: false,
		NowFunc:                                  func() time.Time { return time.Now() },
	})
	if err != nil {
		return nil, fmt.Errorf("打开元数据库失败: %w", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, fmt.Errorf("获取连接池失败: %w", err)
	}
	// SQLite 单写者；限制连接数避免 SQLITE_BUSY 抖动。
	sqlDB.SetMaxOpenConns(4)
	sqlDB.SetMaxIdleConns(4)
	sqlDB.SetConnMaxLifetime(0)
	if err = sqlDB.Ping(); err != nil {
		return nil, fmt.Errorf("元数据库不可用: %w", err)
	}
	return gdb, nil
}

// Ping 探活，供 /readyz 使用。
func Ping(gdb *gorm.DB) error {
	sqlDB, err := gdb.DB()
	if err != nil {
		return err
	}
	return sqlDB.Ping()
}

// gormWriter 把 GORM 日志转发到 slog，保持全局日志格式统一。
type gormWriter struct{ log *slog.Logger }

// Printf 实现 gorm logger.Writer。
func (w gormWriter) Printf(format string, args ...any) {
	w.log.Warn(strings.TrimSpace(fmt.Sprintf(format, args...)))
}

// logWriter 构造 GORM 日志写出器。
func logWriter(l *slog.Logger) logger.Writer {
	if l == nil {
		l = slog.Default()
	}
	return gormWriter{log: l}
}
