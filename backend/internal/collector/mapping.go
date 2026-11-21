package collector

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Normalized meta.yaml 归一化结果。
type Normalized struct {
	Instances        []InstanceSnapshot
	DashboardURLs    []string
	PrometheusURLs   []string
	GrafanaURLs      []string
	AlertmanagerURLs []string
	Version          string
	Arch             string
	ComponentCount   int
	HostCount        int
}

// archNormalize 架构名归一化表。
func NormalizeArch(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "x86_64", "amd64", "x64", "x86-64":
		return "x86_64"
	case "aarch64", "arm64", "armv8", "arm64e":
		return "aarch64"
	default:
		return strings.TrimSpace(raw)
	}
}

// NormalizeDeployMode 部署模式归一化：DR AUTO SYNC → dr_auto_sync，其余 → majority。
func NormalizeDeployMode(raw string) string {
	l := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case l == "":
		return "unknown"
	case strings.Contains(l, "dr") && strings.Contains(l, "auto"):
		return "dr_auto_sync"
	case strings.Contains(l, "majority"):
		return "majority"
	case strings.Contains(l, "sync"):
		return "dr_auto_sync"
	default:
		return l
	}
}

// ParsePDEndpoint 解析 PD 地址（支持 http://ip:2379 或裸 ip:2379）。
func ParsePDEndpoint(raw string) (host string, port int) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 2379
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", 2379
	}
	host = u.Hostname()
	port = 2379
	if p := u.Port(); p != "" {
		if v, convErr := strconv.Atoi(p); convErr == nil {
			port = v
		}
	}
	return host, port
}

// normalizeServers 把 meta.yaml 的 server 段转换为实例快照。
func normalizeServers(servers []MetaServer, component string, defaultPort, defaultStatusPort int, version string) []InstanceSnapshot {
	out := make([]InstanceSnapshot, 0, len(servers))
	for i, s := range servers {
		host := strings.TrimSpace(s.Host)
		if host == "" {
			host = strings.TrimSpace(s.IP)
		}
		if host == "" {
			continue
		}
		port := s.Port
		if port <= 0 {
			port = defaultPort
		}
		statusPort := s.StatusPort
		if statusPort <= 0 {
			statusPort = defaultStatusPort
		}
		role := "service"
		if component == "pd" && i == 0 {
			role = "leader"
		}
		if component == "tikv" || component == "tidb" {
			role = "service"
		}
		out = append(out, InstanceSnapshot{
			Component:  component,
			HostIP:     host,
			Port:       port,
			StatusPort: statusPort,
			Role:       role,
			Status:     "up",
			Version:    version,
			AZ:         s.AZ,
			DeployDir:  s.DeployDir,
			DataDir:    s.DataDir,
		})
	}
	return out
}

// NormalizeMeta 按 ARCHITECTURE §6.3 把 meta.yaml 映射为 Snapshot 字段。
func NormalizeMeta(meta *TiUPMeta, clusterName string) *Normalized {
	n := &Normalized{}
	if meta == nil {
		return n
	}
	n.Version = strings.TrimSpace(meta.ClusterVersion)
	if n.Version == "" {
		n.Version = strings.TrimSpace(meta.Version)
	}
	n.Arch = NormalizeArch(meta.Global.Arch)

	version := n.Version
	n.Instances = append(n.Instances, normalizeServers(meta.PdServers, "pd", 2379, 2379, version)...)
	n.Instances = append(n.Instances, normalizeServers(meta.TidbServers, "tidb", 4000, 10080, version)...)
	n.Instances = append(n.Instances, normalizeServers(meta.TikvServers, "tikv", 20160, 20180, version)...)
	n.Instances = append(n.Instances, normalizeServers(meta.TiflashServers, "tiflash", 9000, 8234, version)...)
	n.Instances = append(n.Instances, normalizeServers(meta.CdcServers, "ticdc", 8300, 8300, version)...)
	n.Instances = append(n.Instances, normalizeServers(meta.TicdcServers, "ticdc", 8300, 8300, version)...)

	// 监控组件端口：Grafana 取 global.grafana_port（默认 3000），Prometheus 9090，Alertmanager 9093。
	grafanaPort := meta.Global.GrafanaPort
	if grafanaPort <= 0 {
		grafanaPort = 3000
	}
	graphanaServers := normalizeServers(meta.GrafanaServers, "grafana", grafanaPort, grafanaPort, version)
	n.Instances = append(n.Instances, graphanaServers...)
	n.Instances = append(n.Instances, normalizeServers(meta.MonitoringServers, "prometheus", 9090, 9090, version)...)
	n.Instances = append(n.Instances, normalizeServers(meta.AlertmanagerServers, "alertmanager", 9093, 9093, version)...)

	// 外部系统地址。
	for _, s := range meta.GrafanaServers {
		if h := pickHost(s); h != "" {
			n.GrafanaURLs = append(n.GrafanaURLs, "http://"+h+":"+strconv.Itoa(grafanaPort))
		}
	}
	for _, s := range meta.MonitoringServers {
		if h := pickHost(s); h != "" {
			n.PrometheusURLs = append(n.PrometheusURLs, "http://"+h+":9090")
		}
	}
	for _, s := range meta.AlertmanagerServers {
		if h := pickHost(s); h != "" {
			n.AlertmanagerURLs = append(n.AlertmanagerURLs, "http://"+h+":9093")
		}
	}
	// TiDB Dashboard 由 PD 提供。
	for _, s := range meta.PdServers {
		if h := pickHost(s); h != "" {
			n.DashboardURLs = append(n.DashboardURLs, "http://"+h+":2379/dashboard")
		}
	}

	n.Instances = dedupeInstances(n.Instances)
	n.ComponentCount = countComponents(n.Instances)
	n.HostCount = countHosts(n.Instances)
	return n
}

// pickHost 从 MetaServer 取主机标识。
func pickHost(s MetaServer) string {
	if h := strings.TrimSpace(s.Host); h != "" {
		return h
	}
	return strings.TrimSpace(s.IP)
}

// dedupeInstances 按 (component, host, port) 去重（兼容 cdc_servers 与 ticdc_servers 同时存在）。
func dedupeInstances(in []InstanceSnapshot) []InstanceSnapshot {
	seen := make(map[string]bool, len(in))
	out := make([]InstanceSnapshot, 0, len(in))
	for _, i := range in {
		k := i.Component + "|" + i.HostIP + "|" + strconv.Itoa(i.Port)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, i)
	}
	sort.SliceStable(out, func(a, b int) bool {
		if out[a].Component != out[b].Component {
			return componentOrder(out[a].Component) < componentOrder(out[b].Component)
		}
		if out[a].HostIP != out[b].HostIP {
			return out[a].HostIP < out[b].HostIP
		}
		return out[a].Port < out[b].Port
	})
	return out
}

// countComponents 统计不同组件种类数。
func countComponents(list []InstanceSnapshot) int {
	set := map[string]bool{}
	for _, i := range list {
		set[i.Component] = true
	}
	return len(set)
}

// countHosts 统计不同主机数。
func countHosts(list []InstanceSnapshot) int {
	set := map[string]bool{}
	for _, i := range list {
		set[i.HostIP] = true
	}
	return len(set)
}

// componentOrder 组件展示顺序。
func componentOrder(c string) int {
	order := map[string]int{
		"pd": 0, "tidb": 1, "tikv": 2, "tiflash": 3, "ticdc": 4,
		"prometheus": 5, "grafana": 6, "alertmanager": 7,
	}
	if v, ok := order[c]; ok {
		return v
	}
	return 99
}

// displayRoleRe 匹配 tiup cluster display 表格中的组件列（含别名）。
var displayRoleRe = regexp.MustCompile(`^(pd|tidb|tikv|tiflash|cdc|ticdc|grafana|prometheus|alertmanager|alert_manager|monitoring)$`)

// ParseTiUPDisplay 解析 `tiup cluster display <name> -T` 的固定列宽表格输出，
// 作为 meta.yaml 读取失败时的兜底路径。
func ParseTiUPDisplay(output string) []InstanceSnapshot {
	var out []InstanceSnapshot
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "ID") || strings.HasPrefix(line, "+") || strings.HasPrefix(line, "Cluster") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 4 {
			continue
		}
		component := strings.ToLower(strings.TrimSpace(fields[1]))
		if !displayRoleRe.MatchString(component) {
			continue
		}
		if component == "cdc" {
			component = "ticdc"
		}
		if component == "monitoring" {
			component = "prometheus"
		}
		if component == "alert_manager" {
			component = "alertmanager"
		}
		host := strings.TrimSpace(fields[2])
		if host == "" {
			continue
		}
		ports := strings.Fields(fields[3])
		port := 0
		if len(ports) > 0 {
			port, _ = strconv.Atoi(ports[0])
		}
		status := "unknown"
		if len(fields) > 4 {
			status = normalizeInstanceStatus(fields[4])
		}
		out = append(out, InstanceSnapshot{
			Component: component,
			HostIP:    host,
			Port:      port,
			Status:    status,
			Role:      "service",
		})
	}
	return out
}

// normalizeInstanceStatus 把 TiUP 文本状态归一为 up/down/unhealthy/unknown。
func normalizeInstanceStatus(raw string) string {
	l := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.HasPrefix(l, "up"), strings.Contains(l, "healthy"):
		return "up"
	case strings.HasPrefix(l, "down"), strings.Contains(l, "offline"):
		return "down"
	case strings.Contains(l, "unhealthy"), strings.Contains(l, "degraded"):
		return "unhealthy"
	default:
		return "unknown"
	}
}

// MergeInstances 用 PD stores 结果校准 TiKV 实例状态与标签（AZ）。
func MergeInstances(base []InstanceSnapshot, stores []Store) []InstanceSnapshot {
	if len(stores) == 0 {
		return base
	}
	byAddr := make(map[string]Store, len(stores))
	for _, s := range stores {
		host, port := splitHostPort(s.Address)
		byAddr[host+"|"+port] = s
	}
	for i := range base {
		if base[i].Component != "tikv" {
			continue
		}
		key := base[i].HostIP + "|" + strconv.Itoa(base[i].Port)
		if s, ok := byAddr[key]; ok {
			base[i].Status = normalizeInstanceStatus(s.StateName)
			if s.Version != "" {
				base[i].Version = s.Version
			}
			if base[i].AZ == "" {
				if az, ok2 := s.Labels["zone"]; ok2 {
					base[i].AZ = az
				}
			}
		}
	}
	return base
}

// splitHostPort 拆分 host:port。
func splitHostPort(addr string) (string, string) {
	idx := strings.LastIndex(addr, ":")
	if idx < 0 {
		return addr, ""
	}
	return addr[:idx], addr[idx+1:]
}

// MergeHostResources 用 SSH 主机资源补齐 hosts（按 IP）。
func MergeHostResources(hosts []HostResource, additional []HostResource) []HostResource {
	seen := make(map[string]bool, len(hosts))
	for _, h := range hosts {
		seen[h.IP] = true
	}
	for _, h := range additional {
		if h.IP == "" || seen[h.IP] {
			continue
		}
		seen[h.IP] = true
		hosts = append(hosts, h)
	}
	return hosts
}

// HostIPsFromInstances 提取实例涉及的主机 IP 列表（去重、排序）。
func HostIPsFromInstances(list []InstanceSnapshot) []string {
	set := map[string]bool{}
	for _, i := range list {
		if i.HostIP != "" {
			set[i.HostIP] = true
		}
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// NowPtr 返回当前时间指针，便于构造快照时间。
func NowPtr() *time.Time {
	now := time.Now()
	return &now
}
