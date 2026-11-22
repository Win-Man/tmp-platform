package repo

import (
	"context"
	"testing"
	"time"

	"github.com/tmp-platform/backend/internal/model"
)

// TestReplaceResourceGroups 锁定「实时刷新写回 Resource Group 缓存」的语义：
// 整批替换（不残留旧 RG）、空结果清空、字段（ru/priority/burstable/userCount）一并落库。
func TestReplaceResourceGroups(t *testing.T) {
	gdb := snapTestDB(t)
	r := NewTiDBObjRepo(gdb)
	ctx := context.Background()

	const clusterID int64 = 7
	now := time.Now()
	first := []model.ResourceGroup{
		{Name: "default", RUPerSec: "UNLIMITED", Priority: "MEDIUM", Burstable: 0, UserCount: 4, Status: "running", FetchedAt: now, CreatedAt: now},
		{Name: "rg_olap", RUPerSec: "2000", Priority: "LOW", Burstable: 1, UserCount: 2, Status: "running", FetchedAt: now, CreatedAt: now},
	}
	if err := r.ReplaceResourceGroups(ctx, clusterID, first); err != nil {
		t.Fatalf("首次写入失败: %v", err)
	}

	var list []model.ResourceGroup
	if err := gdb.Where("cluster_id = ?", clusterID).Order("name ASC").Find(&list).Error; err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("RG 数 = %d，期望 2", len(list))
	}
	if list[1].Name != "rg_olap" || list[1].Burstable != 1 || list[1].UserCount != 2 {
		t.Fatalf("rg_olap 落库异常: %+v", list[1])
	}

	// 第二批只剩一条：旧数据必须被整体替换掉（删除 RG 后刷新应消失）。
	second := []model.ResourceGroup{
		{Name: "default", RUPerSec: "UNLIMITED", Priority: "MEDIUM", Burstable: 0, UserCount: 1, Status: "running", FetchedAt: now, CreatedAt: now},
	}
	if err := r.ReplaceResourceGroups(ctx, clusterID, second); err != nil {
		t.Fatalf("二次写入失败: %v", err)
	}
	list = nil
	if err := gdb.Where("cluster_id = ?", clusterID).Find(&list).Error; err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if len(list) != 1 || list[0].Name != "default" {
		t.Fatalf("替换后 = %+v，期望仅剩 default", list)
	}

	// 空结果同样要清空缓存。
	if err := r.ReplaceResourceGroups(ctx, clusterID, nil); err != nil {
		t.Fatalf("清空失败: %v", err)
	}
	var n int64
	if err := gdb.Model(&model.ResourceGroup{}).Where("cluster_id = ?", clusterID).Count(&n).Error; err != nil {
		t.Fatalf("计数失败: %v", err)
	}
	if n != 0 {
		t.Fatalf("清空后仍有 %d 条", n)
	}
}
