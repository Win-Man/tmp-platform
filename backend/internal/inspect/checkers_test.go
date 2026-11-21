package inspect

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/tmp-platform/backend/internal/collector"
	"github.com/tmp-platform/backend/internal/model"
)

// ---------- 判定函数注册表 ----------

// TestCheckerRegistry 断言注册表规模与关键项存在，且 Lookup 对未知 code 返回 false。
func TestCheckerRegistry(t *testing.T) {
	if len(checkers) < 25 {
		t.Fatalf("判定函数注册表仅 %d 项，PRD 要求 5 类 ≥ 25 项", len(checkers))
	}
	for _, code := range []string{"SYS-01", "SYS-03", "SEC-01", "HA-04", "CAP-01", "VER-01"} {
		if _, ok := Lookup(code); !ok {
			t.Fatalf("注册表缺少判定函数 %s", code)
		}
	}
	if _, ok := Lookup("NOT-EXIST"); ok {
		t.Fatal("Lookup 对未知 code 应返回 false")
	}
}

// ---------- 单个判定函数：通过 / 警告 / 严重 三态 ----------

// sink 构造一个带主机的快照，便于各用例复用。
func sinkCPU(cpu float64) *collector.Snapshot {
	return &collector.Snapshot{
		Hosts: []collector.HostResource{
			{IP: "10.0.0.1", CPUUsagePct: cpu, MemUsagePct: 10, DiskUsagePct: 20, Arch: "x86_64"},
		},
	}
}

func baseInput(snap *collector.Snapshot) *Input {
	return &Input{
		Cluster:  &model.Cluster{ID: 1, Name: "c1", TiDBVersion: "v7.5.2", DeployMode: "majority"},
		Snapshot: snap,
		Thresh:   DefaultThresholds(), // CPU 80 / Disk 85
		Now:      time.Now(),
	}
}

func TestCheckCPUUsage_ThreeStates(t *testing.T) {
	cases := []struct {
		name string
		cpu  float64
		want string
	}{
		{"通过（低于阈值）", 40, model.LevelPass},
		{"警告（等于阈值）", 80, model.LevelWarning},
		{"警告（高于阈值）", 95.5, model.LevelWarning},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkCPUUsage(baseInput(sinkCPU(tc.cpu)))
			if got.Level != tc.want {
				t.Fatalf("cpu=%.1f 时 level = %q, want %q（actual=%q）", tc.cpu, got.Level, tc.want, got.Actual)
			}
		})
	}
	// 无主机数据 → unknown
	if got := checkCPUUsage(baseInput(&collector.Snapshot{})); got.Level != model.LevelUnknown {
		t.Fatalf("无主机数据应 unknown，实际 %q", got.Level)
	}
}

func TestCheckDiskUsage_ThreeStates(t *testing.T) {
	mk := func(disk float64) *collector.Snapshot {
		return &collector.Snapshot{Hosts: []collector.HostResource{
			{IP: "10.0.0.1", DiskUsagePct: disk, DiskTotalGB: 500},
		}}
	}
	cases := []struct {
		name string
		disk float64
		want string
	}{
		{"通过", 50, model.LevelPass},
		{"严重（等于阈值）", 85, model.LevelCritical},
		{"严重（超过阈值）", 93, model.LevelCritical},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkDiskUsage(baseInput(mk(tc.disk)))
			if got.Level != tc.want {
				t.Fatalf("disk=%.1f 时 level = %q, want %q", tc.disk, got.Level, tc.want)
			}
		})
	}
}

// TestCheckInstanceStatus_ThreeStates HA-04：全部 up / 部分 down。
func TestCheckInstanceStatus_ThreeStates(t *testing.T) {
	up := &collector.Snapshot{Instances: []collector.InstanceSnapshot{
		{Component: "tidb", HostIP: "10.0.0.1", Port: 4000, Status: "up"},
		{Component: "tikv", HostIP: "10.0.0.2", Port: 20160, Status: "up"},
	}}
	if got := checkInstanceStatus(baseInput(up)); got.Level != model.LevelPass {
		t.Fatalf("全部 up 应 pass，实际 %q", got.Level)
	}

	down := &collector.Snapshot{Instances: []collector.InstanceSnapshot{
		{Component: "tidb", HostIP: "10.0.0.1", Port: 4000, Status: "up"},
		{Component: "tikv", HostIP: "10.0.0.2", Port: 20160, Status: "down"},
	}}
	got := checkInstanceStatus(baseInput(down))
	if got.Level != model.LevelCritical {
		t.Fatalf("存在 down 实例应 critical，实际 %q", got.Level)
	}
	if got.Actual == "" {
		t.Fatal("critical 结果必须给出 actual 描述")
	}

	if got := checkInstanceStatus(baseInput(&collector.Snapshot{})); got.Level != model.LevelUnknown {
		t.Fatalf("无实例数据应 unknown，实际 %q", got.Level)
	}
}

// TestCheckTiKVWaterLevel_NA CAP-01：无 store 数据时必须是「不适用」而非「通过」。
func TestCheckTiKVWaterLevel_NA(t *testing.T) {
	got := checkTiKVWaterLevel(baseInput(&collector.Snapshot{}))
	if got.Level != model.LevelNA {
		t.Fatalf("无 PD store 数据应为 na（不适用），实际 %q", got.Level)
	}

	// 水位 90% >= 阈值 85% → critical
	high := &collector.Snapshot{Stores: []collector.Store{
		{Capacity: 100 << 30, Available: 10 << 30},
	}}
	if got := checkTiKVWaterLevel(baseInput(high)); got.Level != model.LevelCritical {
		t.Fatalf("水位 90%% 应 critical，实际 %q", got.Level)
	}
	low := &collector.Snapshot{Stores: []collector.Store{
		{Capacity: 100 << 30, Available: 50 << 30},
	}}
	if got := checkTiKVWaterLevel(baseInput(low)); got.Level != model.LevelPass {
		t.Fatalf("水位 50%% 应 pass，实际 %q", got.Level)
	}
}

// TestCheckRootHost_ThreeStates SEC-01：root@% 必须 critical。
func TestCheckRootHost_ThreeStates(t *testing.T) {
	ok := &collector.Snapshot{Users: []collector.TiDBUserRow{{UserName: "root", Host: "127.0.0.1"}}}
	if got := checkRootHost(baseInput(ok)); got.Level != model.LevelPass {
		t.Fatalf("root 仅本地应 pass，实际 %q", got.Level)
	}
	bad := &collector.Snapshot{Users: []collector.TiDBUserRow{{UserName: "root", Host: "%"}}}
	got := checkRootHost(baseInput(bad))
	if got.Level != model.LevelCritical {
		t.Fatalf("root@%% 应 critical，实际 %q", got.Level)
	}
	if got.Evidence == "" {
		t.Fatal("critical 结果必须给出证据")
	}
}

// TestCheckOOMAction_ThreeStates CFG-02：非 cancel 必须 critical。
func TestCheckOOMAction_ThreeStates(t *testing.T) {
	mk := func(v string) *collector.Snapshot {
		return &collector.Snapshot{Variables: []collector.VariableRow{{Name: "oom-action", Value: v}}}
	}
	if got := checkOOMAction(baseInput(mk("cancel"))); got.Level != model.LevelPass {
		t.Fatalf("oom-action=cancel 应 pass，实际 %q", got.Level)
	}
	if got := checkOOMAction(baseInput(mk("log"))); got.Level != model.LevelCritical {
		t.Fatalf("oom-action=log 应 critical，实际 %q", got.Level)
	}
	if got := checkOOMAction(baseInput(&collector.Snapshot{})); got.Level != model.LevelUnknown {
		t.Fatalf("缺参数应 unknown，实际 %q", got.Level)
	}
}

// TestCheckPDReplicas_ThreeStates HA-01：<3 个 PD 必须 critical。
func TestCheckPDReplicas_ThreeStates(t *testing.T) {
	mk := func(n int) *collector.Snapshot {
		s := &collector.Snapshot{}
		for i := 0; i < n; i++ {
			s.Instances = append(s.Instances, collector.InstanceSnapshot{
				Component: "pd", HostIP: "10.0.0.1", Port: 2379, Status: "up",
			})
		}
		return s
	}
	if got := checkPDReplicas(baseInput(mk(3))); got.Level != model.LevelPass {
		t.Fatalf("3 个 PD 应 pass，实际 %q", got.Level)
	}
	if got := checkPDReplicas(baseInput(mk(1))); got.Level != model.LevelCritical {
		t.Fatalf("1 个 PD 应 critical，实际 %q", got.Level)
	}
}

// TestCheckVersionBaseline_ThreeStates VER-01：< v6.5 必须 critical。
func TestCheckVersionBaseline_ThreeStates(t *testing.T) {
	mk := func(v string) *Input {
		in := baseInput(&collector.Snapshot{})
		in.Cluster.TiDBVersion = v
		return in
	}
	if got := checkVersionBaseline(mk("v7.5.2")); got.Level != model.LevelPass {
		t.Fatalf("v7.5.2 应 pass，实际 %q", got.Level)
	}
	if got := checkVersionBaseline(mk("v6.1.0")); got.Level != model.LevelCritical {
		t.Fatalf("v6.1.0 应 critical，实际 %q", got.Level)
	}
	if got := checkVersionBaseline(mk("")); got.Level != model.LevelUnknown {
		t.Fatalf("无版本应 unknown，实际 %q", got.Level)
	}
}

// ---------- 巡检通过率公式 ----------

// TestExecute_PassRateExcludesNAAndUnknown 断言 N/A 与 Unknown 不计入分母。
//
// 公式：通过率 = pass / (critical + warning + pass)，N/A / unknown 不计入分母。
func TestExecute_PassRateExcludesNAAndUnknown(t *testing.T) {
	e := NewEngine()
	items := []model.InspectItem{
		{Code: "SYS-01", Name: "CPU", Category: "系统", Severity: "warning"},     // pass（cpu 未超阈值）
		{Code: "SYS-03", Name: "磁盘", Category: "系统", Severity: "critical"},     // pass（disk 未超阈值）
		{Code: "CFG-02", Name: "OOM", Category: "配置", Severity: "critical"},    // critical（oom-action=log）
		{Code: "CFG-05", Name: "连接数", Category: "配置", Severity: "warning"},     // warning（max_connections=100）
		{Code: "CAP-01", Name: "水位", Category: "容量", Severity: "critical"},     // na（无 stores）
		{Code: "NOT-EXIST", Name: "缺失判定", Category: "其他", Severity: "warning"}, // unknown
	}
	snap := &collector.Snapshot{
		Hosts: []collector.HostResource{{IP: "10.0.0.1", CPUUsagePct: 10, DiskUsagePct: 10, Arch: "x86_64"}},
		Variables: []collector.VariableRow{
			{Name: "oom-action", Value: "log"},
			{Name: "max_connections", Value: "100"},
		},
	}
	res, err := e.Execute(context.Background(), ExecuteRequest{
		Cluster:  &model.Cluster{ID: 1, Name: "c1", TiDBVersion: "v7.5.2"},
		Snapshot: snap,
		Items:    items,
		Thresh:   DefaultThresholds(),
	})
	if err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}

	if res.Total != 6 {
		t.Fatalf("Total = %d, want 6", res.Total)
	}
	if res.PassCount != 2 || res.CriticalCount != 1 || res.WarningCount != 1 {
		t.Fatalf("统计不符: pass=%d critical=%d warning=%d", res.PassCount, res.CriticalCount, res.WarningCount)
	}
	if res.NACount != 1 {
		t.Fatalf("NACount = %d, want 1（CAP-01 无 store 数据）", res.NACount)
	}
	if res.UnknownCount != 1 {
		t.Fatalf("UnknownCount = %d, want 1（未实现判定函数）", res.UnknownCount)
	}

	// 2 / (1+1+2) = 50.0
	want := 50.0
	if math.Abs(res.PassRate-want) > 0.05 {
		t.Fatalf("PassRate = %.1f, want %.1f（N/A 与 unknown 不计入分母）", res.PassRate, want)
	}
	if res.Conclusion != "fail" {
		t.Fatalf("存在 critical 时结论应为 fail，实际 %q", res.Conclusion)
	}
}

// TestExecute_ConclusionRisk 仅警告时结论为 risk。
func TestExecute_ConclusionRisk(t *testing.T) {
	e := NewEngine()
	items := []model.InspectItem{
		{Code: "CFG-05", Name: "连接数", Category: "配置", Severity: "warning"},
		{Code: "SYS-01", Name: "CPU", Category: "系统", Severity: "warning"},
	}
	snap := &collector.Snapshot{
		Hosts:     []collector.HostResource{{IP: "10.0.0.1", CPUUsagePct: 10, Arch: "x86_64"}},
		Variables: []collector.VariableRow{{Name: "max_connections", Value: "100"}},
	}
	res, err := e.Execute(context.Background(), ExecuteRequest{
		Cluster:  &model.Cluster{ID: 1, Name: "c1", TiDBVersion: "v7.5.2"},
		Snapshot: snap,
		Items:    items,
		Thresh:   DefaultThresholds(),
	})
	if err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	if res.WarningCount != 1 || res.PassCount != 1 {
		t.Fatalf("统计不符: warning=%d pass=%d", res.WarningCount, res.PassCount)
	}
	if res.PassRate != 50.0 {
		t.Fatalf("PassRate = %.1f, want 50.0", res.PassRate)
	}
	if res.Conclusion != "risk" {
		t.Fatalf("仅警告时结论应为 risk，实际 %q", res.Conclusion)
	}
}

// TestExecute_AppliesNotApplicable 版本不满足 applies 时降级为 na 且不计入分母。
func TestExecute_AppliesNotApplicable(t *testing.T) {
	e := NewEngine()
	items := []model.InspectItem{
		{Code: "CFG-07", Name: "资源管控", Category: "配置", Severity: "warning", Applies: "min_version:7.5.0"},
	}
	snap := &collector.Snapshot{
		Variables: []collector.VariableRow{{Name: "tidb_enable_resource_control", Value: "ON"}},
	}
	res, err := e.Execute(context.Background(), ExecuteRequest{
		Cluster:  &model.Cluster{ID: 1, Name: "c1", TiDBVersion: "v6.5.1"},
		Snapshot: snap,
		Items:    items,
		Thresh:   DefaultThresholds(),
	})
	if err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	if res.NACount != 1 || res.PassCount != 0 {
		t.Fatalf("v6.5.1 下 min_version:7.5.0 项应 na，实际 na=%d pass=%d", res.NACount, res.PassCount)
	}
	if res.PassRate != 0 {
		t.Fatalf("分母为 0 时 PassRate 应为 0，实际 %.1f", res.PassRate)
	}
}

// TestExecute_AllNAPassRate zero 分母不 panic，通过率为 0。
func TestExecute_AllNAPassRate(t *testing.T) {
	e := NewEngine()
	res, err := e.Execute(context.Background(), ExecuteRequest{
		Cluster:  &model.Cluster{ID: 1, Name: "c1", TiDBVersion: "v7.5.2"},
		Snapshot: &collector.Snapshot{},
		Items: []model.InspectItem{
			{Code: "CAP-01", Name: "水位", Category: "容量", Severity: "critical"},
		},
		Thresh: DefaultThresholds(),
	})
	if err != nil {
		t.Fatalf("Execute 失败: %v", err)
	}
	if res.PassRate != 0 {
		t.Fatalf("全 N/A 时 PassRate = %.1f, want 0", res.PassRate)
	}
}
