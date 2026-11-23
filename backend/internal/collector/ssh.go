package collector

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/tmp-platform/backend/internal/pkg/sshcmd"
)

// cmdRunner 远端命令执行抽象。
//
// 生产路径只有 SSH 一种实现；单测里注入本地执行器，
// 免去「跑单测还得起一个 sshd」的麻烦。
type cmdRunner interface {
	RunTemplate(ctx context.Context, template string, args ...string) (string, error)
	Close() error
}

// sshChannel 真实 SSH 通道：SSH 到 TiUP 中控机执行 `tiup cluster display`
// 并采集该中控机自身的主机资源。
//
// 平台一律以「SSH 远程到 TiUP 机器」的方式采集：不存在本机直读分支，
// 即使用户填 127.0.0.1，也要求该机器开启 sshd 并按 SSH 处理。
type sshChannel struct {
	timeouts *CommandTimeouts
	// dialer 可注入（单测用）；为 nil 时走真实 SSH。
	dialer func(*Target) (cmdRunner, error)
}

// NewSSHChannel 构造 SSH 通道。
func NewSSHChannel(timeouts *CommandTimeouts) SSHChannel {
	return &sshChannel{timeouts: timeouts}
}

// Kind 实现 Channel。
func (s *sshChannel) Kind() Kind { return KindSSH }

// dial 建立到 TiUP 中控机的 SSH 命令通道。
func (s *sshChannel) dial(t *Target) (cmdRunner, error) {
	if s.dialer != nil {
		return s.dialer(t)
	}
	// 组装目标为空时给出明确提示，避免 sshcmd 只报「缺少主机或用户名」。
	if strings.TrimSpace(t.TiUPHost) == "" || strings.TrimSpace(t.TiUPUser) == "" {
		return nil, fmt.Errorf("SSH 登录信息不完整（TiUP 地址=%q，SSH 用户=%q）", t.TiUPHost, t.TiUPUser)
	}
	// 解密失败导致的「空密码」优先报原因，不要落到 sshcmd 的通用提示上。
	if strings.TrimSpace(t.TiUPPassword) == "" && strings.TrimSpace(t.TiUPKey) == "" && t.SSHCredIssue != "" {
		return nil, errors.New(t.SSHCredIssue)
	}
	return sshcmd.Dial(sshcmd.DialOptions{
		Host:           t.TiUPHost,
		Port:           t.TiUPPort,
		User:           t.TiUPUser,
		Password:       t.TiUPPassword,
		PrivateKey:     t.TiUPKey,
		Timeout:        SSHConnectTimeout,
		CommandTimeout: s.timeouts.SSHCommand(),
	})
}

// Probe 探测 SSH 连通性：登录成功 + 能执行 uname -m。
func (s *sshChannel) Probe(ctx context.Context, t *Target) (ProbeResult, error) {
	start := time.Now()
	res := ProbeResult{Scope: ScopeSSH}
	r, err := s.dial(t)
	if err != nil {
		res.Message = "SSH 连接失败: " + err.Error()
		res.LatencyMS = time.Since(start).Milliseconds()
		return res, nil
	}
	defer func() { _ = r.Close() }()

	out, err := r.RunTemplate(ctx, "arch")
	res.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		res.Message = "SSH 登录成功但执行命令失败: " + err.Error()
		return res, nil
	}
	arch := NormalizeArch(strings.TrimSpace(out))
	res.OK = true
	res.Message = "SSH 连接成功"
	res.Detail = map[string]any{"arch": arch, "user": t.TiUPUser}
	return res, nil
}

// tiupBinRe 限定解析出的 tiup 可执行文件路径形态。
//
// 探测结果来自远端命令输出，必须约束成绝对路径再拼进后续命令；
// 不匹配时一律回落到裸 `tiup`，让原始报错（command not found）暴露出来。
var tiupBinRe = regexp.MustCompile(`^/[A-Za-z0-9._/-]+$`)

// resolveTiUPBin 定位 TiUP 可执行文件的绝对路径。
//
// 采集是「SSH 到中控机执行 tiup cluster display」，而 SSH 非交互会话不读
// ~/.bashrc —— 官方安装脚本写进 PATH 的 ~/.tiup/bin 通常不可见，于是命令
// 报 command not found、采集被误判为不可用。这里按以下顺序探测：
//
//  1. 远端 PATH（command -v tiup）；
//  2. 安装脚本默认位置 $HOME/.tiup/bin/tiup；
//  3. 用户显式配置的「TiUP 部署目录」<tiup_dir>/bin/tiup。
//
// 全部失败返回 ("tiup", false)，由调用方把「未找到 tiup」写进错误原因。
func resolveTiUPBin(ctx context.Context, r cmdRunner, t *Target) (string, bool) {
	if out, err := r.RunTemplate(ctx, "tiup_probe"); err == nil {
		if p := lastLine(out); tiupBinRe.MatchString(p) {
			return p, true
		}
	}
	if dir := strings.TrimSpace(t.TiUPDir); dir != "" {
		cand := strings.TrimSuffix(dir, "/") + "/bin/tiup"
		if tiupBinRe.MatchString(cand) {
			// 只做存在性校验（ls -d），再交给后续命令以绝对路径执行。
			if _, lerr := r.RunTemplate(ctx, "tiup_dir", cand); lerr == nil {
				return cand, true
			}
		}
	}
	return "tiup", false
}

// lastLine 取输出的最后一个非空行。
//
// tiup_probe 的 `command -v` 成功时只输出一行；失败时由 `ls` 补位，
// 取最后一行可同时覆盖两种输出形态。
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if v := strings.TrimSpace(lines[i]); v != "" {
			return v
		}
	}
	return ""
}

// FetchTiUPMeta 采集 TiUP 集群元数据。
//
// 实例运行状态只认 `tiup cluster display` 的输出，不读 meta.yaml、也不拿
// PD / MySQL 的状态去补：一旦 display 不可用，就直接返回「实例信息获取失败」，
// 避免把「默认 up」「PD store 状态」混进同一份快照里冒充实测结果。
//
// 取值顺序：
//  1. `tiup cluster display <name> --format=json`（首选：结构稳定、含头部信息）；
//  2. `tiup cluster display <name>` 文本输出（次选：非 TTY 下 TiUP 输出表格）。
//
// 两级全失败时聚合原因返回，逐级原因拼进 message 便于排错。
func (s *sshChannel) FetchTiUPMeta(ctx context.Context, t *Target) (*TiUPMeta, error) {
	r, err := s.dial(t)
	if err != nil {
		return nil, fmt.Errorf("SSH 连接失败: %w", err)
	}
	defer func() { _ = r.Close() }()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var reasons []string
	bin, found := resolveTiUPBin(ctx, r, t)
	if !found {
		reasons = append(reasons,
			"未定位到 tiup 可执行文件（PATH、$HOME/.tiup/bin/tiup、<TiUP 部署目录>/bin/tiup 均不可用）")
	}

	// 第 1 级：display JSON。
	if meta, why := fetchMetaFromDisplayJSON(ctx, r, bin, t); meta != nil {
		return meta, nil
	} else if why != "" {
		reasons = append(reasons, why)
	}

	// 第 2 级：display 文本输出。
	if meta, why := fetchMetaFromDisplayText(ctx, r, bin, t); meta != nil {
		return meta, nil
	} else if why != "" {
		reasons = append(reasons, why)
	}

	return nil, fmt.Errorf("实例信息获取失败：tiup cluster display 不可用（%s）", strings.Join(reasons, "; "))
}

// fetchMetaFromDisplayJSON 第 1 级：解析 `tiup cluster display --format=json`。
func fetchMetaFromDisplayJSON(ctx context.Context, r cmdRunner, bin string, t *Target) (*TiUPMeta, string) {
	out, err := r.RunTemplate(ctx, "tiup_display_json", bin, t.ClusterName)
	if err != nil {
		return nil, "tiup cluster display --format=json 执行失败: " + err.Error()
	}
	display, err := ParseDisplayJSON(out)
	if err != nil {
		return nil, err.Error()
	}
	meta := display.ToMeta()
	if !metaHasServers(meta) {
		return nil, "display JSON 未产出任何实例"
	}
	meta.Source = DisplaySourceJSON
	return meta, ""
}

// fetchMetaFromDisplayText 第 2 级：解析 `tiup cluster display` 的文本输出（头部 + 实例表格）。
func fetchMetaFromDisplayText(ctx context.Context, r cmdRunner, bin string, t *Target) (*TiUPMeta, string) {
	out, err := r.RunTemplate(ctx, "tiup_display", bin, t.ClusterName)
	if err != nil {
		return nil, "tiup cluster display 文本执行失败: " + err.Error()
	}
	display, err := ParseDisplayText(out)
	if err != nil {
		return nil, err.Error()
	}
	meta := display.ToMeta()
	if !metaHasServers(meta) {
		return nil, "display 文本未产出任何实例"
	}
	meta.Source = DisplaySourceText
	return meta, ""
}

// FetchHostResource 采集 TiUP 主机自身资源（CPU/内存/磁盘/使用率）。
//
// 说明：SSH 只采中控机这一台（其余主机没有凭据、也不该逐台登录）。
// 其余主机的硬件信息由 MySQL 通道的 FetchHostHardware 补齐 ——
// 走 information_schema.cluster_hardware（架构 / 核数 / 内存容量 / 磁盘容量与用量）
// 与 information_schema.cluster_load（CPU idle、内存 used-percent），
// 聚合逻辑见 hardware.go:AggregateHostHardware。
func (s *sshChannel) FetchHostResource(ctx context.Context, t *Target) ([]HostResource, error) {
	r, err := s.dial(t)
	if err != nil {
		return nil, fmt.Errorf("SSH 连接失败: %w", err)
	}
	defer func() { _ = r.Close() }()

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	h := HostResource{IP: t.TiUPHost, OSUser: t.TiUPUser}
	if out, err := r.RunTemplate(ctx, "arch"); err == nil {
		h.Arch = NormalizeArch(strings.TrimSpace(out))
	}
	if out, err := r.RunTemplate(ctx, "hostname"); err == nil {
		h.Hostname = strings.TrimSpace(out)
	}
	if out, err := r.RunTemplate(ctx, "nproc"); err == nil {
		h.CPUCores, _ = strconv.Atoi(strings.TrimSpace(out))
	}
	if out, err := r.RunTemplate(ctx, "free"); err == nil {
		h.MemoryGB, h.MemUsagePct = parseFreeM(out)
	}
	if out, err := r.RunTemplate(ctx, "df"); err == nil {
		h.DiskTotalGB, h.DiskUsagePct = parseDfPM(out)
	}
	h.CPUUsagePct = cpuUsage(ctx, r)
	return []HostResource{h}, nil
}

// cpuUsage 通过两次读取 /proc/stat 计算瞬时 CPU 使用率。
func cpuUsage(ctx context.Context, r cmdRunner) float64 {
	first, err := r.RunTemplate(ctx, "cat", "/proc/stat")
	if err != nil {
		return 0
	}
	idle1, total1 := parseProcStat(first)
	time.Sleep(200 * time.Millisecond)
	second, err := r.RunTemplate(ctx, "cat", "/proc/stat")
	if err != nil {
		return 0
	}
	idle2, total2 := parseProcStat(second)
	dt := total2 - total1
	di := idle2 - idle1
	if dt <= 0 {
		return 0
	}
	usage := (1 - float64(di)/float64(dt)) * 100
	if usage < 0 {
		usage = 0
	}
	if usage > 100 {
		usage = 100
	}
	return round1(usage)
}

// parseProcStat 解析 /proc/stat 首行，返回 (idle 总和, 总 jiffies)。
func parseProcStat(out string) (idle, total int64) {
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:]
		for i, f := range fields {
			v, _ := strconv.ParseInt(f, 10, 64)
			total += v
			if i == 3 || i == 4 { // idle + iowait
				idle += v
			}
		}
		return idle, total
	}
	return 0, 0
}

// parseFreeM 解析 `free -m` 输出，返回 (总内存 GB, 内存使用率 %)。
func parseFreeM(out string) (float64, float64) {
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "Mem:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			break
		}
		total, _ := strconv.ParseFloat(fields[1], 64)
		used, _ := strconv.ParseFloat(fields[2], 64)
		if total <= 0 {
			return 0, 0
		}
		return round1(total / 1024), round1(used / total * 100)
	}
	return 0, 0
}

// parseDfPM 解析 `df -Pm` 输出，仅统计 /dev/ 开头的本地文件系统。
func parseDfPM(out string) (float64, float64) {
	var totalMB, usedMB float64
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.HasPrefix(fields[0], "/dev/") {
			continue
		}
		t, err1 := strconv.ParseFloat(fields[1], 64)
		u, err2 := strconv.ParseFloat(fields[2], 64)
		if err1 != nil || err2 != nil {
			continue
		}
		totalMB += t
		usedMB += u
	}
	if totalMB <= 0 {
		return 0, 0
	}
	return round1(totalMB / 1024), round1(usedMB / totalMB * 100)
}

// round1 保留一位小数。
func round1(v float64) float64 {
	return float64(int64(v*10+0.5)) / 10
}
