package repo

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/tmp-platform/backend/internal/model"
)

// 内置 settings 键（ARCHITECTURE §4.1）。
const (
	SettingCollectIntervalSec = "collect.interval_sec"
	SettingCollectTimeoutSec  = "collect.timeout_sec"
	SettingCollectConcurrency = "collect.concurrency"
	SettingCollectRetry       = "collect.retry"
	SettingJWTTokenTTLHours   = "auth.jwt_ttl_hours"
	SettingAuthIdleTimeoutMin = "auth.idle_timeout_min"
	SettingModeMock           = "mode.mock"
	SettingRetentionJobsDays  = "retention.jobs_days"
	SettingRetentionAuditDays = "retention.audit_days"
	SettingThresholdCPUPct    = "threshold.cpu_pct"
	SettingThresholdDiskPct   = "threshold.disk_pct"
	SettingClusterErrorStreak = "cluster.error_streak"
	SettingSetupCompleted     = "setup.completed"
)

// DefaultSettings 内置默认值。
var DefaultSettings = map[string]string{
	SettingCollectIntervalSec: "300",
	SettingCollectTimeoutSec:  "10",
	SettingCollectConcurrency: "4",
	SettingCollectRetry:       "1",
	SettingJWTTokenTTLHours:   "8",
	SettingAuthIdleTimeoutMin: "30",
	SettingModeMock:           "false",
	SettingRetentionJobsDays:  "30",
	SettingRetentionAuditDays: "180",
	SettingThresholdCPUPct:    "80",
	SettingThresholdDiskPct:   "85",
	SettingClusterErrorStreak: "3",
	SettingSetupCompleted:     "false",
}

// SettingRepo 系统设置数据访问。
type SettingRepo struct {
	db *gorm.DB
}

// NewSettingRepo 构造仓库。
func NewSettingRepo(db *gorm.DB) *SettingRepo { return &SettingRepo{db: db} }

// All 返回全部设置的 key→value 映射。
func (r *SettingRepo) All(ctx context.Context) (map[string]string, error) {
	var rows []model.Setting
	if err := r.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, s := range rows {
		out[s.Key] = s.Value
	}
	return out, nil
}

// Get 读取单个设置值，不存在时返回默认值。
func (r *SettingRepo) Get(ctx context.Context, key string) (string, error) {
	var s model.Setting
	err := r.db.WithContext(ctx).Where("key = ?", key).First(&s).Error
	if err == gorm.ErrRecordNotFound {
		if v, ok := DefaultSettings[key]; ok {
			return v, nil
		}
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return s.Value, nil
}

// Set 写入单个设置（upsert 语义）。
func (r *SettingRepo) Set(ctx context.Context, key, value string, by *int64) error {
	var existing model.Setting
	err := r.db.WithContext(ctx).Where("key = ?", key).First(&existing).Error
	now := time.Now()
	if err == gorm.ErrRecordNotFound {
		return r.db.WithContext(ctx).Create(&model.Setting{Key: key, Value: value, UpdatedBy: by, UpdatedAt: now}).Error
	}
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&model.Setting{}).Where("key = ?", key).
		Updates(map[string]any{"value": value, "updated_by": by, "updated_at": now}).Error
}

// SetMany 批量写入（同一事务）。
func (r *SettingRepo) SetMany(ctx context.Context, kv map[string]string, by *int64) error {
	return Tx(ctx, r.db, func(tx *gorm.DB) error {
		now := time.Now()
		for k, v := range kv {
			var existing model.Setting
			err := tx.Where("key = ?", k).First(&existing).Error
			switch {
			case err == gorm.ErrRecordNotFound:
				if cerr := tx.Create(&model.Setting{Key: k, Value: v, UpdatedBy: by, UpdatedAt: now}).Error; cerr != nil {
					return cerr
				}
			case err != nil:
				return err
			default:
				if uerr := tx.Model(&model.Setting{}).Where("key = ?", k).
					Updates(map[string]any{"value": v, "updated_by": by, "updated_at": now}).Error; uerr != nil {
					return uerr
				}
			}
		}
		return nil
	})
}

// SeedDefaults 幂等灌入默认设置：仅插入缺失的键，不覆盖已有值。
func (r *SettingRepo) SeedDefaults(ctx context.Context) error {
	return Tx(ctx, r.db, func(tx *gorm.DB) error {
		now := time.Now()
		for k, v := range DefaultSettings {
			var count int64
			if err := tx.Model(&model.Setting{}).Where("key = ?", k).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				continue
			}
			if err := tx.Create(&model.Setting{Key: k, Value: v, UpdatedAt: now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}
