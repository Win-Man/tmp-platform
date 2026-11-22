package model

import "time"

// Session processlist 会话快照。
type Session struct {
	ID        int64 `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	ClusterID int64 `gorm:"column:cluster_id;not null" json:"clusterId"`
	SessionID int64 `gorm:"column:session_id;not null" json:"sessionId"`
	// Instance 会话所在 TiDB 实例（ip:port）。集群级 cluster_processlist 下
	// session_id 仅实例内唯一，必须与 instance 一起才能定位一条会话。
	Instance string `gorm:"column:instance;type:text" json:"instance"`
	UserName string `gorm:"column:user_name;type:text" json:"userName"`
	Host     string `gorm:"column:host;type:text" json:"host"`
	DB       string `gorm:"column:db;type:text" json:"db"`
	Command  string `gorm:"column:command;type:text" json:"command"`
	TimeSec  int    `gorm:"column:time_sec;not null;default:0" json:"timeSec"`
	State    string `gorm:"column:state;type:text" json:"state"`
	// SQLText 正在执行的 SQL 原文（processlist.INFO），空闲会话为空。
	SQLText string `gorm:"column:sql_text;type:text" json:"sqlText"`
	// SQLDigest SQL 指纹（processlist.DIGEST），同一条语句的归一化结果。
	SQLDigest string    `gorm:"column:sql_digest;type:text" json:"sqlDigest"`
	FetchedAt time.Time `gorm:"column:fetched_at;not null" json:"fetchedAt"`
	CreatedAt time.Time `gorm:"column:created_at;not null" json:"createdAt"`
}

// TableName 固定表名。
func (Session) TableName() string { return "sessions" }
