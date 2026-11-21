package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/tmp-platform/backend/internal/job"
	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/pkg/apperr"
	"github.com/tmp-platform/backend/internal/repo"
)

// HostService 主机服务。
type HostService struct {
	hosts     *repo.HostRepo
	instances *repo.InstanceRepo
	clusters  *repo.ClusterRepo
	engine    *job.Engine
}

// NewHostService 构造主机服务。
func NewHostService(hosts *repo.HostRepo, instances *repo.InstanceRepo, clusters *repo.ClusterRepo) *HostService {
	return &HostService{hosts: hosts, instances: instances, clusters: clusters}
}

// SetEngine 注入任务引擎。
func (s *HostService) SetEngine(e *job.Engine) { s.engine = e }

// List 分页查询主机。
func (s *HostService) List(ctx context.Context, f repo.HostFilter) (*repo.ListResult[repo.HostRow], error) {
	rows, total, err := s.hosts.List(ctx, f)
	if err != nil {
		return nil, apperr.Internal("查询主机列表失败")
	}
	res := repo.NewListResult(rows, total, f.Page)
	return &res, nil
}

// ClusterHostGroup 按集群分组的主机视图。
type ClusterHostGroup struct {
	ClusterID   int64          `json:"clusterId"`
	ClusterName string         `json:"clusterName"`
	Hosts       []repo.HostRow `json:"hosts"`
}

// Grouped 按集群分组返回主机。
//
// 跨集群共享的主机会出现在多个分组中；未被任何集群承载的主机归入「未关联集群」分组。
func (s *HostService) Grouped(ctx context.Context) ([]ClusterHostGroup, error) {
	rows, err := s.hosts.All(ctx)
	if err != nil {
		return nil, apperr.Internal("查询主机失败")
	}
	clusters, err := s.clusters.All(ctx)
	if err != nil {
		return nil, apperr.Internal("查询集群失败")
	}
	nameByID := make(map[int64]string, len(clusters))
	for _, c := range clusters {
		nameByID[c.ID] = c.Name
	}
	groups := map[int64][]repo.HostRow{}
	var order []int64
	unassigned := make([]repo.HostRow, 0)
	for _, h := range rows {
		if len(h.ClusterIDs) == 0 {
			unassigned = append(unassigned, h)
			continue
		}
		for _, cid := range h.ClusterIDs {
			if _, ok := groups[cid]; !ok {
				order = append(order, cid)
			}
			groups[cid] = append(groups[cid], h)
		}
	}
	sort.Slice(order, func(i, j int) bool { return order[i] < order[j] })
	out := make([]ClusterHostGroup, 0, len(order)+1)
	for _, cid := range order {
		out = append(out, ClusterHostGroup{ClusterID: cid, ClusterName: nameByID[cid], Hosts: groups[cid]})
	}
	if len(unassigned) > 0 {
		out = append(out, ClusterHostGroup{ClusterID: 0, ClusterName: "未关联集群", Hosts: unassigned})
	}
	if out == nil {
		out = []ClusterHostGroup{}
	}
	return out, nil
}

// HostDetail 主机详情。
type HostDetail struct {
	Host      *model.Host         `json:"host"`
	Instances []repo.HostInstance `json:"instances"`
	Clusters  []ClusterOption     `json:"clusters"`
}

// Get 查询主机详情。
func (s *HostService) Get(ctx context.Context, id int64) (*HostDetail, error) {
	h, err := s.hosts.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, apperr.NotFound("主机不存在")
		}
		return nil, apperr.Internal("查询主机失败")
	}
	insts, err := s.instances.InstancesByHost(ctx, id)
	if err != nil {
		return nil, apperr.Internal("查询主机实例失败")
	}
	if insts == nil {
		insts = []repo.HostInstance{}
	}
	seen := map[int64]bool{}
	clusters := make([]ClusterOption, 0)
	for _, inst := range insts {
		if seen[inst.ClusterID] {
			continue
		}
		seen[inst.ClusterID] = true
		clusters = append(clusters, ClusterOption{ID: inst.ClusterID, Name: inst.ClusterName})
	}
	return &HostDetail{Host: h, Instances: insts, Clusters: clusters}, nil
}

// Metrics 主机资源趋势（MVP 未接入监控）。
func (s *HostService) Metrics(ctx context.Context, id int64) error {
	if _, err := s.hosts.GetByID(ctx, id); err != nil {
		return apperr.NotFound("主机不存在")
	}
	return apperr.Conflict("未接入监控系统，主机资源趋势不可用")
}

// SSHCheck 发起 SSH 连通性检测任务。
func (s *HostService) SSHCheck(ctx context.Context, ids []int64, operator *model.PlatformUser) (int64, error) {
	if len(ids) == 0 {
		return 0, apperr.BadRequest("请选择主机")
	}
	if len(ids) > 100 {
		return 0, apperr.BadRequest("单次最多检测 100 台主机")
	}
	if s.engine == nil {
		return 0, apperr.Internal("任务引擎未初始化")
	}
	var createdBy *int64
	if operator != nil {
		createdBy = &operator.ID
	}
	label := "批量主机"
	if len(ids) == 1 {
		if h, err := s.hosts.GetByID(ctx, ids[0]); err == nil {
			label = h.IP
		}
	}
	jobID, err := s.engine.Submit(ctx, &model.Job{
		Type:          job.TypeSSHCheck,
		TargetType:    "host",
		TargetLabel:   label,
		CreatedBy:     createdBy,
		CreatedByName: operatorName(operator),
		Payload:       job.Marshal(job.SSHCheckPayload{HostIDs: ids}),
	})
	if err != nil {
		return 0, err
	}
	return jobID, nil
}

// SSHCheckHistory 主机 SSH 检测历史（来自审计 + 任务）。
type SSHCheckHistory struct {
	JobID     int64     `json:"jobId"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"startedAt"`
	Summary   string    `json:"summary"`
}

// SSHChecks 返回与该主机相关的检测任务（按目标标签匹配）。
func (s *HostService) SSHChecks(ctx context.Context, id int64, limit int) ([]SSHCheckHistory, error) {
	h, err := s.hosts.GetByID(ctx, id)
	if err != nil {
		return nil, apperr.NotFound("主机不存在")
	}
	if limit <= 0 {
		limit = 20
	}
	list, _, err := s.engine.Repo().List(ctx, repo.JobFilter{
		Type:    job.TypeSSHCheck,
		Keyword: h.IP,
		Page:    repo.Page{Page: 1, PageSize: limit},
	})
	if err != nil {
		return nil, apperr.Internal("查询检测历史失败")
	}
	out := make([]SSHCheckHistory, 0, len(list))
	for _, j := range list {
		start := j.CreatedAt
		if j.StartedAt != nil {
			start = *j.StartedAt
		}
		out = append(out, SSHCheckHistory{JobID: j.ID, Status: j.Status, StartedAt: start, Summary: j.ErrorSummary})
	}
	return out, nil
}

// DistinctArch 返回可选架构。
func (s *HostService) DistinctArch(ctx context.Context) ([]string, error) {
	archs, err := s.hosts.DistinctArch(ctx)
	if err != nil {
		return nil, apperr.Internal("查询架构失败")
	}
	if archs == nil {
		archs = []string{}
	}
	return archs, nil
}

// RoleLabel 把角色切片拼成展示串。
func RoleLabel(roles []string) string {
	if len(roles) == 0 {
		return "—"
	}
	return strings.Join(roles, "+")
}
