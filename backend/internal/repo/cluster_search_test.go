package repo

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/tmp-platform/backend/internal/model"
)

// clusterSearchFixture 建一套覆盖「主机 IP / 版本 / 中控机 / 多实例 / 草稿」的集群数据。
//
// 数据形态对齐现网：一个集群多条实例（同一台机器上的 TiDB 与另一台机器上的 TiKV），
// 实例冗余存 host_ip（不依赖 hosts 表），另有一个草稿集群用来验证总览口径。
func clusterSearchFixture(t *testing.T) (*gorm.DB, *ClusterRepo) {
	t.Helper()
	gdb := snapTestDB(t)
	r := NewClusterRepo(gdb)

	clusters := []model.Cluster{
		{
			Name: "tidb-demo", NameLower: "tidb-demo", Draft: 0,
			TiUPHost: "127.0.0.1", TiUPPort: 22, TiUPUser: "tidb",
			DBHost: "10.10.10.10", DBPort: 4000, DBUser: "root",
			TiDBVersion: "v8.5.5", DeployMode: model.DeployModeMajority, CPUArch: "x86_64",
			Status: model.ClusterStatusHealthy, CollectStatus: model.ClusterStatusHealthy,
		},
		{
			Name: "tidb-prod", NameLower: "tidb-prod", Draft: 0,
			TiUPHost: "192.168.1.20", TiUPPort: 22, TiUPUser: "tidb",
			DBHost: "10.20.0.9", DBPort: 4000, DBUser: "root",
			TiDBVersion: "v7.5.0", DeployMode: model.DeployModeMajority, CPUArch: "arm64",
			Status: model.ClusterStatusError, CollectStatus: model.ClusterStatusError,
		},
		{
			// 草稿集群：与 tidb-demo 同为 v8.5.5，用来验证总览口径把草稿排除在外。
			Name: "tidb-dr", NameLower: "tidb-dr", Draft: 1,
			TiUPHost: "192.168.1.30", TiUPPort: 22, TiUPUser: "tidb",
			DBHost: "10.30.0.5", DBPort: 4000, DBUser: "root",
			TiDBVersion: "v8.5.5", DeployMode: model.DeployModeMajority, CPUArch: "x86_64",
			Status: model.ClusterStatusHealthy, CollectStatus: model.ClusterStatusUnknown,
		},
	}
	for i := range clusters {
		if err := gdb.Create(&clusters[i]).Error; err != nil {
			t.Fatalf("建集群 %s 失败: %v", clusters[i].Name, err)
		}
	}

	now := time.Now()
	instances := []model.Instance{
		// tidb-demo 两条实例，其中 10.10.10.11 只出现在实例表里（clusters 表无此 IP）。
		{ClusterID: clusters[0].ID, HostIP: "10.10.10.10", Component: "tidb", Port: 4000, Status: model.InstanceStatusUp, Version: "v8.5.5"},
		{ClusterID: clusters[0].ID, HostIP: "10.10.10.11", Component: "tikv", Port: 20160, Status: model.InstanceStatusUp, Version: "v8.5.5"},
		{ClusterID: clusters[1].ID, HostIP: "10.20.0.5", Component: "tidb", Port: 4000, Status: model.InstanceStatusDown, Version: "v7.5.0"},
		{ClusterID: clusters[2].ID, HostIP: "10.30.0.5", Component: "tidb", Port: 4000, Status: model.InstanceStatusUp, Version: "v8.5.5"},
	}
	for i := range instances {
		instances[i].FetchedAt = now
		instances[i].CreatedAt = now
		instances[i].UpdatedAt = now
		if err := gdb.Create(&instances[i]).Error; err != nil {
			t.Fatalf("建实例 %s 失败: %v", instances[i].HostIP, err)
		}
	}
	return gdb, r
}

// clusterNames 返回结果里的集群名（排序后拼接，便于稳定比对）。
func clusterNames(list []model.Cluster) string {
	out := make([]string, 0, len(list))
	for _, c := range list {
		out = append(out, c.Name)
	}
	sort.Strings(out)
	return strings.Join(out, ",")
}

// TestClusterList_KeywordMatchesMultiFields 锁定关键词搜索的多字段命中面。
//
// 用户反馈：输入主机 IP 或版本都会显示「当前没有纳管任何集群」，因为原实现只查了 name。
// 这里逐个字段验证：集群名 / 实例主机 IP / 中控机 IP / TiDB 版本 / DB 地址都要能命中。
func TestClusterList_KeywordMatchesMultiFields(t *testing.T) {
	_, r := clusterSearchFixture(t)
	ctx := context.Background()

	cases := []struct {
		desc    string
		keyword string
		want    string
	}{
		{"无关键词返回全部有效集群（草稿排除）", "", "tidb-demo,tidb-prod"},
		{"按集群名命中", "demo", "tidb-demo"},
		{"按集群名大小写不敏感命中", "DEMO", "tidb-demo"},
		{"按实例主机 IP 命中（该 IP 不在 clusters 表任何列）", "10.10.10.11", "tidb-demo"},
		{"按实例主机 IP 前缀命中", "10.20.0.", "tidb-prod"},
		{"按中控机 IP 命中", "127.0.0.1", "tidb-demo"},
		{"按中控机 IP 网段命中", "192.168.1.20", "tidb-prod"},
		{"按 TiDB 版本命中（草稿集群不计入）", "v8.5.5", "tidb-demo"},
		{"按 TiDB 版本前缀命中", "v7.5", "tidb-prod"},
		{"按 DB 地址命中", "10.20.0.9", "tidb-prod"},
		{"关键词两侧空白被裁剪", "  tidb-prod  ", "tidb-prod"},
	}

	for _, tc := range cases {
		t.Run(tc.desc, func(t *testing.T) {
			list, total, err := r.List(ctx, ClusterFilter{
				Keyword: tc.keyword,
				Page:    Page{Page: 1, PageSize: 20},
			})
			if err != nil {
				t.Fatalf("查询失败: %v", err)
			}
			if got := clusterNames(list); got != tc.want {
				t.Fatalf("关键词 %q 命中 %q，期望 %q", tc.keyword, got, tc.want)
			}
			if want := int64(strings.Count(tc.want, ",") + 1); tc.want != "" && total != want {
				t.Fatalf("关键词 %q total = %d，期望 %d", tc.keyword, total, want)
			}
		})
	}
}

// TestClusterList_KeywordInstanceExistsNoDuplication 锁定「实例用 EXISTS 而非 JOIN」。
//
// tidb-demo 有两条实例：若搜索走 JOIN，该集群会被放大成两行、total 变 2。
func TestClusterList_KeywordInstanceExistsNoDuplication(t *testing.T) {
	gdb, r := clusterSearchFixture(t)
	ctx := context.Background()

	var demo model.Cluster
	if err := gdb.Where("name = ?", "tidb-demo").First(&demo).Error; err != nil {
		t.Fatalf("查集群失败: %v", err)
	}
	var instCount int64
	if err := gdb.Model(&model.Instance{}).Where("cluster_id = ?", demo.ID).Count(&instCount).Error; err != nil {
		t.Fatalf("统计实例失败: %v", err)
	}
	if instCount != 2 {
		t.Fatalf("夹具异常：tidb-demo 实例数 = %d，期望 2", instCount)
	}

	list, total, err := r.List(ctx, ClusterFilter{
		Keyword: "10.10.10",
		Page:    Page{Page: 1, PageSize: 20},
	})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if total != 1 || len(list) != 1 {
		t.Fatalf("total = %d，len = %d，期望均为 1（多实例不得放大行数）", total, len(list))
	}
	if list[0].ID != demo.ID {
		t.Fatalf("命中集群 id = %d，期望 %d", list[0].ID, demo.ID)
	}
}

// TestClusterList_KeywordInstanceMatchPagination 锁定「多个集群共用一台主机时，按该主机 IP 搜索的分页口径」。
//
// 单一集群的多实例已在 TestClusterList_KeywordInstanceExistsNoDuplication 覆盖；
// 这里补的是「同 IP 跨集群」+「pageSize=1」：实例表 4 行命中、集群只有 2 个，
// 若实例部分退化成 JOIN，total 会变成 4、翻页也会出现同一个集群重复出现。
// 这两个数字是用户能直接看到的（列表行数 / 分页器总数），必须锁死。
func TestClusterList_KeywordInstanceMatchPagination(t *testing.T) {
	gdb := snapTestDB(t)
	r := NewClusterRepo(gdb)
	ctx := context.Background()

	// 两台中控机、两套集群，但 tidb/tikv 都跑在同一批业务主机 10.10.10.11 上。
	clusters := []model.Cluster{
		{
			Name: "tidb-a", NameLower: "tidb-a", Draft: 0,
			TiUPHost: "192.168.9.1", TiUPPort: 22, TiUPUser: "tidb",
			DBHost: "10.9.9.1", DBPort: 4000, DBUser: "root",
			TiDBVersion: "v8.5.5", DeployMode: model.DeployModeMajority, CPUArch: "x86_64",
			Status: model.ClusterStatusHealthy, CollectStatus: model.ClusterStatusHealthy,
		},
		{
			Name: "tidb-b", NameLower: "tidb-b", Draft: 0,
			TiUPHost: "192.168.9.2", TiUPPort: 22, TiUPUser: "tidb",
			DBHost: "10.9.9.2", DBPort: 4000, DBUser: "root",
			TiDBVersion: "v8.5.5", DeployMode: model.DeployModeMajority, CPUArch: "x86_64",
			Status: model.ClusterStatusHealthy, CollectStatus: model.ClusterStatusHealthy,
		},
	}
	for i := range clusters {
		if err := gdb.Create(&clusters[i]).Error; err != nil {
			t.Fatalf("建集群 %s 失败: %v", clusters[i].Name, err)
		}
	}

	now := time.Now()
	for _, c := range clusters {
		for _, inst := range []struct {
			component string
			port      int
		}{{"tidb", 4000}, {"tikv", 20160}} {
			row := model.Instance{
				ClusterID: c.ID, HostIP: "10.10.10.11",
				Component: inst.component, Port: inst.port,
				Status: model.InstanceStatusUp, Version: "v8.5.5",
				FetchedAt: now, CreatedAt: now, UpdatedAt: now,
			}
			if err := gdb.Create(&row).Error; err != nil {
				t.Fatalf("建实例失败: %v", err)
			}
		}
	}

	// 命中行数：instances 中 host_ip = 10.10.10.11 共 4 行；预期集群数只有 2。
	list, total, err := r.List(ctx, ClusterFilter{Keyword: "10.10.10.11", Page: Page{Page: 1, PageSize: 20}})
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if total != 2 || len(list) != 2 {
		t.Fatalf("total = %d，len = %d，期望均为 2（4 条实例行不得放大成 4 行集群）", total, len(list))
	}

	// pageSize=1 逐页取，total 必须是稳定的 2，且三页分别拿到 1/1/0 行。
	var seen []int64
	for page := 1; page <= 3; page++ {
		pageList, pageTotal, err := r.List(ctx, ClusterFilter{
			Keyword: "10.10.10.11",
			Page:    Page{Page: page, PageSize: 1},
		})
		if err != nil {
			t.Fatalf("第 %d 页查询失败: %v", page, err)
		}
		if pageTotal != 2 {
			t.Fatalf("第 %d 页 total = %d，期望 2", page, pageTotal)
		}
		wantLen := 1
		if page == 3 {
			wantLen = 0
		}
		if len(pageList) != wantLen {
			t.Fatalf("第 %d 页 len = %d，期望 %d", page, len(pageList), wantLen)
		}
		for _, c := range pageList {
			seen = append(seen, c.ID)
		}
	}
	if len(seen) != 2 {
		t.Fatalf("三页合计命中 %d 行（%v），期望 2 行且不重复", len(seen), seen)
	}
	if seen[0] == seen[1] {
		t.Fatalf("翻页出现同一集群重复：%v", seen)
	}
}

// TestClusterList_KeywordAndFiltersCombine 锁定「关键词 OR 组与其它过滤是 AND 关系」。
//
// 关键词条件内部是多个 OR；如果拼接散架（例如裸写成
// `name LIKE ? OR ... OR EXISTS(...) AND status = ?`），status / arch 过滤就会形同失效，
// 表现为「搜 v8.5.5 同时选了 error 状态，却把 healthy 集群也搜出来」。
// 实测 GORM v2 会给 raw Expr 自动补括号，实现里另有一层显式括号兜底；该用例锁定的是语义，
// 无论括号由谁补上都必须成立。
func TestClusterList_KeywordAndFiltersCombine(t *testing.T) {
	_, r := clusterSearchFixture(t)
	ctx := context.Background()

	type tc struct {
		desc   string
		filter ClusterFilter
		want   string
	}
	cases := []tc{
		{
			desc:   "关键词 + status 不匹配时必须为空（括号优先级）",
			filter: ClusterFilter{Keyword: "v8.5.5", Status: model.ClusterStatusError},
			want:   "",
		},
		{
			desc:   "关键词 + status 同时匹配",
			filter: ClusterFilter{Keyword: "10.10.10", Status: model.ClusterStatusHealthy},
			want:   "tidb-demo",
		},
		{
			desc:   "关键词 + arch 不匹配时必须为空（括号优先级）",
			filter: ClusterFilter{Keyword: "10.10.10", Arch: "arm64"},
			want:   "",
		},
		{
			desc:   "关键词 + arch 同时匹配",
			filter: ClusterFilter{Keyword: "v7.5", Arch: "arm64"},
			want:   "tidb-prod",
		},
		{
			desc:   "关键词 + deployMode 同时匹配",
			filter: ClusterFilter{Keyword: "tidb-demo", DeployMode: model.DeployModeMajority},
			want:   "tidb-demo",
		},
		{
			desc:   "仅 status 过滤（无关键词）仍生效",
			filter: ClusterFilter{Status: model.ClusterStatusError},
			want:   "tidb-prod",
		},
		{
			desc:   "关键词 + IncludeDraft 时草稿一并命中",
			filter: ClusterFilter{Keyword: "v8.5.5", IncludeDraft: true},
			want:   "tidb-demo,tidb-dr",
		},
	}

	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			c.filter.Page = Page{Page: 1, PageSize: 20}
			list, total, err := r.List(ctx, c.filter)
			if err != nil {
				t.Fatalf("查询失败: %v", err)
			}
			if got := clusterNames(list); got != c.want {
				t.Fatalf("命中 %q，期望 %q", got, c.want)
			}
			if c.want == "" && total != 0 {
				t.Fatalf("total = %d，期望 0", total)
			}
		})
	}
}

// TestClusterList_KeywordNoMatch 锁定「无匹配返回空集且不报错、不 panic」。
//
// 前端据此把表格展示为空白（不再显示「暂无纳管集群」的误导提示），
// 因此仓库层必须稳定返回 total=0 + 空列表，而不是错误。
func TestClusterList_KeywordNoMatch(t *testing.T) {
	_, r := clusterSearchFixture(t)
	ctx := context.Background()

	keywords := []string{
		"no-such-cluster",
		"10.99.99.99",
		"v9.9.9",
		"%", // LIKE 通配符必须被转义，否则会变成「匹配一切」
		"_", // 同上
		"'", // 引号不得引发 SQL 语法错误
		"中文关键词",
	}
	for _, kw := range keywords {
		t.Run("kw="+kw, func(t *testing.T) {
			list, total, err := r.List(ctx, ClusterFilter{
				Keyword: kw,
				Page:    Page{Page: 1, PageSize: 20},
			})
			if err != nil {
				t.Fatalf("关键词 %q 查询报错: %v", kw, err)
			}
			if total != 0 || len(list) != 0 {
				t.Fatalf("关键词 %q total = %d，len = %d，期望均为 0", kw, total, len(list))
			}
		})
	}
}
