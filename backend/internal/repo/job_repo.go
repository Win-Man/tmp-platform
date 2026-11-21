package repo

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/tmp-platform/backend/internal/model"
)

// JobRepo 任务、步骤与日志数据访问。
type JobRepo struct {
	db *gorm.DB
}

// NewJobRepo 构造仓库。
func NewJobRepo(db *gorm.DB) *JobRepo { return &JobRepo{db: db} }

// CreateWithSteps 在事务内创建任务与其初始步骤。
func (r *JobRepo) CreateWithSteps(ctx context.Context, job *model.Job, steps []model.JobStep) error {
	return Tx(ctx, r.db, func(tx *gorm.DB) error {
		if err := tx.Create(job).Error; err != nil {
			return err
		}
		for i := range steps {
			steps[i].JobID = job.ID
		}
		if len(steps) > 0 {
			if err := tx.CreateInBatches(steps, 50).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// GetByID 按 ID 查询任务。
func (r *JobRepo) GetByID(ctx context.Context, id int64) (*model.Job, error) {
	var j model.Job
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&j).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// JobFilter 任务筛选。
type JobFilter struct {
	Type      string
	Status    string
	ClusterID int64
	MineBy    int64
	From      *time.Time
	To        *time.Time
	Keyword   string
	Page      Page
}

// buildQuery 构造任务查询。
func (r *JobRepo) buildQuery(ctx context.Context, f JobFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.Job{})
	if f.Type != "" {
		q = q.Where("type = ?", f.Type)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.ClusterID > 0 {
		q = q.Where("cluster_id = ?", f.ClusterID)
	}
	if f.MineBy > 0 {
		q = q.Where("created_by = ?", f.MineBy)
	}
	if f.From != nil {
		q = q.Where("created_at >= ?", *f.From)
	}
	if f.To != nil {
		q = q.Where("created_at <= ?", *f.To)
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		like := likePattern(kw)
		q = q.Where("target_label LIKE ? ESCAPE '\\'", like)
	}
	return q
}

// List 分页查询任务（默认最近 7 天由上层传入 From）。
func (r *JobRepo) List(ctx context.Context, f JobFilter) ([]model.Job, int64, error) {
	f.Page.Normalize()
	q := r.buildQuery(ctx, f).Order("id DESC")
	return ApplyPagination[model.Job](q, f.Page, &model.Job{})
}

// Recent 最近任务，进行中置顶。
func (r *JobRepo) Recent(ctx context.Context, limit int) ([]model.Job, error) {
	if limit <= 0 {
		limit = 10
	}
	var list []model.Job
	err := r.db.WithContext(ctx).
		Order("CASE status WHEN 'running' THEN 0 WHEN 'pending' THEN 1 ELSE 2 END, id DESC").
		Limit(limit).Find(&list).Error
	return list, err
}

// RecentByCluster 某集群最近任务。
func (r *JobRepo) RecentByCluster(ctx context.Context, clusterID int64, limit int) ([]model.Job, error) {
	if limit <= 0 {
		limit = 5
	}
	var list []model.Job
	err := r.db.WithContext(ctx).Where("cluster_id = ?", clusterID).Order("id DESC").Limit(limit).Find(&list).Error
	return list, err
}

// RunningCount 进行中（含排队）任务数。
func (r *JobRepo) RunningCount(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.Job{}).
		Where("status IN ?", []string{model.JobStatusPending, model.JobStatusRunning}).Count(&n).Error
	return n, err
}

// UpdateFields 更新任务字段。
func (r *JobRepo) UpdateFields(ctx context.Context, id int64, fields map[string]any) error {
	fields["updated_at"] = time.Now()
	return r.db.WithContext(ctx).Model(&model.Job{}).Where("id = ?", id).Updates(fields).Error
}

// FindActiveByType 查找指定集群进行中的某类任务（刷新复用）。
func (r *JobRepo) FindActiveByType(ctx context.Context, jobType string, clusterID int64) (*model.Job, error) {
	var j model.Job
	err := r.db.WithContext(ctx).
		Where("type = ? AND cluster_id = ? AND status IN ?", jobType, clusterID,
			[]string{model.JobStatusPending, model.JobStatusRunning}).
		Order("id ASC").First(&j).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &j, nil
}

// Steps 按 seq 返回任务步骤。
func (r *JobRepo) Steps(ctx context.Context, jobID int64) ([]model.JobStep, error) {
	var list []model.JobStep
	err := r.db.WithContext(ctx).Where("job_id = ?", jobID).Order("seq ASC").Find(&list).Error
	return list, err
}

// AddStep 动态追加步骤（巡检逐项、Kill 逐条等场景）。
func (r *JobRepo) AddStep(ctx context.Context, step *model.JobStep) error {
	step.CreatedAt = time.Now()
	return r.db.WithContext(ctx).Create(step).Error
}

// UpdateStep 更新步骤。
func (r *JobRepo) UpdateStep(ctx context.Context, id int64, fields map[string]any) error {
	return r.db.WithContext(ctx).Model(&model.JobStep{}).Where("id = ?", id).Updates(fields).Error
}

// UpdateStepsByJob 批量更新某任务下所有指定状态的步骤。
func (r *JobRepo) UpdateStepsByJob(ctx context.Context, jobID int64, statuses []string, fields map[string]any) error {
	return r.db.WithContext(ctx).Model(&model.JobStep{}).
		Where("job_id = ? AND status IN ?", jobID, statuses).Updates(fields).Error
}

// AppendLogs 批量写入日志。
func (r *JobRepo) AppendLogs(ctx context.Context, logs []model.JobLog) error {
	if len(logs) == 0 {
		return nil
	}
	for i := range logs {
		if logs[i].CreatedAt.IsZero() {
			logs[i].CreatedAt = time.Now()
		}
		if len(logs[i].Message) > 4096 {
			logs[i].Message = logs[i].Message[:4096]
		}
	}
	return r.db.WithContext(ctx).CreateInBatches(logs, 200).Error
}

// LogsAfter 增量拉取日志（afterSeq 为上一批最后一条的 id）。
func (r *JobRepo) LogsAfter(ctx context.Context, jobID int64, afterSeq int64, limit int) ([]model.JobLog, error) {
	if limit <= 0 || limit > 2000 {
		limit = 500
	}
	var list []model.JobLog
	err := r.db.WithContext(ctx).Where("job_id = ? AND id > ?", jobID, afterSeq).
		Order("id ASC").Limit(limit).Find(&list).Error
	return list, err
}

// LogsAll 返回任务全部日志（详情页首屏）。
func (r *JobRepo) LogsAll(ctx context.Context, jobID int64, limit int) ([]model.JobLog, error) {
	if limit <= 0 {
		limit = 2000
	}
	var list []model.JobLog
	err := r.db.WithContext(ctx).Where("job_id = ?", jobID).Order("id ASC").Limit(limit).Find(&list).Error
	return list, err
}

// PendingIDs 返回全部排队中的任务 ID（重启恢复用）。
func (r *JobRepo) PendingIDs(ctx context.Context) ([]int64, error) {
	var ids []int64
	err := r.db.WithContext(ctx).Model(&model.Job{}).
		Where("status = ?", model.JobStatusPending).Order("id ASC").Pluck("id", &ids).Error
	return ids, err
}

// MarkRunningInterrupted 把残留 running 任务置为 interrupted。
func (r *JobRepo) MarkRunningInterrupted(ctx context.Context, summary string) (int64, error) {
	now := time.Now()
	var affected int64
	err := Tx(ctx, r.db, func(tx *gorm.DB) error {
		var jobs []model.Job
		if err := tx.Where("status = ?", model.JobStatusRunning).Find(&jobs).Error; err != nil {
			return err
		}
		for _, j := range jobs {
			if err := tx.Model(&model.Job{}).Where("id = ?", j.ID).Updates(map[string]any{
				"status":        model.JobStatusInterrupted,
				"error_summary": summary,
				"finished_at":   now,
				"updated_at":    now,
			}).Error; err != nil {
				return err
			}
			if err := tx.Model(&model.JobStep{}).
				Where("job_id = ? AND status = ?", j.ID, model.StepStatusRunning).
				Updates(map[string]any{
					"status":      model.StepStatusFailed,
					"error":       summary,
					"finished_at": now,
				}).Error; err != nil {
				return err
			}
			affected++
		}
		return nil
	})
	return affected, err
}

// CancelStale 把 pending 任务置为 canceled（用于启动时无法恢复的场景）。
func (r *JobRepo) CancelStale(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	now := time.Now()
	return r.db.WithContext(ctx).Model(&model.Job{}).Where("id IN ?", ids).Updates(map[string]any{
		"status":      model.JobStatusCanceled,
		"finished_at": now,
		"updated_at":  now,
	}).Error
}

// DeleteBefore 清理过期任务（级联删除步骤与日志），返回删除任务数。
func (r *JobRepo) DeleteBefore(ctx context.Context, before time.Time) (int64, error) {
	var ids []int64
	if err := r.db.WithContext(ctx).Model(&model.Job{}).
		Where("created_at < ? AND status IN ?", before,
			[]string{model.JobStatusSuccess, model.JobStatusFailed, model.JobStatusCanceled, model.JobStatusInterrupted}).
		Pluck("id", &ids).Error; err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	err := Tx(ctx, r.db, func(tx *gorm.DB) error {
		if err := tx.Where("job_id IN ?", ids).Delete(&model.JobLog{}).Error; err != nil {
			return err
		}
		if err := tx.Where("job_id IN ?", ids).Delete(&model.JobStep{}).Error; err != nil {
			return err
		}
		return tx.Where("id IN ?", ids).Delete(&model.Job{}).Error
	})
	if err != nil {
		return 0, err
	}
	return int64(len(ids)), nil
}

// CountByStatus 统计指定状态任务数。
func (r *JobRepo) CountByStatus(ctx context.Context, status string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.Job{}).Where("status = ?", status).Count(&n).Error
	return n, err
}
