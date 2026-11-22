package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/repo"
)

func topoTestService(t *testing.T) (*ClusterService, int64) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "topo.db")
	gdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开临时库失败: %v", err)
	}
	if err := gdb.AutoMigrate(&model.Cluster{}, &model.Host{}, &model.Instance{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}

	ctx := context.Background()
	c := &model.Cluster{
		Name: "tidb-demo", NameLower: "tidb-demo",
		TiUPHost: "10.0.0.1", TiUPUser: "dba", DBHost: "10.0.0.9", DBPort: 4000, DBUser: "root",
		Status: model.ClusterStatusUnknown, CollectStatus: model.ClusterStatusUnknown,
	}
	if err := gdb.WithContext(ctx).Create(c).Error; err != nil {
		t.Fatalf("写入集群失败: %v", err)
	}

	hostA := &model.Host{IP: "10.0.0.10", Hostname: "tidb02", SSHStatus: model.SSHStatusOK, CPUCores: 8, MemoryGB: 16}
	hostB := &model.Host{IP: "10.0.0.9", Hostname: "tidb01", SSHStatus: model.SSHStatusFail, CPUCores: 4, MemoryGB: 8}
	if err := gdb.WithContext(ctx).Create(hostA).Error; err != nil {
		t.Fatalf("写入主机失败: %v", err)
	}
	if err := gdb.WithContext(ctx).Create(hostB).Error; err != nil {
		t.Fatalf("写入主机失败: %v", err)
	}

	now := time.Now()
	insts := []model.Instance{
		{ClusterID: c.ID, HostID: &hostB.ID, HostIP: hostB.IP, Component: "pd", Port: 2379, Status: "up", Version: "v8.5.5", FetchedAt: now},
		{ClusterID: c.ID, HostID: &hostB.ID, HostIP: hostB.IP, Component: "prometheus", Port: 9090, Status: "up", Version: "v8.5.5", FetchedAt: now},
		{ClusterID: c.ID, HostID: &hostB.ID, HostIP: hostB.IP, Component: "grafana", Port: 3000, Status: "up", Version: "v8.5.5", FetchedAt: now},
		// host_id 为空的实例：只能靠 host_ip 归位（历史数据兜底）。
		{ClusterID: c.ID, HostIP: hostA.IP, Component: "tikv", Port: 20160, Status: "up", Version: "v8.5.5", FetchedAt: now},
		{ClusterID: c.ID, HostID: &hostA.ID, HostIP: hostA.IP, Component: "tidb", Port: 4000, Status: "down", Version: "v8.5.5", FetchedAt: now},
	}
	if err := gdb.WithContext(ctx).Create(&insts).Error; err != nil {
		t.Fatalf("写入实例失败: %v", err)
	}

	svc := NewClusterService(ClusterDeps{
		Clusters:  repo.NewClusterRepo(gdb),
		Instances: repo.NewInstanceRepo(gdb),
		Hosts:     repo.NewHostRepo(gdb),
	})
	return svc, c.ID
}

// 拓扑的主视角必须是「每台主机上有哪些实例」，且 IP 按数值排序、元信息按 IP 关联。
func TestTopology_HostCentric(t *testing.T) {
	svc, clusterID := topoTestService(t)
	res, err := svc.Topology(context.Background(), clusterID)
	if err != nil {
		t.Fatalf("Topology 失败: %v", err)
	}

	if res.NodeCount != 5 {
		t.Fatalf("nodeCount = %d, 期望 5", res.NodeCount)
	}
	if res.HostCount != 2 || len(res.Hosts) != 2 {
		t.Fatalf("hostCount = %d / len(hosts) = %d, 期望 2", res.HostCount, len(res.Hosts))
	}

	// 数值序：10.0.0.9 必须排在 10.0.0.10 之前（字典序会反过来）。
	if res.Hosts[0].HostIP != "10.0.0.9" || res.Hosts[1].HostIP != "10.0.0.10" {
		t.Fatalf("主机排序 = [%s, %s], 期望 [10.0.0.9, 10.0.0.10]", res.Hosts[0].HostIP, res.Hosts[1].HostIP)
	}

	tidb01 := res.Hosts[0]
	if tidb01.Hostname != "tidb01" || tidb01.SSHStatus != model.SSHStatusFail || tidb01.CPUCores != 4 {
		t.Fatalf("主机元信息未关联: %+v", tidb01)
	}
	if tidb01.InstanceCount != 3 || len(tidb01.Instances) != 3 {
		t.Fatalf("tidb01 实例数 = %d, 期望 3", tidb01.InstanceCount)
	}
	// 组件顺序按 PD/TiDB/TiKV/... 业务序，而非字典序。
	wantComps := []string{"pd", "prometheus", "grafana"}
	if len(tidb01.Components) != len(wantComps) {
		t.Fatalf("tidb01 组件 = %v, 期望 %v", tidb01.Components, wantComps)
	}
	for i, want := range wantComps {
		if tidb01.Components[i] != want {
			t.Fatalf("tidb01 组件 = %v, 期望 %v", tidb01.Components, wantComps)
		}
	}

	tidb02 := res.Hosts[1]
	if tidb02.InstanceCount != 2 {
		t.Fatalf("tidb02 实例数 = %d, 期望 2（含 host_id 为空、仅靠 IP 归属的 tikv）", tidb02.InstanceCount)
	}
	// 同一主机内的实例按组件业务序（tidb 在 tikv 之前），不是入库/字典序。
	if len(tidb02.Instances) != 2 || tidb02.Instances[0].Component != "tidb" || tidb02.Instances[1].Component != "tikv" {
		t.Fatalf("tidb02 实例顺序 = %v, 期望先 tidb 后 tikv", tidb02.Components)
	}
	// 实例自身必须带 component，前端才能不依赖分组渲染。
	var downSeen bool
	for _, n := range tidb02.Instances {
		if n.Component == "" {
			t.Fatalf("实例 %d 缺少 component 字段", n.ID)
		}
		if n.Status == "down" {
			downSeen = true
		}
	}
	if !downSeen {
		t.Fatal("tidb02 上应有 status=down 的实例，状态未透传")
	}

	// 按组件视角保留，且节点总数一致。
	sum := 0
	for _, g := range res.Groups {
		sum += len(g.Nodes)
	}
	if sum != res.NodeCount {
		t.Fatalf("按组件视角节点数 %d != nodeCount %d", sum, res.NodeCount)
	}
}
