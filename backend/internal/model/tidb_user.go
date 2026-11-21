package model

import "time"

// TiDBUser 来自 mysql.user 的用户快照。
type TiDBUser struct {
	ID              int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ClusterID       int64     `gorm:"column:cluster_id;not null" json:"clusterId"`
	UserName        string    `gorm:"column:user_name;type:text;not null" json:"userName"`
	Host            string    `gorm:"column:host;type:text;not null" json:"host"`
	AuthPlugin      string    `gorm:"column:auth_plugin;type:text" json:"authPlugin"`
	AccountLocked   string    `gorm:"column:account_locked;type:text" json:"accountLocked"`
	PasswordExpired string    `gorm:"column:password_expired;type:text" json:"passwordExpired"`
	ResourceGroup   string    `gorm:"column:resource_group;type:text" json:"resourceGroup"`
	FetchedAt       time.Time `gorm:"column:fetched_at;not null" json:"fetchedAt"`
	CreatedAt       time.Time `gorm:"column:created_at;not null" json:"createdAt"`
}

// TableName 固定表名。
func (TiDBUser) TableName() string { return "tidb_users" }
