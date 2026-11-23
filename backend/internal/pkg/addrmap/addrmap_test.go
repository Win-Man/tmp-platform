package addrmap

import "testing"

// TestMapIP 锁定三种源写法与最长前缀优先的匹配结果。
//
// 多网络平面场景下，网段规则负责整体平移、精确 IP 规则负责个别不规整的机器，
// 一旦优先级搞反（精确规则被 /16 覆盖），个别机器就会指向错误地址。
func TestMapIP(t *testing.T) {
	m := New([]Rule{
		{Source: "10.10.0.0/16", Target: "172.16.0.0/16"},
		{Source: "10.10.9.11", Target: "158.1.9.21"},
		{Source: "192.168.7.", Target: "10.7.7."},
	})

	cases := []struct {
		in   string
		want string
		hit  bool
	}{
		{"10.10.9.11", "158.1.9.21", true},  // 精确 IP 优先于 /16
		{"10.10.9.12", "172.16.9.12", true}, // /16 平移，保留末两段偏移
		{"10.10.3.4", "172.16.3.4", true},
		{"192.168.7.8", "10.7.7.8", true}, // 简写前缀等价 /24
		{"158.1.9.21", "", false},         // 未被覆盖
		{"10.20.0.1", "", false},
		{"not-an-ip", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := m.MapIP(c.in)
		if ok != c.hit {
			t.Fatalf("MapIP(%q) hit=%v, want %v", c.in, ok, c.hit)
		}
		if ok && got != c.want {
			t.Fatalf("MapIP(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestMapString 锁定「只换 IP，不动协议头 / 端口 / 路径」的行为。
//
// 采集回来的 Grafana、Dashboard 地址形态不统一（有裸 ip:port，也有完整 URL），
// 若整串替换会丢掉 :3000 或 /dashboard，页面链接就点不开了。
func TestMapString(t *testing.T) {
	m := New([]Rule{{Source: "10.10.9.0/24", Target: "158.1.9.0/24"}})

	cases := []struct{ in, want string }{
		{"10.10.9.11", "158.1.9.11"},
		{"10.10.9.11:2379", "158.1.9.11:2379"},
		{"http://10.10.9.11:2379/dashboard", "http://158.1.9.11:2379/dashboard"},
		{"https://10.10.9.11:3000/d/tidb?orgId=1", "https://158.1.9.11:3000/d/tidb?orgId=1"},
		{"http://10.10.9.11", "http://158.1.9.11"},
		{"10.20.9.11:2379", "10.20.9.11:2379"}, // 未命中保持原样
		{"grafana.internal:3000", "grafana.internal:3000"},
		{"", ""},
		{"-", "-"},
	}
	for _, c := range cases {
		if got := m.MapString(c.in); got != c.want {
			t.Fatalf("MapString(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestMapStringNilMapper 确认零值映射器是安全的：展示层不必到处判空。
func TestMapStringNilMapper(t *testing.T) {
	var m *Mapper
	if got := m.MapString("10.10.9.11:2379"); got != "10.10.9.11:2379" {
		t.Fatalf("nil mapper 应原样返回，实际 %q", got)
	}
	if !m.Empty() {
		t.Fatal("nil mapper 应报告 Empty")
	}
	if _, ok := m.MapIP("10.10.9.11"); ok {
		t.Fatal("nil mapper 不应命中任何地址")
	}
}

// TestIPv6Untouched 确认 IPv6 不被误改（本功能只面向 TiDB 常见的 IPv4 平面）。
func TestIPv6Untouched(t *testing.T) {
	m := New([]Rule{{Source: "10.10.9.0/24", Target: "158.1.9.0/24"}})
	for _, in := range []string{"[2001:db8::1]:2379", "fe80::1"} {
		if got := m.MapString(in); got != in {
			t.Fatalf("IPv6 %q 应原样返回，实际 %q", in, got)
		}
	}
}

// TestExplain 锁定试算接口返回的命中规则，便于用户在界面上判断哪条规则生效。
func TestExplain(t *testing.T) {
	m := New([]Rule{
		{Source: "10.10.0.0/16", Target: "172.16.0.0/16"},
		{Source: "10.10.9.11", Target: "158.1.9.21"},
	})
	mapped, hit, ok := m.Explain("http://10.10.9.11:3000")
	if !ok {
		t.Fatal("应命中规则")
	}
	if mapped != "http://158.1.9.21:3000" {
		t.Fatalf("mapped = %q", mapped)
	}
	if hit.Source != "10.10.9.11" || hit.Target != "158.1.9.21" {
		t.Fatalf("命中规则不符：%+v", hit)
	}

	if _, _, ok := m.Explain("1.1.1.1"); ok {
		t.Fatal("未覆盖的地址不应命中")
	}
}

// TestNormalizeRule 锁定规则校验：形态必须对齐、网段前缀长度必须一致。
//
// 这些错误如果放过去，会在运行期被静默跳过（New 会丢弃非法规则），
// 用户只会看到「配了却不生效」，所以必须在保存时就明确报错。
func TestNormalizeRule(t *testing.T) {
	okCases := []struct{ src, dst, wantSrc, wantDst string }{
		{"10.10.9.11", "158.1.9.21", "10.10.9.11", "158.1.9.21"},
		{"10.10.9.0/24", "158.1.9.0/24", "10.10.9.0/24", "158.1.9.0/24"},
		{"10.10.9.", "158.1.9.", "10.10.9.0/24", "158.1.9.0/24"},
		{" 10.10. ", " 172.16. ", "10.10.0.0/16", "172.16.0.0/16"},
		{"10.10.9.11/24", "158.1.9.0/24", "10.10.9.0/24", "158.1.9.0/24"},
	}
	for _, c := range okCases {
		got, err := NormalizeRule(c.src, c.dst)
		if err != nil {
			t.Fatalf("NormalizeRule(%q,%q) 报错: %v", c.src, c.dst, err)
		}
		if got.Source != c.wantSrc || got.Target != c.wantDst {
			t.Fatalf("NormalizeRule(%q,%q) = %+v, want %s → %s", c.src, c.dst, got, c.wantSrc, c.wantDst)
		}
	}

	badCases := []struct{ src, dst string }{
		{"", "158.1.9.21"},
		{"10.10.9.11", ""},
		{"bad", "158.1.9.21"},
		{"10.10.9.11", "also-bad"},
		{"10.10.9.0/24", "158.1.9.21"},   // 网段不能映射到单 IP
		{"10.10.9.11", "158.1.9.0/24"},   // 单 IP 不能映射到网段
		{"10.10.0.0/16", "158.1.9.0/24"}, // 前缀长度不一致
		{"10.10.9.11", "10.10.9.11"},     // 源与目标相同
		{"300.1.1.1", "158.1.9.21"},      // 非法 IPv4 字面量
		{"2001:db8::1", "2001:db8::2"},   // IPv6 不支持
	}
	for _, c := range badCases {
		if _, err := NormalizeRule(c.src, c.dst); err == nil {
			t.Fatalf("NormalizeRule(%q,%q) 应报错", c.src, c.dst)
		}
	}
}

// TestNewSkipsInvalidRules 确认坏规则被丢弃但好规则照常生效。
func TestNewSkipsInvalidRules(t *testing.T) {
	m := New([]Rule{
		{Source: "bad", Target: "worse"},
		{Source: "10.10.9.0/24", Target: "158.1.9.0/24"},
	})
	if got := m.MapString("10.10.9.7"); got != "158.1.9.7" {
		t.Fatalf("合法规则应生效，实际 %q", got)
	}
}
