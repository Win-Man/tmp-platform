package db

import (
	"context"
	"fmt"
	"time"

	"gorm.io/gorm"

	"github.com/tmp-platform/backend/internal/inspect"
	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/repo"
)

// Seed 幂等灌入内置数据：默认 settings、内置巡检模板与巡检项。
//
// 重复启动不会重复插入（逐键/逐项判断存在性）。
func Seed(ctx context.Context, gdb *gorm.DB) error {
	if err := seedSettings(ctx, gdb); err != nil {
		return err
	}
	return seedInspectTemplate(ctx, gdb)
}

// seedSettings 灌入缺失的默认设置键。
func seedSettings(ctx context.Context, gdb *gorm.DB) error {
	return gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		for key, value := range repo.DefaultSettings {
			var count int64
			if err := tx.Model(&model.Setting{}).Where("key = ?", key).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				continue
			}
			if err := tx.Create(&model.Setting{Key: key, Value: value, UpdatedAt: now}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// seedInspectTemplate 灌入内置巡检模板与其巡检项。
func seedInspectTemplate(ctx context.Context, gdb *gorm.DB) error {
	tpl, err := inspect.LoadDefaultTemplate()
	if err != nil {
		return err
	}
	return gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		var template model.InspectTemplate
		qerr := tx.Where("code = ?", tpl.Code).First(&template).Error
		switch {
		case qerr == gorm.ErrRecordNotFound:
			template = model.InspectTemplate{
				Code:        tpl.Code,
				Name:        tpl.Name,
				Description: tpl.Description,
				Builtin:     1,
				Enabled:     1,
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			if err := tx.Create(&template).Error; err != nil {
				return fmt.Errorf("创建内置巡检模板失败: %w", err)
			}
		case qerr != nil:
			return qerr
		}

		for _, it := range tpl.Items {
			var count int64
			if err := tx.Model(&model.InspectItem{}).
				Where("template_id = ? AND code = ?", template.ID, it.Code).Count(&count).Error; err != nil {
				return err
			}
			if count > 0 {
				continue
			}
			item := model.InspectItem{
				TemplateID:  template.ID,
				Code:        it.Code,
				Name:        it.Name,
				Category:    it.Category,
				CheckMethod: it.CheckMethod,
				Severity:    it.Severity,
				Expr:        it.Expr,
				Expect:      it.Expect,
				Suggestion:  it.Suggestion,
				DocURL:      it.DocURL,
				Applies:     it.Applies,
				SortOrder:   it.SortOrder,
				Enabled:     1,
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			if err := tx.Create(&item).Error; err != nil {
				return fmt.Errorf("创建巡检项 %s 失败: %w", it.Code, err)
			}
		}

		var total int64
		if err := tx.Model(&model.InspectItem{}).Where("template_id = ?", template.ID).Count(&total).Error; err != nil {
			return err
		}
		return tx.Model(&model.InspectTemplate{}).Where("id = ?", template.ID).
			Updates(map[string]any{"item_count": total, "updated_at": now}).Error
	})
}
