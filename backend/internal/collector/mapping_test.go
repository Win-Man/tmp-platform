package collector

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// sampleMeta 是一段贴合真实 `tiup cluster edit-config` / meta.yaml 结构的样本，
// 覆盖 pd / tidb / tikv / tiflash / ticdc / grafana / prometheus / alertmanager。
const sampleMeta = `
user: tidb
group: tidb
ssh_port: 22
deploy_dir: /data/tidb-deploy
data_dir: /data/tidb-data
version: v7.5.2
cluster_version: v7.5.2
global:
  user: tidb
  group: tidb
  ssh_port: 22
  deploy_dir: /data/tidb-deploy
  data_dir: /data/tidb-data
  arch: amd64
  grafana_port: 3000
pd_servers:
  - host: 10.0.10.1
    port: 2379
    status_port: 2379
    deploy_dir: /data/tidb-deploy/pd-2379
    data_dir: /data/tidb-data/pd-2379
    name: pd-10.0.10.1-2379
  - host: 10.0.10.2
    port: 2379
    status_port: 2379
  - host: 10.0.10.3
    port: 2379
    status_port: 2380
tidb_servers:
  - host: 10.0.10.11
    port: 4000
    status_port: 10080
  - host: 10.0.10.12
    port: 4001
    status_port: 10081
tikv_servers:
  - host: 10.0.10.21
    port: 20160
    status_port: 20180
  - host: 10.0.10.22
    port: 20160
    status_port: 20180
  - host: 10.0.10.23
    port: 20160
    status_port: 20180
tiflash_servers:
  - host: 10.0.10.31
    port: 9000
    status_port: 8234
    data_dir: /data/tidb-data/tiflash-9000
    az: ap-shanghai-1
cdc_servers:
  - host: 10.0.10.41
    port: 8300
ticdc_servers:
  - host: 10.0.10.41
    port: 8300
grafana_servers:
  - host: 10.0.10.51
monitoring_servers:
  - host: 10.0.10.51
    port: 9090
alertmanager_servers:
  - host: 10.0.10.51
    port: 9093
`

func mustParseMeta(t *testing.T) *TiUPMeta {
	t.Helper()
	var meta TiUPMeta
	if err := yaml.Unmarshal([]byte(sampleMeta), &meta); err != nil {
		t.Fatalf("解析 meta.yaml 失败: %v", err)
	}
	return &meta
}

// TestNormalizeMeta_YAML 断言 meta.yaml → instances 的字段映射结果。
func TestNormalizeMeta_YAML(t *testing.T) {
	n := NormalizeMeta(mustParseMeta(t), "tidb-prod")

	if n.Version != "v7.5.2" {
		t.Fatalf("Version = %q, want v7.5.2", n.Version)
	}
	if n.Arch != "x86_64" {
		t.Fatalf("Arch = %q, want 归一化后的 x86_64", n.Arch)
	}

	// 组件条数：pd3 + tidb2 + tikv3 + tiflash1 + ticdc1（cdc/ticdc 去重）+ grafana1 + prometheus1 + alertmanager1
	if got, want := len(n.Instances), 13; got != want {
		t.Fatalf("实例数 = %d, want %d", got, want)
	}
	if got, want := n.ComponentCount, 8; got != want {
		t.Fatalf("ComponentCount = %d, want %d", got, want)
	}
	// 主机：pd x3 + tidb x2 + tikv x3 + tiflash x1 + ticdc x1 + 监控 x1 = 11
	if got, want := n.HostCount, 11; got != want {
		t.Fatalf("HostCount = %d, want %d", got, want)
	}

	// 逐组件断言端口 / 主机 / 版本。
	type want struct {
		component  string
		host       string
		port       int
		statusPort int
	}
	wants := []want{
		{"pd", "10.0.10.1", 2379, 2379},
		{"pd", "10.0.10.3", 2379, 2380}, // 显式 status_port 必须保留
		{"tidb", "10.0.10.11", 4000, 10080},
		{"tidb", "10.0.10.12", 4001, 10081},
		{"tikv", "10.0.10.21", 20160, 20180},
		{"tiflash", "10.0.10.31", 9000, 8234},
		{"ticdc", "10.0.10.41", 8300, 8300},
		{"grafana", "10.0.10.51", 3000, 3000},
		{"prometheus", "10.0.10.51", 9090, 9090},
		{"alertmanager", "10.0.10.51", 9093, 9093},
	}
	for _, w := range wants {
		inst, ok := findInstance(n.Instances, w.component, w.host)
		if !ok {
			t.Fatalf("未找到实例 %s@%s", w.component, w.host)
		}
		if inst.Port != w.port {
			t.Fatalf("%s@%s port = %d, want %d", w.component, w.host, inst.Port, w.port)
		}
		if inst.StatusPort != w.statusPort {
			t.Fatalf("%s@%s statusPort = %d, want %d", w.component, w.host, inst.StatusPort, w.statusPort)
		}
		if inst.Version != "v7.5.2" {
			t.Fatalf("%s@%s version = %q, want v7.5.2", w.component, w.host, inst.Version)
		}
	}
}

// TestNormalizeMeta_PDRole 首台 PD 标记为 leader，其余为 service。
func TestNormalizeMeta_PDRole(t *testing.T) {
	n := NormalizeMeta(mustParseMeta(t), "tidb-prod")
	first, ok := findInstance(n.Instances, "pd", "10.0.10.1")
	if !ok {
		t.Fatal("未找到首台 PD")
	}
	if first.Role != "leader" {
		t.Fatalf("首台 PD role = %q, want leader", first.Role)
	}
	second, ok := findInstance(n.Instances, "pd", "10.0.10.2")
	if !ok {
		t.Fatal("未找到第二台 PD")
	}
	if second.Role != "service" {
		t.Fatalf("第二台 PD role = %q, want service", second.Role)
	}
}

// TestNormalizeMeta_TiFlashAZ 断言 tiflash 的 az 字段被保留。
func TestNormalizeMeta_TiFlashAZ(t *testing.T) {
	n := NormalizeMeta(mustParseMeta(t), "tidb-prod")
	tiflash, ok := findInstance(n.Instances, "tiflash", "10.0.10.31")
	if !ok {
		t.Fatal("未找到 tiflash 实例")
	}
	if tiflash.AZ != "ap-shanghai-1" {
		t.Fatalf("tiflash az = %q, want ap-shanghai-1", tiflash.AZ)
	}
	if tiflash.DataDir != "/data/tidb-data/tiflash-9000" {
		t.Fatalf("tiflash dataDir = %q", tiflash.DataDir)
	}
}

// TestNormalizeMeta_ExternalURLs 断言监控与 Dashboard 地址由 PD/Grafana 推导。
//
// 说明：TiDB Dashboard 由 PD 内置提供（PRD 8.2 / 架构 §6.3），
// 因此不产生 component=tidb-dashboard 的实例，而是产出 DashboardURLs。
func TestNormalizeMeta_ExternalURLs(t *testing.T) {
	n := NormalizeMeta(mustParseMeta(t), "tidb-prod")

	if len(n.GrafanaURLs) != 1 || n.GrafanaURLs[0] != "http://10.0.10.51:3000" {
		t.Fatalf("GrafanaURLs = %v", n.GrafanaURLs)
	}
	if len(n.PrometheusURLs) != 1 || n.PrometheusURLs[0] != "http://10.0.10.51:9090" {
		t.Fatalf("PrometheusURLs = %v", n.PrometheusURLs)
	}
	if len(n.DashboardURLs) != 3 {
		t.Fatalf("DashboardURLs 应随 PD 数量产出 3 条，实际 %v", n.DashboardURLs)
	}
	if !strings.HasSuffix(n.DashboardURLs[0], "/dashboard") {
		t.Fatalf("DashboardURL 应以 /dashboard 结尾，实际 %q", n.DashboardURLs[0])
	}
}

// TestNormalizeMeta_DedupeCDC 断言 cdc_servers 与 ticdc_servers 重复定义时只保留一份。
func TestNormalizeMeta_DedupeCDC(t *testing.T) {
	n := NormalizeMeta(mustParseMeta(t), "tidb-prod")
	count := 0
	for _, i := range n.Instances {
		if i.Component == "ticdc" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("ticdc 实例数 = %d, want 1（去重后）", count)
	}
}

// TestNormalizeMeta_Nil 空 meta 不应 panic。
func TestNormalizeMeta_Nil(t *testing.T) {
	n := NormalizeMeta(nil, "x")
	if n == nil {
		t.Fatal("NormalizeMeta(nil) 不应返回 nil")
	}
	if len(n.Instances) != 0 {
		t.Fatalf("空 meta 不应产生实例，实际 %d", len(n.Instances))
	}
}

// TestNormalizeMeta_IPFieldFallback 只有 ip 字段（无 host）时也要能落实例。
func TestNormalizeMeta_IPFieldFallback(t *testing.T) {
	meta := &TiUPMeta{ClusterVersion: "v6.5.0"}
	meta.PdServers = []MetaServer{{IP: "192.168.1.10"}}
	n := NormalizeMeta(meta, "x")
	if len(n.Instances) != 1 {
		t.Fatalf("实例数 = %d, want 1", len(n.Instances))
	}
	if n.Instances[0].HostIP != "192.168.1.10" {
		t.Fatalf("HostIP = %q, want 192.168.1.10", n.Instances[0].HostIP)
	}
	if n.Instances[0].Port != 2379 {
		t.Fatalf("PD 缺省 port = %d, want 2379", n.Instances[0].Port)
	}
}

// TestNormalizeMeta_VersionFallback 无 cluster_version 时回退 version。
func TestNormalizeMeta_VersionFallback(t *testing.T) {
	meta := &TiUPMeta{Version: "v8.1.0"}
	n := NormalizeMeta(meta, "x")
	if n.Version != "v8.1.0" {
		t.Fatalf("Version = %q, want 回退的 v8.1.0", n.Version)
	}
}

// TestMergeInstances_TiKVStatus 断言 PD store 状态覆盖 TiKV 实例状态与 AZ。
func TestMergeInstances_TiKVStatus(t *testing.T) {
	base := []InstanceSnapshot{
		{Component: "tikv", HostIP: "10.0.10.21", Port: 20160, Status: "up"},
		{Component: "tikv", HostIP: "10.0.10.22", Port: 20160, Status: "up"},
	}
	stores := []Store{
		{Address: "10.0.10.22:20160", StateName: "Offline", Version: "v7.5.2", Labels: map[string]string{"zone": "z2"}},
	}
	got := MergeInstances(base, stores)
	if got[0].Status != "up" {
		t.Fatalf("未匹配的 TiKV 状态应保持 up，实际 %q", got[0].Status)
	}
	if got[1].Status != "down" {
		t.Fatalf("Offline store 应映射为 down，实际 %q", got[1].Status)
	}
	if got[1].AZ != "z2" {
		t.Fatalf("AZ 应取 store label zone，实际 %q", got[1].AZ)
	}
}

// TestNormalizeArch 架构名归一化。
func TestNormalizeArch(t *testing.T) {
	cases := map[string]string{
		"amd64":   "x86_64",
		"x86_64":  "x86_64",
		"X86-64":  "x86_64",
		"arm64":   "aarch64",
		"aarch64": "aarch64",
		"":        "",
	}
	for in, want := range cases {
		if got := NormalizeArch(in); got != want {
			t.Fatalf("NormalizeArch(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestParsePDEndpoint PD 地址解析。
func TestParsePDEndpoint(t *testing.T) {
	cases := []struct {
		in   string
		host string
		port int
	}{
		{"http://10.0.0.1:2379", "10.0.0.1", 2379},
		{"10.0.0.1:2381", "10.0.0.1", 2381},
		{"10.0.0.1", "10.0.0.1", 2379},
		{"", "", 2379},
	}
	for _, tc := range cases {
		h, p := ParsePDEndpoint(tc.in)
		if h != tc.host || p != tc.port {
			t.Fatalf("ParsePDEndpoint(%q) = (%q,%d), want (%q,%d)", tc.in, h, p, tc.host, tc.port)
		}
	}
}

// findInstance 按组件 + 主机查找实例。
func findInstance(list []InstanceSnapshot, component, host string) (InstanceSnapshot, bool) {
	for _, i := range list {
		if i.Component == component && i.HostIP == host {
			return i, true
		}
	}
	return InstanceSnapshot{}, false
}
