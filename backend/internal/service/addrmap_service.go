package service

import (
	"context"
	"errors"
	"strings"

	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/pkg/addrmap"
	"github.com/tmp-platform/backend/internal/pkg/apperr"
	"github.com/tmp-platform/backend/internal/repo"
)

// AddrMapService 集群地址映射服务。
//
// 职责：规则的增删改查、规则校验与「试算」，以及为其他服务提供构建好的映射器。
type AddrMapService struct {
	maps     *repo.AddrMapRepo
	clusters *repo.ClusterRepo
}

// NewAddrMapService 构造服务。
func NewAddrMapService(maps *repo.AddrMapRepo, clusters *repo.ClusterRepo) *AddrMapService {
	return &AddrMapService{maps: maps, clusters: clusters}
}

// AddrMapInput 新建 / 修改入参。
type AddrMapInput struct {
	Source  string `json:"source"`
	Target  string `json:"target"`
	Note    string `json:"note"`
	Enabled *bool  `json:"enabled"`
}

// AddrMapPreview 试算结果。
type AddrMapPreview struct {
	Input   string `json:"input"`
	Mapped  string `json:"mapped"`
	Matched bool   `json:"matched"`
	Source  string `json:"source"`
	Target  string `json:"target"`
}

// List 返回集群下全部规则。
func (s *AddrMapService) List(ctx context.Context, clusterID int64) ([]model.ClusterAddrMap, error) {
	if _, err := s.clusters.GetByID(ctx, clusterID); err != nil {
		return nil, apperr.NotFound("集群不存在")
	}
	list, err := s.maps.List(ctx, clusterID)
	if err != nil {
		return nil, apperr.Internal("查询地址映射失败")
	}
	if list == nil {
		list = []model.ClusterAddrMap{}
	}
	return list, nil
}

// Create 新增规则。
func (s *AddrMapService) Create(ctx context.Context, clusterID int64, in AddrMapInput, operator *model.PlatformUser) (*model.ClusterAddrMap, error) {
	if _, err := s.clusters.GetByID(ctx, clusterID); err != nil {
		return nil, apperr.NotFound("集群不存在")
	}
	rule, err := addrmap.NormalizeRule(in.Source, in.Target)
	if err != nil {
		return nil, apperr.BadRequest(err.Error())
	}
	if n, cerr := s.maps.CountBySource(ctx, clusterID, rule.Source, 0); cerr == nil && n > 0 {
		return nil, apperr.Conflict("源地址 " + rule.Source + " 已有映射规则，请直接修改那一条")
	}
	m := &model.ClusterAddrMap{
		ClusterID: clusterID,
		Source:    rule.Source,
		Target:    rule.Target,
		Note:      clampNote(in.Note),
		Enabled:   1,
	}
	if in.Enabled != nil && !*in.Enabled {
		m.Enabled = 0
	}
	if operator != nil {
		id := operator.ID
		m.CreatedBy = &id
	}
	if err := s.maps.Create(ctx, m); err != nil {
		// 并发下唯一索引可能先命中，给出可读提示而不是 500。
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return nil, apperr.Conflict("源地址 " + rule.Source + " 已有映射规则")
		}
		return nil, apperr.Internal("保存地址映射失败")
	}
	return m, nil
}

// Update 修改规则。
func (s *AddrMapService) Update(ctx context.Context, clusterID, id int64, in AddrMapInput) (*model.ClusterAddrMap, error) {
	m, err := s.mustGet(ctx, clusterID, id)
	if err != nil {
		return nil, err
	}
	rule, nerr := addrmap.NormalizeRule(in.Source, in.Target)
	if nerr != nil {
		return nil, apperr.BadRequest(nerr.Error())
	}
	if n, cerr := s.maps.CountBySource(ctx, clusterID, rule.Source, m.ID); cerr == nil && n > 0 {
		return nil, apperr.Conflict("源地址 " + rule.Source + " 已有映射规则")
	}
	fields := map[string]any{
		"source": rule.Source,
		"target": rule.Target,
		"note":   clampNote(in.Note),
	}
	if in.Enabled != nil {
		if *in.Enabled {
			fields["enabled"] = 1
		} else {
			fields["enabled"] = 0
		}
	}
	if err := s.maps.UpdateFields(ctx, m.ID, fields); err != nil {
		return nil, apperr.Internal("更新地址映射失败")
	}
	m.Source, m.Target, m.Note = rule.Source, rule.Target, clampNote(in.Note)
	if in.Enabled != nil {
		if *in.Enabled {
			m.Enabled = 1
		} else {
			m.Enabled = 0
		}
	}
	return m, nil
}

// Delete 删除规则。
func (s *AddrMapService) Delete(ctx context.Context, clusterID, id int64) error {
	m, err := s.mustGet(ctx, clusterID, id)
	if err != nil {
		return err
	}
	if derr := s.maps.Delete(ctx, m.ID); derr != nil {
		return apperr.Internal("删除地址映射失败")
	}
	return nil
}

// Preview 用当前启用的规则试算一个地址，便于保存前确认规则是否正确。
func (s *AddrMapService) Preview(ctx context.Context, clusterID int64, addr string) (*AddrMapPreview, error) {
	if _, err := s.clusters.GetByID(ctx, clusterID); err != nil {
		return nil, apperr.NotFound("集群不存在")
	}
	input := strings.TrimSpace(addr)
	if input == "" {
		return nil, apperr.BadRequest("请输入要试算的地址")
	}
	list, err := s.maps.ListEnabled(ctx, clusterID)
	if err != nil {
		return nil, apperr.Internal("查询地址映射失败")
	}
	mapped, hit, ok := BuildAddrMapper(list).Explain(input)
	return &AddrMapPreview{
		Input:   input,
		Mapped:  mapped,
		Matched: ok,
		Source:  hit.Source,
		Target:  hit.Target,
	}, nil
}

// Mapper 构建集群的映射器。查询失败返回 nil（映射只是访问增强，
// 读失败不应该让集群详情整体不可用）。
func (s *AddrMapService) Mapper(ctx context.Context, clusterID int64) *addrmap.Mapper {
	if s == nil {
		return nil
	}
	list, err := s.maps.ListEnabled(ctx, clusterID)
	if err != nil {
		return nil
	}
	return BuildAddrMapper(list)
}

func (s *AddrMapService) mustGet(ctx context.Context, clusterID, id int64) (*model.ClusterAddrMap, error) {
	m, err := s.maps.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, apperr.NotFound("地址映射不存在")
		}
		return nil, apperr.Internal("查询地址映射失败")
	}
	// 规则 ID 全局唯一，必须校验归属，否则可跨集群改删别人的规则。
	if m.ClusterID != clusterID {
		return nil, apperr.NotFound("地址映射不存在")
	}
	return m, nil
}

// BuildAddrMapper 由规则列表构建映射器，供集群服务与主机服务共用。
func BuildAddrMapper(list []model.ClusterAddrMap) *addrmap.Mapper {
	if len(list) == 0 {
		return nil
	}
	rules := make([]addrmap.Rule, 0, len(list))
	for _, m := range list {
		rules = append(rules, addrmap.Rule{Source: m.Source, Target: m.Target})
	}
	return addrmap.New(rules)
}

// clampNote 限制备注长度，避免超长文本把列表撑坏。
func clampNote(s string) string {
	v := strings.TrimSpace(s)
	if r := []rune(v); len(r) > 200 {
		return string(r[:200])
	}
	return v
}
