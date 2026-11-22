package repo

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/tmp-platform/backend/internal/model"
)

// hostFilterFixture 建一套对齐现网形态的主机/集群/实例数据。
//
// 127.0.0.1（aarch64，无实例 → 未关联集群），10.10.10.10 与 10.10.10.11
// （x86_64，各承载一条 tidb-demo 实例）。用于锁定 ListAll 的过滤口径。
func hostFilterFixture(t *testing.T) (*HostRepo, int64) {
	t.Helper()
	gdb := snapTestDB(t)
	r := NewHostRepo(gdb)

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
	return r, c.ID
}

// hostIPs 把结果里的主机 IP 排序后拼成逗号串，便于稳定比对。
func hostIPs(rows []HostRow) string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.IP)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// TestHostListAll_Filtering 锁定分组视图底层 ListAll 的筛选口径。
//
// 用户反馈：分组视图搜索/架构/集群三个筛选全部失效，因为原实现走的是 All（全量）。
// 这里逐项验证 ListAll 复用 List 的过滤语义，且关键词的 OR 组不会泄漏。
func TestHostListAll_Filtering(t *testing.T) {
	r, clusterID := hostFilterFixture(t)
	ctx := context.Background()

	cases := []struct {
		desc string
		f    HostFilter
		want string
	}{
		{"无筛选返回全部", HostFilter{}, "10.10.10.10,10.10.10.11,127.0.0.1"},
		{"keyword 命中 IP 前缀", HostFilter{Keyword: "10.10."}, "10.10.10.10,10.10.10.11"},
		{"keyword 命中主机名", HostFilter{Keyword: "MacBook"}, "127.0.0.1"},
		{"按 arch 过滤", HostFilter{Arch: "aarch64"}, "127.0.0.1"},
		{"按 clusterId 过滤", HostFilter{ClusterID: clusterID}, "10.10.10.10,10.10.10.11"},
		{"keyword + arch 不匹配时必须为空（OR 不得泄漏 127.0.0.1）", HostFilter{Keyword: "10.10.", Arch: "aarch64"}, ""},
		{"keyword + arch 同时匹配", HostFilter{Keyword: "10.10.", Arch: "x86_64"}, "10.10.10.10,10.10.10.11"},
		{"keyword + clusterId 同时匹配", HostFilter{Keyword: "10.10.", ClusterID: clusterID}, "10.10.10.10,10.10.10.11"},
		{"无匹配返回空", HostFilter{Keyword: "no-such-host"}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			rows, err := r.ListAll(ctx, tc.f)
			if err != nil {
				t.Fatalf("查询失败: %v", err)
			}
			if got := hostIPs(rows); got != tc.want {
				t.Fatalf("命中 %q，期望 %q", got, tc.want)
			}
		})
	}
}

// TestHostList_SharesFilterWithListAll 锁定 List 与 ListAll 共用同一套过滤逻辑。
//
// 抽 applyHostFilter 后，分页的 List 与不分组视图的 ListAll 行为必须一致：
// 用 pageSize=1 翻页验证 total 稳定，且 keyword+arch 组合不会把 127.0.0.1 漏进来。
func TestHostList_SharesFilterWithListAll(t *testing.T) {
	r, clusterID := hostFilterFixture(t)
	ctx := context.Background()

	list, total, err := r.List(ctx, HostFilter{
		Keyword: "10.10.",
		Arch:    "x86_64",
		Page:    Page{Page: 1, PageSize: 1},
	})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if total != 2 || len(list) != 1 {
		t.Fatalf("total = %d, len = %d, 期望 total=2 len=1", total, len(list))
	}
	if list[0].IP == "127.0.0.1" {
		t.Fatalf("OR 泄漏：127.0.0.1 不应命中 keyword+arch 组合")
	}

	all, err := r.ListAll(ctx, HostFilter{ClusterID: clusterID})
	if err != nil {
		t.Fatalf("ListAll 查询失败: %v", err)
	}
	if got := hostIPs(all); got != "10.10.10.10,10.10.10.11" {
		t.Fatalf("ListAll clusterId 命中 %q", got)
	}
}
