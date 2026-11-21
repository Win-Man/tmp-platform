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

// HostRow 主机列表行（含承载实例与所属集群信息）。
type HostRow struct {
	model.Host
	InstanceCount int      `json:"instanceCount"`
	Roles         []string `json:"roles"`
	ClusterNames  []string `json:"clusterNames"`
	ClusterIDs    []int64  `json:"clusterIds"`
}

// List 分页查询主机。
func (r *HostRepo) List(ctx context.Context, f HostFilter) ([]HostRow, int64, error) {
	f.Page.Normalize()
	q := r.db.WithContext(ctx).Model(&model.Host{})
	if f.Arch != "" {
		q = q.Where("cpu_arch = ?", f.Arch)
	}
	if f.SSHStatus != "" {
		q = q.Where("ssh_status = ?", f.SSHStatus)
	}
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		like := likePattern(kw)
		q = q.Where("ip LIKE ? ESCAPE '\\' OR hostname LIKE ? ESCAPE '\\'", like, like)
	}
	if f.ClusterID > 0 {
		q = q.Where("id IN (SELECT host_id FROM instances WHERE cluster_id = ? AND host_id IS NOT NULL)", f.ClusterID)
	}
	q = q.Order("ip ASC")

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

// All 返回所有主机（分组视图用）。
func (r *HostRepo) All(ctx context.Context) ([]HostRow, error) {
	var hosts []model.Host
	if err := r.db.WithContext(ctx).Order("ip ASC").Find(&hosts).Error; err != nil {
		return nil, err
	}
	return r.decorate(ctx, hosts)
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
	type instRow struct {
		HostID      int64
		Component   string
		ClusterID   int64
		ClusterName string
	}
	var insts []instRow
	err := r.db.WithContext(ctx).Raw(`SELECT i.host_id, i.component, c.id AS cluster_id, c.name AS cluster_name
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
		if row.Roles == nil {
			row.Roles = []string{}
		}
		if row.ClusterNames == nil {
			row.ClusterNames = []string{}
		}
		if row.ClusterIDs == nil {
			row.ClusterIDs = []int64{}
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
