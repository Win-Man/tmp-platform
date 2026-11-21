package service

import (
	"strings"
	"testing"
)

// TestValidateClusterName 集群名白名单：字母开头、3~40 位字母/数字/下划线/短横线。
func TestValidateClusterName(t *testing.T) {
	valid := []string{"abc", "mock-prod-a", "TiDB_01", "a-b_c", strings.Repeat("a", 40)}
	for _, name := range valid {
		if err := ValidateClusterName(name); err != nil {
			t.Fatalf("合法集群名 %q 被拒: %v", name, err)
		}
	}

	invalid := []struct {
		name  string
		input string
		hint  string
	}{
		{"空", "", "不能为空"},
		{"纯空白", "   ", "不能为空"},
		{"数字开头", "1abc", "格式不合法"},
		{"过短", "ab", "格式不合法"},
		{"超长", strings.Repeat("a", 41), "格式不合法"},
		{"含空格", "abc def", "格式不合法"},
		{"含中文", "集群一号", "格式不合法"},
		{"含引号", "x'; DROP TABLE clusters;--", "格式不合法"},
		{"含点号", "abc.def", "格式不合法"},
	}
	for _, tc := range invalid {
		err := ValidateClusterName(tc.input)
		if err == nil {
			t.Fatalf("%s：%q 应被拒绝", tc.name, tc.input)
		}
		if !strings.Contains(err.Error(), tc.hint) {
			t.Fatalf("%s：错误信息 %q 未包含中文提示 %q", tc.name, err.Error(), tc.hint)
		}
	}
}

// TestValidateHostField 主机字段：IPv4 / IPv6 / RFC1123 主机名合法，其余拒绝。
func TestValidateHostField(t *testing.T) {
	valid := []string{
		"10.20.1.11", "127.0.0.1", "::1", "2001:db8::1",
		"tidb-node-01", "db1.prod.example.com", "a", "my-host.",
		// 单标签 DNS 名（不含点）在语法上是合法主机名，校验器按「IP 或主机名」放行，
		// 因此 "not-an-ip" 这种形态是合法的（真正的非法样例见下）。
		"not-an-ip",
	}
	for _, h := range valid {
		if err := ValidateHostField("TiUP 主机地址", h); err != nil {
			t.Fatalf("合法主机 %q 被拒: %v", h, err)
		}
	}

	invalid := []struct {
		input string
		hint  string
	}{
		{"", "不能为空"},
		{"   ", "不能为空"},
		{"this is not an ip", "格式不合法"},
		{"10.0.0.1/24", "格式不合法"},
		{"999.1.1.1", "格式不合法"},
		{"-abc", "格式不合法"},
		{"abc-", "格式不合法"},
		{"a..b", "格式不合法"},
		{"a_b", "格式不合法"},
		{strings.Repeat("a", 64) + ".com", "格式不合法"},
	}
	for _, tc := range invalid {
		err := ValidateHostField("TiUP 主机地址", tc.input)
		if err == nil {
			t.Fatalf("%q 应被拒绝", tc.input)
		}
		if !strings.Contains(err.Error(), "TiUP 主机地址") {
			t.Fatalf("错误信息 %q 应包含字段中文名", err.Error())
		}
		if !strings.Contains(err.Error(), tc.hint) {
			t.Fatalf("%q 的错误信息 %q 未包含 %q", tc.input, err.Error(), tc.hint)
		}
	}
}

// TestValidatePort 端口边界：1 与 65535 合法，0 与 65536 及负数非法。
func TestValidatePort(t *testing.T) {
	for _, p := range []int{1, 22, 4000, 65535} {
		if err := ValidatePort("SSH 端口", p); err != nil {
			t.Fatalf("端口 %d 应合法: %v", p, err)
		}
	}
	for _, p := range []int{0, -1, 65536, 99999} {
		if err := ValidatePort("SSH 端口", p); err == nil {
			t.Fatalf("端口 %d 应非法", p)
		}
	}
	err := ValidatePort("数据库端口", 0)
	if err == nil || !strings.Contains(err.Error(), "数据库端口") || !strings.Contains(err.Error(), "1~65535") {
		t.Fatalf("错误信息应含字段名与范围，实际 %v", err)
	}
}

// TestValidatePDEndpoint PD 地址：可选 http(s):// 前缀，必须 host:port。
func TestValidatePDEndpoint(t *testing.T) {
	valid := []string{
		"http://10.20.1.11:2379",
		"https://pd.example.com:2379",
		"10.20.1.11:2379",
		"pd1:2379",
		"[::1]:2379",
	}
	for _, e := range valid {
		if err := ValidatePDEndpoint(e); err != nil {
			t.Fatalf("合法 PD 地址 %q 被拒: %v", e, err)
		}
	}

	invalid := []struct {
		input string
		hint  string
	}{
		{"", "不能为空"},
		{"::::", "格式不合法"},
		{"http://10.0.0.1", "格式不合法"},
		{"10.0.0.1", "host:port"},
		{"ftp://10.0.0.1:2379", "格式不合法"},
		{"http://:2379", "不合法"},
		{"http://10.0.0.1:70000", "1~65535"},
		// url.Parse 自身会拒绝非数字端口，因此错误信息走「格式不合法」分支。
		{"http://10.0.0.1:abc", "格式不合法"},
		{"10.0.0.1:abc", "端口需为数字"},
	}
	for _, tc := range invalid {
		err := ValidatePDEndpoint(tc.input)
		if err == nil {
			t.Fatalf("%q 应被拒绝", tc.input)
		}
		if !strings.Contains(err.Error(), tc.hint) {
			t.Fatalf("%q 的错误信息 %q 未包含 %q", tc.input, err.Error(), tc.hint)
		}
	}
}

// TestValidateHTTPURL / ValidateURLList 外部地址校验。
func TestValidateHTTPURL(t *testing.T) {
	for _, u := range []string{"http://10.0.0.1:3000", "https://grafana.example.com"} {
		if err := ValidateHTTPURL("Grafana 地址", u); err != nil {
			t.Fatalf("合法地址 %q 被拒: %v", u, err)
		}
	}
	for _, u := range []string{"", "10.0.0.1:3000", "ftp://x/y", "http://"} {
		if err := ValidateHTTPURL("Grafana 地址", u); err == nil {
			t.Fatalf("%q 应被拒绝", u)
		}
	}

	if err := ValidateURLList("Dashboard 地址", nil); err != nil {
		t.Fatalf("空列表应允许: %v", err)
	}
	if err := ValidateURLList("Dashboard 地址", []string{"http://a:1", ""}); err == nil {
		t.Fatal("含空地址应被拒绝")
	}
	if err := ValidateURLList("Dashboard 地址", []string{"http://a:1", "bad"}); err == nil {
		t.Fatal("含非法地址应被拒绝")
	}
}

// TestNormalizeHost 归一化：去空白、去尾部点、小写。
func TestNormalizeHost(t *testing.T) {
	cases := map[string]string{
		"  10.0.0.1  ":  "10.0.0.1",
		"TiDB-Node-01.": "tidb-node-01",
		"Example.COM.":  "example.com",
		"":              "",
		"2001:DB8::1":   "2001:db8::1",
	}
	for in, want := range cases {
		if got := NormalizeHost(in); got != want {
			t.Fatalf("NormalizeHost(%q) = %q, want %q", in, got, want)
		}
	}
}
