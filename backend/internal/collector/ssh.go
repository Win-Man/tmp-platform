package collector

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/tmp-platform/backend/internal/pkg/sshcmd"
)

// cmdRunner 统一抽象 SSH 远端与本机命令执行（TiUP 与平台同机时走本地）。
type cmdRunner interface {
	RunTemplate(template string, args ...string) (string, error)
	Run(cmd string) (string, error)
	Close() error
}

// sshChannel 真实 SSH 通道：读取 TiUP meta.yaml 与主机资源。
type sshChannel struct {
	timeout time.Duration
}

// NewSSHChannel 构造 SSH 通道。
func NewSSHChannel(timeout time.Duration) SSHChannel {
	if timeout <= 0 {
		timeout = ChannelTimeout
	}
	return &sshChannel{timeout: timeout}
}

// Kind 实现 Channel。
func (s *sshChannel) Kind() Kind { return KindSSH }

// dial 建立到 TiUP 主机的命令通道：本机地址直接本地执行，其余走 SSH。
func (s *sshChannel) dial(t *Target) (cmdRunner, error) {
	if sshcmd.IsLocalHost(t.TiUPHost) {
		return sshcmd.NewLocalRunner(5 * time.Second), nil
	}
	return sshcmd.Dial(sshcmd.DialOptions{
		Host:       t.TiUPHost,
		Port:       t.TiUPPort,
		User:       t.TiUPUser,
		Password:   t.TiUPPassword,
		PrivateKey: t.TiUPKey,
		Timeout:    5 * time.Second,
	})
}

// Probe 探测 SSH 连通性：登录成功 + 能执行 uname -m。
func (s *sshChannel) Probe(ctx context.Context, t *Target) (ProbeResult, error) {
	start := time.Now()
	res := ProbeResult{Scope: ScopeSSH}
	local := sshcmd.IsLocalHost(t.TiUPHost)
	r, err := s.dial(t)
	if err != nil {
		res.Message = "SSH 连接失败: " + err.Error()
		res.LatencyMS = time.Since(start).Milliseconds()
		return res, nil
	}
	defer func() { _ = r.Close() }()

	out, err := r.RunTemplate("arch")
	res.LatencyMS = time.Since(start).Milliseconds()
	if err != nil {
		res.Message = "SSH 登录成功但执行命令失败: " + err.Error()
		return res, nil
	}
	arch := NormalizeArch(strings.TrimSpace(out))
	res.OK = true
	if local {
		res.Message = "本地模式：TiUP 与平台同机，已直读元数据"
		res.Detail = map[string]any{"arch": arch, "local": true}
	} else {
		res.Message = "SSH 连接成功"
		res.Detail = map[string]any{"arch": arch, "user": t.TiUPUser}
	}
	return res, nil
}

// metaPath 计算 meta.yaml 路径。
//
// 路径由「TiUP 部署目录 + 集群名」拼成，两段都经过 sshcmd 的字符白名单校验，
// 不接受任何外部任意输入。本机模式下默认取当前用户 home（macOS 为 /Users/<user>）。
func metaPath(t *Target) string {
	base := strings.TrimSpace(t.TiUPDir)
	if base == "" {
		if sshcmd.IsLocalHost(t.TiUPHost) {
			if home, err := os.UserHomeDir(); err == nil && home != "" {
				base = home + "/.tiup"
			}
		}
	}
	if base == "" {
		if t.TiUPUser == "root" {
			base = "/root/.tiup"
		} else {
			base = "/home/" + t.TiUPUser + "/.tiup"
		}
	}
	base = strings.TrimSuffix(base, "/")
	return base + "/storage/cluster/clusters/" + t.ClusterName + "/meta.yaml"
}

// FetchTiUPMeta 按三级顺序采集 TiUP 集群元数据：
//
//  1. `tiup cluster display <name> --format=json`（首选：结构稳定、含头部信息）；
//  2. meta.yaml（次选：display 不可用时的原始拓扑）；
//  3. `tiup cluster display <name>` 文本输出（末选：头部 + 实例表格）。
//
// 三级全失败时返回聚合错误，逐级原因拼进 message 便于排错。
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

	// 第 1 级：display JSON。
	if meta, why := fetchMetaFromDisplayJSON(r, t); meta != nil {
		return meta, nil
	} else if why != "" {
		reasons = append(reasons, why)
	}

	// 第 2 级：meta.yaml。
	if meta, why := fetchMetaFromYAML(r, t); meta != nil {
		return meta, nil
	} else if why != "" {
		reasons = append(reasons, why)
	}

	// 第 3 级：display 文本输出。
	if meta, why := fetchMetaFromDisplayText(r, t); meta != nil {
		return meta, nil
	} else if why != "" {
		reasons = append(reasons, why)
	}

	return nil, fmt.Errorf("采集 TiUP 元数据失败: %s", strings.Join(reasons, "; "))
}

// fetchMetaFromDisplayJSON 第 1 级：解析 `tiup cluster display --format=json`。
func fetchMetaFromDisplayJSON(r cmdRunner, t *Target) (*TiUPMeta, string) {
	out, err := r.RunTemplate("tiup_display_json", t.ClusterName)
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

// fetchMetaFromYAML 第 2 级：读取并解析 TiUP 存储目录下的 meta.yaml。
func fetchMetaFromYAML(r cmdRunner, t *Target) (*TiUPMeta, string) {
	content, err := r.RunTemplate("cat", metaPath(t))
	if err != nil {
		return nil, "读取 meta.yaml 失败: " + err.Error()
	}
	var meta TiUPMeta
	if err := yaml.Unmarshal([]byte(content), &meta); err != nil {
		return nil, "解析 meta.yaml 失败: " + err.Error()
	}
	if meta.ClusterVersion == "" && meta.Version == "" && meta.TiDBVersion == "" && !metaHasServers(&meta) {
		return nil, "meta.yaml 内容为空（无版本信息与实例定义）"
	}
	meta.Source = MetaSourceYAML
	return &meta, ""
}

// fetchMetaFromDisplayText 第 3 级：解析 `tiup cluster display` 的文本输出（头部 + 实例表格）。
func fetchMetaFromDisplayText(r cmdRunner, t *Target) (*TiUPMeta, string) {
	out, err := r.RunTemplate("tiup_display", t.ClusterName)
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
	if out, err := r.RunTemplate("arch"); err == nil {
		h.Arch = NormalizeArch(strings.TrimSpace(out))
	}
	if out, err := r.RunTemplate("hostname"); err == nil {
		h.Hostname = strings.TrimSpace(out)
	}
	if out, err := r.RunTemplate("nproc"); err == nil {
		h.CPUCores, _ = strconv.Atoi(strings.TrimSpace(out))
	}
	if out, err := r.RunTemplate("free"); err == nil {
		h.MemoryGB, h.MemUsagePct = parseFreeM(out)
	}
	if out, err := r.RunTemplate("df"); err == nil {
		h.DiskTotalGB, h.DiskUsagePct = parseDfPM(out)
	}
	h.CPUUsagePct = s.cpuUsage(r)
	return []HostResource{h}, nil
}

// cpuUsage 通过两次读取 /proc/stat 计算瞬时 CPU 使用率。
func (s *sshChannel) cpuUsage(r cmdRunner) float64 {
	first, err := r.RunTemplate("cat", "/proc/stat")
	if err != nil {
		return 0
	}
	idle1, total1 := parseProcStat(first)
	time.Sleep(200 * time.Millisecond)
	second, err := r.RunTemplate("cat", "/proc/stat")
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
