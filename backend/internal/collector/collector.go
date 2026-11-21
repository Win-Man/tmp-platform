// Package collector 定义采集适配层：三通道（SSH / MySQL / PD）与 mock 数据源的统一门面。
package collector

import (
	"context"
	"time"
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
	DeployDir  string `yaml:"deploy_dir" json:"deployDir"`
	DataDir    string `yaml:"data_dir" json:"dataDir"`
	LogDir     string `yaml:"log_dir" json:"logDir"`
	Arch       string `yaml:"arch" json:"arch"`
	SSHPort    int    `yaml:"ssh_port" json:"sshPort"`
	Name       string `yaml:"name" json:"name"`
	AZ         string `yaml:"az" json:"az"`
	NumaNode   string `yaml:"numa_node" json:"numaNode"`
}

// TiUPMeta TiUP 集群元数据（meta.yaml）。
type TiUPMeta struct {
	User           string `yaml:"user" json:"user"`
	Group          string `yaml:"group" json:"group"`
	SSHPort        int    `yaml:"ssh_port" json:"sshPort"`
	DeployDir      string `yaml:"deploy_dir" json:"deployDir"`
	DataDir        string `yaml:"data_dir" json:"dataDir"`
	Version        string `yaml:"version" json:"version"`
	ClusterVersion string `yaml:"cluster_version" json:"clusterVersion"`
	Global         struct {
		User        string `yaml:"user" json:"user"`
		Group       string `yaml:"group" json:"group"`
		SSHPort     int    `yaml:"ssh_port" json:"sshPort"`
		DeployDir   string `yaml:"deploy_dir" json:"deployDir"`
		DataDir     string `yaml:"data_dir" json:"dataDir"`
		Arch        string `yaml:"arch" json:"arch"`
		GrafanaPort int    `yaml:"grafana_port" json:"grafanaPort"`
	} `yaml:"global" json:"global"`

	PdServers           []MetaServer `yaml:"pd_servers" json:"pdServers"`
	TidbServers         []MetaServer `yaml:"tidb_servers" json:"tidbServers"`
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
	ID        int64  `json:"id"`
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
}

// ChannelFailed 判断指定通道是否失败。
func (s *Snapshot) ChannelFailed(k Kind) bool {
	if s.ChanErrors == nil {
		return false
	}
	_, ok := s.ChanErrors[k]
	return ok
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

// Derive 由通道错误 + 连续失败次数推导集群健康状态。
//
// 判定顺序（ARCHITECTURE §6.4）：
//  1. 无通道错误 → healthy，失败计数清零；
//  2. 仅 PD 通道失败 → 仍视为 healthy（PRD R-05：PD 为建议项，不单独降级）；
//  3. 有错误但至少 SSH-Meta 或 MySQL 成功 → degraded，失败计数清零；
//  4. 关键通道全失败 → collectStatus=error，失败计数 +1；
//     若计数达到阈值或从未成功过 → status=error，否则 status=degraded。
//
// 该函数是集群状态的唯一来源，任何 handler 都不得直接写 clusters.status。
func Derive(chanErrors map[Kind]string, prevStreak int, everSucceeded bool, threshold int) (status, collectStatus string, streak int) {
	if threshold <= 0 {
		threshold = 3
	}
	if len(chanErrors) == 0 {
		return HealthHealthy, HealthHealthy, 0
	}
	// 只有 PD 失败：不降级。
	onlyPD := true
	for k := range chanErrors {
		if k != KindPD {
			onlyPD = false
			break
		}
	}
	if onlyPD {
		return HealthHealthy, HealthHealthy, 0
	}
	// Meta 或 MySQL 任一成功即视为有可用数据 → 降级。
	metaOK := chanErrors[KindSSH] == ""
	mysqlOK := chanErrors[KindMySQL] == ""
	if metaOK || mysqlOK {
		return HealthDegraded, HealthDegraded, 0
	}
	streak = prevStreak + 1
	if streak >= threshold || !everSucceeded {
		return HealthError, HealthError, streak
	}
	return HealthDegraded, HealthError, streak
}
