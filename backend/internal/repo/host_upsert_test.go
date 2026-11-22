package repo

import (
	"context"
	"testing"
	"time"

	"github.com/tmp-platform/backend/internal/model"
)

// TestApplySnapshot_HostUpsertKeepsExisting 锁定多集群共享主机的落库语义。
//
// hosts 表按 IP 全局唯一：同一台机器可以同时属于多套 TiDB 集群。
// 若某套集群这次采不到硬件（值为空 / 0），快照落库绝不能把另一套集群刚写入的值抹掉，
// 否则主机页的架构/规格/用量会在两次采集之间反复「闪没」，是最容易回归的点。
func TestApplySnapshot_HostUpsertKeepsExisting(t *testing.T) {
	db := snapTestDB(t)
	r := NewClusterRepo(db)
	ctx := context.Background()

	c := &model.Cluster{
		Name: "tidb-a", NameLower: "tidb-a",
		TiUPHost: "10.0.0.1", TiUPUser: "dba", DBHost: "10.0.0.1", DBPort: 4000, DBUser: "root",
		Status: model.ClusterStatusUnknown, CollectStatus: model.ClusterStatusUnknown,
	}
	if err := db.Create(c).Error; err != nil {
		t.Fatalf("建集群失败: %v", err)
	}

	fetchedAt := time.Now().Add(-time.Minute)
	host := model.Host{
		IP: "10.9.9.9", Hostname: "node-9", CPUArch: "aarch64",
		CPUCores: 16, MemoryGB: 64, DiskTotalGB: 500,
		CPUUsagePct: 21.5, MemUsagePct: 42, DiskUsagePct: 66,
		ResourceFetchedAt: &fetchedAt,
	}
	// 第一次：写完整硬件值。
	if err := r.ApplySnapshot(ctx, &SnapshotWrite{
		ClusterID:     c.ID,
		Status:        model.ClusterStatusHealthy,
		CollectStatus: model.ClusterStatusHealthy,
		Success:       true,
		FetchedAt:     time.Now(),
		Hosts:         []model.Host{host},
	}); err != nil {
		t.Fatalf("首次落库失败: %v", err)
	}

	var first model.Host
	if err := db.Where("ip = ?", "10.9.9.9").First(&first).Error; err != nil {
		t.Fatalf("读取主机失败: %v", err)
	}
	if first.CPUCores != 16 || first.MemoryGB != 64 || first.CPUArch != "aarch64" {
		t.Fatalf("首次落库硬件值不正确: %+v", first)
	}

	// 第二次：另一个集群只采到 IP（硬件全空），不得覆盖已有值。
	if err := r.ApplySnapshot(ctx, &SnapshotWrite{
		ClusterID:     c.ID,
		Status:        model.ClusterStatusHealthy,
		CollectStatus: model.ClusterStatusHealthy,
		Success:       true,
		FetchedAt:     time.Now(),
		Hosts:         []model.Host{{IP: "10.9.9.9"}},
	}); err != nil {
		t.Fatalf("空值落库失败: %v", err)
	}

	var second model.Host
	if err := db.Where("ip = ?", "10.9.9.9").First(&second).Error; err != nil {
		t.Fatalf("读取主机失败: %v", err)
	}
	if second.CPUCores != 16 || second.MemoryGB != 64 || second.DiskTotalGB != 500 {
		t.Errorf("空值快照覆盖了已有的硬件规格: %+v", second)
	}
	if second.CPUArch != "aarch64" || second.Hostname != "node-9" {
		t.Errorf("空值快照覆盖了已有的 arch/hostname: %+v", second)
	}
	if second.CPUUsagePct != 21.5 || second.MemUsagePct != 42 || second.DiskUsagePct != 66 {
		t.Errorf("空值快照覆盖了已有的使用率: %+v", second)
	}
	// 资源采集时间不得被「没有硬件数据的批次」推进。
	if second.ResourceFetchedAt == nil || !second.ResourceFetchedAt.Equal(fetchedAt) {
		t.Errorf("resource_fetched_at 被空值快照推进: %v（期望 %v）", second.ResourceFetchedAt, fetchedAt)
	}

	// 第三次：带来新硬件值时应更新（只补有值的字段）。
	if err := r.ApplySnapshot(ctx, &SnapshotWrite{
		ClusterID:     c.ID,
		Status:        model.ClusterStatusHealthy,
		CollectStatus: model.ClusterStatusHealthy,
		Success:       true,
		FetchedAt:     time.Now(),
		Hosts: []model.Host{{
			IP: "10.9.9.9", CPUCores: 32, MemoryGB: 128,
			ResourceFetchedAt: &fetchedAt,
		}},
	}); err != nil {
		t.Fatalf("更新落库失败: %v", err)
	}
	var third model.Host
	if err := db.Where("ip = ?", "10.9.9.9").First(&third).Error; err != nil {
		t.Fatalf("读取主机失败: %v", err)
	}
	if third.CPUCores != 32 || third.MemoryGB != 128 {
		t.Errorf("有值的字段未被更新: %+v", third)
	}
	// 本次没给的字段保持原值。
	if third.CPUArch != "aarch64" || third.DiskTotalGB != 500 || third.Hostname != "node-9" {
		t.Errorf("本次未提供的字段被清空: %+v", third)
	}
}

// TestApplySnapshot_HostUpsertKeepsSSHStatus 验证快照落库不会覆盖 ssh_status。
func TestApplySnapshot_HostUpsertKeepsSSHStatus(t *testing.T) {
	db := snapTestDB(t)
	r := NewClusterRepo(db)
	ctx := context.Background()

	c := &model.Cluster{
		Name: "tidb-b", NameLower: "tidb-b",
		TiUPHost: "10.0.0.2", TiUPUser: "dba", DBHost: "10.0.0.2", DBPort: 4000, DBUser: "root",
		Status: model.ClusterStatusUnknown, CollectStatus: model.ClusterStatusUnknown,
	}
	if err := db.Create(c).Error; err != nil {
		t.Fatalf("建集群失败: %v", err)
	}
	h := &model.Host{IP: "10.9.9.10", SSHStatus: model.SSHStatusOK, CPUCores: 4}
	if err := db.Create(h).Error; err != nil {
		t.Fatalf("写入主机失败: %v", err)
	}

	if err := r.ApplySnapshot(ctx, &SnapshotWrite{
		ClusterID:     c.ID,
		Status:        model.ClusterStatusHealthy,
		CollectStatus: model.ClusterStatusHealthy,
		Success:       true,
		FetchedAt:     time.Now(),
		Hosts:         []model.Host{{IP: "10.9.9.10", CPUArch: "x86_64", CPUCores: 8}},
	}); err != nil {
		t.Fatalf("落库失败: %v", err)
	}

	var got model.Host
	if err := db.Where("ip = ?", "10.9.9.10").First(&got).Error; err != nil {
		t.Fatalf("读取主机失败: %v", err)
	}
	if got.SSHStatus != model.SSHStatusOK {
		t.Errorf("ssh_status 被快照覆盖: %q", got.SSHStatus)
	}
	if got.CPUArch != "x86_64" || got.CPUCores != 8 {
		t.Errorf("硬件字段未更新: %+v", got)
	}
}
