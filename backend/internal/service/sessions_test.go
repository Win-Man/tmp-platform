package service

import (
	"testing"
	"time"

	"github.com/tmp-platform/backend/internal/collector"
	"github.com/tmp-platform/backend/internal/repo"
)

// TestBuildSessionPage 锁定实时会话的过滤 / 排序 / 分页规则。
//
// 这些规则以前交给 SQL，改成 cluster_processlist 实时查询后在内存执行，
// 行为必须与快照查询保持一致：默认按执行时长倒序、按会话 ID 升序、
// 关键词覆盖用户 / 来源 / 实例 / 库 / SQL。
func TestBuildSessionPage(t *testing.T) {
	now := time.Now()
	rows := []collector.SessionRow{
		{ID: 1, Instance: "10.0.0.1:4000", UserName: "app_order", Host: "10.1.1.1:5000", DB: "order_db", TimeSec: 5, SQLText: "select 1", SQLDigest: "aaa111"},
		{ID: 2, Instance: "10.0.0.2:4000", UserName: "root", Host: "10.1.1.2:5000", TimeSec: 60, SQLText: "select * from orders", SQLDigest: "bbb222"},
		{ID: 3, Instance: "10.0.0.1:4000", UserName: "bi", Host: "10.1.1.3:5000", TimeSec: 30},
	}

	t.Run("默认按执行时长倒序", func(t *testing.T) {
		got, total, _ := buildSessionPage(7, rows, repo.SessionFilter{Page: repo.Page{Page: 1, PageSize: 20}}, now)
		if total != 3 || len(got) != 3 {
			t.Fatalf("total=%d len=%d，期望 3/3", total, len(got))
		}
		if got[0].SessionID != 2 || got[1].SessionID != 3 || got[2].SessionID != 1 {
			t.Fatalf("排序错误: %d,%d,%d", got[0].SessionID, got[1].SessionID, got[2].SessionID)
		}
		if got[0].ClusterID != 7 {
			t.Fatalf("clusterID = %d，期望 7", got[0].ClusterID)
		}
		// 实例必须带出来，前端要按实例列展示。
		if got[0].Instance != "10.0.0.2:4000" {
			t.Fatalf("instance = %q", got[0].Instance)
		}
	})

	t.Run("按会话 ID 升序", func(t *testing.T) {
		got, _, _ := buildSessionPage(7, rows, repo.SessionFilter{SortBy: "id", Page: repo.Page{Page: 1, PageSize: 20}}, now)
		for i, want := range []int64{1, 2, 3} {
			if got[i].SessionID != want {
				t.Fatalf("第 %d 项 = %d，期望 %d", i, got[i].SessionID, want)
			}
		}
	})

	t.Run("关键词命中库名与 SQL", func(t *testing.T) {
		got, total, _ := buildSessionPage(7, rows, repo.SessionFilter{Keyword: "ORDER", Page: repo.Page{Page: 1, PageSize: 20}}, now)
		if total != 2 {
			t.Fatalf("total = %d，期望 2（order_db + orders SQL）", total)
		}
		if got[0].SessionID != 2 || got[1].SessionID != 1 {
			t.Fatalf("命中项错误: %d,%d", got[0].SessionID, got[1].SessionID)
		}
	})

	t.Run("关键词命中实例地址", func(t *testing.T) {
		got, total, _ := buildSessionPage(7, rows, repo.SessionFilter{Keyword: "10.0.0.2", Page: repo.Page{Page: 1, PageSize: 20}}, now)
		if total != 1 || got[0].SessionID != 2 {
			t.Fatalf("total=%d，期望仅命中实例 10.0.0.2 的会话 2", total)
		}
	})

	t.Run("分页与 total", func(t *testing.T) {
		got, total, _ := buildSessionPage(7, rows, repo.SessionFilter{Page: repo.Page{Page: 2, PageSize: 2}}, now)
		if total != 3 {
			t.Fatalf("total = %d，期望 3", total)
		}
		if len(got) != 1 || got[0].SessionID != 1 {
			t.Fatalf("第 2 页内容错误: %+v", got)
		}
	})

	t.Run("SQL 原文与 DIGEST 分别落库", func(t *testing.T) {
		// 两者语义不同：SQLText 是 INFO 原文，SQLDigest 是归一化指纹，
		// 前端两列各展示一份，不能互相覆盖。
		got, _, _ := buildSessionPage(7, rows, repo.SessionFilter{SortBy: "id", Page: repo.Page{Page: 1, PageSize: 20}}, now)
		if got[0].SQLText != "select 1" || got[0].SQLDigest != "aaa111" {
			t.Fatalf("会话 1 的 SQL 字段错误: text=%q digest=%q", got[0].SQLText, got[0].SQLDigest)
		}
		if got[1].SQLText != "select * from orders" || got[1].SQLDigest != "bbb222" {
			t.Fatalf("会话 2 的 SQL 字段错误: text=%q digest=%q", got[1].SQLText, got[1].SQLDigest)
		}
	})

	t.Run("关键词命中 DIGEST", func(t *testing.T) {
		got, total, _ := buildSessionPage(7, rows, repo.SessionFilter{Keyword: "BBB", Page: repo.Page{Page: 1, PageSize: 20}}, now)
		if total != 1 || got[0].SessionID != 2 {
			t.Fatalf("按 DIGEST 关键词应只命中会话 2，实际 total=%d", total)
		}
	})

	t.Run("越界页返回空列表而非报错", func(t *testing.T) {
		got, total, _ := buildSessionPage(7, rows, repo.SessionFilter{Page: repo.Page{Page: 9, PageSize: 10}}, now)
		if total != 3 || len(got) != 0 {
			t.Fatalf("total=%d len=%d，期望 3/0", total, len(got))
		}
	})
}
