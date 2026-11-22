package collector

import (
	"strings"
	"testing"
)

// TestCrossCheckStatus 锁定「TiUP 报告状态与平台实测连通性」的矛盾检测。
//
// 背景：`tiup cluster display` 的状态是 TiUP 自己的一次性探测结果，
// 会因瞬时超时误报整排 Down；meta.yaml 兜底时又没有状态、全部按 up 填写。
// 这两种情况都会让界面显示与真相不符，因此只要矛盾就要出告警，且不得改写状态。
func TestCrossCheckStatus(t *testing.T) {
	base := func() *Snapshot {
		return &Snapshot{
			Normalized: &Normalized{},
			ChanErrors: map[Kind]string{},
		}
	}

	tests := []struct {
		name       string
		mutate     func(*Snapshot)
		wantCount  int
		wantSubstr string
	}{
		{
			name: "无矛盾：全部 up 且通道正常",
			mutate: func(s *Snapshot) {
				s.Instances = []InstanceSnapshot{
					{Component: "pd", HostIP: "10.0.0.1", Status: "up"},
					{Component: "tidb", HostIP: "10.0.0.1", Status: "up"},
					{Component: "tikv", HostIP: "10.0.0.1", Status: "up"},
				}
			},
			wantCount: 0,
		},
		{
			name: "PD 接口实测可达但报告 pd 非 up",
			mutate: func(s *Snapshot) {
				s.Instances = []InstanceSnapshot{
					{Component: "pd", HostIP: "10.0.0.1", Status: "down"},
					{Component: "tidb", HostIP: "10.0.0.1", Status: "up"},
				}
			},
			wantCount:  1,
			wantSubstr: "PD 服务接口实测可访问",
		},
		{
			name: "MySQL 可连但报告全部 tidb 非 up",
			mutate: func(s *Snapshot) {
				s.Instances = []InstanceSnapshot{
					{Component: "pd", HostIP: "10.0.0.1", Status: "up"},
					{Component: "tidb", HostIP: "10.0.0.1", Status: "down"},
					{Component: "tidb", HostIP: "10.0.0.2", Status: "down"},
				}
			},
			wantCount:  1,
			wantSubstr: "MySQL 4000 端口实测可连接",
		},
		{
			name: "PD 有 up store 但报告全部 tikv 非 up",
			mutate: func(s *Snapshot) {
				s.Instances = []InstanceSnapshot{
					{Component: "pd", HostIP: "10.0.0.1", Status: "up"},
					{Component: "tikv", HostIP: "10.0.0.1", Status: "down"},
				}
				s.Stores = []Store{{Address: "10.0.0.1:20160", StateName: "Up"}}
			},
			wantCount:  1,
			wantSubstr: "up 状态的 TiKV store",
		},
		{
			name: "反向：拓扑全正常但 MySQL 连不上（meta.yaml 兜底常见）",
			mutate: func(s *Snapshot) {
				s.ChanErrors[KindMySQL] = "dial tcp 10.0.0.1:4000: connect: connection refused"
				s.Instances = []InstanceSnapshot{
					{Component: "pd", HostIP: "10.0.0.1", Status: "up"},
					{Component: "tidb", HostIP: "10.0.0.1", Status: "up"},
				}
			},
			wantCount:  1,
			wantSubstr: "MySQL 通道连不上",
		},
		{
			name: "只有部分 tidb down（多实例）不误报",
			mutate: func(s *Snapshot) {
				s.Instances = []InstanceSnapshot{
					{Component: "tidb", HostIP: "10.0.0.1", Status: "up"},
					{Component: "tidb", HostIP: "10.0.0.2", Status: "down"},
				}
			},
			wantCount: 0,
		},
		{
			name: "PD 通道失败时不因 pd down 报矛盾",
			mutate: func(s *Snapshot) {
				s.ChanErrors[KindPD] = "PD 无有效响应"
				s.Instances = []InstanceSnapshot{
					{Component: "pd", HostIP: "10.0.0.1", Status: "down"},
					{Component: "tidb", HostIP: "10.0.0.1", Status: "up"},
					{Component: "tikv", HostIP: "10.0.0.1", Status: "down"},
				}
			},
			wantCount: 0,
		},
		{
			name: "无实例时直接返回空",
			mutate: func(s *Snapshot) {
				s.Instances = nil
			},
			wantCount: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := base()
			tc.mutate(s)
			got := crossCheckStatus(s)
			if len(got) != tc.wantCount {
				t.Fatalf("告警条数 = %d, want %d（实际：%v）", len(got), tc.wantCount, got)
			}
			if tc.wantSubstr != "" && !strings.Contains(strings.Join(got, " "), tc.wantSubstr) {
				t.Fatalf("告警内容缺少 %q，实际：%v", tc.wantSubstr, got)
			}
			// 交叉校验只负责报告，不得改写实例状态。
			for _, inst := range s.Instances {
				if inst.Status == "" {
					t.Fatalf("crossCheckStatus 不应修改实例状态")
				}
			}
		})
	}
}

// TestCrossCheckStatus_NilSafe 保证空快照不会 panic（失败路径会传空快照）。
func TestCrossCheckStatus_NilSafe(t *testing.T) {
	if got := crossCheckStatus(nil); len(got) != 0 {
		t.Fatalf("nil 快照应返回空，实际 %v", got)
	}
	if got := crossCheckStatus(&Snapshot{}); len(got) != 0 {
		t.Fatalf("空快照应返回空，实际 %v", got)
	}
}
