package collector

import (
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Normalized TiUP display 解析结果的归一化产物。
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

// NormalizeVersion 统一版本号展示形式（补齐 v 前缀），如 8.5.5 → v8.5.5。
//
// PD stores 返回的版本通常不带 v，display 与 SELECT VERSION() 带 v，
// 统一后避免同一集群里出现两种写法。
func NormalizeVersion(raw string) string {
	v := strings.TrimSpace(raw)
	if v == "" {
		return ""
	}
	if strings.HasPrefix(v, "v") || strings.HasPrefix(v, "V") {
		return "v" + v[1:]
	}
	if v[0] >= '0' && v[0] <= '9' {
		return "v" + v
	}
	return v
}

// archNormalize 架构名归一化表。
//
// 兼容 `tiup cluster display` 的 `linux/aarch64` 形式：含 `/` 时取最后一段。
func NormalizeArch(raw string) string {
	arch := strings.TrimSpace(raw)
	if i := strings.LastIndex(arch, "/"); i >= 0 {
		arch = strings.TrimSpace(arch[i+1:])
	}
	switch strings.ToLower(arch) {
	case "x86_64", "amd64", "x64", "x86-64":
		return "x86_64"
	case "aarch64", "arm64", "armv8", "arm64e":
		return "aarch64"
	default:
		return arch
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

// normalizeServers 把 TiUP display 解析出的 server 段转换为实例快照。
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
		// 状态只来自 `tiup cluster display` 的状态列；display 未给出时记 unknown，
		// 绝不默认成 up —— 否则「采不到状态」会被界面读成「一切正常」。
		status := "unknown"
		if s.Status != "" {
			status = s.Status
		}
		out = append(out, InstanceSnapshot{
			Component:  component,
			HostIP:     host,
			Port:       port,
			StatusPort: statusPort,
			Role:       role,
			Status:     status,
			Version:    NormalizeVersion(version),
			AZ:         s.AZ,
			DeployDir:  s.DeployDir,
			DataDir:    s.DataDir,
		})
	}
	return out
}

// NormalizeMeta 按 ARCHITECTURE §6.3 把 TiUP display 结果映射为 Snapshot 字段。
func NormalizeMeta(meta *TiUPMeta, clusterName string) *Normalized {
	n := &Normalized{}
	if meta == nil {
		return n
	}
	// 版本优先级：tidb_version（真实 TiUP 写法）→ cluster_version → version。
	n.Version = firstNonEmpty(meta.TiDBVersion, meta.ClusterVersion, meta.Version)
	// 架构：global.arch（老版本）→ 各 server 级 arch（真实 meta 常见）→ 后续由 SSH/本地 uname 兜底。
	n.Arch = NormalizeArch(firstNonEmpty(meta.Global.Arch, archOfServers(meta)))

	version := n.Version
	// PD 在真实 meta.yaml 里没有 port，只有 client_port / peer_port，先归一化再映射。
	n.Instances = append(n.Instances, normalizeServers(withPDClientPort(meta.PdServers), "pd", 2379, 2379, version)...)
	n.Instances = append(n.Instances, normalizeServers(meta.TidbServers, "tidb", 4000, 10080, version)...)
	n.Instances = append(n.Instances, normalizeServers(meta.TiproxyServers, "tiproxy", 4000, 3080, version)...)
	n.Instances = append(n.Instances, normalizeServers(meta.TikvServers, "tikv", 20160, 20180, version)...)
	n.Instances = append(n.Instances, normalizeServers(meta.TiflashServers, "tiflash", 9000, 8234, version)...)
	n.Instances = append(n.Instances, normalizeServers(meta.CdcServers, "ticdc", 8300, 8300, version)...)
	n.Instances = append(n.Instances, normalizeServers(meta.TicdcServers, "ticdc", 8300, 8300, version)...)

	// 监控组件端口：优先取 server 级 port，其次 global.grafana_port，最后组件默认端口。
	grafanaPort := firstPositive(meta.Global.GrafanaPort, 3000)
	n.Instances = append(n.Instances, normalizeServers(meta.GrafanaServers, "grafana", grafanaPort, grafanaPort, version)...)
	n.Instances = append(n.Instances, normalizeServers(meta.MonitoringServers, "prometheus", 9090, 9090, version)...)
	n.Instances = append(n.Instances, normalizeServers(meta.AlertmanagerServers, "alertmanager", 9093, 9093, version)...)

	// 外部系统地址（按实际端口拼装，去重后返回）。
	// Grafana / Dashboard 优先采用 `tiup cluster display` 显式给出的地址，
	// 未提供时才按 server 列表与端口推导。
	if len(meta.GrafanaURLs) > 0 {
		n.GrafanaURLs = dedupeStrings(meta.GrafanaURLs)
	} else {
		n.GrafanaURLs = urlsOf(meta.GrafanaServers, grafanaPort)
	}
	// Prometheus：display JSON 不提供该 URL，始终按 monitoring_servers 实际端口推导。
	n.PrometheusURLs = urlsOf(meta.MonitoringServers, 9090)
	n.AlertmanagerURLs = urlsOf(meta.AlertmanagerServers, 9093)
	// TiDB Dashboard 由 PD 提供：优先用 display 给出的地址，否则走 PD client port 推导。
	if len(meta.DashboardURLs) > 0 {
		n.DashboardURLs = dedupeStrings(meta.DashboardURLs)
	} else {
		for _, s := range withPDClientPort(meta.PdServers) {
			if h := pickHost(s); h != "" {
				n.DashboardURLs = append(n.DashboardURLs, "http://"+h+":"+strconv.Itoa(firstPositive(s.Port, 2379))+"/dashboard")
			}
		}
	}
	n.DashboardURLs = dedupeStrings(n.DashboardURLs)
	n.GrafanaURLs = dedupeStrings(n.GrafanaURLs)
	n.PrometheusURLs = dedupeStrings(n.PrometheusURLs)
	n.AlertmanagerURLs = dedupeStrings(n.AlertmanagerURLs)

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

// firstNonEmpty 返回第一个非空字符串（已去首尾空白）。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if s := strings.TrimSpace(v); s != "" {
			return s
		}
	}
	return ""
}

// firstPositive 返回第一个正整数，全为非正数时返回 0。
func firstPositive(vals ...int) int {
	for _, v := range vals {
		if v > 0 {
			return v
		}
	}
	return 0
}

// archOfServers 从各 server 的 arch 字段推断集群架构。
//
// TiUP 拓扑的 arch 通常写在每个 server 上（如 arm64），global 段常常为空。
func archOfServers(m *TiUPMeta) string {
	groups := [][]MetaServer{
		m.PdServers, m.TidbServers, m.TiproxyServers, m.TikvServers,
		m.TiflashServers, m.CdcServers, m.TicdcServers,
		m.MonitoringServers, m.GrafanaServers, m.AlertmanagerServers,
	}
	for _, g := range groups {
		for _, s := range g {
			if a := strings.TrimSpace(s.Arch); a != "" {
				return a
			}
		}
	}
	return ""
}

// withPDClientPort 返回 PD server 副本：PD 的端口来自 client_port，Port 为空时用它补齐。
func withPDClientPort(in []MetaServer) []MetaServer {
	out := make([]MetaServer, len(in))
	copy(out, in)
	for i := range out {
		if out[i].Port <= 0 && out[i].ClientPort > 0 {
			out[i].Port = out[i].ClientPort
		}
		if out[i].StatusPort <= 0 && out[i].PeerPort > 0 {
			out[i].StatusPort = out[i].PeerPort
		}
	}
	return out
}

// urlsOf 按 server 实际端口拼装 http 地址列表（去重）。
func urlsOf(servers []MetaServer, defPort int) []string {
	out := make([]string, 0, len(servers))
	for _, s := range servers {
		h := pickHost(s)
		if h == "" {
			continue
		}
		out = append(out, "http://"+h+":"+strconv.Itoa(firstPositive(s.Port, defPort)))
	}
	return dedupeStrings(out)
}

// dedupeStrings 去重并保持原有顺序。
func dedupeStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
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
		"pd": 0, "tidb": 1, "tiproxy": 2, "tikv": 3, "tiflash": 4, "ticdc": 5,
		"prometheus": 6, "grafana": 7, "alertmanager": 8,
	}
	if v, ok := order[c]; ok {
		return v
	}
	return 99
}

// metaHasServers 判断 meta 是否至少包含一条 server 定义（用于判定解析是否有效）。
//
// 旧实现只看 tidb_servers，会误判只有 PD/TiKV 的集群；这里检查全部组件列表。
func metaHasServers(m *TiUPMeta) bool {
	if m == nil {
		return false
	}
	groups := [][]MetaServer{
		m.PdServers, m.TidbServers, m.TiproxyServers, m.TikvServers,
		m.TiflashServers, m.CdcServers, m.TicdcServers,
		m.GrafanaServers, m.MonitoringServers, m.AlertmanagerServers,
	}
	for _, g := range groups {
		if len(g) > 0 {
			return true
		}
	}
	return false
}

// normalizeInstanceStatus 把 TiUP 文本状态归一为 up/down/unhealthy/unknown。
//
// 判定顺序很重要：unhealthy/degraded 必须先判，否则包含子串 "healthy" 的
// "Unhealthy" 会被误判成 up。up 只认行首或 `|up` 形态（TiUP 用 `Up|L|UI` 表达复合状态），
// 避免 "startup" 之类的词被误判。
func normalizeInstanceStatus(raw string) string {
	l := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(l, "unhealthy"), strings.Contains(l, "degraded"):
		return "unhealthy"
	case strings.HasPrefix(l, "up"), strings.Contains(l, "|up"):
		return "up"
	case strings.HasPrefix(l, "down"), strings.Contains(l, "down"), strings.Contains(l, "offline"):
		return "down"
	default:
		return "unknown"
	}
}

// MergeInstances 用 PD stores 补齐 TiKV 实例的 AZ 与空版本。
//
// 只补齐「静态属性」，绝不改写 Status：实例运行状态唯一来源是
// `tiup cluster display`，PD 的 store 状态不能混进来（两者口径不同，
// 混用会出现「display 说 Up、界面显示 Down」的自相矛盾）。
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
			if base[i].Version == "" && s.Version != "" {
				base[i].Version = NormalizeVersion(s.Version)
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
//
// 语义：只追加 base 里没有的 IP，绝不改动 base 已有记录的任何字段。
// 字段级补齐请用 FillHostResources。
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

// FillHostResources 以 base 为主、用 extra 逐字段补齐 base 中缺失的信息。
//
// 这是「多通道合并主机资源」的核心规则：**base 已有的值绝不被覆盖**，
// 只填补 base 中为空（Arch/Hostname/OSUser）或为 0（各数值字段）的部分；
// extra 中存在而 base 完全没有的 IP 会被追加。
//
// 采集侧按 SSH（中控机实测）→ MySQL cluster_hardware/load（其余主机）→ 仅 IP 占位
// 的顺序逐层补齐，因此 SSH 更权威、MySQL 只负责补空白（ARCHITECTURE §6.6.5）。
func FillHostResources(base, extra []HostResource) []HostResource {
	out := make([]HostResource, 0, len(base)+len(extra))
	index := make(map[string]int, len(base))
	for _, h := range base {
		if h.IP == "" {
			continue
		}
		if _, ok := index[h.IP]; ok {
			continue
		}
		index[h.IP] = len(out)
		out = append(out, h)
	}
	for _, e := range extra {
		if e.IP == "" {
			continue
		}
		i, ok := index[e.IP]
		if !ok {
			index[e.IP] = len(out)
			out = append(out, e)
			continue
		}
		fillMissingHostFields(&out[i], e)
	}
	return out
}

// fillMissingHostFields 只把 dst 中缺失的字段用 src 补齐。
func fillMissingHostFields(dst *HostResource, src HostResource) {
	if dst.Arch == "" {
		dst.Arch = src.Arch
	}
	if dst.Hostname == "" {
		dst.Hostname = src.Hostname
	}
	if dst.OSUser == "" {
		dst.OSUser = src.OSUser
	}
	if dst.CPUCores == 0 {
		dst.CPUCores = src.CPUCores
	}
	if dst.MemoryGB == 0 {
		dst.MemoryGB = src.MemoryGB
	}
	if dst.DiskTotalGB == 0 {
		dst.DiskTotalGB = src.DiskTotalGB
	}
	if dst.CPUUsagePct == 0 {
		dst.CPUUsagePct = src.CPUUsagePct
	}
	if dst.MemUsagePct == 0 {
		dst.MemUsagePct = src.MemUsagePct
	}
	if dst.DiskUsagePct == 0 {
		dst.DiskUsagePct = src.DiskUsagePct
	}
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
