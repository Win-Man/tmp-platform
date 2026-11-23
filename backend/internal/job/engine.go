package job

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/tmp-platform/backend/internal/collector"
	"github.com/tmp-platform/backend/internal/crypto"
	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/pkg/apperr"
	"github.com/tmp-platform/backend/internal/pkg/idgen"
	"github.com/tmp-platform/backend/internal/repo"
)

// 队列常量（ARCHITECTURE §7.3）。
const (
	// QueueCapacity 有界队列容量。
	QueueCapacity = 200
	// submitWait 队列满时的最长阻塞时间。
	submitWait = 3 * time.Second
	// logFlushBatch 日志批量落库阈值。
	logFlushBatch = 20
	// logFlushInterval 日志刷盘最大间隔。
	logFlushInterval = time.Second
	// stepSeqBase 声明式步骤的 seq 间隔，为动态子步骤预留空间。
	stepSeqBase = 1000
)

// ErrCanceled 任务已被取消。
var ErrCanceled = errors.New("任务已被取消")

// Deps 任务引擎依赖。
type Deps struct {
	Repo      *repo.JobRepo
	Clusters  *repo.ClusterRepo
	Instances *repo.InstanceRepo
	Hosts     *repo.HostRepo
	Creds     *repo.CredentialRepo
	TiDBObj   *repo.TiDBObjRepo
	Settings  *repo.SettingRepo
	Inspect   *repo.InspectRepo
	// AddrMaps 地址映射规则：多网络平面下把内网地址换成 TMP 可达地址后再拨号 / 请求。
	AddrMaps *repo.AddrMapRepo
	Cipher   *crypto.Cipher

	// Collector 返回当前模式（真实 / 演示）下的采集器。
	Collector func() collector.Collector
	// IsMock 返回当前是否为演示模式。
	IsMock func() bool
	// Sink 快照落库端口。
	Sink SnapshotSink
	// Inspector 巡检执行端口。
	Inspector InspectRunner

	// RunGen / ReportGen 业务号生成器。
	RunGen    *idgen.Generator
	ReportGen *idgen.Generator

	Log *slog.Logger
}

// Engine 任务引擎。
type Engine struct {
	deps     Deps
	queue    chan int64
	workers  int
	handlers map[string]Handler
	wg       sync.WaitGroup
	stopped  chan struct{}
}

// NewEngine 构造引擎；workers 为 worker 数（默认 4）。
func NewEngine(deps Deps, workers int) *Engine {
	if workers <= 0 {
		workers = 4
	}
	e := &Engine{
		deps:     deps,
		queue:    make(chan int64, QueueCapacity),
		workers:  workers,
		handlers: map[string]Handler{},
		stopped:  make(chan struct{}),
	}
	e.registerHandlers()
	return e
}

// Repo 暴露任务仓库（service 层做查询用）。
func (e *Engine) Repo() *repo.JobRepo { return e.deps.Repo }

// SetSink 注入快照落库端口（构造期循环依赖时延后注入）。
func (e *Engine) SetSink(s SnapshotSink) { e.deps.Sink = s }

// SetInspector 注入巡检执行端口。
func (e *Engine) SetInspector(i InspectRunner) { e.deps.Inspector = i }

// Start 启动 worker pool。
func (e *Engine) Start(ctx context.Context) {
	for i := 0; i < e.workers; i++ {
		e.wg.Add(1)
		go e.worker(ctx, i)
	}
	e.deps.Log.Info("任务引擎已启动", "workers", e.workers, "queue", QueueCapacity)
}

// Stop 等待 worker 退出（ctx 取消后调用）。
func (e *Engine) Stop() {
	select {
	case <-e.stopped:
		return
	default:
		close(e.stopped)
	}
	e.wg.Wait()
}

// QueueLength 当前排队任务数（可观测性指标）。
func (e *Engine) QueueLength() int { return len(e.queue) }

// Submit 创建任务与步骤并入队。
//
// 队列满时最多阻塞 3s，仍失败则返回 42901。
func (e *Engine) Submit(ctx context.Context, job *model.Job) (int64, error) {
	if !IsKnownType(job.Type) {
		return 0, apperr.BadRequest("未知的任务类型: " + job.Type)
	}
	spec := SpecOf(job.Type)
	steps := make([]model.JobStep, 0, len(spec.Steps))
	now := time.Now()
	for i, name := range spec.Steps {
		steps = append(steps, model.JobStep{
			Seq:       (i + 1) * stepSeqBase,
			Name:      name,
			Status:    model.StepStatusPending,
			CreatedAt: now,
		})
	}
	if job.Status == "" {
		job.Status = model.JobStatusPending
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	job.UpdatedAt = now
	if err := e.deps.Repo.CreateWithSteps(ctx, job, steps); err != nil {
		return 0, apperr.Internal("创建任务失败: " + err.Error())
	}
	if err := e.enqueue(ctx, job.ID); err != nil {
		// 入队失败：标记任务失败，避免永久 pending。
		_ = e.deps.Repo.UpdateFields(context.Background(), job.ID, map[string]any{
			"status":        model.JobStatusFailed,
			"error_summary": "任务入队失败：队列已满",
			"finished_at":   time.Now(),
		})
		return 0, err
	}
	return job.ID, nil
}

// enqueue 入队，队满时等待最多 submitWait。
func (e *Engine) enqueue(ctx context.Context, id int64) error {
	select {
	case e.queue <- id:
		return nil
	default:
	}
	timer := time.NewTimer(submitWait)
	defer timer.Stop()
	select {
	case e.queue <- id:
		return nil
	case <-timer.C:
		return apperr.TooMany("任务队列已满，请稍后重试")
	case <-ctx.Done():
		return apperr.Internal("任务入队被中断")
	}
}

// Requeue 把已存在但未执行的任务重新入队（重启恢复）。
func (e *Engine) Requeue(ctx context.Context, id int64) error {
	return e.enqueue(ctx, id)
}

// worker 单个 worker 循环。
func (e *Engine) worker(ctx context.Context, idx int) {
	defer e.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopped:
			return
		case id := <-e.queue:
			e.run(ctx, id, idx)
		}
	}
}

// run 执行单个任务，含 panic 兜底。
func (e *Engine) run(ctx context.Context, id int64, workerIdx int) {
	job, err := e.deps.Repo.GetByID(ctx, id)
	if err != nil || job == nil {
		return
	}
	if job.Status != model.JobStatusPending {
		return
	}
	startedAt := time.Now()
	if err := e.deps.Repo.UpdateFields(ctx, id, map[string]any{
		"status":     model.JobStatusRunning,
		"started_at": startedAt,
		"progress":   1,
	}); err != nil {
		return
	}
	job.Status = model.JobStatusRunning
	job.StartedAt = &startedAt

	steps, err := e.deps.Repo.Steps(ctx, id)
	if err != nil {
		steps = nil
	}
	r := newRunner(ctx, e, job, steps)
	spec := SpecOf(job.Type)
	handler, ok := e.handlers[job.Type]
	if !ok {
		r.finish(model.JobStatusFailed, "未注册的任务类型: "+job.Type)
		return
	}

	jobCtx, cancel := context.WithTimeout(ctx, spec.Timeout)
	defer cancel()

	runErr := e.safeExecute(jobCtx, handler, r, job, workerIdx)
	r.Flush()
	r.touchJobLog()

	switch {
	case runErr == nil:
		r.finish(model.JobStatusSuccess, "")
	case errors.Is(runErr, ErrCanceled):
		r.finish(model.JobStatusCanceled, "任务已被取消")
	default:
		r.finish(model.JobStatusFailed, runErr.Error())
	}

	// 采集/接管任务无论是成功、失败还是取消，都必须释放并发刷新标记，
	// 否则 clusters.collecting 会永久为 1，前端一直显示"采集中"。
	if job.Type == TypeCollect || job.Type == TypeTakeover {
		if job.ClusterID != nil {
			releaseCtx := context.WithoutCancel(ctx)
			if err := e.deps.Clusters.SetCollecting(releaseCtx, *job.ClusterID, 0); err != nil {
				e.deps.Log.Warn("释放采集标记失败", "clusterId", *job.ClusterID, "error", err)
			}
		}
	}
}

// safeExecute 执行 handler 并捕获 panic（不拖垮 worker）。
func (e *Engine) safeExecute(ctx context.Context, h Handler, r *Runner, j *model.Job, workerIdx int) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			e.deps.Log.Error("任务执行 panic",
				"job_id", j.ID, "type", j.Type, "worker", workerIdx,
				"panic", fmt.Sprint(rec), "stack", string(debug.Stack()))
			err = errors.New("内部执行异常（internal panic），请查看服务日志")
		}
	}()
	return h(ctx, r)
}

// ---------------------------------------------------------------------------
// Runner
// ---------------------------------------------------------------------------

// Runner 任务执行上下文，负责步骤状态、进度与日志落库。
type Runner struct {
	ctx   context.Context
	eng   *Engine
	job   *model.Job
	steps []model.JobStep

	mu          sync.Mutex
	seqByID     map[int64]int
	logBuf      []model.JobLog
	lastFlush   time.Time
	totalSteps  int
	doneSteps   int
	maxProgress int
	currentStep string
	lastCancel  time.Time
	canceled    bool
	results     map[string]any
}

// newRunner 构造 Runner。
func newRunner(ctx context.Context, e *Engine, j *model.Job, steps []model.JobStep) *Runner {
	r := &Runner{
		ctx:        ctx,
		eng:        e,
		job:        j,
		steps:      steps,
		seqByID:    make(map[int64]int, len(steps)),
		lastFlush:  time.Now(),
		totalSteps: len(steps),
		results:    map[string]any{},
	}
	for _, s := range steps {
		r.seqByID[s.ID] = s.Seq
	}
	return r
}

// Job 返回当前任务。
func (r *Runner) Job() *model.Job { return r.job }

// Parent 返回引擎。
func (r *Runner) Parent() *Engine { return r.eng }

// Deps 返回依赖集合（handler 使用）。
func (r *Runner) Deps() *Deps { return &r.eng.deps }

// Ctx 返回任务上下文（含超时）。
func (r *Runner) Ctx() context.Context { return r.ctx }

// SetResult 记录任务产出（落 jobs.result）。
func (r *Runner) SetResult(k string, v any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results[k] = v
}

// Log 追加一条日志（缓冲区满或超过 1s 落库）。
func (r *Runner) Log(stepSeq int, level, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	r.mu.Lock()
	var stepPtr *int
	if stepSeq > 0 {
		seq := stepSeq
		stepPtr = &seq
	}
	r.logBuf = append(r.logBuf, model.JobLog{
		JobID:     r.job.ID,
		StepSeq:   stepPtr,
		Level:     level,
		Message:   msg,
		CreatedAt: time.Now(),
	})
	shouldFlush := len(r.logBuf) >= logFlushBatch
	r.mu.Unlock()
	if shouldFlush {
		r.Flush()
	}
}

// Logf 日志便利方法（info 级别）。
func (r *Runner) Logf(stepSeq int, format string, args ...any) {
	r.Log(stepSeq, "info", format, args...)
}

// flushLocked 在持锁状态下取出待写日志。
func (r *Runner) takeLogs() []model.JobLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.logBuf) == 0 {
		return nil
	}
	out := r.logBuf
	r.logBuf = nil
	r.lastFlush = time.Now()
	return out
}

// Flush 把缓冲日志与当前进度落库。
func (r *Runner) Flush() {
	logs := r.takeLogs()
	if len(logs) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		if err := r.eng.deps.Repo.AppendLogs(ctx, logs); err != nil {
			r.eng.deps.Log.Warn("写入任务日志失败", "job_id", r.job.ID, "error", err)
		}
		cancel()
	}
}

// touchJobLog 落库当前步骤与进度。
func (r *Runner) touchJobLog() {
	r.mu.Lock()
	step := r.currentStep
	progress := r.maxProgress
	r.mu.Unlock()
	if step == "" && progress == 0 {
		return
	}
	fields := map[string]any{}
	if step != "" {
		fields["current_step"] = step
	}
	if progress > 0 {
		fields["progress"] = progress
	}
	if len(fields) == 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = r.eng.deps.Repo.UpdateFields(ctx, r.job.ID, fields)
}

// SetProgress 设置进度（单调递增，永不回退）。
func (r *Runner) SetProgress(p int) {
	if p < 0 {
		p = 0
	}
	if p > 100 {
		p = 100
	}
	r.mu.Lock()
	if p > r.maxProgress {
		r.maxProgress = p
	}
	r.mu.Unlock()
}

// recalcProgressLocked 按已完成步骤比例推进进度。
func (r *Runner) recalcProgress() {
	r.mu.Lock()
	total := r.totalSteps
	done := r.doneSteps
	r.mu.Unlock()
	if total <= 0 {
		return
	}
	p := int(float64(done) / float64(total) * 95)
	if p < 1 {
		p = 1
	}
	r.SetProgress(p)
}

// Canceled 检查取消标记（节流到每 500ms 查一次库）。
func (r *Runner) Canceled() bool {
	r.mu.Lock()
	if r.canceled {
		r.mu.Unlock()
		return true
	}
	if time.Since(r.lastCancel) < 500*time.Millisecond {
		r.mu.Unlock()
		return false
	}
	r.lastCancel = time.Now()
	r.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	j, err := r.eng.deps.Repo.GetByID(ctx, r.job.ID)
	if err != nil || j == nil {
		return false
	}
	if j.CancelRequested == 1 {
		r.mu.Lock()
		r.canceled = true
		r.mu.Unlock()
		return true
	}
	return false
}

// checkCanceled 取消时返回 ErrCanceled。
func (r *Runner) checkCanceled() error {
	if err := r.ctx.Err(); err != nil {
		return ErrCanceled
	}
	if r.Canceled() {
		return ErrCanceled
	}
	return nil
}

// stepBySeq 按 seq 取步骤。
func (r *Runner) stepBySeq(seq int) (*model.JobStep, bool) {
	for i := range r.steps {
		if r.steps[i].Seq == seq {
			return &r.steps[i], true
		}
	}
	return nil, false
}

// Do 执行一个声明式步骤（seq 为 1000 的倍数，见 stepSeqBase）。
//
// stepIndex 为 1 基步骤序号。
func (r *Runner) Do(stepIndex int, fn func() error) error {
	seq := stepIndex * stepSeqBase
	return r.do(seq, fn)
}

// do 按 seq 执行步骤。
func (r *Runner) do(seq int, fn func() error) error {
	if err := r.checkCanceled(); err != nil {
		return err
	}
	step, ok := r.stepBySeq(seq)
	if !ok {
		return fmt.Errorf("步骤 seq=%d 不存在", seq)
	}
	now := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_ = r.eng.deps.Repo.UpdateStep(ctx, step.ID, map[string]any{
		"status":     model.StepStatusRunning,
		"started_at": now,
	})
	cancel()
	r.mu.Lock()
	r.currentStep = step.Name
	r.mu.Unlock()

	err := fn()
	finished := time.Now()
	fields := map[string]any{"finished_at": finished}
	switch {
	case err == nil:
		fields["status"] = model.StepStatusSuccess
		fields["progress"] = 100
		r.completeStep()
	default:
		fields["status"] = model.StepStatusFailed
		fields["error"] = err.Error()
		r.Log(seq, "error", "步骤「%s」失败: %s", step.Name, err.Error())
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 5*time.Second)
	_ = r.eng.deps.Repo.UpdateStep(ctx2, step.ID, fields)
	cancel2()
	r.Flush()
	return err
}

// completeStep 记账已完成步骤并推进进度。
func (r *Runner) completeStep() {
	r.mu.Lock()
	r.doneSteps++
	r.mu.Unlock()
	r.recalcProgress()
}

// AddSubStep 在指定父步骤下追加动态子步骤，返回其 seq。
//
// 父步骤 i 的 seq 为 i*1000，子步骤 seq 为 i*1000+j（j 从 1 开始），
// 因此在按 seq 排序时子步骤紧随父步骤之后。
func (r *Runner) AddSubStep(parentIndex int, name string) (int, error) {
	// 找到该父步骤下已用的最大 j。
	maxJ := 0
	base := parentIndex * stepSeqBase
	for _, s := range r.steps {
		if s.Seq > base && s.Seq < base+stepSeqBase && s.Seq-base > maxJ {
			maxJ = s.Seq - base
		}
	}
	seq := base + maxJ + 1
	step := &model.JobStep{
		JobID:     r.job.ID,
		Seq:       seq,
		Name:      name,
		Status:    model.StepStatusPending,
		CreatedAt: time.Now(),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.eng.deps.Repo.AddStep(ctx, step); err != nil {
		return 0, err
	}
	r.mu.Lock()
	r.steps = append(r.steps, *step)
	r.totalSteps++
	r.mu.Unlock()
	return seq, nil
}

// RunSubStep 追加并执行一个子步骤。
func (r *Runner) RunSubStep(parentIndex int, name string, fn func() error) error {
	seq, err := r.AddSubStep(parentIndex, name)
	if err != nil {
		return err
	}
	return r.do(seq, fn)
}

// SkipStep 把步骤标记为跳过。
func (r *Runner) SkipStep(stepIndex int) {
	seq := stepIndex * stepSeqBase
	step, ok := r.stepBySeq(seq)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = r.eng.deps.Repo.UpdateStep(ctx, step.ID, map[string]any{
		"status":      model.StepStatusSkipped,
		"finished_at": time.Now(),
	})
	r.completeStep()
}

// finish 收尾：写任务最终状态。
func (r *Runner) finish(status, errSummary string) {
	now := time.Now()
	fields := map[string]any{
		"status":      status,
		"finished_at": now,
	}
	if status == model.JobStatusSuccess {
		fields["progress"] = 100
	}
	if errSummary != "" {
		fields["error_summary"] = errSummary
	}
	r.mu.Lock()
	if len(r.results) > 0 {
		fields["result"] = Marshal(r.results)
	}
	r.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := r.eng.deps.Repo.UpdateFields(ctx, r.job.ID, fields); err != nil {
		r.eng.deps.Log.Warn("写入任务最终状态失败", "job_id", r.job.ID, "error", err)
	}
}
