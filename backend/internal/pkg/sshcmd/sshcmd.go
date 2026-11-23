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
	"regexp"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
)

// DefaultCommandTimeout 单条远端命令的默认执行上限。
//
// `tiup cluster display` 在大集群上枚举实例可能耗时十几秒，
// 早期把它和建连超时共用 5s，正常集群也会被判超时；
// 现在两者解耦，命令超时默认 30s，并可由「系统设置」调整。
const DefaultCommandTimeout = 30 * time.Second

// DefaultConnectTimeout 建连超时：建连慢基本等于网络不通，快速失败即可。
const DefaultConnectTimeout = 5 * time.Second

// ErrIllegalArg 参数包含非法字符。
var ErrIllegalArg = errors.New("sshcmd: 参数包含非法字符")

// ErrUnknownTemplate 未在白名单中的命令模板。
var ErrUnknownTemplate = errors.New("sshcmd: 命令模板不在白名单内")

// argRe 参数允许字符集。
var argRe = regexp.MustCompile(`^[A-Za-z0-9._/-]+$`)

// templates 预置命令模板。%s 为受限参数，按顺序替换。
//
// 注意：`tiup cluster display` 在 TiUP 1.17 上不接受 `-T`
// （报错 unknown shorthand flag: 'T'），且非 TTY 环境下本身就输出纯文本表格，
// 因此文本模板不带格式参数。
//
// tiup 相关模板的第一个参数是可执行文件路径：SSH 非交互会话不读 ~/.bashrc，
// 官方安装脚本写进 PATH 的 ~/.tiup/bin 往往不可见，必须先定位绝对路径。
var templates = map[string]string{
	"arch":       "uname -m",
	"nproc":      "nproc",
	"free":       "free -m",
	"df":         "df -Pm",
	"uptime":     "cat /proc/uptime",
	"hostname":   "hostname",
	"os_release": "cat /etc/os-release",
	"cat":        "cat %s",
	"tiup_dir":   "ls -d %s",
	// tiup_probe 只读探测：PATH 找不到时回落到安装脚本的默认位置。
	"tiup_probe":        `command -v tiup 2>/dev/null || ls -d "$HOME/.tiup/bin/tiup" 2>/dev/null`,
	"tiup_display":      "%s cluster display %s",
	"tiup_display_json": "%s cluster display %s --format=json",
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
	// 逐个位置替换：%s 的数量必须与 args 一一对应（如 tiup 二进制路径 + 集群名）。
	holders := make([]any, len(args))
	for i := range args {
		holders[i] = args[i]
	}
	return fmt.Sprintf(tpl, holders...), nil
}

// Runner SSH 命令执行器。
type Runner struct {
	client  *ssh.Client
	timeout time.Duration // 单条命令执行上限（与建连超时解耦）
}

// DialOptions SSH 连接参数。
type DialOptions struct {
	Host     string
	Port     int
	User     string
	Password string
	// PrivateKey 为 PEM 内容，与 Password 二选一。
	PrivateKey string
	// Timeout 建连超时，默认 5s。
	Timeout time.Duration
	// CommandTimeout 单条命令执行超时，默认 30s。
	CommandTimeout time.Duration
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
		timeout = DefaultConnectTimeout
	}
	cmdTimeout := opts.CommandTimeout
	if cmdTimeout <= 0 {
		cmdTimeout = DefaultCommandTimeout
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
	return &Runner{client: client, timeout: cmdTimeout}, nil
}

// Close 关闭连接。
func (r *Runner) Close() error {
	if r == nil || r.client == nil {
		return nil
	}
	return r.client.Close()
}

// RunTemplate 执行白名单模板命令并返回标准输出。
//
// ctx 结束（调用方超时或取消）时立即返回，不再等命令自己跑完 ——
// 30s 的命令上限只兜底「命令确实卡住」，正常取消由 ctx 负责。
func (r *Runner) RunTemplate(ctx context.Context, template string, args ...string) (string, error) {
	cmd, err := Build(template, args...)
	if err != nil {
		return "", err
	}
	return r.run(ctx, cmd)
}

// run 执行已构造好的（白名单）命令。
func (r *Runner) run(ctx context.Context, cmd string) (string, error) {
	if r == nil || r.client == nil {
		return "", errors.New("sshcmd: 连接未建立")
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	session, err := r.client.NewSession()
	if err != nil {
		return "", fmt.Errorf("sshcmd: 创建会话失败: %w", err)
	}
	defer func() { _ = session.Close() }()

	var stdout, stderr bytes.Buffer
	session.Stdout = &stdout
	session.Stderr = &stderr

	done := make(chan error, 1)
	go func() {
		done <- session.Run(cmd)
	}()

	timeout := r.timeout
	if timeout <= 0 {
		timeout = DefaultCommandTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-done:
		if err != nil {
			return stdout.String(), fmt.Errorf("sshcmd: 执行失败: %w (stderr: %s)", err, truncate(stderr.String(), 512))
		}
		return stdout.String(), nil
	case <-ctx.Done():
		// 关闭会话（defer）会打断远端命令，避免 goroutine 挂到命令超时才退出。
		return "", fmt.Errorf("sshcmd: 命令执行被取消: %w", ctx.Err())
	case <-timer.C:
		return "", fmt.Errorf("sshcmd: 命令执行超时（>%s）", timeout)
	}
}

// truncate 截断字符串到指定字节数。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
