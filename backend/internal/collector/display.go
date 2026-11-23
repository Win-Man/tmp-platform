package collector

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// 数据来源标识：写入 TiUPMeta.Source，便于前端展示与线上排错。
const (
	// DisplaySourceJSON 来源为 `tiup cluster display <name> --format=json`。
	DisplaySourceJSON = "display_json"
	// DisplaySourceText 来源为 `tiup cluster display <name>` 的文本输出
	// （非 TTY 环境下 TiUP 直接输出文本表格）。
	DisplaySourceText = "display_text"
)

// 端口与 URL 分隔符。
var (
	// displayColSplitRe 文本表格的列分隔符：TiUP 用 2 个以上空格对齐列。
	displayColSplitRe = regexp.MustCompile(`\s{2,}`)
	// displayURLSplitRe 多 URL 分隔符：逗号 / 分号 / 空白。
	displayURLSplitRe = regexp.MustCompile(`[,;\s]+`)
)

// displayHeaderKeys 文本输出头部允许的键名白名单（已小写化）。
//
// 必须与 applyHeader 的 switch 保持一致（display_test.go 里有对应用例守同步）。
// 用白名单而非「像标签词」的正则猜测，是为了彻底消除歧义：
// 主机名编址的实例行（如 pd0:2379 / tikv0:20160）按冒号切分后 key 形如 pd0，
// 若用宽松正则会被当成头部行整行吞掉，导致拓扑残缺甚至解析出 0 个实例。
var displayHeaderKeys = map[string]bool{
	"cluster type":    true,
	"cluster name":    true,
	"cluster version": true,
	"deploy user":     true,
	"ssh type":        true,
	"tls enabled":     true,
	"dashboard url":   true,
	"dashboard urls":  true,
	"grafana url":     true,
	"grafana urls":    true,
}

// DisplayClusterMeta `tiup cluster display` 头部信息（cluster_meta 段）。
type DisplayClusterMeta struct {
	ClusterType    string   `json:"cluster_type"`
	ClusterName    string   `json:"cluster_name"`
	ClusterVersion string   `json:"cluster_version"`
	DeployUser     string   `json:"deploy_user"`
	SSHType        string   `json:"ssh_type"`
	TLSEnabled     bool     `json:"tls_enabled"`
	DashboardURL   string   `json:"dashboard_url"`
	DashboardURLs  []string `json:"dashboard_urls"`
	GrafanaURLs    []string `json:"grafana_urls"`
}

// DisplayInstance `tiup cluster display` 的单个实例行。
//
// ComponentName / Port 在 JSON 输出里已经由 TiUP 计算好（首字母大写键名），
// 文本解析时则分别回退到 Role 与 Ports 的第一段。
type DisplayInstance struct {
	ID            string `json:"id"`
	Role          string `json:"role"`
	Host          string `json:"host"`
	ManageHost    string `json:"manage_host"`
	Ports         string `json:"ports"`
	OSArch        string `json:"os_arch"`
	Status        string `json:"status"`
	DataDir       string `json:"data_dir"`
	DeployDir     string `json:"deploy_dir"`
	Version       string `json:"version"`
	ComponentName string `json:"ComponentName"`
	Port          int    `json:"Port"`
}

// TiUPDisplay `tiup cluster display` 的解析结果，兼容 JSON 与文本两种来源。
type TiUPDisplay struct {
	ClusterMeta DisplayClusterMeta `json:"cluster_meta"`
	Instances   []DisplayInstance  `json:"instances"`

	// source 记录解析来源（display_json / display_text），由解析函数写入。
	source string
}

// Source 返回解析来源标识。
func (d *TiUPDisplay) Source() string {
	if d == nil {
		return ""
	}
	return d.source
}

// ParseDisplayJSON 解析 `tiup cluster display <name> --format=json` 的输出。
//
// 注意：部分 TiUP 版本（或 stderr 被合并进 stdout 时）会在 JSON 之后多输出一段
// `{"exit_code":0}`，因此只能用 json.Decoder 解码**第一个** JSON 值，
// 不能用 json.Unmarshal 解整个字符串。
func ParseDisplayJSON(raw string) (*TiUPDisplay, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("display JSON 输出为空")
	}
	out := &TiUPDisplay{source: DisplaySourceJSON}
	// Decode 只消费第一个 JSON 值，剩余内容（如 {"exit_code":0}）被忽略。
	if err := json.NewDecoder(strings.NewReader(trimmed)).Decode(out); err != nil {
		return nil, fmt.Errorf("解析 display JSON 失败: %w", err)
	}
	out.normalize()
	return out, nil
}

// ParseDisplayText 解析 `tiup cluster display <name>` 的文本输出
// （非 TTY 环境下 TiUP 直接输出文本表格，无需额外 flag）：
// 头部为「Key:   Value」形式的集群信息，其后是空格对齐的实例表格。
func ParseDisplayText(raw string) (*TiUPDisplay, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("display 文本输出为空")
	}
	out := &TiUPDisplay{source: DisplaySourceText}
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// 表尾统计行不属于任何字段。
		if strings.HasPrefix(trimmed, "Total nodes") {
			continue
		}
		// 头部键值行优先判定，避免把表格行（含 host:port）误判为键值行。
		if key, value, ok := splitDisplayHeader(trimmed); ok {
			out.applyHeader(key, value)
			continue
		}
		cols := displayColSplitRe.Split(trimmed, -1)
		if len(cols) < 8 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(cols[0]), "ID") ||
			strings.HasPrefix(strings.TrimSpace(cols[0]), "--") {
			continue
		}
		inst := DisplayInstance{
			ID:        strings.TrimSpace(cols[0]),
			Role:      strings.ToLower(strings.TrimSpace(cols[1])),
			Host:      strings.TrimSpace(cols[2]),
			Ports:     strings.TrimSpace(cols[3]),
			OSArch:    strings.TrimSpace(cols[4]),
			Status:    strings.TrimSpace(cols[5]),
			DataDir:   strings.TrimSpace(cols[6]),
			DeployDir: strings.TrimSpace(cols[7]),
			Version:   out.ClusterMeta.ClusterVersion,
		}
		if inst.Host == "" {
			continue
		}
		out.Instances = append(out.Instances, inst)
	}
	if len(out.Instances) == 0 {
		return nil, errors.New("display 文本输出中没有解析到实例行")
	}
	out.normalize()
	return out, nil
}

// normalize 补齐 ComponentName / Port / ManageHost，并回填版本。
func (d *TiUPDisplay) normalize() {
	if d == nil {
		return
	}
	if len(d.ClusterMeta.DashboardURLs) == 0 && d.ClusterMeta.DashboardURL != "" {
		d.ClusterMeta.DashboardURLs = []string{d.ClusterMeta.DashboardURL}
	}
	d.ClusterMeta.DashboardURLs = dedupeStrings(d.ClusterMeta.DashboardURLs)
	d.ClusterMeta.GrafanaURLs = dedupeStrings(d.ClusterMeta.GrafanaURLs)
	if d.ClusterMeta.DashboardURL == "" && len(d.ClusterMeta.DashboardURLs) > 0 {
		d.ClusterMeta.DashboardURL = d.ClusterMeta.DashboardURLs[0]
	}
	for i := range d.Instances {
		inst := &d.Instances[i]
		if strings.TrimSpace(inst.ComponentName) == "" {
			inst.ComponentName = inst.Role
		}
		if strings.TrimSpace(inst.ManageHost) == "" {
			inst.ManageHost = inst.Host
		}
		if inst.Port <= 0 {
			primary, _ := splitPorts(inst.Ports)
			inst.Port = primary
		}
		if strings.TrimSpace(inst.Version) == "" {
			inst.Version = d.ClusterMeta.ClusterVersion
		}
	}
}

// splitDisplayHeader 拆分头部键值行（按第一个冒号），并校验键名是否在白名单内。
func splitDisplayHeader(line string) (key, value string, ok bool) {
	idx := strings.Index(line, ":")
	if idx <= 0 {
		return "", "", false
	}
	key = strings.ToLower(strings.TrimSpace(line[:idx]))
	if !displayHeaderKeys[key] {
		return "", "", false
	}
	value = strings.TrimSpace(line[idx+1:])
	if value == "" {
		return "", "", false
	}
	return key, value, true
}

// applyHeader 把头部键值写入 ClusterMeta。
func (d *TiUPDisplay) applyHeader(key, value string) {
	switch key {
	case "cluster type":
		d.ClusterMeta.ClusterType = value
	case "cluster name":
		d.ClusterMeta.ClusterName = value
	case "cluster version":
		d.ClusterMeta.ClusterVersion = value
	case "deploy user":
		d.ClusterMeta.DeployUser = value
	case "ssh type":
		d.ClusterMeta.SSHType = value
	case "tls enabled":
		d.ClusterMeta.TLSEnabled = strings.EqualFold(value, "true") || strings.EqualFold(value, "enabled")
	case "dashboard url":
		d.ClusterMeta.DashboardURL = value
		d.ClusterMeta.DashboardURLs = appendUniqueURL(d.ClusterMeta.DashboardURLs, value)
	case "dashboard urls":
		d.ClusterMeta.DashboardURLs = appendURLs(d.ClusterMeta.DashboardURLs, value)
	case "grafana url":
		d.ClusterMeta.GrafanaURLs = appendUniqueURL(d.ClusterMeta.GrafanaURLs, value)
	case "grafana urls":
		d.ClusterMeta.GrafanaURLs = appendURLs(d.ClusterMeta.GrafanaURLs, value)
	}
}

// appendURLs 把一段以逗号/空白分隔的 URL 串追加到列表。
func appendURLs(dst []string, raw string) []string {
	for _, p := range displayURLSplitRe.Split(raw, -1) {
		dst = appendUniqueURL(dst, strings.TrimSpace(p))
	}
	return dst
}

// appendUniqueURL 追加非空且未出现过的 URL。
func appendUniqueURL(dst []string, url string) []string {
	if url == "" {
		return dst
	}
	for _, v := range dst {
		if v == url {
			return dst
		}
	}
	return append(dst, url)
}

// splitPorts 拆解斜杠分隔的端口串（如 9090/9105/9100/12020）。
//
// 约定：第一段是主端口，第二段是 status/peer 端口，其余忽略。
func splitPorts(s string) (primary, secondary int) {
	for _, p := range strings.Split(strings.TrimSpace(s), "/") {
		v, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil || v <= 0 {
			continue
		}
		switch {
		case primary == 0:
			primary = v
		case secondary == 0:
			secondary = v
		}
	}
	return primary, secondary
}

// archFromOSArch 从 OS/Arch 列取架构：linux/aarch64 → aarch64。
func archFromOSArch(s string) string {
	v := strings.TrimSpace(s)
	if v == "" {
		return ""
	}
	if i := strings.LastIndex(v, "/"); i >= 0 {
		v = strings.TrimSpace(v[i+1:])
	}
	return NormalizeArch(v)
}

// displayInstanceStatus 归一化 display 状态，并识别 PD 的 leader 标记（Up|L|UI）。
func displayInstanceStatus(raw string) (status string, leader bool) {
	l := strings.ToLower(strings.TrimSpace(raw))
	status = normalizeInstanceStatus(raw)
	for _, tok := range strings.Split(l, "|") {
		if strings.TrimSpace(tok) == "l" {
			leader = true
		}
	}
	return status, leader
}

// canonicalComponent 组件名归一：cdc→ticdc、monitoring→prometheus、alert_manager→alertmanager。
func canonicalComponent(raw string) string {
	switch l := strings.ToLower(strings.TrimSpace(raw)); l {
	case "cdc", "ticdc":
		return "ticdc"
	case "monitor", "monitoring":
		return "prometheus"
	case "alert_manager", "alert-manager":
		return "alertmanager"
	default:
		return l
	}
}

// cleanDash 把 TiUP 用 `-` 表示的「空值」转为空字符串。
func cleanDash(v string) string {
	s := strings.TrimSpace(v)
	if s == "-" {
		return ""
	}
	return s
}

// ToMeta 把 display 结果转换为 TiUPMeta，供 NormalizeMeta 复用同一条归一化链路。
func (d *TiUPDisplay) ToMeta() *TiUPMeta {
	if d == nil {
		return &TiUPMeta{}
	}
	source := d.source
	if source == "" {
		source = DisplaySourceText
	}
	// 版本优先取头部 cluster_version；个别 TiUP 版本不带该头部，回退到实例版本。
	version := strings.TrimSpace(d.ClusterMeta.ClusterVersion)
	if version == "" {
		for _, inst := range d.Instances {
			if v := strings.TrimSpace(inst.Version); v != "" {
				version = v
				break
			}
		}
	}
	meta := &TiUPMeta{
		User:           strings.TrimSpace(d.ClusterMeta.DeployUser),
		ClusterVersion: version,
		TiDBVersion:    version,
		DashboardURLs:  dedupeStrings(append([]string{}, d.ClusterMeta.DashboardURLs...)),
		GrafanaURLs:    dedupeStrings(append([]string{}, d.ClusterMeta.GrafanaURLs...)),
		Source:         source,
	}
	// PD leader 先入列表：normalizeServers 依据顺序把首个 PD 标记为 leader。
	var pdLeaders, pdFollowers []MetaServer
	for _, inst := range d.Instances {
		host := firstNonEmpty(inst.Host, inst.ManageHost)
		if host == "" {
			continue
		}
		primary, secondary := splitPorts(inst.Ports)
		if inst.Port > 0 {
			primary = inst.Port
		}
		arch := archFromOSArch(inst.OSArch)
		if arch != "" && meta.Global.Arch == "" {
			meta.Global.Arch = arch
		}
		srv := MetaServer{
			Host:       host,
			Port:       primary,
			StatusPort: secondary,
			DeployDir:  cleanDash(inst.DeployDir),
			DataDir:    cleanDash(inst.DataDir),
			Arch:       arch,
		}
		// display 是实例状态的唯一来源：状态列归一后写进 MetaServer.Status，
		// 由 normalizeServers 透传到 InstanceSnapshot，全程不接受其它通道改写。
		status, leader := displayInstanceStatus(inst.Status)
		srv.Status = status
		switch canonicalComponent(firstNonEmpty(inst.ComponentName, inst.Role)) {
		case "pd":
			// PD 的客户端端口落在 ClientPort（display 的 ports 首段），withPDClientPort 会据此补 Port。
			srv.ClientPort = primary
			srv.PeerPort = secondary
			if leader {
				pdLeaders = append(pdLeaders, srv)
			} else {
				pdFollowers = append(pdFollowers, srv)
			}
		case "tidb":
			meta.TidbServers = append(meta.TidbServers, srv)
		case "tiproxy":
			meta.TiproxyServers = append(meta.TiproxyServers, srv)
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
	meta.PdServers = append(pdLeaders, pdFollowers...)
	return meta
}
