package collector

import "testing"

// TestFillHostResources_NoOverwrite 验证「base 已有的值绝不被覆盖，只补空字段」。
//
// 这是多集群共享同一台主机的关键约束：SSH（中控机实测）优先，MySQL cluster_hardware/load
// 只负责给其余主机补空白。若这里反过来覆盖，SSH 实测值就会被集群侧的近似值抹掉。
func TestFillHostResources_NoOverwrite(t *testing.T) {
	base := []HostResource{
		{
			IP: "10.10.10.10", Hostname: "tiup-01", OSUser: "tidb",
			Arch: "aarch64", CPUCores: 16, MemoryGB: 64, DiskTotalGB: 500,
			CPUUsagePct: 12.5, MemUsagePct: 30, DiskUsagePct: 44,
		},
		{
			// SSH 采到了主机名但没采到硬件（例如命令白名单部分失败）。
			IP: "10.10.10.11", Hostname: "node-2",
		},
	}
	extra := []HostResource{
		{
			IP: "10.10.10.10", Hostname: "hijack", OSUser: "root",
			Arch: "x86_64", CPUCores: 2, MemoryGB: 3.4, DiskTotalGB: 128,
			CPUUsagePct: 99, MemUsagePct: 88, DiskUsagePct: 77,
		},
		{
			IP: "10.10.10.11", OSUser: "tidb",
			Arch: "aarch64", CPUCores: 8, MemoryGB: 32, DiskTotalGB: 1000,
			CPUUsagePct: 20, MemUsagePct: 40, DiskUsagePct: 60,
		},
		{
			// extra 独有：应被追加。
			IP: "10.10.10.12", Arch: "x86_64", CPUCores: 4, MemoryGB: 8,
		},
	}

	got := FillHostResources(base, extra)
	if len(got) != 3 {
		t.Fatalf("主机数 = %d, 期望 3: %+v", len(got), got)
	}
	byIP := map[string]HostResource{}
	for _, h := range got {
		byIP[h.IP] = h
	}

	first := byIP["10.10.10.10"]
	if first.Hostname != "tiup-01" || first.OSUser != "tidb" {
		t.Errorf("base 的 Hostname/OSUser 被覆盖: %+v", first)
	}
	if first.Arch != "aarch64" || first.CPUCores != 16 || first.MemoryGB != 64 {
		t.Errorf("base 的 Arch/CPUCores/MemoryGB 被覆盖: %+v", first)
	}
	if first.DiskTotalGB != 500 || first.CPUUsagePct != 12.5 || first.MemUsagePct != 30 || first.DiskUsagePct != 44 {
		t.Errorf("base 的数值字段被覆盖: %+v", first)
	}

	second := byIP["10.10.10.11"]
	if second.Hostname != "node-2" {
		t.Errorf("base 已有的 Hostname 被覆盖: %+v", second)
	}
	if second.Arch != "aarch64" || second.CPUCores != 8 || second.MemoryGB != 32 {
		t.Errorf("base 缺失的字段未被补齐: %+v", second)
	}
	if second.OSUser != "tidb" || second.DiskTotalGB != 1000 {
		t.Errorf("base 缺失的 OSUser/DiskTotalGB 未被补齐: %+v", second)
	}

	third := byIP["10.10.10.12"]
	if third.CPUCores != 4 || third.MemoryGB != 8 || third.Arch != "x86_64" {
		t.Errorf("extra 独有的主机应被完整追加: %+v", third)
	}
}

// TestFillHostResources_DoesNotMutateBase 验证不会原地改写传入的 base 切片。
func TestFillHostResources_DoesNotMutateBase(t *testing.T) {
	base := []HostResource{{IP: "10.0.0.1"}}
	extra := []HostResource{{IP: "10.0.0.1", CPUCores: 8, Arch: "aarch64"}}
	_ = FillHostResources(base, extra)
	if base[0].CPUCores != 0 || base[0].Arch != "" {
		t.Fatalf("FillHostResources 不应修改入参 base: %+v", base[0])
	}
}

// TestFillHostResources_Empty 空输入返回空切片。
func TestFillHostResources_Empty(t *testing.T) {
	got := FillHostResources(nil, nil)
	if len(got) != 0 {
		t.Fatalf("空输入应返回空切片，实际 %+v", got)
	}
	// 空 IP 的记录一律丢弃。
	got = FillHostResources([]HostResource{{IP: ""}}, []HostResource{{IP: ""}})
	if len(got) != 0 {
		t.Fatalf("空 IP 应被丢弃，实际 %+v", got)
	}
}
