package repo

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tmp-platform/backend/internal/model"
)

// snapTestDB 建一个只含快照落库相关表的临时库。
func snapTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "snap.db")
	dsn := path + "?_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)"
	gdb, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开临时库失败: %v", err)
	}
	if err := gdb.AutoMigrate(
		&model.Cluster{}, &model.Host{}, &model.Instance{},
		&model.TiDBUser{}, &model.ResourceGroup{}, &model.Session{}, &model.Variable{},
	); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return gdb
}

// TestApplySnapshot_SourceAndWarnings 锁定「采集来源」与「交叉校验告警」的落库语义。
//
// 两个关键点：
//  1. 来源要写进 clusters.collect_source，界面才能标注数据从哪来；
//  2. 告警为空时必须把旧值清空，否则上一次的「状态存疑」提示会一直挂着。
func TestApplySnapshot_SourceAndWarnings(t *testing.T) {
	db := snapTestDB(t)
	r := NewClusterRepo(db)
	ctx := context.Background()

	c := &model.Cluster{
		Name: "tidb-demo", NameLower: "tidb-demo",
		TiUPHost: "10.0.0.1", TiUPUser: "dba", DBHost: "10.0.0.1", DBPort: 4000, DBUser: "root",
		Status: model.ClusterStatusUnknown, CollectStatus: model.ClusterStatusUnknown,
	}
	if err := db.Create(c).Error; err != nil {
		t.Fatalf("建集群失败: %v", err)
	}

	now := time.Now()
	if err := r.ApplySnapshot(ctx, &SnapshotWrite{
		ClusterID:     c.ID,
		Status:        model.ClusterStatusDegraded,
		CollectStatus: model.ClusterStatusHealthy,
		Success:       true,
		Source:        "display_json",
		Warnings:      []string{"PD 接口实测可访问，但 TiUP 报告 pd 状态非 up", "MySQL 4000 实测可连接"},
		FetchedAt:     now,
	}); err != nil {
		t.Fatalf("落库失败: %v", err)
	}

	var got model.Cluster
	if err := db.First(&got, c.ID).Error; err != nil {
		t.Fatalf("读取集群失败: %v", err)
	}
	if got.CollectSource != "display_json" {
		t.Fatalf("collect_source = %q, want display_json", got.CollectSource)
	}
	if want := `["PD 接口实测可访问，但 TiUP 报告 pd 状态非 up","MySQL 4000 实测可连接"]`; got.CollectWarnings != want {
		t.Fatalf("collect_warnings = %q, want %q", got.CollectWarnings, want)
	}

	// 第二轮：没有矛盾时必须清空旧告警。
	if err := r.ApplySnapshot(ctx, &SnapshotWrite{
		ClusterID:     c.ID,
		Status:        model.ClusterStatusHealthy,
		CollectStatus: model.ClusterStatusHealthy,
		Success:       true,
		Source:        "display_json",
		Warnings:      nil,
		FetchedAt:     now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("二次落库失败: %v", err)
	}
	if err := db.First(&got, c.ID).Error; err != nil {
		t.Fatalf("读取集群失败: %v", err)
	}
	if got.CollectWarnings != "" {
		t.Fatalf("告警应被清空，实际仍为 %q", got.CollectWarnings)
	}

	// 第三轮：来源为空时（例如整体采集失败）不应覆盖已有的来源标注。
	if err := r.ApplySnapshot(ctx, &SnapshotWrite{
		ClusterID: c.ID, Status: model.ClusterStatusError, CollectStatus: model.ClusterStatusError,
		Success: false, Source: "", FetchedAt: now.Add(2 * time.Minute),
	}); err != nil {
		t.Fatalf("三次落库失败: %v", err)
	}
	if err := db.First(&got, c.ID).Error; err != nil {
		t.Fatalf("读取集群失败: %v", err)
	}
	if got.CollectSource != "display_json" {
		t.Fatalf("空来源不应覆盖历史来源，实际 %q", got.CollectSource)
	}
}
