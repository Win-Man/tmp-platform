package model

import "time"

// 主机 SSH 检测状态。
const (
	SSHStatusOK      = "ok"
	SSHStatusFail    = "fail"
	SSHStatusUnknown = "unknown"
)

// Host 全局主机（跨集群共享同一 IP）。
type Host struct {
	ID                int64      `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	IP                string     `gorm:"column:ip;type:text;not null" json:"ip"`
	Hostname          string     `gorm:"column:hostname;type:text" json:"hostname"`
	OSUser            string     `gorm:"column:os_user;type:text" json:"osUser"`
	CPUArch           string     `gorm:"column:cpu_arch;type:text" json:"cpuArch"`
	CPUCores          int        `gorm:"column:cpu_cores" json:"cpuCores"`
	MemoryGB          float64    `gorm:"column:memory_gb" json:"memoryGb"`
	DiskTotalGB       float64    `gorm:"column:disk_total_gb" json:"diskTotalGb"`
	CPUUsagePct       float64    `gorm:"column:cpu_usage_pct" json:"cpuUsagePct"`
	MemUsagePct       float64    `gorm:"column:mem_usage_pct" json:"memUsagePct"`
	DiskUsagePct      float64    `gorm:"column:disk_usage_pct" json:"diskUsagePct"`
	SSHStatus         string     `gorm:"column:ssh_status;type:text;not null;default:unknown" json:"sshStatus"`
	SSHCheckedAt      *time.Time `gorm:"column:ssh_checked_at" json:"sshCheckedAt"`
	SSHError          string     `gorm:"column:ssh_error;type:text" json:"sshError"`
	ResourceFetchedAt *time.Time `gorm:"column:resource_fetched_at" json:"resourceFetchedAt"`
	CreatedAt         time.Time  `gorm:"column:created_at;not null" json:"createdAt"`
	UpdatedAt         time.Time  `gorm:"column:updated_at;not null" json:"updatedAt"`
}

// TableName 固定表名。
func (Host) TableName() string { return "hosts" }
