package job

import (
	"context"
	"testing"
	"time"

	"github.com/tmp-platform/backend/internal/collector"
)

// TestJobTimeout_CollectCoversSSHWindow 断言采集类任务的整体超时不会小于
// SSH 通道窗口 —— 否则「系统设置里把 SSH 命令超时调大」会被任务 ctx 先截断。
func TestJobTimeout_CollectCoversSSHWindow(t *testing.T) {
	e := &Engine{}
	got := e.jobTimeout(context.Background(), SpecOf(TypeCollect))
	// 默认 30s 单命令：5s 建连 + 3×30s + 10s 其余通道开销。
	want := collector.SSHConnectTimeout + 90*time.Second + collector.ChannelTimeout
	if got != want {
		t.Fatalf("collect 任务超时 = %v, want %v", got, want)
	}
	if got <= SpecOf(TypeCollect).Timeout {
		t.Fatalf("collect 任务超时 %v 不应等于固定的 %v", got, SpecOf(TypeCollect).Timeout)
	}
}

// TestJobTimeout_OtherTypesUnchanged 断言非采集类任务仍用各自固定超时。
func TestJobTimeout_OtherTypesUnchanged(t *testing.T) {
	e := &Engine{}
	for _, typ := range []string{TypeCreateTiDBUser, TypeKillSession, TypeSSHCheck} {
		spec := SpecOf(typ)
		if got := e.jobTimeout(context.Background(), spec); got != spec.Timeout {
			t.Fatalf("%s 任务超时 = %v, want %v", typ, got, spec.Timeout)
		}
	}
}

// TestJobTimeout_TakeoverCoversSSHWindow 断言接管任务同样不会被 60s 截断。
func TestJobTimeout_TakeoverCoversSSHWindow(t *testing.T) {
	e := &Engine{}
	got := e.jobTimeout(context.Background(), SpecOf(TypeTakeover))
	if got <= SpecOf(TypeTakeover).Timeout {
		t.Fatalf("takeover 任务超时 = %v, 应大于固定的 %v", got, SpecOf(TypeTakeover).Timeout)
	}
}
