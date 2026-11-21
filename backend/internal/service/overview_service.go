package service

import (
	"context"
	"sort"
	"time"

	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/pkg/apperr"
	"github.com/tmp-platform/backend/internal/repo"
)

// OverviewService 总览聚合服务。
type OverviewService struct {
	clusters *repo.ClusterRepo
	hosts    *repo.HostRepo
	jobs     *repo.JobRepo
	inspects *repo.InspectRepo
	mode     *ModeManager
}

// NewOverviewService 构造总览服务。
func NewOverviewService(clusters *repo.ClusterRepo, hosts *repo.HostRepo, jobs *repo.JobRepo, inspects *repo.InspectRepo, mode *ModeManager) *OverviewService {
	return &OverviewService{clusters: clusters, hosts: hosts, jobs: jobs, inspects: inspects, mode: mode}
}

// OverviewSummary 4 张核心指标卡 + 版本分布。
type OverviewSummary struct {
	ClusterTotal  int64 `json:"clusterTotal"`
	InstanceTotal int64 `json:"instanceTotal"`
	HostTotal     int64 `json:"hostTotal"`
	// AbnormalTotal 异常集群数：status<>healthy 或存在非 up 实例。
	AbnormalTotal int64 `json:"abnormalTotal"`
	// InstanceAbnormalTotal 异常实例总数（status<>up）。
	InstanceAbnormalTotal int64            `json:"instanceAbnormalTotal"`
	TiDBVersionDist       map[string]int64 `json:"tidbVersionDist"`
	MockMode              bool             `json:"mockMode"`
}

// Summary 核心指标汇总（不含草稿，草稿不计入任何统计）。
func (s *OverviewService) Summary(ctx context.Context) (*OverviewSummary, error) {
	st, err := s.clusters.Stats(ctx)
	if err != nil {
		return nil, apperr.Internal("统计集群指标失败")
	}
	dist, err := s.clusters.VersionDistribution(ctx)
	if err != nil {
		dist = map[string]int64{}
	}
	return &OverviewSummary{
		ClusterTotal:          st.ClusterTotal,
		InstanceTotal:         st.InstanceTotal,
		HostTotal:             st.HostTotal,
		AbnormalTotal:         st.AbnormalTotal,
		InstanceAbnormalTotal: st.InstanceAbnormalTotal,
		TiDBVersionDist:       dist,
		MockMode:              s.mode.IsMock(),
	}, nil
}

// HealthItem 健康分布条目。
type HealthItem struct {
	Status string `json:"status"`
	Count  int64  `json:"count"`
}

// HealthDistribution 集群健康分布（环形图数据，顺序固定）。
func (s *OverviewService) HealthDistribution(ctx context.Context) ([]HealthItem, error) {
	dist, err := s.clusters.HealthDistribution(ctx)
	if err != nil {
		return nil, apperr.Internal("统计健康分布失败")
	}
	order := []string{model.ClusterStatusHealthy, model.ClusterStatusDegraded, model.ClusterStatusError, model.ClusterStatusUnknown}
	out := make([]HealthItem, 0, len(order))
	for _, st := range order {
		out = append(out, HealthItem{Status: st, Count: dist[st]})
	}
	return out, nil
}

// RecentJobs 近期任务（进行中置顶）。
func (s *OverviewService) RecentJobs(ctx context.Context, limit int) ([]model.Job, error) {
	if limit <= 0 {
		limit = 10
	}
	list, err := s.jobs.Recent(ctx, limit)
	if err != nil {
		return nil, apperr.Internal("查询近期任务失败")
	}
	if list == nil {
		list = []model.Job{}
	}
	return list, nil
}

// LatestInspection 最近一次巡检摘要。
type LatestInspection struct {
	Found         bool       `json:"found"`
	ClusterID     *int64     `json:"clusterId"`
	ClusterName   string     `json:"clusterName"`
	RunID         int64      `json:"runId"`
	RunNo         string     `json:"runNo"`
	PassRate      float64    `json:"passRate"`
	CriticalCount int        `json:"criticalCount"`
	WarningCount  int        `json:"warningCount"`
	Conclusion    string     `json:"conclusion"`
	ReportID      int64      `json:"reportId"`
	ExecutedAt    *time.Time `json:"executedAt"`
}

// LatestInspection 查询最近一次巡检结论。
func (s *OverviewService) LatestInspection(ctx context.Context) (*LatestInspection, error) {
	run, err := s.inspects.LatestRun(ctx, 0)
	if err != nil {
		return &LatestInspection{Found: false}, nil
	}
	out := &LatestInspection{
		Found:         true,
		ClusterID:     run.ClusterID,
		ClusterName:   run.ClusterName,
		RunID:         run.ID,
		RunNo:         run.RunNo,
		PassRate:      run.PassRate,
		CriticalCount: run.CriticalCount,
		WarningCount:  run.WarningCount,
		Conclusion:    run.Conclusion,
		ExecutedAt:    run.FinishedAt,
	}
	if out.ExecutedAt == nil {
		out.ExecutedAt = run.StartedAt
	}
	if rp, rerr := s.inspects.GetReportByRun(ctx, run.ID); rerr == nil && rp != nil {
		out.ReportID = rp.ID
	}
	return out, nil
}

// CollectStatusInfo 采集状态。
type CollectStatusInfo struct {
	LastCollectAt *time.Time `json:"lastCollectAt"`
	SuccessCount  int64      `json:"successCount"`
	FailedCount   int64      `json:"failedCount"`
	MockMode      bool       `json:"mockMode"`
	DataSource    string     `json:"dataSource"`
}

// CollectStatus 全量采集状态概览。
func (s *OverviewService) CollectStatus(ctx context.Context) (*CollectStatusInfo, error) {
	lastAt, success, failed, err := s.clusters.CollectSummary(ctx)
	if err != nil {
		return nil, apperr.Internal("统计采集状态失败")
	}
	return &CollectStatusInfo{
		LastCollectAt: lastAt,
		SuccessCount:  success,
		FailedCount:   failed,
		MockMode:      s.mode.IsMock(),
		DataSource:    s.mode.DataSource(),
	}, nil
}

// EnumDict 枚举字典（前端下拉复用）。
type EnumDict struct {
	ClusterStatus     []EnumOption `json:"clusterStatus"`
	CollectStatus     []EnumOption `json:"collectStatus"`
	InstanceStatus    []EnumOption `json:"instanceStatus"`
	DeployMode        []EnumOption `json:"deployMode"`
	CPUArch           []EnumOption `json:"cpuArch"`
	JobStatus         []EnumOption `json:"jobStatus"`
	JobType           []EnumOption `json:"jobType"`
	StepStatus        []EnumOption `json:"stepStatus"`
	Role              []EnumOption `json:"role"`
	UserStatus        []EnumOption `json:"userStatus"`
	InspectLevel      []EnumOption `json:"inspectLevel"`
	InspectCategory   []EnumOption `json:"inspectCategory"`
	CheckMethod       []EnumOption `json:"checkMethod"`
	Severity          []EnumOption `json:"severity"`
	VariableWhitelist []string     `json:"variableWhitelist"`
	ProbeScope        []EnumOption `json:"probeScope"`
}

// EnumOption 枚举项。
type EnumOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Enums 返回全部枚举字典。
func Enums(jobTypes []string) *EnumDict {
	sort.Strings(jobTypes)
	jobTypeOpts := make([]EnumOption, 0, len(jobTypes))
	for _, t := range jobTypes {
		jobTypeOpts = append(jobTypeOpts, EnumOption{Value: t, Label: jobTypeLabel(t)})
	}
	return &EnumDict{
		ClusterStatus: []EnumOption{
			{"healthy", "正常"}, {"degraded", "降级"}, {"error", "异常"}, {"unknown", "未知"},
		},
		CollectStatus: []EnumOption{
			{"healthy", "正常"}, {"degraded", "降级"}, {"error", "失败"}, {"unknown", "未采集"},
		},
		InstanceStatus: []EnumOption{
			{"up", "Up"}, {"down", "Down"}, {"unhealthy", "Unhealthy"}, {"unknown", "Unknown"},
			{"abnormal", "异常（非 Up）"},
		},
		DeployMode: []EnumOption{
			{"majority", "Majority"}, {"dr_auto_sync", "DR Auto-Sync"}, {"unknown", "未知"},
		},
		CPUArch: []EnumOption{
			{"x86_64", "x86_64"}, {"aarch64", "aarch64"},
		},
		JobStatus: []EnumOption{
			{"pending", "排队中"}, {"running", "进行中"}, {"success", "成功"},
			{"failed", "失败"}, {"canceled", "已取消"}, {"interrupted", "已中断"},
		},
		JobType: jobTypeOpts,
		StepStatus: []EnumOption{
			{"pending", "待执行"}, {"running", "执行中"}, {"success", "成功"},
			{"failed", "失败"}, {"skipped", "已跳过"},
		},
		Role: []EnumOption{
			{"admin", "管理员"}, {"operator", "DBA"}, {"viewer", "只读观察者"},
		},
		UserStatus: []EnumOption{
			{"active", "启用"}, {"disabled", "禁用"},
		},
		InspectLevel: []EnumOption{
			{"critical", "严重"}, {"warning", "警告"}, {"pass", "通过"},
			{"na", "不适用"}, {"unknown", "未知"},
		},
		InspectCategory: []EnumOption{
			{"system", "系统层"}, {"tidb_config", "TiDB 配置"}, {"security", "安全合规"},
			{"capacity", "容量水位"}, {"ha", "高可用"}, {"version", "版本规范"},
		},
		CheckMethod: []EnumOption{
			{"sql", "SQL"}, {"ssh", "SSH"}, {"pd_api", "PD API"},
		},
		Severity: []EnumOption{
			{"critical", "严重"}, {"warning", "警告"},
		},
		VariableWhitelist: VariableWhitelist(),
		ProbeScope: []EnumOption{
			{"ssh", "SSH"}, {"mysql", "数据库"}, {"pd", "PD"},
		},
	}
}

// jobTypeLabel 任务类型展示名（避免 service 依赖 job 包的展示表）。
func jobTypeLabel(t string) string {
	labels := map[string]string{
		"takeover": "接管采集", "collect": "刷新采集", "inspect": "集群巡检",
		"ssh_check": "SSH 连通性检测", "create_tidb_user": "创建 TiDB 用户",
		"drop_tidb_user": "删除 TiDB 用户", "kill_session": "Kill 会话",
		"reset_tidb_password": "重置 TiDB 用户密码", "report": "生成巡检报告",
	}
	if v, ok := labels[t]; ok {
		return v
	}
	return t
}
