package model

import "time"

// 实例状态枚举。
const (
	InstanceStatusUp        = "up"
	InstanceStatusDown      = "down"
	InstanceStatusUnhealthy = "unhealthy"
	InstanceStatusUnknown   = "unknown"
)

// Instance 集群拓扑最小单元；每次采集全量替换。
type Instance struct {
	ID         int64      `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ClusterID  int64      `gorm:"column:cluster_id;not null" json:"clusterId"`
	HostID     *int64     `gorm:"column:host_id" json:"hostId"`
	HostIP     string     `gorm:"column:host_ip;type:text;not null" json:"hostIp"`
	Component  string     `gorm:"column:component;type:text;not null" json:"component"`
	Role       string     `gorm:"column:role;type:text" json:"role"`
	Port       int        `gorm:"column:port;not null" json:"port"`
	StatusPort int        `gorm:"column:status_port" json:"statusPort"`
	Status     string     `gorm:"column:status;type:text;not null;default:unknown" json:"status"`
	Version    string     `gorm:"column:version;type:text" json:"version"`
	StartedAt  *time.Time `gorm:"column:started_at" json:"startedAt"`
	AZ         string     `gorm:"column:az;type:text" json:"az"`
	DeployDir  string     `gorm:"column:deploy_dir;type:text" json:"deployDir"`
	DataDir    string     `gorm:"column:data_dir;type:text" json:"dataDir"`
	Raw        string     `gorm:"column:raw;type:text" json:"-"`
	// RawHostIP 采集到的原始地址，仅在地址映射生效且改写成功时填写（不落库）。
	// 便于界面标注「显示的是可达地址，原始地址是内网平面」。
	RawHostIP string    `gorm:"-" json:"rawHostIp,omitempty"`
	FetchedAt time.Time `gorm:"column:fetched_at;not null" json:"fetchedAt"`
	CreatedAt time.Time `gorm:"column:created_at;not null" json:"createdAt"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null" json:"updatedAt"`
}

// TableName 固定表名。
func (Instance) TableName() string { return "instances" }
