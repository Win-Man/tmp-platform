package service

import (
	"strings"
	"testing"
	"time"
)

// probeFixture 构造一组基准连接参数。
func probeFixture() (tiupHost, tiupUser string, sshPort int, dbHost string, dbPort int, pdEndpoint string) {
	return "10.20.1.11", "tidb", 22, "10.20.1.21", 4000, "http://10.20.1.31:2379"
}

func fixtureFingerprint() string {
	return ProbeFingerprint(probeFixture())
}

// TestProbeToken_RoundTrip 签发后可校验通过。
func TestProbeToken_RoundTrip(t *testing.T) {
	s := NewProbeTokenSigner("qa-secret")
	now := time.Now()
	token := s.Sign(fixtureFingerprint(), now)
	if token == "" {
		t.Fatal("凭证不应为空")
	}
	if err := s.Verify(fixtureFingerprint(), token, now.Add(time.Minute)); err != nil {
		t.Fatalf("同参数 + 1 分钟后应校验通过: %v", err)
	}
}

// TestProbeToken_FingerprintSensitive 任一指纹字段变更后必须拒绝（防「测 A 提交 B」）。
func TestProbeToken_FingerprintSensitive(t *testing.T) {
	s := NewProbeTokenSigner("qa-secret")
	now := time.Now()
	token := s.Sign(fixtureFingerprint(), now)

	cases := []struct {
		name string
		fp   string
	}{
		{"换 TiUP 主机", ProbeFingerprint("10.20.1.99", "tidb", 22, "10.20.1.21", 4000, "http://10.20.1.31:2379")},
		{"换 SSH 用户", ProbeFingerprint("10.20.1.11", "root", 22, "10.20.1.21", 4000, "http://10.20.1.31:2379")},
		{"换 SSH 端口", ProbeFingerprint("10.20.1.11", "tidb", 2222, "10.20.1.21", 4000, "http://10.20.1.31:2379")},
		{"换 DB 主机", ProbeFingerprint("10.20.1.11", "tidb", 22, "10.20.1.22", 4000, "http://10.20.1.31:2379")},
		{"换 DB 端口", ProbeFingerprint("10.20.1.11", "tidb", 22, "10.20.1.21", 4001, "http://10.20.1.31:2379")},
		{"换 PD 地址", ProbeFingerprint("10.20.1.11", "tidb", 22, "10.20.1.21", 4000, "http://10.20.1.32:2379")},
	}
	for _, tc := range cases {
		if tc.fp == fixtureFingerprint() {
			t.Fatalf("%s：指纹未变化，测试设计有误", tc.name)
		}
		if err := s.Verify(tc.fp, token, now); err == nil {
			t.Fatalf("%s：参数已变更，凭证必须被拒绝", tc.name)
		}
	}
}

// TestProbeToken_HostNormalization 主机大小写/尾点/空白差异不应导致误拒。
func TestProbeToken_HostNormalization(t *testing.T) {
	s := NewProbeTokenSigner("qa-secret")
	now := time.Now()
	base := ProbeFingerprint("TiDB-Node-01", "tidb", 22, "10.0.0.2", 4000, "http://PD1:2379")
	token := s.Sign(base, now)

	same := ProbeFingerprint("  tidb-node-01.  ", "tidb", 22, "10.0.0.2", 4000, "http://pd1:2379/")
	if same != base {
		t.Fatal("主机大小写/尾点/空白与 PD 末尾斜杠应被归一化为同一指纹")
	}
	if err := s.Verify(same, token, now); err != nil {
		t.Fatalf("归一化后应校验通过: %v", err)
	}
}

// TestProbeToken_TTL 超过 10 分钟必须过期。
func TestProbeToken_TTL(t *testing.T) {
	s := NewProbeTokenSigner("qa-secret")
	issued := time.Now()
	token := s.Sign(fixtureFingerprint(), issued)

	// 9 分钟：仍在有效期内。
	if err := s.Verify(fixtureFingerprint(), token, issued.Add(9*time.Minute)); err != nil {
		t.Fatalf("9 分钟应仍有效: %v", err)
	}
	// 10 分钟 +1 秒：过期。
	err := s.Verify(fixtureFingerprint(), token, issued.Add(10*time.Minute+time.Second))
	if err == nil {
		t.Fatal("超过 10 分钟必须过期")
	}
	if !strings.Contains(err.Error(), "过期") {
		t.Fatalf("过期错误信息应可读，实际 %q", err.Error())
	}
}

// TestProbeToken_ClockSkew 未来时间超过 1 分钟拒绝（容忍 1 分钟偏差）。
func TestProbeToken_ClockSkew(t *testing.T) {
	s := NewProbeTokenSigner("qa-secret")
	now := time.Now()
	token := s.Sign(fixtureFingerprint(), now)

	if err := s.Verify(fixtureFingerprint(), token, now.Add(-30*time.Second)); err != nil {
		t.Fatalf("30 秒内时钟偏差应容忍: %v", err)
	}
	err := s.Verify(fixtureFingerprint(), token, now.Add(-2*time.Minute))
	if err == nil || !strings.Contains(err.Error(), "时间异常") {
		t.Fatalf("未来时间超过 1 分钟应拒绝，实际 %v", err)
	}
}

// TestProbeToken_Tampered 篡改格式、时间戳或签名均拒绝。
func TestProbeToken_Tampered(t *testing.T) {
	s := NewProbeTokenSigner("qa-secret")
	now := time.Now()
	token := s.Sign(fixtureFingerprint(), now)
	parts := strings.SplitN(token, ".", 2)
	if len(parts) != 2 {
		t.Fatalf("凭证格式异常: %q", token)
	}

	bad := []struct {
		name  string
		token string
	}{
		{"空", ""},
		{"无分隔符", "abc"},
		{"多分隔符", token + ".x"},
		{"空时间戳", "." + parts[1]},
		{"空签名", parts[0] + "."},
		{"时间戳非数字", "abc." + parts[1]},
		{"签名被改", parts[0] + "." + flipFirst(parts[1])},
		{"时间戳被改", "1" + parts[0] + "." + parts[1]},
	}
	for _, tc := range bad {
		if err := s.Verify(fixtureFingerprint(), tc.token, now); err == nil {
			t.Fatalf("%s：非法凭证必须被拒绝（token=%q）", tc.name, tc.token)
		}
	}
}

// TestProbeToken_DifferentSecret 不同主密钥签发的凭证互不通过（重启换密钥即失效）。
func TestProbeToken_DifferentSecret(t *testing.T) {
	a := NewProbeTokenSigner("secret-a")
	b := NewProbeTokenSigner("secret-b")
	now := time.Now()
	token := a.Sign(fixtureFingerprint(), now)
	if err := b.Verify(fixtureFingerprint(), token, now); err == nil {
		t.Fatal("不同密钥签发/校验必须失败")
	}
}

// flipFirst 把首字符改成 A 或 B（与 JWT 测试同理：首字符必然改变解码字节）。
func flipFirst(sig string) string {
	if len(sig) == 0 {
		return "A"
	}
	b := []byte(sig)
	if b[0] == 'A' {
		b[0] = 'B'
	} else {
		b[0] = 'A'
	}
	return string(b)
}
