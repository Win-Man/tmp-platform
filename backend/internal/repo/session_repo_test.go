package repo

import (
	"context"
	"testing"
	"time"

	"github.com/tmp-platform/backend/internal/model"
)

// TestReplaceSessions 锁定「实时刷新写回会话缓存」的语义：
// 整批替换（不残留旧会话）、空结果清空、instance 一并落库。
func TestReplaceSessions(t *testing.T) {
	gdb := snapTestDB(t)
	r := NewTiDBObjRepo(gdb)
	ctx := context.Background()

	const clusterID int64 = 7
	now := time.Now()
	first := []model.Session{
		{SessionID: 101, Instance: "10.0.0.1:4000", UserName: "app", FetchedAt: now, CreatedAt: now},
		{SessionID: 102, Instance: "10.0.0.2:4000", UserName: "bi", FetchedAt: now, CreatedAt: now},
	}
	if err := r.ReplaceSessions(ctx, clusterID, first); err != nil {
		t.Fatalf("首次写入失败: %v", err)
	}

	var list []model.Session
	if err := gdb.Where("cluster_id = ?", clusterID).Order("session_id ASC").Find(&list).Error; err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("会话数 = %d，期望 2", len(list))
	}
	if list[1].Instance != "10.0.0.2:4000" {
		t.Fatalf("instance = %q，期望 10.0.0.2:4000", list[1].Instance)
	}

	// 第二批只剩一条：旧数据必须被整体替换掉。
	second := []model.Session{
		{SessionID: 201, Instance: "10.0.0.1:4000", UserName: "root", FetchedAt: now, CreatedAt: now},
	}
	if err := r.ReplaceSessions(ctx, clusterID, second); err != nil {
		t.Fatalf("二次写入失败: %v", err)
	}
	list = nil
	if err := gdb.Where("cluster_id = ?", clusterID).Find(&list).Error; err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if len(list) != 1 || list[0].SessionID != 201 {
		t.Fatalf("替换后 = %+v，期望仅剩 201", list)
	}

	// 空结果表示「当前没有会话」，同样要清空缓存。
	if err := r.ReplaceSessions(ctx, clusterID, nil); err != nil {
		t.Fatalf("清空失败: %v", err)
	}
	var n int64
	if err := gdb.Model(&model.Session{}).Where("cluster_id = ?", clusterID).Count(&n).Error; err != nil {
		t.Fatalf("计数失败: %v", err)
	}
	if n != 0 {
		t.Fatalf("清空后仍有 %d 条", n)
	}
}
