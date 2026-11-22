package collector

import (
	"math"
	"testing"
)

// hardwareRow 构造一行 cluster_hardware / cluster_load 的结果（键统一小写，与 queryMaps 一致）。
func hardwareRow(instance, deviceType, deviceName, name, value string) map[string]string {
	return map[string]string{
		"instance":    instance,
		"device_type": deviceType,
		"device_name": deviceName,
		"name":        name,
		"value":       value,
	}
}

// realHardwareFixture 主机硬件聚合的样例行。
//
// 主机 10.10.10.10：**来自真实集群（TiDB v8.5.5）实测**——
// arm64 / 2 逻辑核 / 3.4GiB 内存 / 两块盘（/ 48.9G，/data 127.9G）。
//
// 主机 10.10.10.11：**合成样例（仅用于覆盖「多主机 + 单挂载点 + 无 load 行」分支），
// 不是真实集群数据**，其中的 x86_64 / 8 核 / 16GiB 都是人工填的，只要求内部自洽。
//
// 另外掺入 TiDB 会给出的 0.0.0.0 通配地址行，必须被跳过。
func realHardwareFixture() []map[string]string {
	return []map[string]string{
		hardwareRow("10.10.10.10:4000", "cpu", "cpu", "cpu-arch", "arm64"),
		hardwareRow("10.10.10.10:4000", "cpu", "cpu", "cpu-logical-cores", "2"),
		hardwareRow("10.10.10.10:4000", "cpu", "cpu", "cpu-physical-cores", "2"),
		hardwareRow("10.10.10.10:4000", "memory", "memory", "capacity", "3661103104"),
		// 磁盘：/dev/vda5 挂在 /
		hardwareRow("10.10.10.10:4000", "disk", "vda5", "path", "/"),
		hardwareRow("10.10.10.10:4000", "disk", "vda5", "total", "52516859904"),
		hardwareRow("10.10.10.10:4000", "disk", "vda5", "used", "4104007680"),
		hardwareRow("10.10.10.10:4000", "disk", "vda5", "free", "48412852224"),
		hardwareRow("10.10.10.10:4000", "disk", "vda5", "used-percent", "0.0781"),
		// 磁盘：/dev/vdb 挂在 /data（容量最大 → 应被选中）
		hardwareRow("10.10.10.10:4000", "disk", "vdb", "path", "/data"),
		hardwareRow("10.10.10.10:4000", "disk", "vdb", "total", "137371844608"),
		hardwareRow("10.10.10.10:4000", "disk", "vdb", "used", "991621120"),
		hardwareRow("10.10.10.10:4000", "disk", "vdb", "free", "136380223488"),
		hardwareRow("10.10.10.10:4000", "disk", "vdb", "used-percent", "0.0072"),

		hardwareRow("10.10.10.11:4000", "cpu", "cpu", "cpu-arch", "x86_64"),
		hardwareRow("10.10.10.11:4000", "cpu", "cpu", "cpu-logical-cores", "8"),
		hardwareRow("10.10.10.11:4000", "memory", "memory", "capacity", "17179869184"),
		hardwareRow("10.10.10.11:4000", "disk", "nvme0n1p1", "path", "/"),
		hardwareRow("10.10.10.11:4000", "disk", "nvme0n1p1", "total", "53687091200"),
		hardwareRow("10.10.10.11:4000", "disk", "nvme0n1p1", "used", "26843545600"),

		// TiDB 在部分版本会给出未绑定地址，必须跳过。
		hardwareRow("0.0.0.0:4000", "cpu", "cpu", "cpu-arch", "arm64"),
		hardwareRow("0.0.0.0:4000", "memory", "memory", "capacity", "1073741824"),
	}
}

// realLoadFixture 真实集群（TiDB v8.5.5）采到的 cluster_load 样例行（含 swap 的 NaN）。
func realLoadFixture() []map[string]string {
	return []map[string]string{
		hardwareRow("10.10.10.10:4000", "cpu", "usage", "idle", "0.77"),
		hardwareRow("10.10.10.10:4000", "memory", "virtual", "used-percent", "0.80"),
		// swap 的 used-percent 为 NaN 字符串，必须忽略且不 panic。
		hardwareRow("10.10.10.10:4000", "memory", "swap", "used-percent", "NaN"),
		hardwareRow("0.0.0.0:4000", "cpu", "usage", "idle", "0.10"),
	}
}

// loadRowWithType 构造一行带 TYPE 列的 cluster_load 结果。
//
// cluster_load 真实有 TYPE 列（tidb / tikv / pd / tiflash …），聚合时用它优先取
// tidb 实例的负载；hardwareRow 是「不含 TYPE」的版本，供旧版本 / 无 TYPE 场景使用。
func loadRowWithType(instance, typeName, deviceType, deviceName, name, value string) map[string]string {
	return map[string]string{
		"type":        typeName,
		"instance":    instance,
		"device_type": deviceType,
		"device_name": deviceName,
		"name":        name,
		"value":       value,
	}
}

// TestAggregateHostHardware_PrefersTiDBInstanceLoad 验证多实例主机的负载取数是确定性的。
//
// 现象（QA 实测回归点）：同一台主机上同时有 2379/4000/20160 三个实例时，旧实现
// 「后写覆盖前写」，结果取决于数据库返回顺序 —— 同一份数据两次采集可能给出
// 24%（tidb idle=0.77）或 15%（tikv 视角）两种读数。
//
// 规则：TYPE='tidb' 的实例优先，其次 INSTANCE 字符串最小者；与输入行顺序无关。
func TestAggregateHostHardware_PrefersTiDBInstanceLoad(t *testing.T) {
	hw := []map[string]string{
		hardwareRow("10.10.10.10:4000", "cpu", "cpu", "cpu-logical-cores", "2"),
	}
	// pd(2379) idle=0.20 → 80%；tidb(4000) idle=0.85 → 15%；tikv(20160) idle=0.50 → 50%。
	// 内存同理：tidb used-percent=0.60 → 60%。
	load := []map[string]string{
		loadRowWithType("10.10.10.10:2379", "pd", "cpu", "usage", "idle", "0.20"),
		loadRowWithType("10.10.10.10:4000", "tidb", "cpu", "usage", "idle", "0.85"),
		loadRowWithType("10.10.10.10:20160", "tikv", "cpu", "usage", "idle", "0.50"),
		loadRowWithType("10.10.10.10:2379", "pd", "memory", "virtual", "used-percent", "0.20"),
		loadRowWithType("10.10.10.10:4000", "tidb", "memory", "virtual", "used-percent", "0.60"),
		loadRowWithType("10.10.10.10:20160", "tikv", "memory", "virtual", "used-percent", "0.30"),
	}
	// 打乱输入顺序等价于数据库返回顺序变化：结果必须始终是 tidb 的读数。
	orders := [][]int{
		{0, 1, 2, 3, 4, 5},
		{5, 4, 3, 2, 1, 0},
		{2, 0, 5, 1, 4, 3},
		{3, 1, 4, 0, 2, 5},
	}
	for _, order := range orders {
		rows := make([]map[string]string, 0, len(load))
		for _, i := range order {
			rows = append(rows, load[i])
		}
		for run := 0; run < 5; run++ {
			got := AggregateHostHardware(hw, rows)
			if len(got) != 1 {
				t.Fatalf("主机数 = %d, 期望 1: %+v", len(got), got)
			}
			if got[0].CPUUsagePct != 15 {
				t.Fatalf("顺序 %v 第 %d 次: CPUUsagePct = %v, 期望恒为 15（tidb 实例 idle=0.85 优先）",
					order, run, got[0].CPUUsagePct)
			}
			if got[0].MemUsagePct != 60 {
				t.Fatalf("顺序 %v 第 %d 次: MemUsagePct = %v, 期望恒为 60（tidb 实例 used-percent=0.60 优先）",
					order, run, got[0].MemUsagePct)
			}
		}
	}
}

// TestAggregateHostHardware_LoadWithoutTypeIsDeterministic 验证无 TYPE 列时的兜底规则。
//
// TYPE 缺失（旧版本 / 只给了 device 行）时按 INSTANCE 字符串升序取第一台：
// "10.10.10.10:20160" < "10.10.10.10:2379" < "10.10.10.10:4000"（'0' < '3' < '4'），
// 因此固定取 20160 那台（idle=0.50 → 50%）。重点不是「哪个值更合理」，
// 而是**同样输入永远同样输出**，不随 map 遍历顺序或数据库返回顺序漂移。
func TestAggregateHostHardware_LoadWithoutTypeIsDeterministic(t *testing.T) {
	hw := []map[string]string{
		hardwareRow("10.10.10.10:4000", "cpu", "cpu", "cpu-logical-cores", "2"),
	}
	load := []map[string]string{
		hardwareRow("10.10.10.10:4000", "cpu", "usage", "idle", "0.85"),  // 4000 → 15%
		hardwareRow("10.10.10.10:20160", "cpu", "usage", "idle", "0.50"), // 20160 → 50%（字符串最小）
		hardwareRow("10.10.10.10:2379", "cpu", "usage", "idle", "0.20"),  // 2379 → 80%
	}
	orders := [][]int{{0, 1, 2}, {2, 1, 0}, {1, 2, 0}}
	var want float64 = -1
	for _, order := range orders {
		rows := make([]map[string]string, 0, len(load))
		for _, i := range order {
			rows = append(rows, load[i])
		}
		for run := 0; run < 10; run++ {
			got := AggregateHostHardware(hw, rows)
			if len(got) != 1 {
				t.Fatalf("主机数 = %d, 期望 1: %+v", len(got), got)
			}
			v := got[0].CPUUsagePct
			if want < 0 {
				want = v
			}
			if v != want {
				t.Fatalf("顺序 %v 第 %d 次: CPUUsagePct = %v, 与首次 %v 不一致（取数不确定）",
					order, run, v, want)
			}
		}
	}
	if want != 50 {
		t.Errorf("CPUUsagePct = %v, 期望 50（INSTANCE 字符串最小的 10.10.10.10:20160, idle=0.50）", want)
	}
}

// TestAggregateHostHardware_LoadFallsBackPerMetric 验证 tidb 实例缺少某个指标时仍能取到值。
//
// 真实场景：某台机器的 tidb 实例只回了 cpu 行（memory 行被超时截断），
// 此时 CPU 用 tidb 的读数、内存退到下一优先级的实例，而不是两个指标一起变 0。
func TestAggregateHostHardware_LoadFallsBackPerMetric(t *testing.T) {
	hw := []map[string]string{
		hardwareRow("10.10.10.10:4000", "cpu", "cpu", "cpu-logical-cores", "2"),
	}
	load := []map[string]string{
		loadRowWithType("10.10.10.10:4000", "tidb", "cpu", "usage", "idle", "0.85"),
		loadRowWithType("10.10.10.10:20160", "tikv", "memory", "virtual", "used-percent", "0.40"),
	}
	got := AggregateHostHardware(hw, load)
	if len(got) != 1 {
		t.Fatalf("主机数 = %d, 期望 1: %+v", len(got), got)
	}
	if got[0].CPUUsagePct != 15 {
		t.Errorf("CPUUsagePct = %v, 期望 15（tidb idle=0.85）", got[0].CPUUsagePct)
	}
	if got[0].MemUsagePct != 40 {
		t.Errorf("MemUsagePct = %v, 期望 40（tidb 无 memory 行时退到 tikv）", got[0].MemUsagePct)
	}
}

// TestAggregateHostHardware_RealRows 用真实样例行验证字段口径。
func TestAggregateHostHardware_RealRows(t *testing.T) {
	got := AggregateHostHardware(realHardwareFixture(), realLoadFixture())
	if len(got) != 2 {
		t.Fatalf("主机数 = %d, 期望 2（0.0.0.0 应被跳过）: %+v", len(got), got)
	}
	byIP := map[string]HostResource{}
	for _, h := range got {
		byIP[h.IP] = h
	}
	first, ok := byIP["10.10.10.10"]
	if !ok {
		t.Fatalf("缺少 10.10.10.10 的聚合结果: %+v", got)
	}
	// 架构：arm64 → aarch64（NormalizeArch）。
	if first.Arch != "aarch64" {
		t.Errorf("Arch = %q, 期望 aarch64", first.Arch)
	}
	// 核数：逻辑核优先。
	if first.CPUCores != 2 {
		t.Errorf("CPUCores = %d, 期望 2", first.CPUCores)
	}
	// 内存：3661103104 / 1024^3 = 3.4099… → 3.4
	if math.Abs(first.MemoryGB-3.4) > 0.001 {
		t.Errorf("MemoryGB = %v, 期望 3.4", first.MemoryGB)
	}
	// 磁盘：选 total 最大的挂载点（/data = 137371844608 → 127.9GB）。
	if math.Abs(first.DiskTotalGB-127.9) > 0.001 {
		t.Errorf("DiskTotalGB = %v, 期望 127.9（应选 /data 而非 /）", first.DiskTotalGB)
	}
	// 磁盘使用率：0.0072*100 = 0.72 → 0.7
	if math.Abs(first.DiskUsagePct-0.7) > 0.001 {
		t.Errorf("DiskUsagePct = %v, 期望 0.7", first.DiskUsagePct)
	}
	// CPU 使用率：(1-0.77)*100 = 23.0
	if math.Abs(first.CPUUsagePct-23.0) > 0.001 {
		t.Errorf("CPUUsagePct = %v, 期望 23", first.CPUUsagePct)
	}
	// 内存使用率：cluster_load virtual used-percent = 0.80 → 80
	if math.Abs(first.MemUsagePct-80.0) > 0.001 {
		t.Errorf("MemUsagePct = %v, 期望 80（swap 的 NaN 不得覆盖 virtual）", first.MemUsagePct)
	}

	second, ok := byIP["10.10.10.11"]
	if !ok {
		t.Fatalf("缺少 10.10.10.11 的聚合结果: %+v", got)
	}
	if second.Arch != "x86_64" {
		t.Errorf("Arch = %q, 期望 x86_64", second.Arch)
	}
	if second.CPUCores != 8 {
		t.Errorf("CPUCores = %d, 期望 8", second.CPUCores)
	}
	if math.Abs(second.MemoryGB-16.0) > 0.001 {
		t.Errorf("MemoryGB = %v, 期望 16", second.MemoryGB)
	}
	if math.Abs(second.DiskTotalGB-50.0) > 0.001 {
		t.Errorf("DiskTotalGB = %v, 期望 50", second.DiskTotalGB)
	}
	// 没有 used-percent 时用 used/total 反算：50%。
	if math.Abs(second.DiskUsagePct-50.0) > 0.001 {
		t.Errorf("DiskUsagePct = %v, 期望 50", second.DiskUsagePct)
	}
	// 该主机没有 cluster_load 行：使用率保持 0，不得凭空造数。
	if second.CPUUsagePct != 0 || second.MemUsagePct != 0 {
		t.Errorf("无 load 行时应为 0，实际 CPU=%v Mem=%v", second.CPUUsagePct, second.MemUsagePct)
	}
}

// TestAggregateHostHardware_FallbackAndTolerance 覆盖核数回退、脏数据容错与边界。
func TestAggregateHostHardware_FallbackAndTolerance(t *testing.T) {
	tests := []struct {
		name     string
		hw       []map[string]string
		load     []map[string]string
		wantIP   string
		wantCore int
		wantMem  float64
		wantCPU  float64
		wantMemU float64
		wantCnt  int
	}{
		{
			name: "物理核兜底",
			hw: []map[string]string{
				hardwareRow("10.0.0.1:4000", "cpu", "cpu", "cpu-physical-cores", "4"),
			},
			wantIP: "10.0.0.1", wantCore: 4, wantCnt: 1,
		},
		{
			name: "空串与非法值不 panic，按缺省处理",
			hw: []map[string]string{
				hardwareRow("10.0.0.2:4000", "cpu", "cpu", "cpu-logical-cores", ""),
				hardwareRow("10.0.0.2:4000", "memory", "memory", "capacity", "abc"),
				hardwareRow("10.0.0.2:4000", "disk", "vda", "total", ""),
				hardwareRow("10.0.0.2:4000", "disk", "vda", "used-percent", "NaN"),
			},
			wantIP: "10.0.0.2", wantCore: 0, wantMem: 0, wantCnt: 1,
		},
		{
			name: "带单位的内存容量",
			hw: []map[string]string{
				hardwareRow("10.0.0.3:4000", "memory", "memory", "capacity", "16GiB"),
			},
			wantIP: "10.0.0.3", wantMem: 16, wantCnt: 1,
		},
		{
			name: "idle 越界时夹到 0~100",
			hw: []map[string]string{
				hardwareRow("10.0.0.4:4000", "cpu", "cpu", "cpu-logical-cores", "2"),
			},
			load: []map[string]string{
				hardwareRow("10.0.0.4:4000", "cpu", "usage", "idle", "1.4"),
				hardwareRow("10.0.0.4:4000", "memory", "virtual", "used-percent", "NaN"),
				hardwareRow("10.0.0.4:4000", "memory", "virtual", "used", "0"),
			},
			wantIP: "10.0.0.4", wantCore: 2, wantCPU: 0, wantMemU: 0, wantCnt: 1,
		},
		{
			name: "内存 used-percent 缺失时用 used/total 反算",
			hw: []map[string]string{
				hardwareRow("10.0.0.5:4000", "memory", "memory", "capacity", "8589934592"),
				hardwareRow("10.0.0.5:4000", "memory", "memory", "used", "2147483648"),
			},
			wantIP: "10.0.0.5", wantMem: 8, wantMemU: 25, wantCnt: 1,
		},
		{
			name: "全部是本机/通配地址时返回空",
			hw: []map[string]string{
				hardwareRow("127.0.0.1:4000", "cpu", "cpu", "cpu-logical-cores", "2"),
				hardwareRow("0.0.0.0:4000", "cpu", "cpu", "cpu-logical-cores", "2"),
				hardwareRow("[::]:4000", "cpu", "cpu", "cpu-logical-cores", "2"),
				hardwareRow(":4000", "cpu", "cpu", "cpu-logical-cores", "2"),
			},
			wantCnt: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := AggregateHostHardware(tc.hw, tc.load)
			if len(got) != tc.wantCnt {
				t.Fatalf("主机数 = %d, 期望 %d: %+v", len(got), tc.wantCnt, got)
			}
			if tc.wantCnt == 0 {
				return
			}
			h := got[0]
			if h.IP != tc.wantIP {
				t.Errorf("IP = %q, 期望 %q", h.IP, tc.wantIP)
			}
			if h.CPUCores != tc.wantCore {
				t.Errorf("CPUCores = %d, 期望 %d", h.CPUCores, tc.wantCore)
			}
			if math.Abs(h.MemoryGB-tc.wantMem) > 0.001 {
				t.Errorf("MemoryGB = %v, 期望 %v", h.MemoryGB, tc.wantMem)
			}
			if math.Abs(h.CPUUsagePct-tc.wantCPU) > 0.001 {
				t.Errorf("CPUUsagePct = %v, 期望 %v", h.CPUUsagePct, tc.wantCPU)
			}
			if math.Abs(h.MemUsagePct-tc.wantMemU) > 0.001 {
				t.Errorf("MemUsagePct = %v, 期望 %v", h.MemUsagePct, tc.wantMemU)
			}
		})
	}
}

// TestAggregateHostHardware_Empty 空输入返回空切片，不 panic。
func TestAggregateHostHardware_Empty(t *testing.T) {
	if got := AggregateHostHardware(nil, nil); len(got) != 0 {
		t.Fatalf("空输入应返回空切片，实际 %+v", got)
	}
}

// TestParseHardwareFloat 覆盖 VALUE 列的各类脏数据。
func TestParseHardwareFloat(t *testing.T) {
	cases := map[string]float64{
		"":           0,
		"NaN":        0,
		"nan":        0,
		"+Inf":       0,
		"abc":        0,
		"3661103104": 3661103104,
		"0.77":       0.77,
		"16GiB":      16 * 1024 * 1024 * 1024,
		"1.5G":       1.5 * 1024 * 1024 * 1024,
		"512M":       512 * 1024 * 1024,
		"128KiB":     128 * 1024,
		"1024 bytes": 1024,
	}
	for raw, want := range cases {
		if got := parseHardwareFloat(raw); math.Abs(got-want) > 0.001 {
			t.Errorf("parseHardwareFloat(%q) = %v, 期望 %v", raw, got, want)
		}
	}
}

// TestHardwareHostIP 覆盖 INSTANCE → 主机 IP 的解析。
func TestHardwareHostIP(t *testing.T) {
	cases := map[string]string{
		"10.10.10.10:4000": "10.10.10.10",
		"10.10.10.10":      "10.10.10.10",
		"[fe80::1]:4000":   "fe80::1",
		"":                 "",
		"  10.0.0.1:4000 ": "10.0.0.1",
	}
	for in, want := range cases {
		if got := HardwareHostIP(in); got != want {
			t.Errorf("HardwareHostIP(%q) = %q, 期望 %q", in, got, want)
		}
	}
	for _, ip := range []string{"", "0.0.0.0", "::", "127.0.0.1", "::1", "localhost"} {
		if !SkipHardwareHost(ip) {
			t.Errorf("SkipHardwareHost(%q) 应为 true", ip)
		}
	}
	for _, ip := range []string{"10.10.10.10", "192.168.1.1"} {
		if SkipHardwareHost(ip) {
			t.Errorf("SkipHardwareHost(%q) 应为 false", ip)
		}
	}
}
