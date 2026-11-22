package service

import (
	"context"
	"errors"
	"time"

	"github.com/tmp-platform/backend/internal/job"
	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/pkg/apperr"
	"github.com/tmp-platform/backend/internal/repo"
)

// JobService 任务中心服务。
type JobService struct {
	jobs   *repo.JobRepo
	engine *job.Engine
}

// NewJobService 构造任务服务。
func NewJobService(jobs *repo.JobRepo) *JobService { return &JobService{jobs: jobs} }

// SetEngine 注入任务引擎。
func (s *JobService) SetEngine(e *job.Engine) { s.engine = e }

// EngineBound 报告任务引擎是否已注入。
func (s *JobService) EngineBound() bool { return s.engine != nil }

// JobView 任务视图（补充类型展示名）。
type JobView struct {
	model.Job
	TypeLabel  string `json:"typeLabel"`
	DurationMS int64  `json:"durationMs"`
}

// toView 转换为视图。
func toView(j *model.Job) JobView {
	view := JobView{Job: *j, TypeLabel: job.TypeLabel(j.Type)}
	if j.StartedAt != nil {
		end := j.UpdatedAt
		if j.FinishedAt != nil {
			end = *j.FinishedAt
		}
		view.DurationMS = end.Sub(*j.StartedAt).Milliseconds()
	}
	return view
}

// List 分页查询任务；默认最近 7 天。
func (s *JobService) List(ctx context.Context, f repo.JobFilter) (*repo.ListResult[JobView], error) {
	list, total, err := s.jobs.List(ctx, f)
	if err != nil {
		return nil, apperr.Internal("查询任务列表失败")
	}
	views := make([]JobView, 0, len(list))
	for i := range list {
		views = append(views, toView(&list[i]))
	}
	res := repo.NewListResult(views, total, f.Page)
	return &res, nil
}

// Get 查询任务详情。
func (s *JobService) Get(ctx context.Context, id int64) (*JobView, error) {
	j, err := s.jobs.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, apperr.NotFound("任务不存在")
		}
		return nil, apperr.Internal("查询任务失败")
	}
	v := toView(j)
	return &v, nil
}

// Steps 返回任务步骤。
func (s *JobService) Steps(ctx context.Context, id int64) ([]model.JobStep, error) {
	if _, err := s.jobs.GetByID(ctx, id); err != nil {
		return nil, apperr.NotFound("任务不存在")
	}
	list, err := s.jobs.Steps(ctx, id)
	if err != nil {
		return nil, apperr.Internal("查询任务步骤失败")
	}
	if list == nil {
		list = []model.JobStep{}
	}
	return list, nil
}

// Logs 增量拉取日志；afterSeq 为上一批最后一条 ID。
func (s *JobService) Logs(ctx context.Context, id int64, afterSeq int64, limit int) ([]model.JobLog, error) {
	if _, err := s.jobs.GetByID(ctx, id); err != nil {
		return nil, apperr.NotFound("任务不存在")
	}
	list, err := s.jobs.LogsAfter(ctx, id, afterSeq, limit)
	if err != nil {
		return nil, apperr.Internal("查询任务日志失败")
	}
	if list == nil {
		list = []model.JobLog{}
	}
	return list, nil
}

// RunningCount 进行中任务数（顶栏角标）。
func (s *JobService) RunningCount(ctx context.Context) (int64, error) {
	n, err := s.jobs.RunningCount(ctx)
	if err != nil {
		return 0, apperr.Internal("统计进行中任务失败")
	}
	return n, nil
}

// Retry 重试失败任务：新建任务并关联原 ID，原任务状态不变。
func (s *JobService) Retry(ctx context.Context, id int64, operator *model.PlatformUser) (int64, error) {
	origin, err := s.jobs.GetByID(ctx, id)
	if err != nil {
		return 0, apperr.NotFound("任务不存在")
	}
	switch origin.Status {
	case model.JobStatusFailed, model.JobStatusCanceled, model.JobStatusInterrupted:
	default:
		return 0, apperr.Conflict("仅失败/取消/中断的任务可重试")
	}
	var createdBy *int64
	if operator != nil {
		createdBy = &operator.ID
	}
	originID := origin.ID
	newJob := &model.Job{
		Type:          origin.Type,
		ClusterID:     origin.ClusterID,
		TargetType:    origin.TargetType,
		TargetLabel:   origin.TargetLabel,
		CreatedBy:     createdBy,
		CreatedByName: operatorName(operator),
		Payload:       origin.Payload,
		RetryOf:       &originID,
	}
	if s.engine == nil {
		return 0, apperr.Internal("任务引擎未初始化")
	}
	jobID, serr := s.engine.Submit(ctx, newJob)
	if serr != nil {
		return 0, serr
	}
	return jobID, nil
}

// Cancel 取消任务：进行中置 cancel_requested=1；排队中直接置 canceled。
func (s *JobService) Cancel(ctx context.Context, id int64) error {
	j, err := s.jobs.GetByID(ctx, id)
	if err != nil {
		return apperr.NotFound("任务不存在")
	}
	switch j.Status {
	case model.JobStatusPending:
		return s.jobs.UpdateFields(ctx, id, map[string]any{
			"status":           model.JobStatusCanceled,
			"cancel_requested": 1,
			"finished_at":      nowPtr(),
			"error_summary":    "任务已被取消",
		})
	case model.JobStatusRunning:
		return s.jobs.UpdateFields(ctx, id, map[string]any{
			"cancel_requested": 1,
		})
	default:
		return apperr.Conflict("任务已结束，不可取消")
	}
}

// nowPtr 当前时间指针。
func nowPtr() *time.Time {
	t := time.Now()
	return &t
}
