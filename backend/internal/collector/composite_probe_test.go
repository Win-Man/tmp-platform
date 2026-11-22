package collector

import (
	"context"
	"testing"
	"time"
)

// TestCompositeProbe_OnlyRequestedScopes 锁定「按需探测」契约：
// 只请求 MySQL 时必须只返回 1 条 mysql 结果，不能返回 SSH/PD 的占位项。
// 回归背景：接管任务按下标取第一个结果做判定，占位项会让「数据库连通性检查」必然失败。
func TestCompositeProbe_OnlyRequestedScopes(t *testing.T) {
	c := NewCompositeCollector(2 * time.Second)
	// 指向一个必定拒绝连接的地址，保证快速失败（不需要真实集群）。
	target := &Target{
		ClusterName: "scope-test",
		TiUPHost:    "127.0.0.1",
		TiUPPort:    1,
		TiUPUser:    "nobody",
		DBHost:      "127.0.0.1",
		DBPort:      1,
		DBUser:      "nobody",
		PDHost:      "127.0.0.1",
		PDPort:      1,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	res, err := c.Probe(ctx, target, []ProbeScope{ScopeMySQL})
	if err != nil {
		t.Fatalf("Probe 不应返回错误: %v", err)
	}
	if len(res) != 1 {
		t.Fatalf("只请求 MySQL 时应返回 1 条结果，实际 %d 条: %+v", len(res), res)
	}
	if res[0].Scope != ScopeMySQL {
		t.Fatalf("结果通道应为 mysql，实际 %q", res[0].Scope)
	}
	if res[0].OK {
		t.Fatalf("不可达地址不应探测成功")
	}
	if res[0].Message == "" || res[0].Message == "未执行该项探测" {
		t.Fatalf("应返回真实失败原因，实际 %q", res[0].Message)
	}

	// 请求三项时顺序必须固定为 SSH → MySQL → PD。
	all, err := c.Probe(ctx, target, nil)
	if err != nil {
		t.Fatalf("Probe(nil) 不应返回错误: %v", err)
	}
	if len(all) != 3 {
		t.Fatalf("不传 scopes 时应探测三项，实际 %d", len(all))
	}
	want := []ProbeScope{ScopeSSH, ScopeMySQL, ScopePD}
	for i, w := range want {
		if all[i].Scope != w {
			t.Fatalf("第 %d 项应为 %s，实际 %s", i, w, all[i].Scope)
		}
	}
}
