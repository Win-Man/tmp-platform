package repo

import (
	"context"
	"testing"

	"github.com/tmp-platform/backend/internal/model"
)

// TestHostRow_ClustersCarryTiUPHost 验证主机列表的「所属集群」明细带上中控机。
//
// 需求背景：不同中控机上允许存在同名集群（唯一键是 (tiup_host, name_lower)），
// 主机页要列出「所属集群 @ 中控机」，用户才能区分两台机器上同名的两套集群。
// HostRow.Clusters[].TiUPHost 为空时该列在界面上永远是空白，等同需求未实现。
func TestHostRow_ClustersCarryTiUPHost(t *testing.T) {
	db := snapTestDB(t)
	ctx := context.Background()

	c := &model.Cluster{
		Name: "tidb-demo", NameLower: "tidb-demo",
		TiUPHost: "10.0.0.77", TiUPUser: "tidb",
		DBHost: "10.0.0.78", DBPort: 4000, DBUser: "root",
		Status: model.ClusterStatusHealthy, CollectStatus: model.ClusterStatusHealthy,
	}
	if err := db.Create(c).Error; err != nil {
		t.Fatalf("建集群失败: %v", err)
	}
	h := &model.Host{IP: "10.0.0.78", Hostname: "node-78", CPUArch: "aarch64", CPUCores: 8}
	if err := db.Create(h).Error; err != nil {
		t.Fatalf("建主机失败: %v", err)
	}
	inst := &model.Instance{
		ClusterID: c.ID, HostID: &h.ID, HostIP: h.IP, Port: 4000, Component: "tidb",
	}
	if err := db.Create(inst).Error; err != nil {
		t.Fatalf("建实例失败: %v", err)
	}

	rows, err := NewHostRepo(db).All(ctx)
	if err != nil {
		t.Fatalf("查询主机列表失败: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("主机数 = %d, 期望 1: %+v", len(rows), rows)
	}
	if len(rows[0].Clusters) != 1 {
		t.Fatalf("所属集群数 = %d, 期望 1: %+v", len(rows[0].Clusters), rows[0].Clusters)
	}
	brief := rows[0].Clusters[0]
	if brief.ID != c.ID || brief.Name != "tidb-demo" {
		t.Errorf("所属集群 = %+v, 期望 id=%d name=tidb-demo", brief, c.ID)
	}
	if brief.TiUPHost != "10.0.0.77" {
		t.Errorf("所属集群中控机 tiupHost = %q, 期望 %q（空值说明 SQL 列名与结构体字段没对上）",
			brief.TiUPHost, "10.0.0.77")
	}
}

// TestHostRow_SharedHostKeepsTwoSameNameClusters 验证「一台主机属于两套同名但中控机不同的集群」。
//
// 这是需求 A+B+C 的交点：同批机器上跑两套同名集群（各自有独立中控机）时，
// 主机页必须同时列出两条明细并各自带中控机，前端才能靠 name@tiupHost 消歧。
// 若这里按集群名去重（或只保留第一条），前端永远只看到一个标签、也就永远不判重名。
func TestHostRow_SharedHostKeepsTwoSameNameClusters(t *testing.T) {
	db := snapTestDB(t)
	ctx := context.Background()

	shared := &model.Host{IP: "10.0.0.90", Hostname: "shared-90", CPUArch: "x86_64", CPUCores: 4}
	if err := db.Create(shared).Error; err != nil {
		t.Fatalf("建主机失败: %v", err)
	}
	// 两套同名集群，中控机不同（唯一键 (tiup_host, name_lower) 允许共存）。
	clusters := []model.Cluster{
		{Name: "tidb-prod", NameLower: "tidb-prod", TiUPHost: "10.0.0.11", TiUPUser: "tidb",
			DBHost: "10.0.0.90", DBPort: 4000, DBUser: "root",
			Status: model.ClusterStatusHealthy, CollectStatus: model.ClusterStatusHealthy},
		{Name: "tidb-prod", NameLower: "tidb-prod", TiUPHost: "10.0.0.12", TiUPUser: "tidb",
			DBHost: "10.0.0.90", DBPort: 4000, DBUser: "root",
			Status: model.ClusterStatusHealthy, CollectStatus: model.ClusterStatusHealthy},
	}
	for i := range clusters {
		if err := db.Create(&clusters[i]).Error; err != nil {
			t.Fatalf("建集群 %s@%s 失败: %v", clusters[i].Name, clusters[i].TiUPHost, err)
		}
		inst := &model.Instance{
			ClusterID: clusters[i].ID, HostID: &shared.ID, HostIP: shared.IP,
			Port: 4000, Component: "tidb",
		}
		if err := db.Create(inst).Error; err != nil {
			t.Fatalf("建实例失败: %v", err)
		}
	}

	rows, err := NewHostRepo(db).All(ctx)
	if err != nil {
		t.Fatalf("查询主机列表失败: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("主机数 = %d, 期望 1: %+v", len(rows), rows)
	}
	got := rows[0]
	if len(got.Clusters) != 2 {
		t.Fatalf("所属集群明细数 = %d, 期望 2（同名不同中控机必须都保留）: %+v", len(got.Clusters), got.Clusters)
	}
	hosts := map[string]bool{}
	for _, c := range got.Clusters {
		if c.Name != "tidb-prod" {
			t.Errorf("集群名 = %q, 期望 tidb-prod", c.Name)
		}
		if c.TiUPHost == "" {
			t.Errorf("集群 %d 的中控机为空，前端无法消歧: %+v", c.ID, c)
		}
		hosts[c.TiUPHost] = true
	}
	if !hosts["10.0.0.11"] || !hosts["10.0.0.12"] {
		t.Errorf("中控机集合 = %v, 期望同时含 10.0.0.11 与 10.0.0.12", hosts)
	}
}
