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
			status, collect, streak := Derive(tc.chanErrors, tc.prevStreak, tc.everSucceeded, tc.threshold)
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
		status, collect, streak := Derive(map[Kind]string{KindPD: "down"}, prev, true, 3)
		if status != HealthHealthy || collect != HealthHealthy || streak != 0 {
			t.Fatalf("prevStreak=%d 时仅 PD 失败仍应 healthy/healthy/0，实际 %s/%s/%d",
				prev, status, collect, streak)
		}
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
