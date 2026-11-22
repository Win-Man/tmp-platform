// Package collector 定义采集适配层：三通道（SSH / MySQL / PD）与 mock 数据源的统一门面。
package collector

import (
	"context"
	"time"

	"gopkg.in/yaml.v3"
)

// Kind 采集通道类型。
type Kind string

// 通道类型常量。
const (
	KindSSH   Kind = "ssh"
	KindMySQL Kind = "mysql"
	KindPD    Kind = "pd"
	KindMock  Kind = "mock"
)

// ProbeScope 连通性探测范围。
type ProbeScope string

// 探测范围常量。
const (
	ScopeSSH   ProbeScope = "ssh"
	ScopeMySQL ProbeScope = "mysql"
	ScopePD    ProbeScope = "pd"
)

// 采集相关超时常量（ARCHITECTURE §6.5）。
const (
	// ChannelTimeout 单通道超时。
	ChannelTimeout = 10 * time.Second
	// TotalTimeout 单集群整体超时。
	TotalTimeout = 15 * time.Second
	// ProbeItemTimeout 连接测试单项超时。
	ProbeItemTimeout = 5 * time.Second
)

// Target 采集目标（由集群配置 + 解密后的凭据构成）。
type Target struct {
	ClusterID   int64
	ClusterName string

	TiUPHost     string
	TiUPPort     int
	TiUPUser     string
	TiUPPassword string
	TiUPKey      string
	TiUPDir      string

	DBHost     string
	DBPort     int
	DBUser     string
	DBPassword string

	PDHost string
	PDPort int
}

// ProbeResult 单项连通性探测结果。
type ProbeResult struct {
	Scope     ProbeScope     `json:"scope"`
	OK        bool           `json:"ok"`
	LatencyMS int64          `json:"latencyMs"`
	Message   string         `json:"message"`
	Detail    map[string]any `json:"detail,omitempty"`
}

// Capabilities 版本能力探测结果（D-6：靠能力探测而非硬编码版本分支）。
type Capabilities struct {
	TiDBVersion            string            `json:"tidbVersion"`
	Major                  int               `json:"-"`
	Minor                  int               `json:"-"`
	SupportsResourceGroup  bool              `json:"supportsResourceGroup"`
	SupportsSessionKill    bool              `json:"supportsSessionKill"`
	SupportsPlacementRules bool              `json:"supportsPlacementRules"`
	ProbeErrors            map[string]string `json:"probeErrors,omitempty"`
	ProbedAt               time.Time         `json:"probedAt"`
}

// CollectScope 采集范围；全量采集时全部为 true。
type CollectScope struct {
	Meta      bool
	Hosts     bool
	Users     bool
	Variables bool
	Sessions  bool
	RGs       bool
}

// FullScope 全量采集范围。
func FullScope() CollectScope {
	return CollectScope{Meta: true, Hosts: true, Users: true, Variables: true, Sessions: true, RGs: true}
}

// Channel 单通道适配器。
type Channel interface {
	Kind() Kind
	Probe(ctx context.Context, t *Target) (ProbeResult, error)
}

// MetaServer TiUP meta.yaml 中的单条 server 定义。
type MetaServer struct {
	Host       string `yaml:"host" json:"host"`
	IP         string `yaml:"ip" json:"ip"`
	Port       int    `yaml:"port" json:"port"`
	StatusPort int    `yaml:"status_port" json:"statusPort"`
	// ClientPort / PeerPort 为 PD 专用字段（真实 meta.yaml 里 PD 没有 port，只有 client_port/peer_port）。
	ClientPort int    `yaml:"client_port" json:"clientPort"`
	PeerPort   int    `yaml:"peer_port" json:"peerPort"`
	DeployDir  string `yaml:"deploy_dir" json:"deployDir"`
	DataDir    string `yaml:"data_dir" json:"dataDir"`
	LogDir     string `yaml:"log_dir" json:"logDir"`
	Arch       string `yaml:"arch" json:"arch"`
	SSHPort    int    `yaml:"ssh_port" json:"sshPort"`
	Name       string `yaml:"name" json:"name"`
	AZ         string `yaml:"az" json:"az"`
	NumaNode   string `yaml:"numa_node" json:"numaNode"`
	// Status 实例状态（up/down/unhealthy/unknown）。
	// 只有 `tiup cluster display` 解析路径会填该字段；meta.yaml 里没有状态信息，
	// 保持空字符串时由 normalizeServers 按历史行为默认 up。
	Status string `yaml:"status" json:"status,omitempty"`
}

// TiUPMeta TiUP 集群元数据（meta.yaml）。
// MetaGlobal meta.yaml 的 global 段（集群级默认配置）。
type MetaGlobal struct {
	User        string `yaml:"user" json:"user"`
	Group       string `yaml:"group" json:"group"`
	SSHPort     int    `yaml:"ssh_port" json:"sshPort"`
	DeployDir   string `yaml:"deploy_dir" json:"deployDir"`
	DataDir     string `yaml:"data_dir" json:"dataDir"`
	Arch        string `yaml:"arch" json:"arch"`
	GrafanaPort int    `yaml:"grafana_port" json:"grafanaPort"`
}

// MetaTopology 真实 meta.yaml 的 topology 段：所有 server 列表与 global 都嵌套在这里。
type MetaTopology struct {
	Global              MetaGlobal   `yaml:"global"`
	PdServers           []MetaServer `yaml:"pd_servers"`
	TidbServers         []MetaServer `yaml:"tidb_servers"`
	TiproxyServers      []MetaServer `yaml:"tiproxy_servers"`
	TikvServers         []MetaServer `yaml:"tikv_servers"`
	TiflashServers      []MetaServer `yaml:"tiflash_servers"`
	CdcServers          []MetaServer `yaml:"cdc_servers"`
	TicdcServers        []MetaServer `yaml:"ticdc_servers"`
	GrafanaServers      []MetaServer `yaml:"grafana_servers"`
	MonitoringServers   []MetaServer `yaml:"monitoring_servers"`
	AlertmanagerServers []MetaServer `yaml:"alertmanager_servers"`
}

// UnmarshalYAML 兼容两种写法：
//
//  1. 真实 TiUP 产出的嵌套结构：server 列表在 topology 段内；
//  2. 扁平结构（测试与历史数据）：server 列表直接在顶层。
//
// 合并规则：顶层字段优先，topology 段只补空，因此两种写法可同时存在。
func (m *TiUPMeta) UnmarshalYAML(node *yaml.Node) error {
	type plain TiUPMeta // 避免递归调用本方法
	var p plain
	if err := node.Decode(&p); err != nil {
		return err
	}
	var wrapper struct {
		Topology MetaTopology `yaml:"topology"`
	}
	if err := node.Decode(&wrapper); err != nil {
		return err
	}
	*m = TiUPMeta(p)

	topo := wrapper.Topology
	if len(m.PdServers) == 0 {
		m.PdServers = topo.PdServers
	}
	if len(m.TidbServers) == 0 {
		m.TidbServers = topo.TidbServers
	}
	if len(m.TiproxyServers) == 0 {
		m.TiproxyServers = topo.TiproxyServers
	}
	if len(m.TikvServers) == 0 {
		m.TikvServers = topo.TikvServers
	}
	if len(m.TiflashServers) == 0 {
		m.TiflashServers = topo.TiflashServers
	}
	if len(m.CdcServers) == 0 {
		m.CdcServers = topo.CdcServers
	}
	if len(m.TicdcServers) == 0 {
		m.TicdcServers = topo.TicdcServers
	}
	if len(m.GrafanaServers) == 0 {
		m.GrafanaServers = topo.GrafanaServers
	}
	if len(m.MonitoringServers) == 0 {
		m.MonitoringServers = topo.MonitoringServers
	}
	if len(m.AlertmanagerServers) == 0 {
		m.AlertmanagerServers = topo.AlertmanagerServers
	}
	if m.Global.User == "" && m.Global.SSHPort == 0 && m.Global.DeployDir == "" {
		m.Global = topo.Global
	}
	return nil
}

type TiUPMeta struct {
	User      string `yaml:"user" json:"user"`
	Group     string `yaml:"group" json:"group"`
	SSHPort   int    `yaml:"ssh_port" json:"sshPort"`
	DeployDir string `yaml:"deploy_dir" json:"deployDir"`
	DataDir   string `yaml:"data_dir" json:"dataDir"`
	// TiDBVersion 是真实 TiUP meta.yaml 的版本字段（形如 v8.5.5）。
	// Version / ClusterVersion 为兼容其他写法的兜底键，读取顺序见 NormalizeMeta。
	TiDBVersion    string     `yaml:"tidb_version" json:"tidbVersion"`
	Version        string     `yaml:"version" json:"version"`
	ClusterVersion string     `yaml:"cluster_version" json:"clusterVersion"`
	Global         MetaGlobal `yaml:"global" json:"global"`
	// Topology 仅用于保留原始嵌套结构（解析时已并入下面的平铺字段）。
	Topology MetaTopology `yaml:"topology" json:"topology"`

	// 以下字段来自 `tiup cluster display` 的头部信息，不参与 meta.yaml 解析
	// （yaml:"-"），由 collector 显式填充。
	DashboardURLs []string `yaml:"-" json:"dashboardUrls,omitempty"`
	GrafanaURLs   []string `yaml:"-" json:"grafanaUrls,omitempty"`
	// Source 元数据来源：display_json / display_text / meta_yaml。
	Source string `yaml:"-" json:"source,omitempty"`

	PdServers           []MetaServer `yaml:"pd_servers" json:"pdServers"`
	TidbServers         []MetaServer `yaml:"tidb_servers" json:"tidbServers"`
	TiproxyServers      []MetaServer `yaml:"tiproxy_servers" json:"tiproxyServers"`
	TikvServers         []MetaServer `yaml:"tikv_servers" json:"tikvServers"`
	TiflashServers      []MetaServer `yaml:"tiflash_servers" json:"tiflashServers"`
	CdcServers          []MetaServer `yaml:"cdc_servers" json:"cdcServers"`
	TicdcServers        []MetaServer `yaml:"ticdc_servers" json:"ticdcServers"`
	GrafanaServers      []MetaServer `yaml:"grafana_servers" json:"grafanaServers"`
	MonitoringServers   []MetaServer `yaml:"monitoring_servers" json:"monitoringServers"`
	AlertmanagerServers []MetaServer `yaml:"alertmanager_servers" json:"alertmanagerServers"`
}

// HostResource SSH 采集到的主机资源信息。
type HostResource struct {
	IP           string  `json:"ip"`
	Hostname     string  `json:"hostname"`
	OSUser       string  `json:"osUser"`
	Arch         string  `json:"arch"`
	CPUCores     int     `json:"cpuCores"`
	MemoryGB     float64 `json:"memoryGb"`
	DiskTotalGB  float64 `json:"diskTotalGb"`
	CPUUsagePct  float64 `json:"cpuUsagePct"`
	MemUsagePct  float64 `json:"memUsagePct"`
	DiskUsagePct float64 `json:"diskUsagePct"`
}

// InstanceSnapshot 归一化后的实例。
type InstanceSnapshot struct {
	Component  string     `json:"component"`
	HostIP     string     `json:"hostIp"`
	Port       int        `json:"port"`
	StatusPort int        `json:"statusPort"`
	Role       string     `json:"role"`
	Status     string     `json:"status"`
	Version    string     `json:"version"`
	AZ         string     `json:"az"`
	DeployDir  string     `json:"deployDir"`
	DataDir    string     `json:"dataDir"`
	StartedAt  *time.Time `json:"startedAt"`
	Raw        string     `json:"-"`
}

// TiDBUserRow mysql.user 一行。
type TiDBUserRow struct {
	UserName        string `json:"userName"`
	Host            string `json:"host"`
	AuthPlugin      string `json:"authPlugin"`
	AccountLocked   string `json:"accountLocked"`
	PasswordExpired string `json:"passwordExpired"`
	ResourceGroup   string `json:"resourceGroup"`
}

// VariableRow SHOW GLOBAL VARIABLES 一行。
type VariableRow struct {
	Name         string `json:"name"`
	Value        string `json:"value"`
	DefaultValue string `json:"defaultValue"`
	Scope        string `json:"scope"`
	IsDynamic    bool   `json:"isDynamic"`
	Description  string `json:"description"`
}

// SessionRow processlist 一行。
type SessionRow struct {
	ID int64 `json:"id"`
	// Instance 会话所在的 TiDB 实例（ip:port），来自 cluster_processlist.INSTANCE；
	// 退回 processlist 时用当前连接地址兜底。
	Instance  string `json:"instance"`
	UserName  string `json:"userName"`
	Host      string `json:"host"`
	DB        string `json:"db"`
	Command   string `json:"command"`
	TimeSec   int    `json:"timeSec"`
	State     string `json:"state"`
	SQLDigest string `json:"sqlDigest"`
}

// RGRow Resource Group 一行。
type RGRow struct {
	Name      string `json:"name"`
	RUPerSec  string `json:"ruPerSec"`
	Priority  string `json:"priority"`
	Burstable bool   `json:"burstable"`
	UserCount int    `json:"userCount"`
	Status    string `json:"status"`
}

// ClusterInfo information_schema.cluster_info 汇总。
type ClusterInfo struct {
	ClusterID     string `json:"clusterId"`
	TiDBVersion   string `json:"tidbVersion"`
	DeployMode    string `json:"deployMode"`
	InstanceCount int    `json:"instanceCount"`
	HostCount     int    `json:"hostCount"`
	SchemaVersion string `json:"schemaVersion"`
}

// Store PD store 信息。
type Store struct {
	Address     string            `json:"address"`
	StateName   string            `json:"stateName"`
	Version     string            `json:"version"`
	Labels      map[string]string `json:"labels"`
	LeaderCount int               `json:"leaderCount"`
	RegionCount int               `json:"regionCount"`
	Capacity    int64             `json:"capacity"`
	Available   int64             `json:"available"`
}

// PDClusterStatus PD /cluster/status 响应。
type PDClusterStatus struct {
	ClusterID       int64  `json:"clusterId"`
	PDVersion       string `json:"pdVersion"`
	StoreCount      int    `json:"storeCount"`
	RegionCount     int    `json:"regionCount"`
	LeaderCount     int    `json:"leaderCount"`
	ReplicationMode string `json:"replicationMode"`
}

// PDMember PD 成员。
type PDMember struct {
	Name       string   `json:"name"`
	MemberID   uint64   `json:"memberId"`
	ClientURLs []string `json:"clientUrls"`
}

// Snapshot 一次采集的完整产出。
//
// ChanErrors 是健康判定的唯一依据：某通道失败即在此登记，绝不由 handler 直接改 clusters.status。
type Snapshot struct {
	Meta         *TiUPMeta
	Hosts        []HostResource
	Instances    []InstanceSnapshot
	Users        []TiDBUserRow
	Variables    []VariableRow
	Sessions     []SessionRow
	RGs          []RGRow
	ClusterInfo  *ClusterInfo
	Capabilities *Capabilities
	// Stores PD 返回的 TiKV store 列表（容量水位巡检与拓扑校准用）。
	Stores []Store
	// Normalized meta.yaml 的归一化结果（实例 / 地址 / 版本 / 架构）。
	Normalized *Normalized
	// DeployModeHint PD 通道给出的部署模式提示（majority / dr_auto_sync）。
	DeployModeHint string
	ChanErrors     map[Kind]string
	FetchedAt      time.Time
	Mock           bool
	// Source 本次元数据的来源（display_json / display_text / meta_yaml），
	// 由 Meta.Source 透传，落库后供界面展示「这批数据是从哪采的」。
	Source string
	// Consistency 交叉校验告警：display 报告的实例状态与平台自有通道
	//（MySQL / PD 的实测连通性）矛盾时记录，用于暴露「假正常 / 假故障」。
	Consistency []string
}

// ChannelFailed 判断指定通道是否失败。
func (s *Snapshot) ChannelFailed(k Kind) bool {
	if s.ChanErrors == nil {
		return false
	}
	_, ok := s.ChanErrors[k]
	return ok
}

// DownInstances 统计状态为 down 的实例数，供 Derive 判定集群是否降级。
func (s *Snapshot) DownInstances() int {
	if s == nil {
		return 0
	}
	n := 0
	for _, i := range s.Instances {
		if i.Status == "down" {
			n++
		}
	}
	return n
}

// SSHChannel SSH 通道。
type SSHChannel interface {
	Channel
	FetchTiUPMeta(ctx context.Context, t *Target) (*TiUPMeta, error)
	FetchHostResource(ctx context.Context, t *Target) ([]HostResource, error)
}

// MySQLChannel MySQL 协议通道。
type MySQLChannel interface {
	Channel
	Capabilities(ctx context.Context, t *Target) (*Capabilities, error)
	FetchClusterInfo(ctx context.Context, t *Target) (*ClusterInfo, error)
	FetchUsers(ctx context.Context, t *Target) ([]TiDBUserRow, error)
	FetchVariables(ctx context.Context, t *Target) ([]VariableRow, error)
	FetchSessions(ctx context.Context, t *Target) ([]SessionRow, error)
	FetchResourceGroups(ctx context.Context, t *Target) ([]RGRow, error)
	// FetchHostHardware 通过 information_schema.cluster_hardware / cluster_load
	// 采集「集群内所有主机」的硬件规格与负载（无需逐台 SSH 登录）。
	// 硬件表可能因权限或版本不可用，调用方必须把它当作可选通道处理。
	FetchHostHardware(ctx context.Context, t *Target) ([]HostResource, error)
	Exec(ctx context.Context, t *Target, st Statement) error
	// QueryMaps 执行只读查询并把结果集扫描为 map 列表（SHOW GRANTS 等场景）。
	QueryMaps(ctx context.Context, t *Target, query string, args ...any) ([]map[string]string, error)
}

// PDChannel PD HTTP API 通道。
type PDChannel interface {
	Channel
	FetchStores(ctx context.Context, t *Target) ([]Store, error)
	FetchClusterStatus(ctx context.Context, t *Target) (*PDClusterStatus, error)
	FetchMembers(ctx context.Context, t *Target) ([]PDMember, error)
	FetchReplicationMode(ctx context.Context, t *Target) (string, error)
}

// Statement 对 TiDB 执行的白名单语句（P1 使用；语句体绝不含用户输入拼接）。
type Statement struct {
	SQL  string
	Args []any
}

// Collector 对外统一门面，Service 只依赖它。
type Collector interface {
	Kind() Kind
	Probe(ctx context.Context, t *Target, scopes []ProbeScope) ([]ProbeResult, error)
	Capabilities(ctx context.Context, t *Target) (*Capabilities, error)
	Collect(ctx context.Context, t *Target, sc CollectScope) (*Snapshot, error)
	// Exec 执行白名单写语句（mock 模式下仅记录不执行）。
	Exec(ctx context.Context, t *Target, st Statement) error
}

// 健康判定结果常量，与 model 层保持字符串一致。
const (
	HealthHealthy  = "healthy"
	HealthDegraded = "degraded"
	HealthError    = "error"
	HealthUnknown  = "unknown"
)

// Derive 由通道错误 + 实例状态 + 连续失败次数推导集群健康状态。
//
// 判定顺序（ARCHITECTURE §6.4，严格按序求值）：
//  1. 关键通道（SSH-Meta 与 MySQL）全失败 → collectStatus=error，失败计数 +1；
//     计数达阈值或从未成功过 → status=error，否则 status=degraded。
//     该分支优先级最高：down 实例不得把 error 洗成 degraded；
//  2. 无通道错误：无 down 实例 → healthy；有 down 实例 → degraded
//     （采集本身成功，故 collectStatus 仍 healthy），失败计数清零；
//  3. 仅 PD 通道失败：无 down 实例 → healthy（PRD R-05：PD 为建议项，不单独降级）；
//     有 down 实例 → degraded（实例异常优先于 R-05 的宽容），collectStatus 仍 healthy；
//  4. 其余（至少 SSH-Meta 或 MySQL 成功，即可用数据仍在）→ degraded，失败计数清零。
//
// 该函数是集群状态的唯一来源，任何 handler 都不得直接写 clusters.status。
func Derive(chanErrors map[Kind]string, prevStreak int, everSucceeded bool, threshold, downInstances int) (status, collectStatus string, streak int) {
	if threshold <= 0 {
		threshold = 3
	}
	metaOK := chanErrors[KindSSH] == ""
	mysqlOK := chanErrors[KindMySQL] == ""
	// 1. 关键通道全失败：error 语义优先，down 实例数不影响结论。
	if !metaOK && !mysqlOK {
		streak = prevStreak + 1
		if streak >= threshold || !everSucceeded {
			return HealthError, HealthError, streak
		}
		return HealthDegraded, HealthError, streak
	}
	// 2. 无通道错误：只看是否真有 down 实例。
	if len(chanErrors) == 0 {
		if downInstances > 0 {
			return HealthDegraded, HealthHealthy, 0
		}
		return HealthHealthy, HealthHealthy, 0
	}
	// 3. 只有 PD 失败：不单独降级（PRD R-05），但存在 down 实例时以降级为准。
	if onlyPDFailed(chanErrors) {
		if downInstances > 0 {
			return HealthDegraded, HealthHealthy, 0
		}
		return HealthHealthy, HealthHealthy, 0
	}
	// 4. 其余情况：至少一个关键通道成功 → 有可用数据，降级。
	return HealthDegraded, HealthDegraded, 0
}

// onlyPDFailed 判断通道错误是否只来自 PD（PD 为 PRD R-05 的建议项）。
func onlyPDFailed(chanErrors map[Kind]string) bool {
	if len(chanErrors) == 0 {
		return false
	}
	for k := range chanErrors {
		if k != KindPD {
			return false
		}
	}
	return true
}
