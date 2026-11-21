package collector

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/tmp-platform/backend/internal/pkg/sshcmd"
)

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

// dial 建立到 TiUP 主机的 SSH 连接。
func (s *sshChannel) dial(t *Target) (*sshcmd.Runner, error) {
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
	res.Message = "SSH 连接成功"
	res.Detail = map[string]any{"arch": arch, "user": t.TiUPUser}
	return res, nil
}

// metaPath 计算 meta.yaml 路径。
//
// 路径由「TiUP 部署目录 + 集群名」拼成，两段都经过 sshcmd 的字符白名单校验，
// 不接受任何外部任意输入。
func metaPath(t *Target) string {
	base := strings.TrimSpace(t.TiUPDir)
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

// FetchTiUPMeta 读取并解析 meta.yaml；失败时回退到 `tiup cluster display -T`。
func (s *sshChannel) FetchTiUPMeta(ctx context.Context, t *Target) (*TiUPMeta, error) {
	r, err := s.dial(t)
	if err != nil {
		return nil, fmt.Errorf("SSH 连接失败: %w", err)
	}
	defer func() { _ = r.Close() }()

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	path := metaPath(t)
	content, err := r.RunTemplate("cat", path)
	if err != nil {
		// 兜底：tiup cluster display -T
		return s.fetchMetaFromDisplay(r, t)
	}
	var meta TiUPMeta
	if err := yaml.Unmarshal([]byte(content), &meta); err != nil {
		return s.fetchMetaFromDisplay(r, t)
	}
	if meta.ClusterVersion == "" && meta.Version == "" && len(meta.TidbServers) == 0 {
		return s.fetchMetaFromDisplay(r, t)
	}
	return &meta, nil
}

// fetchMetaFromDisplay 用 `tiup cluster display <name> -T` 的文本表兜底构造 meta。
func (s *sshChannel) fetchMetaFromDisplay(r *sshcmd.Runner, t *Target) (*TiUPMeta, error) {
	out, err := r.RunTemplate("tiup_display", t.ClusterName)
	if err != nil {
		return nil, fmt.Errorf("读取 meta.yaml 失败且 tiup cluster display 也不可用: %w", err)
	}
	instances := ParseTiUPDisplay(out)
	if len(instances) == 0 {
		return nil, errors.New("无法从 tiup cluster display 解析出实例列表")
	}
	meta := &TiUPMeta{}
	for _, inst := range instances {
		srv := MetaServer{Host: inst.HostIP, Port: inst.Port, StatusPort: inst.StatusPort}
		switch inst.Component {
		case "pd":
			meta.PdServers = append(meta.PdServers, srv)
		case "tidb":
			meta.TidbServers = append(meta.TidbServers, srv)
		case "tikv":
			meta.TikvServers = append(meta.TikvServers, srv)
		case "tiflash":
			meta.TiflashServers = append(meta.TiflashServers, srv)
		case "ticdc":
			meta.CdcServers = append(meta.CdcServers, srv)
		case "grafana":
			meta.GrafanaServers = append(meta.GrafanaServers, srv)
		case "prometheus":
			meta.MonitoringServers = append(meta.MonitoringServers, srv)
		case "alertmanager":
			meta.AlertmanagerServers = append(meta.AlertmanagerServers, srv)
		}
	}
	return meta, nil
}

// FetchHostResource 采集 TiUP 主机自身资源（CPU/内存/磁盘/使用率）。
//
// 说明：MVP 只采集 TiUP 主机；其余主机的 CPU 核数与内存来自
// information_schema.cluster_hardware（MySQL 通道），无需逐台 SSH 登录。
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
func (s *sshChannel) cpuUsage(r *sshcmd.Runner) float64 {
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
