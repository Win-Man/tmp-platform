package collector

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestCommandTimeouts_Default 默认单条命令上限为 30s（与建连超时解耦）。
func TestCommandTimeouts_Default(t *testing.T) {
	c := NewCommandTimeouts(0)
	if got := c.SSHCommand(); got != 30*time.Second {
		t.Fatalf("SSHCommand = %v, want 30s", got)
	}
	if got := c.SSHWindow(); got != SSHConnectTimeout+90*time.Second {
		t.Fatalf("SSHWindow = %v, want %v", got, SSHConnectTimeout+90*time.Second)
	}
}

// TestCommandTimeouts_HotUpdate 保存设置后立即生效：窗口随之放大。
func TestCommandTimeouts_HotUpdate(t *testing.T) {
	c := NewCommandTimeouts(0)
	c.SetSSHCommand(120 * time.Second)
	if got := c.SSHCommand(); got != 120*time.Second {
		t.Fatalf("SSHCommand = %v, want 120s", got)
	}
	if got, want := c.SSHWindow(), SSHConnectTimeout+360*time.Second; got != want {
		t.Fatalf("SSHWindow = %v, want %v", got, want)
	}
}

// TestCommandTimeouts_NilSafe 零值指针回落默认值，避免调用方漏初始化就崩。
func TestCommandTimeouts_NilSafe(t *testing.T) {
	var c *CommandTimeouts
	if got := c.SSHCommand(); got != DefaultSSHCommandTimeout {
		t.Fatalf("nil SSHCommand = %v, want %v", got, DefaultSSHCommandTimeout)
	}
}

// TestSSHChannel_DialAlwaysSSH 127.0.0.1 也不再走本机直读：
// 平台一律经 SSH 到 TiUP 机器执行，同机场景要求本机开 sshd。
func TestSSHChannel_DialAlwaysSSH(t *testing.T) {
	ch := &sshChannel{timeouts: NewCommandTimeouts(0)}
	// 端口 1 一定没有 sshd，能确定性地区分「走了 SSH」与「本地直读」。
	_, err := ch.dial(&Target{TiUPHost: "127.0.0.1", TiUPPort: 1, TiUPUser: "root", TiUPPassword: "x"})
	if err == nil {
		t.Fatal("127.0.0.1 也应走 SSH 拨号，端口 1 不可能连上")
	}
	if !strings.Contains(err.Error(), "sshcmd") {
		t.Fatalf("错误应来自 SSH 拨号，实际: %v", err)
	}
}

// TestSSHChannel_DialRejectsIncompleteTarget 缺主机或用户时给出明确提示。
func TestSSHChannel_DialRejectsIncompleteTarget(t *testing.T) {
	ch := &sshChannel{timeouts: NewCommandTimeouts(0)}
	_, err := ch.dial(&Target{TiUPHost: "10.0.0.1"})
	if err == nil || !strings.Contains(err.Error(), "SSH 登录信息不完整") {
		t.Fatalf("应提示登录信息不完整，实际: %v", err)
	}
}

// TestSSHChannel_DialerInjection 注入的执行器优先于真实 SSH（单测通道）。
func TestSSHChannel_DialerInjection(t *testing.T) {
	fr := &fakeRunner{out: map[string]string{"arch": "x86_64\n"}}
	ch := &sshChannel{
		timeouts: NewCommandTimeouts(0),
		dialer:   func(*Target) (cmdRunner, error) { return fr, nil },
	}
	res, err := ch.Probe(context.Background(), &Target{TiUPHost: "127.0.0.1", TiUPUser: "tidb"})
	if err != nil {
		t.Fatalf("Probe 失败: %v", err)
	}
	if !res.OK || res.Message != "SSH 连接成功" {
		t.Fatalf("Probe = %+v, want ok/SSH 连接成功", res)
	}
	if res.Detail["arch"] != "x86_64" {
		t.Fatalf("arch = %v, want x86_64", res.Detail["arch"])
	}
}
