package model

import "time"

// ResourceGroup TiDB v7.5+ 的 Resource Group 快照。
type ResourceGroup struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ClusterID int64     `gorm:"column:cluster_id;not null" json:"clusterId"`
	Name      string    `gorm:"column:name;type:text;not null" json:"name"`
	RUPerSec  string    `gorm:"column:ru_per_sec;type:text" json:"ruPerSec"`
	Priority  string    `gorm:"column:priority;type:text" json:"priority"`
	Burstable int       `gorm:"column:burstable;not null;default:0" json:"burstable"`
	UserCount int       `gorm:"column:user_count;not null;default:0" json:"userCount"`
	Status    string    `gorm:"column:status;type:text" json:"status"`
	FetchedAt time.Time `gorm:"column:fetched_at;not null" json:"fetchedAt"`
	CreatedAt time.Time `gorm:"column:created_at;not null" json:"createdAt"`
}

// TableName 固定表名。
func (ResourceGroup) TableName() string { return "resource_groups" }
