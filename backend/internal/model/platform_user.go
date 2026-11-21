package model

import "time"

// PlatformUser 平台自身账号（区别于被纳管 TiDB 集群内的用户）。
type PlatformUser struct {
	ID                 int64      `gorm:"column:id;primaryKey;autoIncrement" json:"id"`
	Username           string     `gorm:"column:username;type:text;not null" json:"username"`
	DisplayName        string     `gorm:"column:display_name;type:text;not null" json:"displayName"`
	PasswordHash       string     `gorm:"column:password_hash;type:text;not null" json:"-"`
	Role               string     `gorm:"column:role;type:text;not null;default:viewer" json:"role"`
	Status             string     `gorm:"column:status;type:text;not null;default:active" json:"status"`
	MustChangePassword int        `gorm:"column:must_change_password;not null;default:0" json:"mustChangePassword"`
	FailedLoginCount   int        `gorm:"column:failed_login_count;not null;default:0" json:"-"`
	LockedUntil        *time.Time `gorm:"column:locked_until" json:"lockedUntil"`
	LastLoginAt        *time.Time `gorm:"column:last_login_at" json:"lastLoginAt"`
	LastLoginIP        string     `gorm:"column:last_login_ip;type:text" json:"lastLoginIp"`
	// TokenVersion 用户改密/被禁用时 +1，使已签发 JWT 立即失效（吊销机制，不使用内存黑名单）。
	TokenVersion int       `gorm:"column:token_version;not null;default:0" json:"-"`
	CreatedAt    time.Time `gorm:"column:created_at;not null" json:"createdAt"`
	UpdatedAt    time.Time `gorm:"column:updated_at;not null" json:"updatedAt"`
}

// TableName 固定表名，避免 GORM 复数化歧义。
func (PlatformUser) TableName() string { return "platform_users" }

// IsActive 账号是否可用。
func (u *PlatformUser) IsActive() bool { return u.Status == "active" }
