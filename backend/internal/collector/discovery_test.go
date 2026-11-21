package collector

import (
	"context"
	"testing"
)

// TestNormalizePDEndpoint 各种写法的 PD 地址都应归一化为 http(s)://host:port。
func TestNormalizePDEndpoint(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"10.0.0.11:2379", "http://10.0.0.11:2379"},
		{"10.0.0.11", "http://10.0.0.11:2379"},
		{"http://10.0.0.11:2379", "http://10.0.0.11:2379"},
		{"http://10.0.0.11:2379/", "http://10.0.0.11:2379"},
		{"https://pd.example.com:2379", "https://pd.example.com:2379"},
		{"  10.0.0.11:2380  ", "http://10.0.0.11:2380"},
		{"::1:2379", "http://[::1:2379]:2379"}, // 无方括号的 IPv6 视为裸主机（不合法输入，不 panic）
		{"", ""},
	}
	for _, c := range cases {
		if got := normalizePDEndpoint(c.in); got != c.want {
			t.Fatalf("normalizePDEndpoint(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestPDEndpointsFromServers meta.yaml 的 pd_servers 转地址列表：去重、补默认端口、排序。
func TestPDEndpointsFromServers(t *testing.T) {
	servers := []MetaServer{
		{Host: "10.0.0.12", Port: 2379},
		{Host: "10.0.0.11", Port: 2379},
		{Host: "10.0.0.11", Port: 2379}, // 重复项
		{Host: "", IP: "10.0.0.13"},     // 未填 port → 2379；host 空时用 ip
		{Host: "", IP: ""},              // 空条目应被跳过
	}
	got := pdEndpointsFromServers(servers)
	want := []string{"http://10.0.0.11:2379", "http://10.0.0.12:2379", "http://10.0.0.13:2379"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 项 = %q, want %q（应去重并排序）", i, got[i], want[i])
		}
	}
}

// TestMockDiscoverPD 演示模式必须能自动发现 PD 地址，保证「自动获取」在无真实集群时可用。
func TestMockDiscoverPD(t *testing.T) {
	m := NewMockCollector()
	for _, name := range MockClusterNames {
		disc, err := m.DiscoverPD(context.Background(), &Target{ClusterName: name})
		if err != nil {
			t.Fatalf("%s: DiscoverPD 返回错误: %v", name, err)
		}
		if len(disc.Endpoints) == 0 {
			t.Fatalf("%s: 未发现任何 PD 地址", name)
		}
		if disc.Source != "mock" {
			t.Fatalf("%s: Source = %q, want mock", name, disc.Source)
		}
		if disc.Primary() != disc.Endpoints[0] {
			t.Fatalf("%s: Primary 应等于首个地址", name)
		}
		// 演示数据的 PD 节点端口应归一化为真实的 client port 2379。
		for _, ep := range disc.Endpoints {
			if host, port := ParsePDEndpoint(ep); host == "" || port != 2379 {
				t.Fatalf("%s: 端点 %q 解析异常 host=%q port=%d", name, ep, host, port)
			}
		}
	}
}

// TestMockCollectorImplementsPDDiscoverer 编译期契约：演示模式必须实现 PDDiscoverer。
func TestMockCollectorImplementsPDDiscoverer(t *testing.T) {
	var _ PDDiscoverer = (*MockCollector)(nil)
	var _ PDDiscoverer = (*CompositeCollector)(nil)
}
