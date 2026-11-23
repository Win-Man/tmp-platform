package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/pkg/apperr"
	"github.com/tmp-platform/backend/internal/repo"
)

// addrMapTestEnv 建一个只含集群 / 映射两张表的服务实例。
func addrMapTestEnv(t *testing.T) (*AddrMapService, *gorm.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "addrmap.db")
	gdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开临时库失败: %v", err)
	}
	if err := gdb.AutoMigrate(&model.Cluster{}, &model.ClusterAddrMap{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	clusterRepo := repo.NewClusterRepo(gdb)
	if err := clusterRepo.Create(context.Background(), &model.Cluster{
		Name: "c1", NameLower: "c1", DeployMode: model.DeployModeUnknown,
		Status: model.ClusterStatusUnknown, CollectStatus: "unknown",
		TiUPHost: "158.1.9.10", TiUPUser: "tidb", DBHost: "158.1.9.11", DBUser: "root",
	}); err != nil {
		t.Fatalf("建集群失败: %v", err)
	}
	if err := clusterRepo.Create(context.Background(), &model.Cluster{
		Name: "c2", NameLower: "c2", DeployMode: model.DeployModeUnknown,
		Status: model.ClusterStatusUnknown, CollectStatus: "unknown",
		TiUPHost: "158.1.9.20", TiUPUser: "tidb", DBHost: "158.1.9.21", DBUser: "root",
	}); err != nil {
		t.Fatalf("建集群失败: %v", err)
	}
	return NewAddrMapService(repo.NewAddrMapRepo(gdb), clusterRepo), gdb
}

// errCode 取出 apperr 的业务码，便于断言具体错误类型。
func errCode(err error) int {
	var ae *apperr.Error
	if errors.As(err, &ae) {
		return ae.Code
	}
	return 0
}

// TestAddrMapCRUD 覆盖新建 → 判重 → 修改 → 删除的完整闭环。
func TestAddrMapCRUD(t *testing.T) {
	svc, _ := addrMapTestEnv(t)
	ctx := context.Background()

	list, err := svc.List(ctx, 1)
	if err != nil {
		t.Fatalf("List 报错: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("初始应为空，实际 %d 条", len(list))
	}

	// 简写前缀应被规范化成 CIDR，保证存储与展示口径一致。
	m, err := svc.Create(ctx, 1, AddrMapInput{Source: "10.10.9.", Target: "158.1.9."}, nil)
	if err != nil {
		t.Fatalf("Create 报错: %v", err)
	}
	if m.Source != "10.10.9.0/24" || m.Target != "158.1.9.0/24" {
		t.Fatalf("未规范化：%s → %s", m.Source, m.Target)
	}
	if m.Enabled != 1 {
		t.Fatal("默认应为启用")
	}

	// 同一源地址（不同写法命中同一网段）应判重。
	if _, err := svc.Create(ctx, 1, AddrMapInput{Source: "10.10.9.0/24", Target: "172.16.9.0/24"}, nil); errCode(err) != apperr.CodeConflict {
		t.Fatalf("重复源地址应返回 409，实际 %v", err)
	}

	// 精确 IP 规则可与网段规则共存（不同源）。
	if _, err := svc.Create(ctx, 1, AddrMapInput{Source: "10.10.9.11", Target: "158.1.9.21"}, nil); err != nil {
		t.Fatalf("精确 IP 规则应允许共存: %v", err)
	}

	// 修改：网段改成另一个网段。
	enabled := false
	updated, err := svc.Update(ctx, 1, m.ID, AddrMapInput{Source: "10.10.8.0/24", Target: "158.1.8.0/24", Note: "备用平面", Enabled: &enabled})
	if err != nil {
		t.Fatalf("Update 报错: %v", err)
	}
	if updated.Enabled != 0 {
		t.Fatal("应被停用")
	}

	// 停用后不参与映射。
	preview, err := svc.Preview(ctx, 1, "10.10.8.5:2379")
	if err != nil {
		t.Fatalf("Preview 报错: %v", err)
	}
	if preview.Matched {
		t.Fatal("停用规则不应命中")
	}

	// 精确规则仍生效。
	preview, err = svc.Preview(ctx, 1, "http://10.10.9.11:3000/d/xx")
	if err != nil {
		t.Fatalf("Preview 报错: %v", err)
	}
	if !preview.Matched || preview.Mapped != "http://158.1.9.21:3000/d/xx" {
		t.Fatalf("试算结果不符：%+v", preview)
	}

	if err := svc.Delete(ctx, 1, m.ID); err != nil {
		t.Fatalf("Delete 报错: %v", err)
	}
	list, _ = svc.List(ctx, 1)
	if len(list) != 1 {
		t.Fatalf("删除后应剩 1 条，实际 %d", len(list))
	}
}

// TestAddrMapCrossClusterIsolation 确认规则 ID 全局唯一时不能跨集群改删。
//
// 这是最容易漏的越权点：路径里的 :id 是当前集群，:mapId 是规则，
// 若不校验归属，A 集群的管理员就能改掉 B 集群的映射规则。
func TestAddrMapCrossClusterIsolation(t *testing.T) {
	svc, _ := addrMapTestEnv(t)
	ctx := context.Background()

	m, err := svc.Create(ctx, 2, AddrMapInput{Source: "10.20.9.0/24", Target: "158.2.9.0/24"}, nil)
	if err != nil {
		t.Fatalf("Create 报错: %v", err)
	}

	if _, err := svc.Update(ctx, 1, m.ID, AddrMapInput{Source: "10.20.9.0/24", Target: "1.1.1.0/24"}); errCode(err) != apperr.CodeNotFound {
		t.Fatalf("跨集群修改应 404，实际 %v", err)
	}
	if err := svc.Delete(ctx, 1, m.ID); errCode(err) != apperr.CodeNotFound {
		t.Fatalf("跨集群删除应 404，实际 %v", err)
	}
	// 原规则必须仍在。
	list, _ := svc.List(ctx, 2)
	if len(list) != 1 {
		t.Fatalf("原规则应保留，实际 %d 条", len(list))
	}
}

// TestAddrMapInvalidRule 确认非法规则在保存阶段就被拒绝（而不是静默丢弃）。
func TestAddrMapInvalidRule(t *testing.T) {
	svc, _ := addrMapTestEnv(t)
	ctx := context.Background()

	cases := []AddrMapInput{
		{Source: "", Target: "158.1.9.21"},
		{Source: "10.10.9.0/24", Target: "158.1.9.21"}, // 网段 → 单 IP
		{Source: "10.10.0.0/16", Target: "158.1.9.0/24"},
		{Source: "bad", Target: "worse"},
	}
	for _, in := range cases {
		if _, err := svc.Create(ctx, 1, in, nil); errCode(err) != apperr.CodeBadRequest {
			t.Fatalf("%+v 应返回 400，实际 %v", in, err)
		}
	}
}

// TestAddrMapMapperSkipsDisabled 确认构建映射器时只取启用规则。
func TestAddrMapMapperSkipsDisabled(t *testing.T) {
	svc, _ := addrMapTestEnv(t)
	ctx := context.Background()

	off := false
	if _, err := svc.Create(ctx, 1, AddrMapInput{Source: "10.10.9.0/24", Target: "158.1.9.0/24", Enabled: &off}, nil); err != nil {
		t.Fatalf("Create 报错: %v", err)
	}
	if mp := svc.Mapper(ctx, 1); !mp.Empty() {
		t.Fatal("停用规则不应进入映射器")
	}

	if _, err := svc.Create(ctx, 1, AddrMapInput{Source: "10.10.8.0/24", Target: "158.1.8.0/24"}, nil); err != nil {
		t.Fatalf("Create 报错: %v", err)
	}
	mp := svc.Mapper(ctx, 1)
	if got := mp.MapString("10.10.8.7:2379"); got != "158.1.8.7:2379" {
		t.Fatalf("映射结果不符：%q", got)
	}
	if got := mp.MapString("10.10.9.7:2379"); got != "10.10.9.7:2379" {
		t.Fatalf("停用规则不应生效：%q", got)
	}
}
