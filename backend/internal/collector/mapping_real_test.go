package collector

import (
	"os"
	"testing"

	"gopkg.in/yaml.v3"
)

// loadMetaFixture 读取真实的 TiUP meta.yaml 结构样本。
func loadMetaFixture(t *testing.T) *TiUPMeta {
	t.Helper()
	raw, err := os.ReadFile("testdata/tiup-meta-minimal.yaml")
	if err != nil {
		t.Fatalf("读取样本失败: %v", err)
	}
	var meta TiUPMeta
	if err := yaml.Unmarshal(raw, &meta); err != nil {
		t.Fatalf("解析样本失败: %v", err)
	}
	return &meta
}

// TestNormalizeMeta_RealTiUPFixture 用真实结构的 meta.yaml 验证映射：
// 版本取 tidb_version、PD 取 client_port、架构取服务级 arch、监控地址用实际端口。
func TestNormalizeMeta_RealTiUPFixture(t *testing.T) {
	meta := loadMetaFixture(t)
	n := NormalizeMeta(meta, "tidb-demo")

	if n.Version != "v8.5.5" {
		t.Fatalf("版本应为 v8.5.5（tidb_version），实际 %q", n.Version)
	}
	if n.Arch != "aarch64" {
		t.Fatalf("架构应由服务级 arch=arm64 归一化为 aarch64，实际 %q", n.Arch)
	}

	// 1 TiDB + 1 TiKV + 1 PD + 1 Prometheus + 1 Grafana = 5
	if len(n.Instances) != 5 {
		t.Fatalf("实例数应为 5，实际 %d: %+v", len(n.Instances), n.Instances)
	}
	if n.HostCount != 2 {
		t.Fatalf("主机数应为 2（10.10.10.10 / 10.10.10.11），实际 %d", n.HostCount)
	}
	if n.ComponentCount != 5 {
		t.Fatalf("组件种类应为 5，实际 %d", n.ComponentCount)
	}

	byComp := map[string]InstanceSnapshot{}
	for _, inst := range n.Instances {
		byComp[inst.Component] = inst
	}
	if pd, ok := byComp["pd"]; !ok {
		t.Fatal("缺少 pd 实例")
	} else if pd.Port != 2379 || pd.HostIP != "10.10.10.10" {
		t.Fatalf("PD 应取 client_port 2379，实际 %s:%d", pd.HostIP, pd.Port)
	}
	if tidb, ok := byComp["tidb"]; !ok || tidb.Port != 4000 {
		t.Fatalf("TiDB 实例端口应为 4000，实际 %+v", tidb)
	}
	if prom, ok := byComp["prometheus"]; !ok || prom.Port != 9090 {
		t.Fatalf("Prometheus 实例端口应为 9090，实际 %+v", prom)
	}
	if graf, ok := byComp["grafana"]; !ok || graf.Port != 3000 {
		t.Fatalf("Grafana 实例端口应为 3000，实际 %+v", graf)
	}

	expectURL := func(name string, got []string, want string) {
		t.Helper()
		if len(got) != 1 || got[0] != want {
			t.Fatalf("%s 应为 [%s]，实际 %v", name, want, got)
		}
	}
	expectURL("Dashboard 地址", n.DashboardURLs, "http://10.10.10.10:2379/dashboard")
	expectURL("Prometheus 地址", n.PrometheusURLs, "http://10.10.10.11:9090")
	expectURL("Grafana 地址", n.GrafanaURLs, "http://10.10.10.11:3000")
}

// TestNormalizeMeta_TiProxyAndCustomPorts 覆盖 TiProxy 与自定义端口的场景。
func TestNormalizeMeta_TiProxyAndCustomPorts(t *testing.T) {
	var meta TiUPMeta
	raw := `
tidb_version: v8.5.5
topology:
  global:
    grafana_port: 3001
  pd_servers:
    - host: 10.0.0.1
      client_port: 12379
      peer_port: 12380
  tidb_servers: []
  tiproxy_servers:
    - host: 10.0.0.2
      port: 6000
      status_port: 3081
      arch: amd64
  monitoring_servers:
    - host: 10.0.0.3
      port: 19090
  grafana_servers:
    - host: 10.0.0.3
`
	if err := yaml.Unmarshal([]byte(raw), &meta); err != nil {
		t.Fatalf("解析失败: %v", err)
	}
	n := NormalizeMeta(&meta, "tiproxy-cluster")

	if len(n.Instances) != 4 {
		t.Fatalf("应为 pd/tiproxy/prometheus/grafana 共 4 个实例，实际 %d: %+v", len(n.Instances), n.Instances)
	}
	if n.Arch != "x86_64" {
		t.Fatalf("arch=amd64 应归一化为 x86_64，实际 %q", n.Arch)
	}
	if got := n.DashboardURLs[0]; got != "http://10.0.0.1:12379/dashboard" {
		t.Fatalf("Dashboard 应用 PD client_port，实际 %q", got)
	}
	if got := n.PrometheusURLs[0]; got != "http://10.0.0.3:19090" {
		t.Fatalf("Prometheus 应用实例端口 19090，实际 %q", got)
	}
	// Grafana 未显式给 port，回退 global.grafana_port。
	if got := n.GrafanaURLs[0]; got != "http://10.0.0.3:3001" {
		t.Fatalf("Grafana 应回退 global.grafana_port=3001，实际 %q", got)
	}
	var tiproxy *InstanceSnapshot
	for i := range n.Instances {
		if n.Instances[i].Component == "tiproxy" {
			tiproxy = &n.Instances[i]
		}
	}
	if tiproxy == nil {
		t.Fatal("缺少 tiproxy 实例")
	}
	if tiproxy.Port != 6000 || tiproxy.StatusPort != 3081 {
		t.Fatalf("TiProxy 端口映射错误: %+v", *tiproxy)
	}
}
