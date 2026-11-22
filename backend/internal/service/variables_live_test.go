package service

import (
	"testing"
	"time"

	"github.com/tmp-platform/backend/internal/collector"
	"github.com/tmp-platform/backend/internal/repo"
)

// TestBuildVariablePage 锁定实时参数的过滤 / 排序 / 分页规则。
//
// 参数刷新走 SHOW GLOBAL VARIABLES 后内存处理，口径必须与快照查询一致：
// 按名称升序、关键词匹配参数名或值、onlyDiff 只留与默认值不同的项。
func TestBuildVariablePage(t *testing.T) {
	now := time.Now()
	rows := []collector.VariableRow{
		{Name: "tidb_mem_quota_query", Value: "1073741824", DefaultValue: "1073741824", Scope: "global", IsDynamic: true},
		{Name: "tidb_opt_agg_push_down", Value: "ON", DefaultValue: "OFF", Scope: "session", IsDynamic: true},
		{Name: "autocommit", Value: "ON", DefaultValue: "ON", Scope: "both", IsDynamic: true},
	}

	t.Run("按名称升序", func(t *testing.T) {
		got, total, _ := buildVariablePage(9, rows, repo.VariableFilter{Page: repo.Page{Page: 1, PageSize: 20}}, now)
		if total != 3 || len(got) != 3 {
			t.Fatalf("total=%d len=%d，期望 3/3", total, len(got))
		}
		want := []string{"autocommit", "tidb_mem_quota_query", "tidb_opt_agg_push_down"}
		for i, name := range want {
			if got[i].Name != name {
				t.Fatalf("第 %d 项 = %q，期望 %q", i, got[i].Name, name)
			}
		}
		if got[0].ClusterID != 9 {
			t.Fatalf("clusterID = %d，期望 9", got[0].ClusterID)
		}
	})

	t.Run("differs 按默认值比较", func(t *testing.T) {
		got, _, _ := buildVariablePage(9, rows, repo.VariableFilter{Page: repo.Page{Page: 1, PageSize: 20}}, now)
		byName := map[string]int{}
		for _, v := range got {
			byName[v.Name] = v.Differs
		}
		if byName["tidb_opt_agg_push_down"] != 1 {
			t.Fatal("值与默认值不同时应标记 differs=1")
		}
		if byName["autocommit"] != 0 {
			t.Fatal("值与默认值相同时 differs 应为 0")
		}
	})

	t.Run("onlyDiff 只留差异项", func(t *testing.T) {
		got, total, _ := buildVariablePage(9, rows, repo.VariableFilter{OnlyDiff: true, Page: repo.Page{Page: 1, PageSize: 20}}, now)
		if total != 1 || got[0].Name != "tidb_opt_agg_push_down" {
			t.Fatalf("onlyDiff 结果错误: total=%d", total)
		}
	})

	t.Run("关键词匹配参数名与当前值", func(t *testing.T) {
		got, total, _ := buildVariablePage(9, rows, repo.VariableFilter{Keyword: "MEM_QUOTA", Page: repo.Page{Page: 1, PageSize: 20}}, now)
		if total != 1 || got[0].Name != "tidb_mem_quota_query" {
			t.Fatalf("按参数名匹配失败: total=%d", total)
		}
		// 口径与快照查询一致：只匹配参数名与当前值，不匹配默认值。
		got, total, _ = buildVariablePage(9, rows, repo.VariableFilter{Keyword: "1073741824", Page: repo.Page{Page: 1, PageSize: 20}}, now)
		if total != 1 || got[0].Name != "tidb_mem_quota_query" {
			t.Fatalf("按当前值匹配失败: total=%d", total)
		}
	})

	t.Run("分页与越界", func(t *testing.T) {
		got, total, _ := buildVariablePage(9, rows, repo.VariableFilter{Page: repo.Page{Page: 2, PageSize: 2}}, now)
		if total != 3 || len(got) != 1 || got[0].Name != "tidb_opt_agg_push_down" {
			t.Fatalf("第 2 页内容错误: total=%d got=%v", total, got)
		}
		got, _, _ = buildVariablePage(9, rows, repo.VariableFilter{Page: repo.Page{Page: 9, PageSize: 10}}, now)
		if len(got) != 0 {
			t.Fatalf("越界页应返回空列表，实际 %d 条", len(got))
		}
	})
}
