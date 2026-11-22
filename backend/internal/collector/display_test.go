package collector

import (
	"os"
	"reflect"
	"testing"
)

// readDisplayFixture 读取 `tiup cluster display` 的真实输出样本。
func readDisplayFixture(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatalf("读取测试样本 %s 失败: %v", name, err)
	}
	return string(raw)
}

// TestDisplay_JSONAndTextEquivalent 断言 JSON 与文本两条解析路径产出同构的归一化结果。
func TestDisplay_JSONAndTextEquivalent(t *testing.T) {
	type parseFunc func(string) (*TiUPDisplay, error)
	cases := []struct {
		name       string
		fixture    string
		parse      parseFunc
		wantSource string
	}{
		{name: "json", fixture: "tiup-display.json", parse: ParseDisplayJSON, wantSource: DisplaySourceJSON},
		{name: "text", fixture: "tiup-display.txt", parse: ParseDisplayText, wantSource: DisplaySourceText},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			display, err := tc.parse(readDisplayFixture(t, tc.fixture))
			if err != nil {
				t.Fatalf("解析失败: %v", err)
			}
			if got := display.Source(); got != tc.wantSource {
				t.Fatalf("Source = %q, want %q", got, tc.wantSource)
			}
			if got := display.ClusterMeta.ClusterVersion; got != "v8.5.5" {
				t.Fatalf("ClusterVersion = %q, want v8.5.5", got)
			}
			if len(display.Instances) != 5 {
				t.Fatalf("实例数 = %d, want 5: %+v", len(display.Instances), display.Instances)
			}

			meta := display.ToMeta()
			if meta.Source != tc.wantSource {
				t.Fatalf("meta.Source = %q, want %q", meta.Source, tc.wantSource)
			}
			if meta.TiDBVersion != "v8.5.5" {
				t.Fatalf("meta.TiDBVersion = %q, want v8.5.5", meta.TiDBVersion)
			}
			if !metaHasServers(meta) {
				t.Fatalf("meta 未产出任何 server: %+v", meta)
			}

			n := NormalizeMeta(meta, "tidb-demo")
			if n.Version != "v8.5.5" {
				t.Fatalf("归一化版本 = %q, want v8.5.5", n.Version)
			}
			if len(n.Instances) != 5 {
				t.Fatalf("归一化实例数 = %d, want 5: %+v", len(n.Instances), n.Instances)
			}
			if n.HostCount != 2 {
				t.Fatalf("主机数 = %d, want 2", n.HostCount)
			}
			if n.ComponentCount != 5 {
				t.Fatalf("组件种类 = %d, want 5", n.ComponentCount)
			}
			if n.Arch != "aarch64" {
				t.Fatalf("架构 = %q, want aarch64（来自 linux/aarch64）", n.Arch)
			}

			expectURLs := func(name string, got []string, want ...string) {
				t.Helper()
				if len(got) != len(want) {
					t.Fatalf("%s = %v, want %v", name, got, want)
				}
				for i := range want {
					if got[i] != want[i] {
						t.Fatalf("%s = %v, want %v", name, got, want)
					}
				}
			}
			expectURLs("DashboardURLs", n.DashboardURLs, "http://10.10.10.10:2379/dashboard")
			expectURLs("GrafanaURLs", n.GrafanaURLs, "http://10.10.10.11:3000")
			expectURLs("PrometheusURLs", n.PrometheusURLs, "http://10.10.10.11:9090")

			// PD：主端口 / status 端口（peer）/ leader（来自 Up|L|UI）。
			pd, ok := findInstance(n.Instances, "pd", "10.10.10.10")
			if !ok {
				t.Fatal("缺少 pd 实例")
			}
			if pd.Port != 2379 || pd.StatusPort != 2380 {
				t.Fatalf("PD 端口 = %d/%d, want 2379/2380", pd.Port, pd.StatusPort)
			}
			if pd.Role != "leader" {
				t.Fatalf("PD role = %q, want leader（Up|L|UI）", pd.Role)
			}
			if pd.DataDir != "/tidb-data/pd-2379" || pd.DeployDir != "/tidb-deploy/pd-2379" {
				t.Fatalf("PD 目录 = %q / %q", pd.DataDir, pd.DeployDir)
			}

			tidb, ok := findInstance(n.Instances, "tidb", "10.10.10.10")
			if !ok {
				t.Fatal("缺少 tidb 实例")
			}
			if tidb.Port != 4000 || tidb.StatusPort != 10080 {
				t.Fatalf("TiDB 端口 = %d/%d, want 4000/10080", tidb.Port, tidb.StatusPort)
			}
			// `-` 表示的 data_dir 应归一为空串，而不是字面量 "-"。
			if tidb.DataDir != "" {
				t.Fatalf("TiDB dataDir = %q, want 空（原值为 -）", tidb.DataDir)
			}

			tikv, ok := findInstance(n.Instances, "tikv", "10.10.10.10")
			if !ok {
				t.Fatal("缺少 tikv 实例")
			}
			if tikv.Port != 20160 || tikv.StatusPort != 20180 {
				t.Fatalf("TiKV 端口 = %d/%d, want 20160/20180", tikv.Port, tikv.StatusPort)
			}

			prom, ok := findInstance(n.Instances, "prometheus", "10.10.10.11")
			if !ok {
				t.Fatal("缺少 prometheus 实例")
			}
			if prom.Port != 9090 {
				t.Fatalf("Prometheus 主端口 = %d, want 9090（端口串 9090/9105/9100/12020 的首段）", prom.Port)
			}
		})
	}
}

// TestParseDisplayJSON_TrailingExitCode 证明 JSON 尾部额外的 {"exit_code":0} 不影响解析。
func TestParseDisplayJSON_TrailingExitCode(t *testing.T) {
	raw := readDisplayFixture(t, "tiup-display.json")
	display, err := ParseDisplayJSON(raw)
	if err != nil {
		t.Fatalf("带尾部 {\"exit_code\":0} 时解析失败: %v", err)
	}
	if len(display.Instances) != 5 {
		t.Fatalf("实例数 = %d, want 5", len(display.Instances))
	}
	if display.ClusterMeta.ClusterVersion != "v8.5.5" {
		t.Fatalf("ClusterVersion = %q", display.ClusterMeta.ClusterVersion)
	}
	// 末行为非零 exit_code 时同样只解码第一个 JSON 值。
	display2, err := ParseDisplayJSON(raw + "\n{\"exit_code\":1}\n")
	if err != nil {
		t.Fatalf("尾部第二段 JSON 不应影响解析: %v", err)
	}
	if len(display2.Instances) != 5 {
		t.Fatalf("实例数 = %d, want 5", len(display2.Instances))
	}
}

// TestSplitDisplayHeader_HostPortLinesRejected 断言「主机名:端口」形态的实例行不会被当成头部行。
func TestSplitDisplayHeader_HostPortLinesRejected(t *testing.T) {
	for _, line := range []string{"pd0:2379", "tikv0:20160", "10.0.0.1:2379", "grafana-0:3000", "pd-0:2380"} {
		if key, value, ok := splitDisplayHeader(line); ok {
			t.Fatalf("%q 不应被识别为头部行（key=%q value=%q）", line, key, value)
		}
	}
	for _, line := range []string{"Cluster name: demo", "Dashboard URL: http://x:2379/dashboard", "Cluster version: v8.5.5"} {
		if _, _, ok := splitDisplayHeader(line); !ok {
			t.Fatalf("%q 应被识别为头部行", line)
		}
	}
}

// TestDisplayHeaderKeys_InSyncWithApplyHeader 保证白名单与 applyHeader 的 switch 不脱节：
// 白名单里的每个键都必须真正被 applyHeader 消费。
func TestDisplayHeaderKeys_InSyncWithApplyHeader(t *testing.T) {
	for key := range displayHeaderKeys {
		d := &TiUPDisplay{}
		d.applyHeader(key, "true")
		if reflect.DeepEqual(d.ClusterMeta, DisplayClusterMeta{}) {
			t.Fatalf("键 %q 在白名单内但 applyHeader 未处理（白名单与 switch 失同步）", key)
		}
	}
}

// TestParseDisplayJSON_Invalid 非 JSON 输入必须报错而非返回半成品。
func TestParseDisplayJSON_Invalid(t *testing.T) {
	if _, err := ParseDisplayJSON(""); err == nil {
		t.Fatal("空输出应返回错误")
	}
	if _, err := ParseDisplayJSON("not a json at all"); err == nil {
		t.Fatal("非 JSON 输出应返回错误")
	}
}

// TestParseDisplayText_HostnameAddressed 主机名编址（如 pd0:2379、tikv0:20160）的集群
// 必须与 IP 编址一样解析成功：行首的「主机名:端口」不是头部键值行。
func TestParseDisplayText_HostnameAddressed(t *testing.T) {
	raw := `Cluster type:       tidb
Cluster name:       demo
Cluster version:    v8.1.0
Deploy user:        tidb
Dashboard URL:      http://pd0:2379/dashboard
ID                 Role        Host         Ports                 OS/Arch        Status   Data Dir                    Deploy Dir
--                 ----        ----         -----                 -------        ------   --------                    ----------
pd0:2379           pd          pd0          2379/2380             linux/x86_64   Up|L     /data/pd-2379               /deploy/pd-2379
tikv0:20160        tikv        tikv0        20160/20180           linux/x86_64   Up       /data/tikv-20160            /deploy/tikv-20160
tikv1:20160        tikv        tikv1        20160/20180           linux/x86_64   Down     /data/tikv-20161            /deploy/tikv-20160
Total nodes: 3
`
	display, err := ParseDisplayText(raw)
	if err != nil {
		t.Fatalf("主机名编址集群解析失败: %v", err)
	}
	if len(display.Instances) != 3 {
		t.Fatalf("实例数 = %d, want 3: %+v", len(display.Instances), display.Instances)
	}
	// 头部仍要正常解析，不能被实例行干扰。
	if display.ClusterMeta.ClusterVersion != "v8.1.0" {
		t.Fatalf("ClusterVersion = %q, want v8.1.0", display.ClusterMeta.ClusterVersion)
	}
	if len(display.ClusterMeta.DashboardURLs) != 1 ||
		display.ClusterMeta.DashboardURLs[0] != "http://pd0:2379/dashboard" {
		t.Fatalf("DashboardURLs = %v", display.ClusterMeta.DashboardURLs)
	}

	meta := display.ToMeta()
	if !metaHasServers(meta) {
		t.Fatal("ToMeta 未产出 server")
	}
	n := NormalizeMeta(meta, "demo")
	if n.Version != "v8.1.0" {
		t.Fatalf("版本 = %q, want v8.1.0", n.Version)
	}
	if n.HostCount != 3 {
		t.Fatalf("主机数 = %d, want 3（pd0/tikv0/tikv1）", n.HostCount)
	}
	if n.Arch != "x86_64" {
		t.Fatalf("架构 = %q, want x86_64", n.Arch)
	}
	pd, ok := findInstance(n.Instances, "pd", "pd0")
	if !ok {
		t.Fatalf("缺少 pd 实例: %+v", n.Instances)
	}
	if pd.Port != 2379 || pd.StatusPort != 2380 {
		t.Fatalf("PD 端口 = %d/%d, want 2379/2380", pd.Port, pd.StatusPort)
	}
	if pd.Role != "leader" {
		t.Fatalf("PD role = %q, want leader", pd.Role)
	}
	tikvDown, ok := findInstance(n.Instances, "tikv", "tikv1")
	if !ok {
		t.Fatalf("缺少 tikv1 实例: %+v", n.Instances)
	}
	if tikvDown.Port != 20160 || tikvDown.StatusPort != 20180 {
		t.Fatalf("tikv1 端口 = %d/%d, want 20160/20180", tikvDown.Port, tikvDown.StatusPort)
	}
	// display 明确给出 Down 的节点必须体现在实例状态上（默认 up 只在无状态信息时生效）。
	if tikvDown.Status != "down" {
		t.Fatalf("tikv1 状态 = %q, want down", tikvDown.Status)
	}
}

// TestParseDisplayText_MultiURLs 覆盖头部一列多 URL（逗号/空格分隔）的情形。
func TestParseDisplayText_MultiURLs(t *testing.T) {
	raw := `Cluster type:       tidb
Cluster name:       multi-url
Cluster version:    v7.5.2
Deploy user:        tidb
Dashboard URLs:     http://10.0.0.1:2379/dashboard, http://10.0.0.2:2379/dashboard
Grafana URLs:       http://10.0.0.3:3000 http://10.0.0.4:3000
ID                 Role        Host         Ports                 OS/Arch        Status   Data Dir                    Deploy Dir
--                 ----        ----         -----                 -------        ------   --------                    ----------
10.0.0.1:2379      pd          10.0.0.1     2379/2380             linux/x86_64   Up|L      /data/pd-2379               /deploy/pd-2379
10.0.0.3:9090      prometheus  10.0.0.3     9090/9105             linux/x86_64   Up       /data/prom-9090             /deploy/prom-9090
Total nodes: 2
`
	display, err := ParseDisplayText(raw)
	if err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	if len(display.Instances) != 2 {
		t.Fatalf("实例数 = %d, want 2: %+v", len(display.Instances), display.Instances)
	}
	meta := display.ToMeta()
	if len(meta.DashboardURLs) != 2 {
		t.Fatalf("DashboardURLs = %v, want 2 条", meta.DashboardURLs)
	}
	if len(meta.GrafanaURLs) != 2 {
		t.Fatalf("GrafanaURLs = %v, want 2 条", meta.GrafanaURLs)
	}
	n := NormalizeMeta(meta, "multi-url")
	if n.Version != "v7.5.2" {
		t.Fatalf("版本 = %q, want v7.5.2", n.Version)
	}
	if n.Arch != "x86_64" {
		t.Fatalf("架构 = %q, want x86_64", n.Arch)
	}
	if len(n.DashboardURLs) != 2 {
		t.Fatalf("归一化 DashboardURLs = %v, want 2 条", n.DashboardURLs)
	}
	if len(n.GrafanaURLs) != 2 {
		t.Fatalf("归一化 GrafanaURLs = %v, want 2 条", n.GrafanaURLs)
	}
	if n.DashboardURLs[0] != "http://10.0.0.1:2379/dashboard" || n.DashboardURLs[1] != "http://10.0.0.2:2379/dashboard" {
		t.Fatalf("DashboardURLs 顺序/内容错误: %v", n.DashboardURLs)
	}
	if len(n.PrometheusURLs) != 1 || n.PrometheusURLs[0] != "http://10.0.0.3:9090" {
		t.Fatalf("PrometheusURLs = %v, want [http://10.0.0.3:9090]", n.PrometheusURLs)
	}
	// 无 PD 之外的双端口：pd 的 status 端口应取 2380。
	if pd, ok := findInstance(n.Instances, "pd", "10.0.0.1"); !ok || pd.StatusPort != 2380 {
		t.Fatalf("PD = %+v", pd)
	}
}

// TestParseDisplayText_Empty 空输出必须报错。
func TestParseDisplayText_Empty(t *testing.T) {
	if _, err := ParseDisplayText("  \n"); err == nil {
		t.Fatal("空输出应返回错误")
	}
}

// TestSplitPorts 断言端口串拆解规则：首段主端口、第二段 status 端口、其余忽略。
func TestSplitPorts(t *testing.T) {
	cases := []struct {
		in        string
		primary   int
		secondary int
	}{
		{in: "3000", primary: 3000, secondary: 0},
		{in: "2379/2380", primary: 2379, secondary: 2380},
		{in: "9090/9105/9100/12020", primary: 9090, secondary: 9105},
		{in: "", primary: 0, secondary: 0},
		{in: "abc", primary: 0, secondary: 0},
	}
	for _, tc := range cases {
		p, s := splitPorts(tc.in)
		if p != tc.primary || s != tc.secondary {
			t.Fatalf("splitPorts(%q) = (%d,%d), want (%d,%d)", tc.in, p, s, tc.primary, tc.secondary)
		}
	}
}

// TestArchFromOSArch 断言 OS/Arch 列的架构提取。
func TestArchFromOSArch(t *testing.T) {
	cases := map[string]string{
		"linux/aarch64": "aarch64",
		"linux/x86_64":  "x86_64",
		"aarch64":       "aarch64",
		"linux/arm64":   "aarch64",
		"":              "",
	}
	for in, want := range cases {
		if got := archFromOSArch(in); got != want {
			t.Fatalf("archFromOSArch(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestDisplayInstanceStatus 断言状态归一与 PD leader 识别。
func TestDisplayInstanceStatus(t *testing.T) {
	cases := []struct {
		in         string
		wantStatus string
		wantLeader bool
	}{
		{in: "Up", wantStatus: "up"},
		{in: "Up|L|UI", wantStatus: "up", wantLeader: true},
		{in: "Up|UI", wantStatus: "up"},
		{in: "Down", wantStatus: "down"},
		{in: "Offline", wantStatus: "down"},
		{in: "Unhealthy", wantStatus: "unhealthy"},
		{in: "Degraded", wantStatus: "unhealthy"},
		{in: "", wantStatus: "unknown"},
	}
	for _, tc := range cases {
		status, leader := displayInstanceStatus(tc.in)
		if status != tc.wantStatus || leader != tc.wantLeader {
			t.Fatalf("displayInstanceStatus(%q) = (%q,%v), want (%q,%v)", tc.in, status, leader, tc.wantStatus, tc.wantLeader)
		}
	}
}

// TestCanonicalComponent 断言组件别名归一。
func TestCanonicalComponent(t *testing.T) {
	cases := map[string]string{
		"cdc":           "ticdc",
		"ticdc":         "ticdc",
		"monitoring":    "prometheus",
		"monitor":       "prometheus",
		"alert_manager": "alertmanager",
		"PD":            "pd",
		"":              "",
	}
	for in, want := range cases {
		if got := canonicalComponent(in); got != want {
			t.Fatalf("canonicalComponent(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestNormalizeArch_PlatformPrefixed 断言 NormalizeArch 能吃掉 linux/ 前缀。
func TestNormalizeArch_PlatformPrefixed(t *testing.T) {
	cases := map[string]string{
		"linux/aarch64": "aarch64",
		"linux/amd64":   "x86_64",
		"linux/mips64":  "mips64",
		"arm64":         "aarch64",
		"":              "",
	}
	for in, want := range cases {
		if got := NormalizeArch(in); got != want {
			t.Fatalf("NormalizeArch(%q) = %q, want %q", in, got, want)
		}
	}
}
