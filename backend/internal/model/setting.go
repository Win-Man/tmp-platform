package model

import "time"

// Setting 系统设置 K/V。
type Setting struct {
	ID        int64     `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Key       string    `gorm:"column:key;type:text;not null" json:"key"`
	Value     string    `gorm:"column:value;type:text" json:"value"`
	UpdatedBy *int64    `gorm:"column:updated_by" json:"updatedBy"`
	UpdatedAt time.Time `gorm:"column:updated_at;not null" json:"updatedAt"`
}

// TableName 固定表名。
func (Setting) TableName() string { return "settings" }
