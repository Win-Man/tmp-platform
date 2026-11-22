package collector

import "testing"

// QA 补充用例（独立于实现者）：覆盖 hardware.go / mapping.go 中实现者未断言的边界。
//
// 1) INSTANCE 不带端口时（TiDB 某些版本的 cluster_hardware 会直接给纯 IP），
//    仍必须能归到该 IP 上，不能因为 SplitHostPort 失败就丢数据；
//    同一台主机「带端口」与「不带端口」的行必须聚合成同一条记录。
// 2) FillHostResources 的「逐字段补齐」必须是字段粒度的：
//    base 的 Hostname 为空、CPUCores=0，但 MemoryGB 已有值时，
//    只能补 hostname 与核数，MemoryGB 绝不能被 extra 覆盖。

// TestAggregateHostHardware_InstanceWithoutPort 验证 INSTANCE 无端口的主机仍能被采集到。
func TestAggregateHostHardware_InstanceWithoutPort(t *testing.T) {
	hw := []map[string]string{
		// 不带端口：架构与磁盘。
		hardwareRow("10.0.0.5", "cpu", "cpu", "cpu-arch", "x86_64"),
		hardwareRow("10.0.0.5", "disk", "vda1", "path", "/"),
		hardwareRow("10.0.0.5", "disk", "vda1", "total", "53687091200"),
		// 同一个 IP 的另一批行带端口：核数与内存必须并入同一条主机记录。
		hardwareRow("10.0.0.5:4000", "cpu", "cpu", "cpu-logical-cores", "4"),
		hardwareRow("10.0.0.5:4000", "memory", "memory", "capacity", "8589934592"),
	}
	load := []map[string]string{
		hardwareRow("10.0.0.5", "cpu", "usage", "idle", "0.5"),
	}

	got := AggregateHostHardware(hw, load)
	if len(got) != 1 {
		t.Fatalf("主机数 = %d, 期望 1（无端口与带端口应聚合为同一台）: %+v", len(got), got)
	}
	h := got[0]
	if h.IP != "10.0.0.5" {
		t.Fatalf("IP = %q, 期望 10.0.0.5", h.IP)
	}
	if h.Arch != "x86_64" {
		t.Errorf("Arch = %q, 期望 x86_64", h.Arch)
	}
	if h.CPUCores != 4 {
		t.Errorf("CPUCores = %d, 期望 4", h.CPUCores)
	}
	if h.MemoryGB != 8 {
		t.Errorf("MemoryGB = %v, 期望 8", h.MemoryGB)
	}
	if h.DiskTotalGB != 50 {
		t.Errorf("DiskTotalGB = %v, 期望 50", h.DiskTotalGB)
	}
	if h.CPUUsagePct != 50 {
		t.Errorf("CPUUsagePct = %v, 期望 50", h.CPUUsagePct)
	}
}

// TestFillHostResources_PartialFieldFill 验证补齐是字段级的，而非整条记录级。
func TestFillHostResources_PartialFieldFill(t *testing.T) {
	base := []HostResource{
		// SSH 只拿到部分信息：无 hostname、无核数，但内存是实测值。
		{IP: "10.88.0.1", CPUCores: 0, MemoryGB: 8},
	}
	extra := []HostResource{
		{IP: "10.88.0.1", Hostname: "node-88", OSUser: "tidb", CPUCores: 32, MemoryGB: 64, DiskTotalGB: 1000},
	}

	got := FillHostResources(base, extra)
	if len(got) != 1 {
		t.Fatalf("主机数 = %d, 期望 1: %+v", len(got), got)
	}
	h := got[0]
	if h.Hostname != "node-88" {
		t.Errorf("Hostname = %q, 期望 node-88（空值应被补齐）", h.Hostname)
	}
	if h.CPUCores != 32 {
		t.Errorf("CPUCores = %d, 期望 32（0 值应被补齐）", h.CPUCores)
	}
	// 关键断言：base 已有的 MemoryGB 必须保留，不能被 extra 的 64 覆盖。
	if h.MemoryGB != 8 {
		t.Errorf("MemoryGB = %v, 期望 8（base 已有值不得被覆盖）", h.MemoryGB)
	}
	if h.DiskTotalGB != 1000 {
		t.Errorf("DiskTotalGB = %v, 期望 1000（缺失字段应被补齐）", h.DiskTotalGB)
	}
}
