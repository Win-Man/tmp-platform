package collector

import "testing"

// TestDerive 覆盖 PRD 8.2 / ARCHITECTURE §6.4 的健康状态推导四类场景。
func TestDerive(t *testing.T) {
	cases := []struct {
		name          string
		chanErrors    map[Kind]string
		prevStreak    int
		everSucceeded bool
		threshold     int
		downInstances int
		wantStatus    string
		wantCollect   string
		wantStreak    int
	}{
		{
			name:          "a) 全通道成功→healthy",
			chanErrors:    nil,
			prevStreak:    2,
			everSucceeded: true,
			threshold:     3,
			wantStatus:    HealthHealthy,
			wantCollect:   HealthHealthy,
			wantStreak:    0,
		},
		{
			name:          "a1b) 无通道错误但有 1 台 down 实例→status degraded、collect 仍 healthy",
			chanErrors:    nil,
			prevStreak:    2,
			everSucceeded: true,
			threshold:     3,
			downInstances: 1,
			wantStatus:    HealthDegraded,
			wantCollect:   HealthHealthy,
			wantStreak:    0,
		},
		{
			name:          "a2) 空错误 map→healthy 且失败计数清零",
			chanErrors:    map[Kind]string{},
			prevStreak:    5,
			everSucceeded: true,
			threshold:     3,
			wantStatus:    HealthHealthy,
			wantCollect:   HealthHealthy,
			wantStreak:    0,
		},
		{
			name:          "b) 仅 PD 通道失败→不得降级，仍 healthy",
			chanErrors:    map[Kind]string{KindPD: "connection refused"},
			prevStreak:    0,
			everSucceeded: true,
			threshold:     3,
			wantStatus:    HealthHealthy,
			wantCollect:   HealthHealthy,
			wantStreak:    0,
		},
		{
			name:          "b2) 仅 PD 失败且此前已有失败计数→也清回 healthy",
			chanErrors:    map[Kind]string{KindPD: "timeout"},
			prevStreak:    4,
			everSucceeded: true,
			threshold:     3,
			wantStatus:    HealthHealthy,
			wantCollect:   HealthHealthy,
			wantStreak:    0,
		},
		{
			name:          "c) SSH+MySQL 均失败且 fail_streak 达阈值→abnormal(error)",
			chanErrors:    map[Kind]string{KindSSH: "ssh handshake failed", KindMySQL: "i/o timeout", KindPD: "timeout"},
			prevStreak:    2,
			everSucceeded: true,
			threshold:     3,
			wantStatus:    HealthError,
			wantCollect:   HealthError,
			wantStreak:    3,
		},
		{
			name:          "c2) 关键通道全失败且从未成功→直接 error",
			chanErrors:    map[Kind]string{KindSSH: "refused", KindMySQL: "refused"},
			prevStreak:    0,
			everSucceeded: false,
			threshold:     3,
			wantStatus:    HealthError,
			wantCollect:   HealthError,
			wantStreak:    1,
		},
		{
			name:          "d) 部分通道失败且未达阈值→degraded",
			chanErrors:    map[Kind]string{KindSSH: "permission denied", KindMySQL: "access denied"},
			prevStreak:    0,
			everSucceeded: true,
			threshold:     3,
			wantStatus:    HealthDegraded,
			wantCollect:   HealthError,
			wantStreak:    1,
		},
		{
			name:          "e) 仅 MySQL 失败（SSH 成功）→degraded，计数清零",
			chanErrors:    map[Kind]string{KindMySQL: "access denied"},
			prevStreak:    2,
			everSucceeded: true,
			threshold:     3,
			wantStatus:    HealthDegraded,
			wantCollect:   HealthDegraded,
			wantStreak:    0,
		},
		{
			name:          "f) 仅 SSH 失败（MySQL 成功）→degraded，计数清零",
			chanErrors:    map[Kind]string{KindSSH: "no route to host"},
			prevStreak:    1,
			everSucceeded: true,
			threshold:     3,
			wantStatus:    HealthDegraded,
			wantCollect:   HealthDegraded,
			wantStreak:    0,
		},
		{
			name:          "g) threshold<=0 回退为 3",
			chanErrors:    map[Kind]string{KindSSH: "refused", KindMySQL: "refused"},
			prevStreak:    2,
			everSucceeded: true,
			threshold:     0,
			wantStatus:    HealthError,
			wantCollect:   HealthError,
			wantStreak:    3,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, collect, streak := Derive(tc.chanErrors, tc.prevStreak, tc.everSucceeded, tc.threshold, tc.downInstances)
			if status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", status, tc.wantStatus)
			}
			if collect != tc.wantCollect {
				t.Fatalf("collectStatus = %q, want %q", collect, tc.wantCollect)
			}
			if streak != tc.wantStreak {
				t.Fatalf("streak = %d, want %d", streak, tc.wantStreak)
			}
		})
	}
}

// TestDerive_PDOnlyNeverDegrades 单独强调 PRD R-05：PD 为建议项，任何连续失败次数下都不降级。
func TestDerive_PDOnlyNeverDegrades(t *testing.T) {
	for prev := 0; prev <= 10; prev++ {
		status, collect, streak := Derive(map[Kind]string{KindPD: "down"}, prev, true, 3, 0)
		if status != HealthHealthy || collect != HealthHealthy || streak != 0 {
			t.Fatalf("prevStreak=%d 时仅 PD 失败仍应 healthy/healthy/0，实际 %s/%s/%d",
				prev, status, collect, streak)
		}
	}
}

// TestDerive_DownInstancesOnly 无通道错误时，down 实例数是唯一降级依据，且必须清零失败计数。
func TestDerive_DownInstancesOnly(t *testing.T) {
	for _, prev := range []int{0, 2, 9} {
		status, collect, streak := Derive(nil, prev, true, 3, 2)
		if status != HealthDegraded || collect != HealthHealthy || streak != 0 {
			t.Fatalf("prevStreak=%d 时 2 台 down 应 degraded/healthy/0，实际 %s/%s/%d",
				prev, status, collect, streak)
		}
	}
	// 0 台 down 时行为与历史一致。
	if status, collect, streak := Derive(nil, 3, true, 3, 0); status != HealthHealthy || collect != HealthHealthy || streak != 0 {
		t.Fatalf("无 down 实例应 healthy/healthy/0，实际 %s/%s/%d", status, collect, streak)
	}
}

// TestDerive_DownWithChannelErrors 有通道错误时，仍按原有通道规则判定，down 实例数不改变结果。
func TestDerive_DownWithChannelErrors(t *testing.T) {
	// 仅 MySQL 失败 + 有 down 实例 → 仍是 degraded/degraded/0（不因 down 变成 collect 失败）。
	status, collect, streak := Derive(map[Kind]string{KindMySQL: "access denied"}, 2, true, 3, 3)
	if status != HealthDegraded || collect != HealthDegraded || streak != 0 {
		t.Fatalf("仅 MySQL 失败 + down 实例应 degraded/degraded/0，实际 %s/%s/%d", status, collect, streak)
	}
	// 关键通道全失败 → 仍按失败计数判定为 error（down 实例数不影响）。
	status, collect, streak = Derive(map[Kind]string{KindSSH: "refused", KindMySQL: "refused"}, 2, true, 3, 5)
	if status != HealthError || collect != HealthError || streak != 3 {
		t.Fatalf("关键通道全失败 + down 实例应 error/error/3，实际 %s/%s/%d", status, collect, streak)
	}
}

// TestDerive_PDOnlyWithDownInstances 守住「仅 PD 失败」分支与 down 实例的优先级：
// PD 通道不可用（如 2379 未对平台开放）时，display 拿到的 down 实例仍必须体现为 degraded。
func TestDerive_PDOnlyWithDownInstances(t *testing.T) {
	pdErr := map[Kind]string{KindPD: "PD 无有效响应"}

	// 1) 仅 PD 失败 + 2 台 down → degraded/healthy/0（down 实例不被 onlyPD 分支短路）。
	if status, collect, streak := Derive(pdErr, 0, true, 3, 2); status != HealthDegraded || collect != HealthHealthy || streak != 0 {
		t.Fatalf("仅 PD 失败 + 2 台 down 应 degraded/healthy/0，实际 %s/%s/%d", status, collect, streak)
	}
	// 2) 仅 PD 失败 + 无 down → healthy/healthy/0（PRD R-05 语义不变）。
	if status, collect, streak := Derive(pdErr, 0, true, 3, 0); status != HealthHealthy || collect != HealthHealthy || streak != 0 {
		t.Fatalf("仅 PD 失败 + 无 down 应 healthy/healthy/0，实际 %s/%s/%d", status, collect, streak)
	}
	// 3) 关键通道全失败 + 5 台 down → 结论与「无 down」完全一致：down 不得把 error 洗成可用的 healthy/degraded(collect)。
	allErr := map[Kind]string{KindSSH: "refused", KindMySQL: "refused"}
	// 3a) 曾经成功过且未达阈值 → status=degraded，collectStatus=error（与既有用例 d 一致）。
	if status, collect, streak := Derive(allErr, 0, true, 3, 5); status != HealthDegraded || collect != HealthError || streak != 1 {
		t.Fatalf("关键通道全失败（未达阈值）+ 5 台 down 应 degraded/error/1，实际 %s/%s/%d", status, collect, streak)
	}
	// 3b) 从未成功过 → status=error，collectStatus=error（down 数不改变终态）。
	if status, collect, streak := Derive(allErr, 0, false, 3, 5); status != HealthError || collect != HealthError || streak != 1 {
		t.Fatalf("关键通道全失败（从未成功）+ 5 台 down 应 error/error/1，实际 %s/%s/%d", status, collect, streak)
	}
	// 4) 仅 PD 失败 + down，且此前有失败计数 → 仍以实例降级为准并清零计数。
	if status, collect, streak := Derive(pdErr, 4, true, 3, 1); status != HealthDegraded || collect != HealthHealthy || streak != 0 {
		t.Fatalf("仅 PD 失败 + down + prevStreak=4 应 degraded/healthy/0，实际 %s/%s/%d", status, collect, streak)
	}
}

// TestSnapshot_DownInstances 校验 down 实例统计（Derive 的入参来源）。
func TestSnapshot_DownInstances(t *testing.T) {
	var nilSnap *Snapshot
	if got := nilSnap.DownInstances(); got != 0 {
		t.Fatalf("nil 快照应返回 0，实际 %d", got)
	}
	snap := &Snapshot{Instances: []InstanceSnapshot{
		{Component: "pd", Status: "up"},
		{Component: "tidb", Status: "down"},
		{Component: "tikv", Status: "down"},
		{Component: "grafana", Status: "unhealthy"},
	}}
	if got := snap.DownInstances(); got != 2 {
		t.Fatalf("down 实例数 = %d, want 2（unhealthy 不计入）", got)
	}
}

// TestSnapshot_ChannelFailed 校验通道失败判定辅助方法。
func TestSnapshot_ChannelFailed(t *testing.T) {
	snap := &Snapshot{}
	if snap.ChannelFailed(KindSSH) {
		t.Fatal("ChanErrors 为 nil 时不应判定失败")
	}
	snap.ChanErrors = map[Kind]string{KindSSH: "boom"}
	if !snap.ChannelFailed(KindSSH) {
		t.Fatal("已登记的通道应判定为失败")
	}
	if snap.ChannelFailed(KindMySQL) {
		t.Fatal("未登记的通道不应判定为失败")
	}
}
