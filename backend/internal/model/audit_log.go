package model

import "time"

// AuditLog 审计日志；不提供任何更新/删除接口。
type AuditLog struct {
	ID           int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	UserID       *int64    `gorm:"column:user_id" json:"userId"`
	Username     string    `gorm:"column:username;type:text" json:"username"`
	Role         string    `gorm:"column:role;type:text" json:"role"`
	Action       string    `gorm:"column:action;type:text;not null" json:"action"`
	ResourceType string    `gorm:"column:resource_type;type:text" json:"resourceType"`
	ResourceID   string    `gorm:"column:resource_id;type:text" json:"resourceId"`
	ResourceName string    `gorm:"column:resource_name;type:text" json:"resourceName"`
	Result       string    `gorm:"column:result;type:text;not null;default:success" json:"result"`
	Detail       string    `gorm:"column:detail;type:text" json:"detail"`
	IP           string    `gorm:"column:ip;type:text" json:"ip"`
	UserAgent    string    `gorm:"column:user_agent;type:text" json:"userAgent"`
	RequestID    string    `gorm:"column:request_id;type:text" json:"requestId"`
	CreatedAt    time.Time `gorm:"column:created_at;not null" json:"createdAt"`
}

// TableName 固定表名。
func (AuditLog) TableName() string { return "audit_logs" }
