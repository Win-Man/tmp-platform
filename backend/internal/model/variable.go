package model

import "time"

// Variable SHOW GLOBAL VARIABLES 快照。
type Variable struct {
	ID           int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ClusterID    int64     `gorm:"column:cluster_id;not null" json:"clusterId"`
	Name         string    `gorm:"column:name;type:text;not null" json:"name"`
	Value        string    `gorm:"column:value;type:text" json:"value"`
	DefaultValue string    `gorm:"column:default_value;type:text" json:"defaultValue"`
	Scope        string    `gorm:"column:scope;type:text" json:"scope"`
	IsDynamic    int       `gorm:"column:is_dynamic;not null;default:1" json:"isDynamic"`
	Description  string    `gorm:"column:description;type:text" json:"description"`
	Differs      int       `gorm:"column:differs;not null;default:0" json:"differs"`
	FetchedAt    time.Time `gorm:"column:fetched_at;not null" json:"fetchedAt"`
	CreatedAt    time.Time `gorm:"column:created_at;not null" json:"createdAt"`
}

// TableName 固定表名。
func (Variable) TableName() string { return "variables" }
