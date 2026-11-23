package service

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/repo"
)

// groupedTestDB 建一个只含集群/主机/实例三张表的 HostService，并返回底层连接供写数据。
func groupedTestDB(t *testing.T) (*HostService, *gorm.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "grouped.db")
	gdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开临时库失败: %v", err)
	}
	if err := gdb.AutoMigrate(&model.Cluster{}, &model.Host{}, &model.Instance{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return NewHostService(
		repo.NewHostRepo(gdb),
		repo.NewInstanceRepo(gdb),
		repo.NewClusterRepo(gdb),
		// 分组测试不涉及地址映射，传 nil 走原样透传分支。
		nil,
	), gdb
}

// groupSummary 把分组结果拼成 "名称:主机数" 的稳定串。
func groupSummary(groups []ClusterHostGroup) string {
	out := make([]string, 0, len(groups))
	for _, g := range groups {
		out = append(out, fmt.Sprintf("%s:%d", g.ClusterName, len(g.Hosts)))
	}
	return strings.Join(out, ",")
}

// TestHostGrouped_AppliesFilters 锁定「按集群分组」视图的筛选口径。
//
// 回归背景：分组视图原走全量 All，keyword/arch/clusterId 全部失效，导致
// 右上角「共 N 台主机」与分组内容对不上。过滤后，被 keyword 排除的未关联主机
// 不能再以「未关联集群」分组的形式出现。
func TestHostGrouped_AppliesFilters(t *testing.T) {
	svc, gdb := groupedTestDB(t)
	ctx := context.Background()

	c := &model.Cluster{
		Name: "tidb-demo", NameLower: "tidb-demo",
		TiUPHost: "127.0.0.1", TiUPUser: "tidb", DBHost: "10.10.10.10", DBPort: 4000, DBUser: "root",
		Status: model.ClusterStatusHealthy, CollectStatus: model.ClusterStatusHealthy,
	}
	if err := gdb.Create(c).Error; err != nil {
		t.Fatalf("建集群失败: %v", err)
	}

	hosts := []model.Host{
		{IP: "127.0.0.1", Hostname: "MacBook-Pro.local", CPUArch: "aarch64"},
		{IP: "10.10.10.10", Hostname: "node-10", CPUArch: "x86_64"},
		{IP: "10.10.10.11", Hostname: "node-11", CPUArch: "x86_64"},
	}
	for i := range hosts {
		if err := gdb.Create(&hosts[i]).Error; err != nil {
			t.Fatalf("建主机 %s 失败: %v", hosts[i].IP, err)
		}
	}
	instances := []model.Instance{
		{ClusterID: c.ID, HostID: &hosts[1].ID, HostIP: hosts[1].IP, Component: "tidb", Port: 4000},
		{ClusterID: c.ID, HostID: &hosts[2].ID, HostIP: hosts[2].IP, Component: "pd", Port: 2379},
	}
	for i := range instances {
		if err := gdb.Create(&instances[i]).Error; err != nil {
			t.Fatalf("建实例 %s 失败: %v", instances[i].HostIP, err)
		}
	}

	cases := []struct {
		desc string
		f    repo.HostFilter
		want string
	}{
		{"无筛选：正常集群 + 未关联集群", repo.HostFilter{}, "tidb-demo:2,未关联集群:1"},
		{"keyword 命中 10.10. 时不再出现未关联集群", repo.HostFilter{Keyword: "10.10."}, "tidb-demo:2"},
		{"keyword 命中 127.0.0.1 只留未关联集群", repo.HostFilter{Keyword: "127.0.0.1"}, "未关联集群:1"},
		{"按 clusterId 过滤只留该集群", repo.HostFilter{ClusterID: c.ID}, "tidb-demo:2"},
		{"keyword + arch 无匹配返回空", repo.HostFilter{Keyword: "10.10.", Arch: "aarch64"}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			groups, err := svc.Grouped(ctx, tc.f)
			if err != nil {
				t.Fatalf("查询失败: %v", err)
			}
			if got := groupSummary(groups); got != tc.want {
				t.Fatalf("分组 = %q，期望 %q", got, tc.want)
			}
		})
	}
}
