package service

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/tmp-platform/backend/internal/job"
	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/pkg/addrmap"
	"github.com/tmp-platform/backend/internal/pkg/apperr"
	"github.com/tmp-platform/backend/internal/repo"
)

// HostService 主机服务。
type HostService struct {
	hosts     *repo.HostRepo
	instances *repo.InstanceRepo
	clusters  *repo.ClusterRepo
	engine    *job.Engine
	// addrMaps 地址映射规则仓储；为 nil 时不标注映射地址（单测场景）。
	addrMaps *repo.AddrMapRepo
}

// NewHostService 构造主机服务。
func NewHostService(hosts *repo.HostRepo, instances *repo.InstanceRepo, clusters *repo.ClusterRepo, addrMaps *repo.AddrMapRepo) *HostService {
	return &HostService{hosts: hosts, instances: instances, clusters: clusters, addrMaps: addrMaps}
}

// SetEngine 注入任务引擎。
func (s *HostService) SetEngine(e *job.Engine) { s.engine = e }

// EngineBound 报告任务引擎是否已注入。
func (s *HostService) EngineBound() bool { return s.engine != nil }

// List 分页查询主机。
func (s *HostService) List(ctx context.Context, f repo.HostFilter) (*repo.ListResult[repo.HostRow], error) {
	rows, total, err := s.hosts.List(ctx, f)
	if err != nil {
		return nil, apperr.Internal("查询主机列表失败")
	}
	s.fillMappedIP(ctx, rows, 0)
	res := repo.NewListResult(rows, total, f.Page)
	return &res, nil
}

// addrMappers 一次性加载所有启用规则，按集群构建映射器。
//
// 主机列表不分集群，逐台去查规则会形成 N+1；规则表很小，一次全量读取更划算。
func (s *HostService) addrMappers(ctx context.Context) map[int64]*addrmap.Mapper {
	if s.addrMaps == nil {
		return nil
	}
	list, err := s.addrMaps.ListAllEnabled(ctx)
	if err != nil || len(list) == 0 {
		return nil
	}
	grouped := map[int64][]model.ClusterAddrMap{}
	for _, m := range list {
		grouped[m.ClusterID] = append(grouped[m.ClusterID], m)
	}
	out := make(map[int64]*addrmap.Mapper, len(grouped))
	for cid, rules := range grouped {
		out[cid] = BuildAddrMapper(rules)
	}
	return out
}

// fillMappedIP 给主机行补上「映射后可访问地址」。
//
// clusterID > 0 时按该集群的规则映射；为 0 时只对「仅属于一个集群」的主机推断，
// 因为同一台机器在不同集群里可能有不同映射，猜错比留空更危险。
func (s *HostService) fillMappedIP(ctx context.Context, rows []repo.HostRow, clusterID int64) {
	if s.addrMaps == nil || len(rows) == 0 {
		return
	}
	mappers := s.addrMappers(ctx)
	if len(mappers) == 0 {
		return
	}
	for i := range rows {
		cid := clusterID
		if cid == 0 {
			if len(rows[i].ClusterIDs) != 1 {
				continue
			}
			cid = rows[i].ClusterIDs[0]
		}
		mp := mappers[cid]
		if mp.Empty() {
			continue
		}
		if mapped, ok := mp.MapIP(rows[i].IP); ok {
			rows[i].MappedIP = mapped
		}
	}
}

// ClusterHostGroup 按集群分组的主机视图。
type ClusterHostGroup struct {
	ClusterID   int64          `json:"clusterId"`
	ClusterName string         `json:"clusterName"`
	Hosts       []repo.HostRow `json:"hosts"`
}

// Grouped 按集群分组返回主机（应用 keyword / arch / clusterId 筛选）。
//
// 跨集群共享的主机会出现在多个分组中；未被任何集群承载的主机归入「未关联集群」分组。
func (s *HostService) Grouped(ctx context.Context, f repo.HostFilter) ([]ClusterHostGroup, error) {
	rows, err := s.hosts.ListAll(ctx, f)
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
		hosts := groups[cid]
		// 分组视图本身就是「按集群看」，可以放心用该集群的映射规则。
		s.fillMappedIP(ctx, hosts, cid)
		out = append(out, ClusterHostGroup{ClusterID: cid, ClusterName: nameByID[cid], Hosts: hosts})
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
