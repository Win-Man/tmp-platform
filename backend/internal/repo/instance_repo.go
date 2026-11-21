package repo

import (
	"context"
	"sort"
	"strings"

	"gorm.io/gorm"

	"github.com/tmp-platform/backend/internal/model"
)

// InstanceRepo 实例数据访问。
type InstanceRepo struct {
	db *gorm.DB
}

// NewInstanceRepo 构造仓库。
func NewInstanceRepo(db *gorm.DB) *InstanceRepo { return &InstanceRepo{db: db} }

// InstanceFilterAbnormal 实例状态聚合筛选项：非 up（down / unhealthy / unknown）。
const InstanceFilterAbnormal = "abnormal"

// InstanceFilter 实例筛选。
type InstanceFilter struct {
	Component string
	Status    string
	Keyword   string
	Page      Page
}

// List 分页查询集群实例。
func (r *InstanceRepo) List(ctx context.Context, clusterID int64, f InstanceFilter) ([]model.Instance, int64, error) {
	f.Page.Normalize()
	q := r.db.WithContext(ctx).Model(&model.Instance{}).Where("cluster_id = ?", clusterID)
	if f.Component != "" {
		q = q.Where("component = ?", f.Component)
	}
	if f.Status != "" {
		// abnormal 是聚合筛选项：等价于「非 up」（down / unhealthy / unknown），
		// 供集群列表的「实例异常 N」徽标跳转使用。
		if strings.EqualFold(f.Status, InstanceFilterAbnormal) {
			q = q.Where("status <> ?", model.InstanceStatusUp)
		} else {
			q = q.Where("status = ?", f.Status)
		}
	}
	if f.Keyword != "" {
		like := likePattern(f.Keyword)
		q = q.Where("host_ip LIKE ? ESCAPE '\\'", like)
	}
	nameOrder := "CASE component WHEN 'pd' THEN 0 WHEN 'tidb' THEN 1 WHEN 'tikv' THEN 2 WHEN 'tiflash' THEN 3 WHEN 'ticdc' THEN 4 ELSE 9 END, host_ip, port"
	q = q.Order(nameOrder)
	return ApplyPagination[model.Instance](q, f.Page, &model.Instance{})
}

// ListAll 返回集群全部实例（拓扑图用）。
func (r *InstanceRepo) ListAll(ctx context.Context, clusterID int64) ([]model.Instance, error) {
	var list []model.Instance
	err := r.db.WithContext(ctx).Where("cluster_id = ?", clusterID).Order("component, host_ip, port").Find(&list).Error
	return list, err
}

// CountByCluster 统计集群实例数。
func (r *InstanceRepo) CountByCluster(ctx context.Context, clusterID int64) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.Instance{}).Where("cluster_id = ?", clusterID).Count(&n).Error
	return n, err
}

// CountByStatus 统计指定状态的实例数。
func (r *InstanceRepo) CountByStatus(ctx context.Context, clusterID int64, status string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.Instance{}).Where("cluster_id = ? AND status = ?", clusterID, status).Count(&n).Error
	return n, err
}

// ComponentAgg 组件聚合结果。
type ComponentAgg struct {
	Component     string   `json:"component"`
	InstanceCount int      `json:"instanceCount"`
	Versions      []string `json:"versions"`
	UpCount       int      `json:"upCount"`
	DownCount     int      `json:"downCount"`
}

// AggregateComponents 按组件聚合实例数与版本。
func (r *InstanceRepo) AggregateComponents(ctx context.Context, clusterID int64) ([]ComponentAgg, error) {
	var list []model.Instance
	if err := r.db.WithContext(ctx).Where("cluster_id = ?", clusterID).Find(&list).Error; err != nil {
		return nil, err
	}
	byComp := map[string]*ComponentAgg{}
	versionSet := map[string]map[string]bool{}
	for _, i := range list {
		agg, ok := byComp[i.Component]
		if !ok {
			agg = &ComponentAgg{Component: i.Component}
			byComp[i.Component] = agg
			versionSet[i.Component] = map[string]bool{}
		}
		agg.InstanceCount++
		if i.Status == model.InstanceStatusUp {
			agg.UpCount++
		} else if i.Status == model.InstanceStatusDown {
			agg.DownCount++
		}
		if i.Version != "" {
			versionSet[i.Component][i.Version] = true
		}
	}
	out := make([]ComponentAgg, 0, len(byComp))
	for comp, agg := range byComp {
		for v := range versionSet[comp] {
			agg.Versions = append(agg.Versions, v)
		}
		sort.Strings(agg.Versions)
		out = append(out, *agg)
	}
	sort.Slice(out, func(a, b int) bool { return componentOrderKey(out[a].Component) < componentOrderKey(out[b].Component) })
	return out, nil
}

// componentOrderKey 组件固定展示顺序。
func componentOrderKey(c string) int {
	order := map[string]int{
		"pd": 0, "tidb": 1, "tikv": 2, "tiflash": 3, "ticdc": 4,
		"prometheus": 5, "grafana": 6, "alertmanager": 7,
	}
	if v, ok := order[c]; ok {
		return v
	}
	return 99
}

// InstancesByHost 返回某主机承载的实例（含集群名）。
type HostInstance struct {
	model.Instance
	ClusterName string `json:"clusterName"`
}

// InstancesByHost 查询主机承载的实例。
func (r *InstanceRepo) InstancesByHost(ctx context.Context, hostID int64) ([]HostInstance, error) {
	var rows []HostInstance
	err := r.db.WithContext(ctx).Raw(`SELECT i.*, c.name AS cluster_name
		FROM instances i JOIN clusters c ON c.id = i.cluster_id
		WHERE i.host_id = ? AND c.deleted_at IS NULL
		ORDER BY c.name, i.component`, hostID).Scan(&rows).Error
	return rows, err
}

// CountByClusterAll 统计全部实例（用于总览校验）。
func (r *InstanceRepo) CountByClusterAll(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.Instance{}).Count(&n).Error
	return n, err
}
