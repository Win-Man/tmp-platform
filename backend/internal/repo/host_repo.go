package repo

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/tmp-platform/backend/internal/model"
)

// HostRepo 主机数据访问。
type HostRepo struct {
	db *gorm.DB
}

// NewHostRepo 构造仓库。
func NewHostRepo(db *gorm.DB) *HostRepo { return &HostRepo{db: db} }

// GetByID 按 ID 查询主机。
func (r *HostRepo) GetByID(ctx context.Context, id int64) (*model.Host, error) {
	var h model.Host
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&h).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// HostFilter 主机筛选。
type HostFilter struct {
	ClusterID int64
	Arch      string
	SSHStatus string
	Keyword   string
	Page      Page
}

// HostClusterBrief 主机所属集群的精简信息。
//
// 多套集群可以共享同一批机器，且不同中控机上允许存在同名集群，
// 因此必须带上 tiupHost 才能在界面上区分「同名不同中控机」的两个集群。
type HostClusterBrief struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	TiUPHost string `json:"tiupHost"`
}

// HostRow 主机列表行（含承载实例与所属集群信息）。
type HostRow struct {
	model.Host
	InstanceCount int      `json:"instanceCount"`
	Roles         []string `json:"roles"`
	ClusterNames  []string `json:"clusterNames"`
	ClusterIDs    []int64  `json:"clusterIds"`
	// Clusters 所属集群明细（含中控机），前端据此区分同名集群。
	Clusters []HostClusterBrief `json:"clusters"`
	// MappedIP 地址映射后的可达地址。仅当能确定所属集群（分组视图或唯一归属）时填写，
	// 用于在主机管理页提示「这台机器的内网地址实际走这个地址访问」。
	MappedIP string `json:"mappedIp,omitempty"`
}

// applyHostFilter 把主机筛选条件应用到查询（List 与 ListAll 共用，避免两处维护）。
//
// keyword 命中 IP 或主机名，两条 LIKE 必须用括号包成一组 OR：否则与 arch / clusterId
// 组合时会退化成 `ip LIKE ? OR hostname LIKE ? AND cpu_arch = ?`，后置过滤形同失效，
// 表现为「搜 10.10. 却把 127.0.0.1 也带出来」。
func (r *HostRepo) applyHostFilter(q *gorm.DB, f HostFilter) *gorm.DB {
	if f.Arch != "" {
		q = q.Where("cpu_arch = ?", f.Arch)
	}
	if f.SSHStatus != "" {
		q = q.Where("ssh_status = ?", f.SSHStatus)
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		like := likePattern(kw)
		q = q.Where("(ip LIKE ? ESCAPE '\\' OR hostname LIKE ? ESCAPE '\\')", like, like)
	}
	if f.ClusterID > 0 {
		q = q.Where("id IN (SELECT host_id FROM instances WHERE cluster_id = ? AND host_id IS NOT NULL)", f.ClusterID)
	}
	return q
}

// List 分页查询主机。
func (r *HostRepo) List(ctx context.Context, f HostFilter) ([]HostRow, int64, error) {
	f.Page.Normalize()
	q := r.applyHostFilter(r.db.WithContext(ctx).Model(&model.Host{}), f).Order("ip ASC")

	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var hosts []model.Host
	if err := q.Offset(f.Page.Offset()).Limit(f.Page.PageSize).Find(&hosts).Error; err != nil {
		return nil, 0, err
	}
	rows, err := r.decorate(ctx, hosts)
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// ListAll 返回全部匹配主机（不带分页），分组视图用。
//
// 分组视图需要展示所有匹配主机，不能用分页的 List（否则只取第一页），
// 但过滤语义与 List 完全一致，因此复用 applyHostFilter。
func (r *HostRepo) ListAll(ctx context.Context, f HostFilter) ([]HostRow, error) {
	q := r.applyHostFilter(r.db.WithContext(ctx).Model(&model.Host{}), f).Order("ip ASC")
	var hosts []model.Host
	if err := q.Find(&hosts).Error; err != nil {
		return nil, err
	}
	return r.decorate(ctx, hosts)
}

// All 返回所有主机（无筛选，等价于 ListAll(HostFilter{})）。
func (r *HostRepo) All(ctx context.Context) ([]HostRow, error) {
	return r.ListAll(ctx, HostFilter{})
}

// ListByCluster 返回某集群承载实例的主机（拓扑按主机聚合用，不做额外装饰查询）。
//
// 只取 hosts 表里的基础信息，实例归属由调用方按 host_ip 关联：实例的 host_id
// 在历史数据里可能为空，用 IP 兜底才不会丢主机。
func (r *HostRepo) ListByCluster(ctx context.Context, clusterID int64) ([]model.Host, error) {
	var hosts []model.Host
	err := r.db.WithContext(ctx).
		Where("id IN (SELECT host_id FROM instances WHERE cluster_id = ? AND host_id IS NOT NULL)", clusterID).
		Order("ip ASC").
		Find(&hosts).Error
	if err != nil {
		return nil, err
	}
	return hosts, nil
}

// decorate 为一批主机补充承载实例、角色分布与所属集群。
func (r *HostRepo) decorate(ctx context.Context, hosts []model.Host) ([]HostRow, error) {
	if len(hosts) == 0 {
		return []HostRow{}, nil
	}
	ids := make([]int64, 0, len(hosts))
	for _, h := range hosts {
		ids = append(ids, h.ID)
	}
	// instRow 是 Raw(...).Scan 的目标，列名一律显式声明。
	//
	// 注意：不要依赖 GORM 的命名推导——它会把 TiUPHost 推成 ti_up_host，与实际列
	// tiup_host 对不上，导致 Scan 后恒为零值（集群 ID/名称恰好推导正确，所以只有
	// 中控机字段会静默变空，界面表现为「所属集群的中控机永远是 —」）。
	type instRow struct {
		HostID      int64  `gorm:"column:host_id"`
		Component   string `gorm:"column:component"`
		ClusterID   int64  `gorm:"column:cluster_id"`
		ClusterName string `gorm:"column:cluster_name"`
		TiUPHost    string `gorm:"column:tiup_host"`
	}
	var insts []instRow
	err := r.db.WithContext(ctx).Raw(`SELECT i.host_id, i.component, c.id AS cluster_id, c.name AS cluster_name, c.tiup_host
		FROM instances i JOIN clusters c ON c.id = i.cluster_id
		WHERE i.host_id IN ? AND c.deleted_at IS NULL`, ids).Scan(&insts).Error
	if err != nil {
		return nil, err
	}
	roleSet := map[int64]map[string]bool{}
	clusterSet := map[int64]map[int64]string{}
	count := map[int64]int{}
	for _, x := range insts {
		if roleSet[x.HostID] == nil {
			roleSet[x.HostID] = map[string]bool{}
			clusterSet[x.HostID] = map[int64]string{}
		}
		roleSet[x.HostID][componentLabel(x.Component)] = true
		clusterSet[x.HostID][x.ClusterID] = x.ClusterName
		count[x.HostID]++
	}
	// 集群明细（id/name/tiupHost）：同名不同中控机的集群需要靠 tiupHost 区分。
	briefSet := map[int64]map[int64]HostClusterBrief{}
	for _, x := range insts {
		if briefSet[x.HostID] == nil {
			briefSet[x.HostID] = map[int64]HostClusterBrief{}
		}
		briefSet[x.HostID][x.ClusterID] = HostClusterBrief{
			ID:       x.ClusterID,
			Name:     x.ClusterName,
			TiUPHost: x.TiUPHost,
		}
	}
	out := make([]HostRow, 0, len(hosts))
	for _, h := range hosts {
		row := HostRow{Host: h, InstanceCount: count[h.ID]}
		for role := range roleSet[h.ID] {
			row.Roles = append(row.Roles, role)
		}
		sort.Strings(row.Roles)
		for cid, name := range clusterSet[h.ID] {
			row.ClusterIDs = append(row.ClusterIDs, cid)
			row.ClusterNames = append(row.ClusterNames, name)
		}
		sort.Slice(row.ClusterIDs, func(a, b int) bool { return row.ClusterIDs[a] < row.ClusterIDs[b] })
		sort.Strings(row.ClusterNames)
		for _, cid := range row.ClusterIDs {
			if brief, ok := briefSet[h.ID][cid]; ok {
				row.Clusters = append(row.Clusters, brief)
			}
		}
		if row.Roles == nil {
			row.Roles = []string{}
		}
		if row.ClusterNames == nil {
			row.ClusterNames = []string{}
		}
		if row.ClusterIDs == nil {
			row.ClusterIDs = []int64{}
		}
		if row.Clusters == nil {
			row.Clusters = []HostClusterBrief{}
		}
		out = append(out, row)
	}
	return out, nil
}

// componentLabel 组件展示名（主机角色分布用）。
func componentLabel(c string) string {
	switch c {
	case "pd":
		return "PD"
	case "tidb":
		return "TiDB"
	case "tikv":
		return "TiKV"
	case "tiflash":
		return "TiFlash"
	case "ticdc":
		return "TiCDC"
	case "prometheus":
		return "Prometheus"
	case "grafana":
		return "Grafana"
	case "alertmanager":
		return "Alertmanager"
	default:
		return strings.ToUpper(c)
	}
}

// UpdateSSHStatus 回写 SSH 检测结果。
func (r *HostRepo) UpdateSSHStatus(ctx context.Context, id int64, status, errMsg string) error {
	now := time.Now()
	return r.db.WithContext(ctx).Model(&model.Host{}).Where("id = ?", id).Updates(map[string]any{
		"ssh_status":     status,
		"ssh_error":      errMsg,
		"ssh_checked_at": now,
		"updated_at":     now,
	}).Error
}

// Count 统计主机总数。
func (r *HostRepo) Count(ctx context.Context) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.Host{}).Count(&n).Error
	return n, err
}

// DistinctArch 返回全部 CPU 架构（筛选下拉用）。
func (r *HostRepo) DistinctArch(ctx context.Context) ([]string, error) {
	var out []string
	err := r.db.WithContext(ctx).Model(&model.Host{}).
		Where("cpu_arch IS NOT NULL AND cpu_arch <> ''").
		Distinct().Pluck("cpu_arch", &out).Error
	return out, err
}
