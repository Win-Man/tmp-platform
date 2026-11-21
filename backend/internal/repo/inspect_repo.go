package repo

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/tmp-platform/backend/internal/model"
)

// InspectRepo 巡检模板、巡检项、运行、明细、报告数据访问。
type InspectRepo struct {
	db *gorm.DB
}

// NewInspectRepo 构造仓库。
func NewInspectRepo(db *gorm.DB) *InspectRepo { return &InspectRepo{db: db} }

// ListTemplates 返回全部巡检模板。
func (r *InspectRepo) ListTemplates(ctx context.Context) ([]model.InspectTemplate, error) {
	var list []model.InspectTemplate
	err := r.db.WithContext(ctx).Order("id ASC").Find(&list).Error
	return list, err
}

// GetTemplate 按 ID 查询模板。
func (r *InspectRepo) GetTemplate(ctx context.Context, id int64) (*model.InspectTemplate, error) {
	var t model.InspectTemplate
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// GetTemplateByCode 按 code 查询模板。
func (r *InspectRepo) GetTemplateByCode(ctx context.Context, code string) (*model.InspectTemplate, error) {
	var t model.InspectTemplate
	err := r.db.WithContext(ctx).Where("code = ?", code).First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// DefaultTemplate 返回第一个启用的内置模板（未显式指定 templateId 时使用）。
func (r *InspectRepo) DefaultTemplate(ctx context.Context) (*model.InspectTemplate, error) {
	var t model.InspectTemplate
	err := r.db.WithContext(ctx).Where("enabled = 1").Order("id ASC").First(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// CreateTemplate 创建模板。
func (r *InspectRepo) CreateTemplate(ctx context.Context, t *model.InspectTemplate) error {
	return r.db.WithContext(ctx).Create(t).Error
}

// UpsertItems 幂等灌入巡检项：按 (template_id, code) 判断存在则跳过。
func (r *InspectRepo) UpsertItems(ctx context.Context, items []model.InspectItem) (inserted int, err error) {
	err = Tx(ctx, r.db, func(tx *gorm.DB) error {
		now := time.Now()
		for i := range items {
			it := &items[i]
			var count int64
			if cerr := tx.Model(&model.InspectItem{}).
				Where("template_id = ? AND code = ?", it.TemplateID, it.Code).Count(&count).Error; cerr != nil {
				return cerr
			}
			if count > 0 {
				continue
			}
			it.CreatedAt = now
			it.UpdatedAt = now
			if cerr := tx.Create(it).Error; cerr != nil {
				return cerr
			}
			inserted++
		}
		// 同步模板的 item_count。
		var total int64
		if cerr := tx.Model(&model.InspectItem{}).Where("template_id = ?", items[0].TemplateID).Count(&total).Error; cerr != nil {
			return cerr
		}
		return tx.Model(&model.InspectTemplate{}).Where("id = ?", items[0].TemplateID).
			Updates(map[string]any{"item_count": total, "updated_at": now}).Error
	})
	return inserted, err
}

// UpdateTemplateItemCount 重新统计模板项数。
func (r *InspectRepo) UpdateTemplateItemCount(ctx context.Context, templateID int64) error {
	var n int64
	if err := r.db.WithContext(ctx).Model(&model.InspectItem{}).Where("template_id = ?", templateID).Count(&n).Error; err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&model.InspectTemplate{}).Where("id = ?", templateID).
		Updates(map[string]any{"item_count": n, "updated_at": time.Now()}).Error
}

// ItemsByTemplate 返回模板下全部启用的巡检项（按 sort_order）。
func (r *InspectRepo) ItemsByTemplate(ctx context.Context, templateID int64) ([]model.InspectItem, error) {
	var list []model.InspectItem
	err := r.db.WithContext(ctx).Where("template_id = ? AND enabled = 1", templateID).
		Order("sort_order ASC, id ASC").Find(&list).Error
	return list, err
}

// ItemsFiltered 按模板与分类查询巡检项。
func (r *InspectRepo) ItemsFiltered(ctx context.Context, templateID int64, category string) ([]model.InspectItem, error) {
	q := r.db.WithContext(ctx).Where("template_id = ?", templateID)
	if category != "" {
		q = q.Where("category = ?", category)
	}
	var list []model.InspectItem
	err := q.Order("sort_order ASC, id ASC").Find(&list).Error
	return list, err
}

// CreateRun 创建巡检运行。
func (r *InspectRepo) CreateRun(ctx context.Context, run *model.InspectRun) error {
	return r.db.WithContext(ctx).Create(run).Error
}

// GetRun 按 ID 查询运行。
func (r *InspectRepo) GetRun(ctx context.Context, id int64) (*model.InspectRun, error) {
	var run model.InspectRun
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

// UpdateRun 更新运行。
func (r *InspectRepo) UpdateRun(ctx context.Context, id int64, fields map[string]any) error {
	return r.db.WithContext(ctx).Model(&model.InspectRun{}).Where("id = ?", id).Updates(fields).Error
}

// RunFilter 巡检运行筛选。
type RunFilter struct {
	ClusterID int64
	Page      Page
}

// ListRuns 分页查询运行记录。
func (r *InspectRepo) ListRuns(ctx context.Context, f RunFilter) ([]model.InspectRun, int64, error) {
	f.Page.Normalize()
	q := r.db.WithContext(ctx).Model(&model.InspectRun{})
	if f.ClusterID > 0 {
		q = q.Where("cluster_id = ?", f.ClusterID)
	}
	q = q.Order("id DESC")
	return ApplyPagination[model.InspectRun](q, f.Page, &model.InspectRun{})
}

// LatestRun 最近一次运行（总览巡检摘要用）。
func (r *InspectRepo) LatestRun(ctx context.Context, clusterID int64) (*model.InspectRun, error) {
	q := r.db.WithContext(ctx).Model(&model.InspectRun{})
	if clusterID > 0 {
		q = q.Where("cluster_id = ?", clusterID)
	}
	var run model.InspectRun
	err := q.Order("id DESC").First(&run).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &run, nil
}

// CreateResults 批量写入巡检明细。
func (r *InspectRepo) CreateResults(ctx context.Context, results []model.InspectResult) error {
	if len(results) == 0 {
		return nil
	}
	now := time.Now()
	for i := range results {
		results[i].CreatedAt = now
	}
	return r.db.WithContext(ctx).CreateInBatches(results, 100).Error
}

// ResultsByRun 查询某次运行的全部明细。
func (r *InspectRepo) ResultsByRun(ctx context.Context, runID int64) ([]model.InspectResult, error) {
	var list []model.InspectResult
	err := r.db.WithContext(ctx).Where("run_id = ?", runID).Order("id ASC").Find(&list).Error
	return list, err
}

// CreateReport 创建报告。
func (r *InspectRepo) CreateReport(ctx context.Context, rp *model.InspectReport) error {
	return r.db.WithContext(ctx).Create(rp).Error
}

// UpdateReport 更新报告。
func (r *InspectRepo) UpdateReport(ctx context.Context, id int64, fields map[string]any) error {
	return r.db.WithContext(ctx).Model(&model.InspectReport{}).Where("id = ?", id).Updates(fields).Error
}

// GetReport 按 ID 查询报告。
func (r *InspectRepo) GetReport(ctx context.Context, id int64) (*model.InspectReport, error) {
	var rp model.InspectReport
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&rp).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rp, nil
}

// GetReportByRun 按运行 ID 查询报告（1:1）。
func (r *InspectRepo) GetReportByRun(ctx context.Context, runID int64) (*model.InspectReport, error) {
	var rp model.InspectReport
	err := r.db.WithContext(ctx).Where("run_id = ?", runID).First(&rp).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &rp, nil
}

// ReportFilter 报告列表筛选。
type ReportFilter struct {
	ClusterID int64
	Keyword   string
	From      *time.Time
	To        *time.Time
	Page      Page
}

// ListReports 分页查询报告（时间倒序）。
func (r *InspectRepo) ListReports(ctx context.Context, f ReportFilter) ([]model.InspectReport, int64, error) {
	f.Page.Normalize()
	q := r.db.WithContext(ctx).Model(&model.InspectReport{})
	if f.ClusterID > 0 {
		q = q.Where("cluster_id = ?", f.ClusterID)
	}
	if f.Keyword != "" {
		like := likePattern(f.Keyword)
		q = q.Where("cluster_name LIKE ? ESCAPE '\\' OR report_no LIKE ? ESCAPE '\\'", like, like)
	}
	if f.From != nil {
		q = q.Where("created_at >= ?", *f.From)
	}
	if f.To != nil {
		q = q.Where("created_at <= ?", *f.To)
	}
	q = q.Order("id DESC")
	return ApplyPagination[model.InspectReport](q, f.Page, &model.InspectReport{})
}

// MaxRunSeq 返回某前缀某天已使用的最大序号。
func (r *InspectRepo) MaxRunSeq(ctx context.Context, prefix, day string) (int, error) {
	var seq sql.NullInt64
	like := prefix + day + "-%"
	// 业务号格式 PREFIX(2) + YYYYMMDD(8) + "-" + 0001，序号从第 12 位开始。
	if err := r.db.WithContext(ctx).Raw(
		"SELECT MAX(CAST(SUBSTR(run_no, 12) AS INTEGER)) FROM inspect_runs WHERE run_no LIKE ?", like).
		Scan(&seq).Error; err != nil {
		return 0, err
	}
	if !seq.Valid {
		return 0, nil
	}
	return int(seq.Int64), nil
}

// MaxReportSeq 返回报告号在某天的最大序号。
func (r *InspectRepo) MaxReportSeq(ctx context.Context, prefix, day string) (int, error) {
	var seq sql.NullInt64
	like := prefix + day + "-%"
	if err := r.db.WithContext(ctx).Raw(
		"SELECT MAX(CAST(SUBSTR(report_no, 12) AS INTEGER)) FROM inspect_reports WHERE report_no LIKE ?", like).
		Scan(&seq).Error; err != nil {
		return 0, err
	}
	if !seq.Valid {
		return 0, nil
	}
	return int(seq.Int64), nil
}

// CountReportByRun 判断运行是否已生成过报告。
func (r *InspectRepo) CountReportByRun(ctx context.Context, runID int64) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.InspectReport{}).Where("run_id = ?", runID).Count(&n).Error
	return n, err
}

// Count 统计报告总数。
func (r *InspectRepo) Count(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.InspectReport{}).Count(&n).Error
	return n, err
}
