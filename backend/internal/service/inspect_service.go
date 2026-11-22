package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/tmp-platform/backend/internal/collector"
	"github.com/tmp-platform/backend/internal/crypto"
	"github.com/tmp-platform/backend/internal/inspect"
	"github.com/tmp-platform/backend/internal/job"
	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/pkg/apperr"
	"github.com/tmp-platform/backend/internal/pkg/idgen"
	"github.com/tmp-platform/backend/internal/repo"
)

// InspectService 巡检服务，同时实现 job.InspectRunner。
type InspectService struct {
	repo     *repo.InspectRepo
	clusters *repo.ClusterRepo
	creds    *repo.CredentialRepo
	cipher   *crypto.Cipher
	settings *SettingService
	mode     *ModeManager
	engine   *inspect.Engine
	runs     *idgen.Generator
	reports  *idgen.Generator
	jobEng   *job.Engine
}

// InspectDeps 巡检服务依赖。
type InspectDeps struct {
	Repo     *repo.InspectRepo
	Clusters *repo.ClusterRepo
	Creds    *repo.CredentialRepo
	Cipher   *crypto.Cipher
	Settings *SettingService
	Mode     *ModeManager
}

// NewInspectService 构造巡检服务。
func NewInspectService(d InspectDeps) *InspectService {
	s := &InspectService{
		repo:     d.Repo,
		clusters: d.Clusters,
		creds:    d.Creds,
		cipher:   d.Cipher,
		settings: d.Settings,
		mode:     d.Mode,
		engine:   inspect.NewEngine(),
	}
	// 业务号按日计数，启动时以库中最大序号为基线。
	s.runs = idgen.New(func(prefix, day string) (int, error) {
		return d.Repo.MaxRunSeq(context.Background(), prefix, day)
	})
	s.reports = idgen.New(func(prefix, day string) (int, error) {
		return d.Repo.MaxReportSeq(context.Background(), prefix, day)
	})
	return s
}

// SetEngine 注入任务引擎。
func (s *InspectService) SetEngine(e *job.Engine) { s.jobEng = e }

// EngineBound 报告任务引擎是否已注入。
func (s *InspectService) EngineBound() bool { return s.jobEng != nil }

// ---------------------------------------------------------------------------
// 模板与巡检项
// ---------------------------------------------------------------------------

// TemplateView 模板视图。
type TemplateView struct {
	ID          int64              `json:"id"`
	Code        string             `json:"code"`
	Name        string             `json:"name"`
	Description string             `json:"description"`
	Builtin     bool               `json:"builtin"`
	Enabled     bool               `json:"enabled"`
	ItemCount   int                `json:"itemCount"`
	Categories  []CategoryStat     `json:"categories"`
	Items       []inspect.ItemView `json:"items,omitempty"`
}

// CategoryStat 分类统计。
type CategoryStat struct {
	Category string `json:"category"`
	Label    string `json:"label"`
	Count    int    `json:"count"`
}

// Templates 返回模板列表（含分类统计）。
func (s *InspectService) Templates(ctx context.Context) ([]TemplateView, error) {
	list, err := s.repo.ListTemplates(ctx)
	if err != nil {
		return nil, apperr.Internal("查询巡检模板失败")
	}
	out := make([]TemplateView, 0, len(list))
	for _, t := range list {
		items, ierr := s.repo.ItemsByTemplate(ctx, t.ID)
		if ierr != nil {
			items = nil
		}
		out = append(out, TemplateView{
			ID:          t.ID,
			Code:        t.Code,
			Name:        t.Name,
			Description: t.Description,
			Builtin:     t.Builtin == 1,
			Enabled:     t.Enabled == 1,
			ItemCount:   len(items),
			Categories:  categoryStats(items),
		})
	}
	return out, nil
}

// categoryStats 统计各分类项数。
func categoryStats(items []model.InspectItem) []CategoryStat {
	counts := map[string]int{}
	for _, it := range items {
		counts[it.Category]++
	}
	out := make([]CategoryStat, 0, len(counts))
	for _, c := range inspect.CategoryOrder {
		if n, ok := counts[c]; ok {
			out = append(out, CategoryStat{Category: c, Label: inspect.CategoryLabel(c), Count: n})
		}
	}
	for c, n := range counts {
		found := false
		for _, known := range inspect.CategoryOrder {
			if known == c {
				found = true
				break
			}
		}
		if !found {
			out = append(out, CategoryStat{Category: c, Label: inspect.CategoryLabel(c), Count: n})
		}
	}
	return out
}

// TemplateDetail 模板详情（含项明细）。
func (s *InspectService) TemplateDetail(ctx context.Context, id int64) (*TemplateView, error) {
	t, err := s.repo.GetTemplate(ctx, id)
	if err != nil {
		return nil, apperr.NotFound("巡检模板不存在")
	}
	items, ierr := s.repo.ItemsByTemplate(ctx, id)
	if ierr != nil {
		return nil, apperr.Internal("查询巡检项失败")
	}
	views := make([]inspect.ItemView, 0, len(items))
	for _, it := range items {
		views = append(views, toItemView(it))
	}
	return &TemplateView{
		ID:          t.ID,
		Code:        t.Code,
		Name:        t.Name,
		Description: t.Description,
		Builtin:     t.Builtin == 1,
		Enabled:     t.Enabled == 1,
		ItemCount:   len(items),
		Categories:  categoryStats(items),
		Items:       views,
	}, nil
}

// toItemView 转换为对外视图。
func toItemView(it model.InspectItem) inspect.ItemView {
	return inspect.ItemView{
		ID:            it.ID,
		Code:          it.Code,
		Name:          it.Name,
		Category:      it.Category,
		CategoryLabel: inspect.CategoryLabel(it.Category),
		CheckMethod:   it.CheckMethod,
		Severity:      it.Severity,
		Expr:          it.Expr,
		Expect:        it.Expect,
		Suggestion:    it.Suggestion,
		DocURL:        it.DocURL,
		Applies:       it.Applies,
		SortOrder:     it.SortOrder,
	}
}

// Items 查询巡检项。
func (s *InspectService) Items(ctx context.Context, templateID int64, category string) ([]inspect.ItemView, error) {
	if templateID <= 0 {
		tpl, err := s.repo.DefaultTemplate(ctx)
		if err != nil {
			return nil, apperr.NotFound("未找到可用的巡检模板")
		}
		templateID = tpl.ID
	}
	items, err := s.repo.ItemsFiltered(ctx, templateID, category)
	if err != nil {
		return nil, apperr.Internal("查询巡检项失败")
	}
	out := make([]inspect.ItemView, 0, len(items))
	for _, it := range items {
		out = append(out, toItemView(it))
	}
	return out, nil
}

// ---------------------------------------------------------------------------
// 发起巡检
// ---------------------------------------------------------------------------

// StartRunInput 发起巡检入参。
type StartRunInput struct {
	ClusterIDs []int64 `json:"clusterIds"`
	TemplateID int64   `json:"templateId"`
}

// RunStartResult 单个集群的巡检受理结果。
type RunStartResult struct {
	ClusterID   int64  `json:"clusterId"`
	ClusterName string `json:"clusterName"`
	RunID       int64  `json:"runId"`
	RunNo       string `json:"runNo"`
	JobID       int64  `json:"jobId"`
}

// StartRuns 对多个集群发起巡检，各建一个任务。
func (s *InspectService) StartRuns(ctx context.Context, in StartRunInput, operator *model.PlatformUser) ([]RunStartResult, error) {
	if len(in.ClusterIDs) == 0 {
		return nil, apperr.BadRequest("请选择巡检目标集群")
	}
	if len(in.ClusterIDs) > 20 {
		return nil, apperr.BadRequest("单次最多巡检 20 个集群")
	}
	tpl, err := s.resolveTemplate(ctx, in.TemplateID)
	if err != nil {
		return nil, err
	}
	var createdBy *int64
	if operator != nil {
		createdBy = &operator.ID
	}
	out := make([]RunStartResult, 0, len(in.ClusterIDs))
	for _, cid := range in.ClusterIDs {
		c, cerr := s.clusters.GetByID(ctx, cid)
		if cerr != nil {
			continue
		}
		now := time.Now()
		run := &model.InspectRun{
			RunNo:       s.runs.Next(idgen.PrefixInspectRun),
			TemplateID:  tpl.ID,
			ClusterID:   &c.ID,
			ClusterName: c.Name,
			TriggerType: "manual",
			Status:      model.InspectRunPending,
			CreatedAt:   now,
		}
		if err := s.repo.CreateRun(ctx, run); err != nil {
			return nil, apperr.Internal("创建巡检运行失败")
		}
		jobID, jerr := s.jobEng.Submit(ctx, &model.Job{
			Type:          job.TypeInspect,
			ClusterID:     &c.ID,
			TargetType:    "inspection",
			TargetLabel:   c.Name,
			CreatedBy:     createdBy,
			CreatedByName: operatorName(operator),
			Payload: job.Marshal(job.InspectPayload{
				RunID:      run.ID,
				ClusterID:  c.ID,
				TemplateID: tpl.ID,
			}),
		})
		if jerr != nil {
			_ = s.repo.UpdateRun(ctx, run.ID, map[string]any{"status": model.InspectRunFailed})
			return nil, jerr
		}
		_ = s.repo.UpdateRun(ctx, run.ID, map[string]any{"job_id": jobID})
		out = append(out, RunStartResult{ClusterID: c.ID, ClusterName: c.Name, RunID: run.ID, RunNo: run.RunNo, JobID: jobID})
	}
	if len(out) == 0 {
		return nil, apperr.NotFound("选中的集群均不存在")
	}
	return out, nil
}

// resolveTemplate 解析模板，未指定时使用默认模板。
func (s *InspectService) resolveTemplate(ctx context.Context, id int64) (*model.InspectTemplate, error) {
	if id > 0 {
		t, err := s.repo.GetTemplate(ctx, id)
		if err != nil {
			return nil, apperr.NotFound("巡检模板不存在")
		}
		return t, nil
	}
	t, err := s.repo.DefaultTemplate(ctx)
	if err != nil {
		return nil, apperr.NotFound("未找到可用的巡检模板")
	}
	return t, nil
}

// ---------------------------------------------------------------------------
// 运行与报告查询
// ---------------------------------------------------------------------------

// RunDetail 巡检运行详情。
type RunDetail struct {
	Run           *model.InspectRun      `json:"run"`
	Template      *model.InspectTemplate `json:"template"`
	Results       []model.InspectResult  `json:"results"`
	CategoryStats []CategoryStat         `json:"categoryStats"`
	ReportID      int64                  `json:"reportId"`
	ReportNo      string                 `json:"reportNo"`
	ReportStatus  string                 `json:"reportStatus"`
}

// GetRun 查询巡检运行详情。
func (s *InspectService) GetRun(ctx context.Context, id int64) (*RunDetail, error) {
	run, err := s.repo.GetRun(ctx, id)
	if err != nil {
		return nil, apperr.NotFound("巡检运行不存在")
	}
	tpl, terr := s.repo.GetTemplate(ctx, run.TemplateID)
	if terr != nil {
		tpl = &model.InspectTemplate{ID: run.TemplateID, Name: "未知模板"}
	}
	results, rerr := s.repo.ResultsByRun(ctx, id)
	if rerr != nil {
		results = nil
	}
	if results == nil {
		results = []model.InspectResult{}
	}
	detail := &RunDetail{Run: run, Template: tpl, Results: results, CategoryStats: resultCategoryStats(results)}
	if rp, err2 := s.repo.GetReportByRun(ctx, id); err2 == nil && rp != nil {
		detail.ReportID = rp.ID
		detail.ReportNo = rp.ReportNo
		detail.ReportStatus = rp.Status
	}
	return detail, nil
}

// resultCategoryStats 按分类统计结果条数。
func resultCategoryStats(results []model.InspectResult) []CategoryStat {
	counts := map[string]int{}
	for _, r := range results {
		counts[r.Category]++
	}
	out := make([]CategoryStat, 0, len(counts))
	for _, c := range inspect.CategoryOrder {
		if n, ok := counts[c]; ok {
			out = append(out, CategoryStat{Category: c, Label: inspect.CategoryLabel(c), Count: n})
		}
	}
	return out
}

// ReportContent 报告全文。
type ReportContent struct {
	ID          int64      `json:"id"`
	ReportNo    string     `json:"reportNo"`
	RunID       int64      `json:"runId"`
	ClusterName string     `json:"clusterName"`
	Status      string     `json:"status"`
	ContentMD   string     `json:"contentMd"`
	ContentHTML string     `json:"contentHtml"`
	FileSize    int        `json:"fileSize"`
	GeneratedAt *time.Time `json:"generatedAt"`
	CreatedAt   time.Time  `json:"createdAt"`
}

// Reports 分页查询报告。
func (s *InspectService) Reports(ctx context.Context, f repo.ReportFilter) (*repo.ListResult[ReportSummary], error) {
	list, total, err := s.repo.ListReports(ctx, f)
	if err != nil {
		return nil, apperr.Internal("查询巡检报告失败")
	}
	views := make([]ReportSummary, 0, len(list))
	for i := range list {
		views = append(views, s.toReportSummary(ctx, &list[i]))
	}
	res := repo.NewListResult(views, total, f.Page)
	return &res, nil
}

// ReportSummary 报告列表行。
type ReportSummary struct {
	ID             int64      `json:"id"`
	ReportNo       string     `json:"reportNo"`
	RunID          int64      `json:"runId"`
	RunNo          string     `json:"runNo"`
	ClusterID      *int64     `json:"clusterId"`
	ClusterName    string     `json:"clusterName"`
	TemplateName   string     `json:"templateName"`
	Status         string     `json:"status"`
	TriggerType    string     `json:"triggerType"`
	PassRate       float64    `json:"passRate"`
	CriticalCount  int        `json:"criticalCount"`
	WarningCount   int        `json:"warningCount"`
	PassCount      int        `json:"passCount"`
	TotalCount     int        `json:"totalCount"`
	Conclusion     string     `json:"conclusion"`
	FileSize       int        `json:"fileSize"`
	ExecutedAt     *time.Time `json:"executedAt"`
	GeneratedAt    *time.Time `json:"generatedAt"`
	CreatedAt      time.Time  `json:"createdAt"`
	ClusterRemoved bool       `json:"clusterRemoved"`
}

// toReportSummary 组装报告列表行。
func (s *InspectService) toReportSummary(ctx context.Context, rp *model.InspectReport) ReportSummary {
	view := ReportSummary{
		ID:          rp.ID,
		ReportNo:    rp.ReportNo,
		RunID:       rp.RunID,
		ClusterID:   rp.ClusterID,
		ClusterName: rp.ClusterName,
		Status:      rp.Status,
		FileSize:    rp.FileSize,
		GeneratedAt: rp.GeneratedAt,
		CreatedAt:   rp.CreatedAt,
	}
	if run, err := s.repo.GetRun(ctx, rp.RunID); err == nil && run != nil {
		view.RunNo = run.RunNo
		view.TriggerType = run.TriggerType
		view.PassRate = run.PassRate
		view.CriticalCount = run.CriticalCount
		view.WarningCount = run.WarningCount
		view.PassCount = run.PassCount
		view.TotalCount = run.TotalCount
		view.Conclusion = run.Conclusion
		view.ExecutedAt = run.FinishedAt
		if view.ExecutedAt == nil {
			view.ExecutedAt = run.StartedAt
		}
		if tpl, terr := s.repo.GetTemplate(ctx, run.TemplateID); terr == nil && tpl != nil {
			view.TemplateName = tpl.Name
		}
	}
	if rp.ClusterID != nil {
		if _, err := s.clusters.GetByID(ctx, *rp.ClusterID); err != nil && errors.Is(err, repo.ErrNotFound) {
			view.ClusterRemoved = true
		}
	}
	return view
}

// Report 报告详情（含明细与建议）。
type ReportDetail struct {
	ReportSummary
	ContentMD     string                `json:"contentMd"`
	ContentHTML   string                `json:"contentHtml"`
	Results       []model.InspectResult `json:"results"`
	Suggestions   []model.InspectResult `json:"suggestions"`
	CategoryStats []CategoryStat        `json:"categoryStats"`
}

// GetReport 报告详情。
func (s *InspectService) GetReport(ctx context.Context, id int64) (*ReportDetail, error) {
	rp, err := s.repo.GetReport(ctx, id)
	if err != nil {
		return nil, apperr.NotFound("巡检报告不存在")
	}
	detail := &ReportDetail{ReportSummary: s.toReportSummary(ctx, rp)}
	detail.ContentMD = rp.ContentMD
	detail.ContentHTML = rp.ContentHTML
	results, rerr := s.repo.ResultsByRun(ctx, rp.RunID)
	if rerr != nil {
		results = nil
	}
	if results == nil {
		results = []model.InspectResult{}
	}
	detail.Results = results
	detail.CategoryStats = resultCategoryStats(results)
	sug := make([]model.InspectResult, 0)
	for _, r := range results {
		if r.Level == model.LevelCritical || r.Level == model.LevelWarning {
			sug = append(sug, r)
		}
	}
	detail.Suggestions = sug
	return detail, nil
}

// Download 下载报告，返回内容与建议文件名。
func (s *InspectService) Download(ctx context.Context, id int64, format string) ([]byte, string, string, error) {
	rp, err := s.repo.GetReport(ctx, id)
	if err != nil {
		return nil, "", "", apperr.NotFound("巡检报告不存在")
	}
	if rp.Status != model.ReportStatusReady {
		return nil, "", "", apperr.Conflict("报告尚未生成完成")
	}
	stamp := rp.CreatedAt.Format("20060102-150405")
	if rp.GeneratedAt != nil {
		stamp = rp.GeneratedAt.Format("20060102-150405")
	}
	base := fmt.Sprintf("巡检报告_%s_%s", sanitizeFilename(rp.ClusterName), stamp)
	switch strings.ToLower(format) {
	case "md", "markdown":
		return []byte(rp.ContentMD), base + ".md", "text/markdown; charset=utf-8", nil
	case "html", "":
		return []byte(rp.ContentHTML), base + ".html", "text/html; charset=utf-8", nil
	default:
		return nil, "", "", apperr.BadRequest("不支持的导出格式，仅支持 md / html")
	}
}

// sanitizeFilename 清洗文件名中的非法字符。
func sanitizeFilename(name string) string {
	replacer := strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_", " ", "_")
	out := replacer.Replace(strings.TrimSpace(name))
	if out == "" {
		out = "cluster"
	}
	return out
}

// LatestSummary 最近一次巡检摘要。
func (s *InspectService) LatestSummary(ctx context.Context, clusterID int64) (*RunDetail, error) {
	run, err := s.repo.LatestRun(ctx, clusterID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, apperr.NotFound("暂无巡检记录")
		}
		return nil, apperr.Internal("查询巡检记录失败")
	}
	return s.GetRun(ctx, run.ID)
}

// ---------------------------------------------------------------------------
// 执行（实现 job.InspectRunner）
// ---------------------------------------------------------------------------

// ExecuteRun 执行巡检：采集 → 判定 → 汇总 → 报告。
func (s *InspectService) ExecuteRun(ctx context.Context, runID int64, hooks job.RunHooks) error {
	run, tpl, items, cluster, err := s.loadRunContext(ctx, runID, hooks)
	if err != nil {
		return err
	}
	_ = s.repo.UpdateRun(ctx, run.ID, map[string]any{
		"status":     model.InspectRunRunning,
		"started_at": time.Now(),
	})

	// 阶段 2：采集最新快照。
	if hooks.OnPhaseStart != nil {
		hooks.OnPhaseStart(job.PhaseCollect)
	}
	snap, snapErr := s.collectForInspect(ctx, cluster)
	if hooks.OnPhaseEnd != nil {
		hooks.OnPhaseEnd(job.PhaseCollect, snapErr)
	}
	if snapErr != nil {
		s.failRun(ctx, run.ID, snapErr)
		return snapErr
	}

	// 阶段 3：逐项判定。
	if hooks.OnPhaseStart != nil {
		hooks.OnPhaseStart(job.PhaseJudge)
	}
	var progress inspect.ProgressFunc
	if hooks.OnItemDone != nil {
		progress = func(done, total int, item model.InspectItem, res model.InspectResult) {
			hooks.OnItemDone(done, total, item.Code+" "+item.Name, res.Level)
		}
	}
	execResult, execErr := s.engine.Execute(ctx, inspect.ExecuteRequest{
		Cluster:  cluster,
		Snapshot: snap,
		Caps:     snap.Capabilities,
		Items:    items,
		Thresh:   s.settings.Thresholds(ctx),
		Progress: progress,
	})
	if hooks.OnPhaseEnd != nil {
		hooks.OnPhaseEnd(job.PhaseJudge, execErr)
	}
	if execErr != nil {
		s.failRun(ctx, run.ID, execErr)
		return execErr
	}

	// 阶段 4：汇总统计落库。
	if hooks.OnPhaseStart != nil {
		hooks.OnPhaseStart(job.PhaseSummarize)
	}
	sumErr := s.persistRunResult(ctx, run.ID, execResult)
	if hooks.OnPhaseEnd != nil {
		hooks.OnPhaseEnd(job.PhaseSummarize, sumErr)
	}
	if sumErr != nil {
		s.failRun(ctx, run.ID, sumErr)
		return sumErr
	}

	// 阶段 5：生成报告。
	if hooks.OnPhaseStart != nil {
		hooks.OnPhaseStart(job.PhaseReport)
	}
	reportErr := s.generateReport(ctx, run.ID, tpl, items, snap.FetchedAt)
	if hooks.OnPhaseEnd != nil {
		hooks.OnPhaseEnd(job.PhaseReport, reportErr)
	}
	if reportErr != nil {
		return reportErr
	}
	return nil
}

// loadRunContext 加载运行所需的上下文数据（阶段 1）。
func (s *InspectService) loadRunContext(ctx context.Context, runID int64, hooks job.RunHooks) (
	*model.InspectRun, *model.InspectTemplate, []model.InspectItem, *model.Cluster, error) {
	if hooks.OnPhaseStart != nil {
		hooks.OnPhaseStart(job.PhaseLoadTemplate)
	}
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		final := apperr.NotFound("巡检运行不存在")
		if hooks.OnPhaseEnd != nil {
			hooks.OnPhaseEnd(job.PhaseLoadTemplate, final)
		}
		return nil, nil, nil, nil, final
	}
	tpl, terr := s.repo.GetTemplate(ctx, run.TemplateID)
	if terr != nil {
		final := apperr.NotFound("巡检模板不存在")
		if hooks.OnPhaseEnd != nil {
			hooks.OnPhaseEnd(job.PhaseLoadTemplate, final)
		}
		return nil, nil, nil, nil, final
	}
	items, ierr := s.repo.ItemsByTemplate(ctx, tpl.ID)
	if ierr != nil || len(items) == 0 {
		final := apperr.Conflict("巡检模板下没有可执行的巡检项")
		if hooks.OnPhaseEnd != nil {
			hooks.OnPhaseEnd(job.PhaseLoadTemplate, final)
		}
		return nil, nil, nil, nil, final
	}
	var cluster *model.Cluster
	if run.ClusterID != nil {
		c, cerr := s.clusters.GetByIDWithDeleted(ctx, *run.ClusterID)
		if cerr == nil {
			cluster = c
		}
	}
	if cluster == nil {
		cluster = &model.Cluster{ID: 0, Name: run.ClusterName, TiDBVersion: ""}
	}
	if hooks.OnPhaseEnd != nil {
		hooks.OnPhaseEnd(job.PhaseLoadTemplate, nil)
	}
	return run, tpl, items, cluster, nil
}

// collectForInspect 为巡检采集最新快照。
//
// 即便部分通道失败也继续执行（由单项判定降级为 unknown / na），保证巡检不整体失败。
func (s *InspectService) collectForInspect(ctx context.Context, cluster *model.Cluster) (*collector.Snapshot, error) {
	if cluster.ID == 0 {
		// 集群已移除：使用最近一次快照（从库中重建最小快照）。
		return s.snapshotFromDB(ctx, cluster)
	}
	cred, err := s.creds.GetByClusterID(ctx, cluster.ID)
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return nil, apperr.Internal("读取凭据失败")
	}
	target := collector.NewTarget(cluster, cred, s.cipher)
	snap, cerr := s.mode.Current().Collect(ctx, target, collector.FullScope())
	if cerr != nil {
		// 采集完全失败时退回库中快照，仍产出报告（标注数据截至）。
		return s.snapshotFromDB(ctx, cluster)
	}
	return snap, nil
}

// snapshotFromDB 用库中现有快照重建 collector.Snapshot（集群不可达/已移除时使用）。
func (s *InspectService) snapshotFromDB(ctx context.Context, cluster *model.Cluster) (*collector.Snapshot, error) {
	snap := &collector.Snapshot{
		ChanErrors: map[collector.Kind]string{},
		FetchedAt:  time.Now(),
	}
	if cluster.FetchedAt != nil {
		snap.FetchedAt = *cluster.FetchedAt
	}
	snap.Capabilities = collector.BuildCapabilities(cluster.TiDBVersion, nil)
	snap.ClusterInfo = &collector.ClusterInfo{TiDBVersion: cluster.TiDBVersion, DeployMode: cluster.DeployMode}
	snap.DeployModeHint = cluster.DeployMode

	instances, err := s.clusters.DB().WithContext(ctx).
		Raw("SELECT * FROM instances WHERE cluster_id = ?", cluster.ID).Rows()
	if err == nil {
		defer func() { _ = instances.Close() }()
		for instances.Next() {
			var inst model.Instance
			if serr := s.clusters.DB().ScanRows(instances, &inst); serr == nil {
				snap.Instances = append(snap.Instances, collector.InstanceSnapshot{
					Component: inst.Component, HostIP: inst.HostIP, Port: inst.Port,
					StatusPort: inst.StatusPort, Status: inst.Status, Role: inst.Role,
					Version: inst.Version, AZ: inst.AZ,
				})
			}
		}
	}
	// 主机与账号等由各 Checker 通过快照读取；此处补充最小可用数据。
	var hosts []model.Host
	if err := s.clusters.DB().WithContext(ctx).Find(&hosts).Error; err == nil {
		for _, h := range hosts {
			snap.Hosts = append(snap.Hosts, collector.HostResource{
				IP: h.IP, Hostname: h.Hostname, Arch: h.CPUArch, CPUCores: h.CPUCores,
				MemoryGB: h.MemoryGB, CPUUsagePct: h.CPUUsagePct, MemUsagePct: h.MemUsagePct,
				DiskUsagePct: h.DiskUsagePct,
			})
		}
	}
	var vars []model.Variable
	if err := s.clusters.DB().WithContext(ctx).Where("cluster_id = ?", cluster.ID).Find(&vars).Error; err == nil {
		for _, v := range vars {
			snap.Variables = append(snap.Variables, collector.VariableRow{
				Name: v.Name, Value: v.Value, DefaultValue: v.DefaultValue, Scope: v.Scope, IsDynamic: v.IsDynamic == 1,
			})
		}
	}
	var users []model.TiDBUser
	if err := s.clusters.DB().WithContext(ctx).Where("cluster_id = ?", cluster.ID).Find(&users).Error; err == nil {
		for _, u := range users {
			snap.Users = append(snap.Users, collector.TiDBUserRow{
				UserName: u.UserName, Host: u.Host, AuthPlugin: u.AuthPlugin,
				AccountLocked: u.AccountLocked, PasswordExpired: u.PasswordExpired, ResourceGroup: u.ResourceGroup,
			})
		}
	}
	// 不再重复读取快照表，回退数据已足够支撑各项判定。
	return snap, nil
}

// persistRunResult 写入明细并更新运行统计。
func (s *InspectService) persistRunResult(ctx context.Context, runID int64, res *inspect.ExecResult) error {
	results := make([]model.InspectResult, len(res.Results))
	for i := range res.Results {
		results[i] = res.Results[i]
		results[i].RunID = runID
	}
	if err := s.repo.CreateResults(ctx, results); err != nil {
		return apperr.Internal("写入巡检明细失败")
	}
	now := time.Now()
	return s.repo.UpdateRun(ctx, runID, map[string]any{
		"total_count":    res.Total,
		"critical_count": res.CriticalCount,
		"warning_count":  res.WarningCount,
		"pass_count":     res.PassCount,
		"na_count":       res.NACount,
		"unknown_count":  res.UnknownCount,
		"pass_rate":      res.PassRate,
		"conclusion":     res.Conclusion,
		"finished_at":    now,
	})
}

// failRun 标记运行失败。
func (s *InspectService) failRun(ctx context.Context, runID int64, cause error) {
	now := time.Now()
	_ = s.repo.UpdateRun(ctx, runID, map[string]any{
		"status":      model.InspectRunFailed,
		"finished_at": now,
	})
	// 报告也标记失败，便于前端展示重试入口。
	if rp, err := s.repo.GetReportByRun(ctx, runID); err == nil && rp != nil {
		_ = s.repo.UpdateReport(ctx, rp.ID, map[string]any{
			"status": model.ReportStatusFailed,
			"error":  cause.Error(),
		})
	}
}

// generateReport 渲染并落库报告。
func (s *InspectService) generateReport(ctx context.Context, runID int64, tpl *model.InspectTemplate, items []model.InspectItem, fetchedAt time.Time) error {
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		return apperr.Internal("读取巡检运行失败")
	}
	_ = s.repo.UpdateRun(ctx, runID, map[string]any{"status": model.InspectRunSuccess})
	results, _ := s.repo.ResultsByRun(ctx, runID)

	data := &inspect.ReportData{
		ReportNo:     "",
		ClusterName:  run.ClusterName,
		TemplateName: tpl.Name,
		Run:          run,
		Results:      results,
		Items:        items,
		FetchedAt:    &fetchedAt,
		Now:          time.Now(),
	}

	// 已存在报告则覆盖重建。
	existing, _ := s.repo.GetReportByRun(ctx, runID)
	reportNo := s.reports.Next(idgen.PrefixReport)
	if existing != nil && existing.ReportNo != "" {
		reportNo = existing.ReportNo
	}
	data.ReportNo = reportNo

	md := inspect.RenderMarkdown(data)
	htmlDoc := inspect.RenderHTML(data)
	now := time.Now()

	if existing != nil {
		return s.repo.UpdateReport(ctx, existing.ID, map[string]any{
			"status":       model.ReportStatusReady,
			"content_md":   md,
			"content_html": htmlDoc,
			"file_size":    len(htmlDoc),
			"error":        "",
			"generated_at": now,
		})
	}
	report := &model.InspectReport{
		ReportNo:    reportNo,
		RunID:       runID,
		ClusterID:   run.ClusterID,
		ClusterName: run.ClusterName,
		Status:      model.ReportStatusReady,
		ContentMD:   md,
		ContentHTML: htmlDoc,
		FileSize:    len(htmlDoc),
		GeneratedAt: &now,
		CreatedAt:   now,
	}
	if err := s.repo.CreateReport(ctx, report); err != nil {
		return apperr.Internal("写入巡检报告失败")
	}
	return nil
}

// RegenerateReportWithHooks 仅重新生成报告（失败重试）。
func (s *InspectService) RegenerateReportWithHooks(ctx context.Context, runID int64, hooks job.RunHooks) error {
	if hooks.OnPhaseStart != nil {
		hooks.OnPhaseStart(job.PhaseLoadTemplate)
	}
	run, err := s.repo.GetRun(ctx, runID)
	if err != nil {
		if hooks.OnPhaseEnd != nil {
			hooks.OnPhaseEnd(job.PhaseLoadTemplate, err)
		}
		return apperr.NotFound("巡检运行不存在")
	}
	tpl, terr := s.repo.GetTemplate(ctx, run.TemplateID)
	if terr != nil {
		tpl = &model.InspectTemplate{ID: run.TemplateID, Name: "未知模板"}
	}
	items, _ := s.repo.ItemsByTemplate(ctx, run.TemplateID)
	if hooks.OnPhaseEnd != nil {
		hooks.OnPhaseEnd(job.PhaseLoadTemplate, nil)
	}

	if hooks.OnPhaseStart != nil {
		hooks.OnPhaseStart(job.PhaseReport)
	}
	fetchedAt := time.Now()
	if run.StartedAt != nil {
		fetchedAt = *run.StartedAt
	}
	gerr := s.generateReport(ctx, runID, tpl, items, fetchedAt)
	if hooks.OnPhaseEnd != nil {
		hooks.OnPhaseEnd(job.PhaseReport, gerr)
	}
	return gerr
}

// GroupResultsByCategory 按分类分组（报告详情页分组展示用）。
func GroupResultsByCategory(results []model.InspectResult) map[string][]model.InspectResult {
	out := map[string][]model.InspectResult{}
	for _, r := range results {
		out[r.Category] = append(out[r.Category], r)
	}
	for k := range out {
		list := out[k]
		sort.SliceStable(list, func(i, j int) bool { return list[i].ItemCode < list[j].ItemCode })
		out[k] = list
	}
	return out
}
