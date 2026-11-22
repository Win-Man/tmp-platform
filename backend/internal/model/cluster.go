package model

import (
	"time"

	"gorm.io/gorm"
)

// 集群状态枚举。
const (
	ClusterStatusHealthy  = "healthy"
	ClusterStatusDegraded = "degraded"
	ClusterStatusError    = "error"
	ClusterStatusUnknown  = "unknown"
)

// 部署模式枚举。
const (
	DeployModeMajority   = "majority"
	DeployModeDRAutoSync = "dr_auto_sync"
	DeployModeUnknown    = "unknown"
)

// Cluster 集群聚合根。
//
// 大小写不敏感唯一：不使用 SQLite 的 COLLATE NOCASE，而是用 name + name_lower 双列。
//
// 唯一键是「同一中控机内唯一」而非全局唯一：业务唯一键为 (tiup_host, name_lower)，
// 对应唯一索引 uk_clusters_host_name（db/migrate.go: ensureClusterIdentityIndex）。
// 原因：同一批机器上可以部署多套 TiDB 集群，每套集群各有自己的中控机（tiup_host），
// 不同中控机上出现同名集群（如都叫 tidb-prod）是完全合法的部署形态；
// 只有「同一台中控机上的同名集群」才是重复纳管。
// 移除纳管时把 name_lower 改写为 "<name>#removed#<id>" 以释放唯一键（见 service.ClusterService.Delete）。
type Cluster struct {
	ID        int64  `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Name      string `gorm:"column:name;type:text;not null" json:"name"`
	NameLower string `gorm:"column:name_lower;type:text;not null" json:"-"`
	Draft     int    `gorm:"column:draft;not null;default:0" json:"draft"`
	// IsDemo 标识该集群由演示模式采集（内置样例数据，未连接真实集群），用于界面明确区分。
	IsDemo            int        `gorm:"column:is_demo;not null;default:0" json:"isDemo"`
	TiDBVersion       string     `gorm:"column:tidb_version;type:text" json:"tidbVersion"`
	DeployMode        string     `gorm:"column:deploy_mode;type:text;not null;default:unknown" json:"deployMode"`
	CPUArch           string     `gorm:"column:cpu_arch;type:text" json:"cpuArch"`
	ComponentCount    int        `gorm:"column:component_count;not null;default:0" json:"componentCount"`
	HostCount         int        `gorm:"column:host_count;not null;default:0" json:"hostCount"`
	InstanceCount     int        `gorm:"column:instance_count;not null;default:0" json:"instanceCount"`
	TiUPHost          string     `gorm:"column:tiup_host;type:text;not null" json:"tiupHost"`
	TiUPPort          int        `gorm:"column:tiup_port;not null;default:22" json:"tiupPort"`
	TiUPUser          string     `gorm:"column:tiup_user;type:text;not null" json:"tiupUser"`
	TiUPDir           string     `gorm:"column:tiup_dir;type:text" json:"tiupDir"`
	DBHost            string     `gorm:"column:db_host;type:text" json:"dbHost"`
	DBPort            int        `gorm:"column:db_port;not null;default:4000" json:"dbPort"`
	DBUser            string     `gorm:"column:db_user;type:text" json:"dbUser"`
	PDEndpoint        string     `gorm:"column:pd_endpoint;type:text" json:"pdEndpoint"`
	DashboardURLs     string     `gorm:"column:dashboard_urls;type:text" json:"dashboardUrls"`
	PrometheusURLs    string     `gorm:"column:prometheus_urls;type:text" json:"prometheusUrls"`
	GrafanaURLs       string     `gorm:"column:grafana_urls;type:text" json:"grafanaUrls"`
	AlertmanagerURLs  string     `gorm:"column:alertmanager_urls;type:text" json:"alertmanagerUrls"`
	Status            string     `gorm:"column:status;type:text;not null;default:unknown" json:"status"`
	CollectStatus     string     `gorm:"column:collect_status;type:text;not null;default:unknown" json:"collectStatus"`
	CollectFailStreak int        `gorm:"column:collect_fail_streak;not null;default:0" json:"collectFailStreak"`
	Collecting        int        `gorm:"column:collecting;not null;default:0" json:"collecting"`
	LastCollectAt     *time.Time `gorm:"column:last_collect_at" json:"lastCollectAt"`
	LastSuccessAt     *time.Time `gorm:"column:last_success_at" json:"lastSuccessAt"`
	LastCollectError  string     `gorm:"column:last_collect_error;type:text" json:"lastCollectError"`
	// CollectSource 最近一次成功采集的元数据来源（display_json / display_text / meta_yaml），
	// 用于界面标注「这批数据的可信来源」，避免把降级数据误当实测。
	CollectSource string `gorm:"column:collect_source;type:text" json:"collectSource"`
	// CollectWarnings 最近一次采集的交叉校验告警（JSON 数组）：
	// TiUP 报告的实例状态与平台实测连通性矛盾时逐条记录。
	CollectWarnings string         `gorm:"column:collect_warnings;type:text" json:"collectWarnings"`
	Capabilities    string         `gorm:"column:capabilities;type:text" json:"capabilities"`
	FetchedAt       *time.Time     `gorm:"column:fetched_at" json:"fetchedAt"`
	CreatedBy       *int64         `gorm:"column:created_by" json:"createdBy"`
	CreatedAt       time.Time      `gorm:"column:created_at;not null" json:"createdAt"`
	UpdatedAt       time.Time      `gorm:"column:updated_at;not null" json:"updatedAt"`
	DeletedAt       gorm.DeletedAt `gorm:"column:deleted_at;index" json:"-"`
}

// TableName 固定表名。
func (Cluster) TableName() string { return "clusters" }

// ClusterCredential 集群凭据，1 集群 1 份，字段全部为 AES-256-GCM 密文。
type ClusterCredential struct {
	ID             int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ClusterID      int64     `gorm:"column:cluster_id;not null" json:"clusterId"`
	SSHPasswordEnc string    `gorm:"column:ssh_password_enc;type:text" json:"-"`
	SSHKeyEnc      string    `gorm:"column:ssh_key_enc;type:text" json:"-"`
	DBPasswordEnc  string    `gorm:"column:db_password_enc;type:text" json:"-"`
	KeyVersion     int       `gorm:"column:key_version;not null;default:1" json:"keyVersion"`
	UpdatedBy      *int64    `gorm:"column:updated_by" json:"updatedBy"`
	CreatedAt      time.Time `gorm:"column:created_at;not null" json:"createdAt"`
	UpdatedAt      time.Time `gorm:"column:updated_at;not null" json:"updatedAt"`
}

// TableName 固定表名。
func (ClusterCredential) TableName() string { return "cluster_credentials" }
