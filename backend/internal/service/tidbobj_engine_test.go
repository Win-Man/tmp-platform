package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/repo"
)

// TestKillSessions_EngineNotBound 锁定「漏注入任务引擎」时的行为。
//
// 历史 bug：main 装配期漏调 tidbObjSvc.SetEngine，Kill 会话在 nil engine 上
// 解引用直接 panic，接口返回 50001「服务内部错误」，用户只看到报错。
// 现在必须返回可读错误，而不是 panic。
func TestKillSessions_EngineNotBound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kill.db")
	gdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开临时库失败: %v", err)
	}
	if err := gdb.AutoMigrate(&model.Cluster{}, &model.ClusterCredential{}, &model.Session{}, &model.Job{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	clusters := repo.NewClusterRepo(gdb)
	ctx := context.Background()
	c := &model.Cluster{Name: "kill-demo", TiUPHost: "10.0.0.11", DBHost: "10.0.0.11", DBPort: 4000, DBUser: "root"}
	if err := clusters.Create(ctx, c); err != nil {
		t.Fatalf("建集群失败: %v", err)
	}

	// 不注入 engine，模拟装配遗漏。
	svc := NewTiDBObjService(clusters, repo.NewCredentialRepo(gdb), repo.NewTiDBObjRepo(gdb), nil, nil)

	_, kerr := svc.KillSessions(ctx, c.ID, []int64{1001}, nil)
	if kerr == nil {
		t.Fatal("未注入引擎时 KillSessions 应返回错误")
	}
	if !strings.Contains(kerr.Error(), "任务引擎未初始化") {
		t.Fatalf("错误信息应说明引擎未初始化，实际: %v", kerr)
	}
}
