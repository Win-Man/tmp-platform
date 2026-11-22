// Package sshcmd 提供 SSH 固定命令白名单执行器。
//
// 安全约束：只允许预置命令模板，参数必须通过字符白名单校验（^[A-Za-z0-9._/-]+$），
// 绝不接收任意命令，也不做字符串拼接用户输入。
package sshcmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
)

// ErrIllegalArg 参数包含非法字符。
var ErrIllegalArg = errors.New("sshcmd: 参数包含非法字符")

// ErrUnknownTemplate 未在白名单中的命令模板。
var ErrUnknownTemplate = errors.New("sshcmd: 命令模板不在白名单内")

// argRe 参数允许字符集。
var argRe = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// templates 预置命令模板。%s 为单个受限参数。
//
// 注意：`tiup cluster display` 在 TiUP 1.17 上不接受 `-T`
// （报错 unknown shorthand flag: 'T'），且非 TTY 环境下本身就输出纯文本表格，
// 因此文本模板不带格式参数。
var templates = map[string]string{
	"arch":              "uname -m",
	"nproc":             "nproc",
	"free":              "free -m",
	"df":                "df -Pm",
	"uptime":            "cat /proc/uptime",
	"hostname":          "hostname",
	"os_release":        "cat /etc/os-release",
	"cat":               "cat %s",
	"tiup_dir":          "ls -d %s",
	"tiup_version":      "tiup --version",
	"tiup_display":      "tiup cluster display %s",
	"tiup_display_json": "tiup cluster display %s --format=json",
	"echo_ok":           "echo TMP_OK",
}

// Build 按模板名构造最终命令字符串；参数逐个校验字符白名单。
func Build(template string, args ...string) (string, error) {
	tpl, ok := templates[template]
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrUnknownTemplate, template)
	}
	for _, a := range args {
		if !argRe.MatchString(a) || len(a) > 512 {
			return "", fmt.Errorf("%w: %q", ErrIllegalArg, a)
		}
	}
	if len(args) == 0 {
		return tpl, nil
	}
	out := tpl
	for _, a := range args {
		out = fmt.Sprintf(out, a)
	}
	return out, nil
}

// Runner SSH 命令执行器。
type Runner struct {
	client  *ssh.Client
	timeout time.Duration
}

// DialOptions SSH 连接参数。
type DialOptions struct {
	Host     string
	Port     int
	User     string
	Password string
	// PrivateKey 为 PEM 内容，与 Password 二选一。
	PrivateKey string
	// Timeout 连接超时，默认 5s。
	Timeout time.Duration
	// InsecureIgnoreHostKey 演示/内网场景下跳过 host key 校验，默认 true。
	InsecureIgnoreHostKey bool
}

// Dial 建立 SSH 连接。
func Dial(opts DialOptions) (*Runner, error) {
	if opts.Host == "" || opts.User == "" {
		return nil, errors.New("sshcmd: 缺少主机或用户名")
	}
	port := opts.Port
	if port <= 0 {
		port = 22
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	var authMethods []ssh.AuthMethod
	if opts.PrivateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(opts.PrivateKey))
		if err != nil {
			return nil, fmt.Errorf("sshcmd: 解析私钥失败: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}
	if opts.Password != "" {
		authMethods = append(authMethods, ssh.Password(opts.Password))
	}
	if len(authMethods) == 0 {
		return nil, errors.New("sshcmd: 缺少凭据（密码或私钥）")
	}
	cfg := &ssh.ClientConfig{
		User:            opts.User,
		Auth:            authMethods,
		Timeout:         timeout,
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), //nolint:gosec // 内网纳管场景，主机指纹不做强制校验
	}
	addr := net.JoinHostPort(opts.Host, strconv.Itoa(port))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("sshcmd: 连接 %s 失败: %w", addr, err)
	}
	return &Runner{client: client, timeout: timeout}, nil
}

// Close 关闭连接。
func (r *Runner) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	return r.client.Close()
}

// RunTemplate 执行白名单模板命令并返回标准输出。
func (r *Runner) RunTemplate(template string, args ...string) (string, error) {
	cmd, err := Build(template, args...)
	if err != nil {
		return "", err
	}
	return r.Run(cmd)
}

// Run 执行已构造好的（白名单）命令。
func (r *Runner) Run(cmd string) (string, error) {
	if r == nil || r.client == nil {
		return "", errors.New("sshcmd: 连接未建立")
	}
	session, err := r.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("sshcmd: 创建会话失败: %w", err)
	}
	defer func() { _ = session.Close() }()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	type result struct {
		err error
	}
	done := make(chan result, 1)
	go func() {
		done <- result{err: session.Run(cmd)}
	}()

	timeout := r.timeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	select {
	case res := <-done:
		if res.err != nil {
			return stdout.String(), fmt.Errorf("sshcmd: 执行失败: %w (stderr: %s)", res.err, truncate(stderr.String(), 512))
		}
		return stdout.String(), nil
	case <-time.After(timeout):
		return "", errors.New("sshcmd: 命令执行超时")
	}
}

// truncate 截断字符串到指定字节数。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// IsLocalHost 判断 host 是否指向当前机器。
//
// 命中条件：回环地址 / localhost / 本机 hostname（含短名与 .local 后缀）/ 本机任一网卡 IP。
// 用于「TiUP 与平台部署在同一台机器」的场景，此时无需 SSH，直接本地读元数据。
func IsLocalHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "" {
		return false
	}
	if h == "localhost" || h == "127.0.0.1" || h == "::1" || h == "0.0.0.0" {
		return true
	}
	if name, err := os.Hostname(); err == nil && name != "" {
		n := strings.ToLower(name)
		if h == n || h == n+".local" || strings.Split(n, ".")[0] == strings.Split(h, ".")[0] {
			return true
		}
	}
	if ip := net.ParseIP(h); ip != nil {
		if ip.IsLoopback() {
			return true
		}
		ifaces, err := net.Interfaces()
		if err != nil {
			return false
		}
		for _, iface := range ifaces {
			addrs, err := iface.Addrs()
			if err != nil {
				continue
			}
			for _, addr := range addrs {
				if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.Equal(ip) {
					return true
				}
			}
		}
	}
	return false
}

// LocalRunner 本地命令执行器（TiUP 与平台同机时使用，免 SSH）。
//
// 与 SSH Runner 共用同一套命令模板与参数白名单，因此安全约束一致。
type LocalRunner struct {
	timeout time.Duration
}

// NewLocalRunner 构造本地执行器。
func NewLocalRunner(timeout time.Duration) *LocalRunner {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &LocalRunner{timeout: timeout}
}

// RunTemplate 执行白名单模板命令。
func (l *LocalRunner) RunTemplate(template string, args ...string) (string, error) {
	cmd, err := Build(template, args...)
	if err != nil {
		return "", err
	}
	return l.Run(cmd)
}

// Run 本地执行已构造好的（白名单）命令。
func (l *LocalRunner) Run(cmd string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), l.timeout)
	defer cancel()

	c := exec.CommandContext(ctx, "/bin/sh", "-c", cmd)
	var stdout, stderr bytes.Buffer
	c.Stdout = &stdout
	c.Stderr = &stderr
	if err := c.Run(); err != nil {
		if ctx.Err() != nil {
			return stdout.String(), errors.New("sshcmd: 本地命令执行超时")
		}
		return stdout.String(), fmt.Errorf("sshcmd: 本地执行失败: %w (stderr: %s)", err, truncate(stderr.String(), 512))
	}
	return stdout.String(), nil
}

// Close 本地执行器无连接需要释放。
func (l *LocalRunner) Close() error { return nil }
