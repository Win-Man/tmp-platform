package repo

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/tmp-platform/backend/internal/model"
)

// ClusterRepo 集群聚合根数据访问。
type ClusterRepo struct {
	db *gorm.DB
}

// NewClusterRepo 构造仓库。
func NewClusterRepo(db *gorm.DB) *ClusterRepo { return &ClusterRepo{db: db} }

// DB 暴露底层连接供跨聚合事务使用（仅 service 层在明确需要时使用）。
func (r *ClusterRepo) DB() *gorm.DB { return r.db }

// Create 新建集群。
func (r *ClusterRepo) Create(ctx context.Context, c *model.Cluster) error {
	return r.db.WithContext(ctx).Create(c).Error
}

// GetByID 按 ID 查询（软删自动排除）。
func (r *ClusterRepo) GetByID(ctx context.Context, id int64) (*model.Cluster, error) {
	var c model.Cluster
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetByIDWithDeleted 按 ID 查询，包含已移除纳管的集群（巡检报告回溯用）。
func (r *ClusterRepo) GetByIDWithDeleted(ctx context.Context, id int64) (*model.Cluster, error) {
	var c model.Cluster
	err := r.db.WithContext(ctx).Unscoped().Where("id = ?", id).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// GetByHostAndNameLower 按「中控机 + 小写集群名」查询，用于唯一性校验。
//
// 集群的业务唯一键是 (tiup_host, name_lower)：不同中控机上可以有同名集群
// （同一批机器部署多套 TiDB 集群是常见形态），只有同一台中控机上的同名才算重复。
// 软删除行由 GORM 的 deleted_at 条件自动排除，语义与移除纳管后「可重新接管」一致。
func (r *ClusterRepo) GetByHostAndNameLower(ctx context.Context, tiupHost, nameLower string) (*model.Cluster, error) {
	var c model.Cluster
	err := r.db.WithContext(ctx).
		Where("tiup_host = ? AND name_lower = ?", tiupHost, nameLower).
		First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ClusterFilter 集群列表筛选。
type ClusterFilter struct {
	Keyword    string
	Version    string
	DeployMode string
	Status     string
	Arch       string
	SortBy     string
	SortOrder  string
	// IncludeDraft 为 false 时排除草稿（总览统计口径）。
	IncludeDraft bool
	Page         Page
}

// buildQuery 构造集群列表查询。
func (r *ClusterRepo) buildQuery(ctx context.Context, f ClusterFilter) *gorm.DB {
	q := r.db.WithContext(ctx).Model(&model.Cluster{})
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		q = q.Where("name LIKE ? ESCAPE '\\'", likePattern(kw))
	}
	if f.Version != "" {
		q = q.Where("tidb_version = ?", f.Version)
	}
	if f.DeployMode != "" {
		q = q.Where("deploy_mode = ?", f.DeployMode)
	}
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.Arch != "" {
		q = q.Where("cpu_arch = ?", f.Arch)
	}
	if !f.IncludeDraft {
		q = q.Where("draft = 0")
	}
	return q
}

// List 分页查询集群。
func (r *ClusterRepo) List(ctx context.Context, f ClusterFilter) ([]model.Cluster, int64, error) {
	f.Page.Normalize()
	q := r.buildQuery(ctx, f)

	sortBy := "created_at"
	switch f.SortBy {
	case "name":
		sortBy = "name"
	case "createdAt", "":
		sortBy = "created_at"
	case "alertCount":
		// MVP 未接入告警，等价于按状态倒序（error 优先）。
		sortBy = "status"
	}
	order := "DESC"
	if strings.EqualFold(f.SortOrder, "asc") {
		order = "ASC"
	}
	q = q.Order(sortBy + " " + order).Order("id DESC")

	list, total, err := ApplyPagination[model.Cluster](q, f.Page, &model.Cluster{})
	return list, total, err
}

// AllActive 返回全部纳管（非草稿、未删除）集群，供调度器遍历。
func (r *ClusterRepo) AllActive(ctx context.Context) ([]model.Cluster, error) {
	var list []model.Cluster
	err := r.db.WithContext(ctx).Where("draft = 0").Order("id ASC").Find(&list).Error
	return list, err
}

// All 返回全部集群（含草稿），供演示模式初始化判断。
func (r *ClusterRepo) All(ctx context.Context) ([]model.Cluster, error) {
	var list []model.Cluster
	err := r.db.WithContext(ctx).Order("id ASC").Find(&list).Error
	return list, err
}

// Options 下拉用精简列表。
//
// 带上 tiup_host：不同中控机上允许同名集群，下拉项必须能区分（name @ tiupHost）。
func (r *ClusterRepo) Options(ctx context.Context) ([]model.Cluster, error) {
	var list []model.Cluster
	err := r.db.WithContext(ctx).Select("id", "name", "status", "tidb_version", "draft", "tiup_host").
		Order("name ASC").Find(&list).Error
	return list, err
}

// UpdateFields 更新指定字段。
func (r *ClusterRepo) UpdateFields(ctx context.Context, id int64, fields map[string]any) error {
	fields["updated_at"] = time.Now()
	return r.db.WithContext(ctx).Model(&model.Cluster{}).Where("id = ?", id).Updates(fields).Error
}

// SetCollecting 设置并发刷新幂等标记。
func (r *ClusterRepo) SetCollecting(ctx context.Context, id int64, v int) error {
	return r.db.WithContext(ctx).Model(&model.Cluster{}).Where("id = ?", id).
		Updates(map[string]any{"collecting": v, "updated_at": time.Now()}).Error
}

// ResetAllCollecting 重启恢复：清空所有 collecting 标记，避免刷新永久"复用"死任务。
func (r *ClusterRepo) ResetAllCollecting(ctx context.Context) (int64, error) {
	res := r.db.WithContext(ctx).Model(&model.Cluster{}).Where("collecting <> 0").Update("collecting", 0)
	return res.RowsAffected, res.Error
}

// Remove 移除纳管（软删 + 释放唯一键）。
//
// name_lower 改写为 "<name>#removed#<id>"，从而允许同名集群重新接管。
func (r *ClusterRepo) Remove(ctx context.Context, id int64, nameLower string) error {
	return Tx(ctx, r.db, func(tx *gorm.DB) error {
		var c model.Cluster
		if err := tx.Where("id = ?", id).First(&c).Error; err != nil {
			return err
		}
		newLower := nameLower + "#removed#" + itoa(id)
		if err := tx.Model(&model.Cluster{}).Where("id = ?", id).
			Updates(map[string]any{"name_lower": newLower, "updated_at": time.Now()}).Error; err != nil {
			return err
		}
		return tx.Delete(&model.Cluster{}, id).Error
	})
}

// ClusterStats 总览统计（不含草稿）。
type ClusterStats struct {
	ClusterTotal  int64 `json:"clusterTotal"`
	InstanceTotal int64 `json:"instanceTotal"`
	HostTotal     int64 `json:"hostTotal"`
	// AbnormalTotal 异常集群数：健康状态非 healthy，或存在非 up 实例。
	AbnormalTotal int64 `json:"abnormalTotal"`
	// InstanceAbnormalTotal 异常实例总数（status <> up）。
	InstanceAbnormalTotal int64 `json:"instanceAbnormalTotal"`
}

// Stats 计算总览指标卡数字（草稿不计入任何统计）。
//
// 异常口径（对齐 QA 反馈）：
//   - 异常集群 = status <> healthy 或 该集群存在 status <> up 的实例（任一即计入）；
//   - 异常实例 = 有效集群下 status <> up 的实例总数。
func (r *ClusterRepo) Stats(ctx context.Context) (ClusterStats, error) {
	var st ClusterStats
	db := r.db.WithContext(ctx)

	if err := db.Model(&model.Cluster{}).Where("draft = 0").Count(&st.ClusterTotal).Error; err != nil {
		return st, err
	}
	// 实例数：仅统计有效集群下的实例。
	row := db.Raw(`SELECT COUNT(*) FROM instances i
		JOIN clusters c ON c.id = i.cluster_id
		WHERE c.deleted_at IS NULL AND c.draft = 0`).Row()
	if err := row.Scan(&st.InstanceTotal); err != nil {
		return st, err
	}
	if err := db.Model(&model.Host{}).Count(&st.HostTotal).Error; err != nil {
		return st, err
	}
	if err := db.Raw(`SELECT COUNT(*) FROM clusters c
		WHERE c.deleted_at IS NULL AND c.draft = 0
		  AND (c.status <> ?
		       OR EXISTS (SELECT 1 FROM instances i WHERE i.cluster_id = c.id AND i.status <> ?))`,
		model.ClusterStatusHealthy, model.InstanceStatusUp).Row().Scan(&st.AbnormalTotal); err != nil {
		return st, err
	}
	if err := db.Raw(`SELECT COUNT(*) FROM instances i
		JOIN clusters c ON c.id = i.cluster_id
		WHERE c.deleted_at IS NULL AND c.draft = 0 AND i.status <> ?`,
		model.InstanceStatusUp).Row().Scan(&st.InstanceAbnormalTotal); err != nil {
		return st, err
	}
	return st, nil
}

// AbnormalInstanceCounts 统计各集群「非 up」实例数。
//
// 列表页用它展示「实例异常 N」徽标；单批次查询避免逐集群 COUNT 的 N+1。
func (r *ClusterRepo) AbnormalInstanceCounts(ctx context.Context, clusterIDs []int64) (map[int64]int64, error) {
	out := make(map[int64]int64, len(clusterIDs))
	if len(clusterIDs) == 0 {
		return out, nil
	}
	type row struct {
		ClusterID int64
		Count     int64
	}
	var rows []row
	err := r.db.WithContext(ctx).Model(&model.Instance{}).
		Select("cluster_id, COUNT(*) AS count").
		Where("cluster_id IN ? AND status <> ?", clusterIDs, model.InstanceStatusUp).
		Group("cluster_id").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, x := range rows {
		out[x.ClusterID] = x.Count
	}
	return out, nil
}

// HealthDistribution 集群健康分布。
func (r *ClusterRepo) HealthDistribution(ctx context.Context) (map[string]int64, error) {
	type row struct {
		Status string
		Count  int64
	}
	var rows []row
	err := r.db.WithContext(ctx).Model(&model.Cluster{}).
		Select("status, COUNT(*) AS count").Where("draft = 0").Group("status").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := map[string]int64{
		model.ClusterStatusHealthy:  0,
		model.ClusterStatusDegraded: 0,
		model.ClusterStatusError:    0,
		model.ClusterStatusUnknown:  0,
	}
	for _, x := range rows {
		out[x.Status] = x.Count
	}
	return out, nil
}

// VersionDistribution TiDB 版本分布。
func (r *ClusterRepo) VersionDistribution(ctx context.Context) (map[string]int64, error) {
	type row struct {
		Version string
		Count   int64
	}
	var rows []row
	err := r.db.WithContext(ctx).Model(&model.Cluster{}).
		Select("COALESCE(NULLIF(tidb_version, ''), 'unknown') AS version, COUNT(*) AS count").
		Where("draft = 0").Group("version").Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[string]int64, len(rows))
	for _, x := range rows {
		out[x.Version] = x.Count
	}
	return out, nil
}

// CollectSummary 采集状态汇总。
func (r *ClusterRepo) CollectSummary(ctx context.Context) (lastAt *time.Time, success int64, failed int64, err error) {
	db := r.db.WithContext(ctx)
	var latest model.Cluster
	if qerr := db.Where("draft = 0 AND last_collect_at IS NOT NULL").Order("last_collect_at DESC").First(&latest).Error; qerr == nil {
		lastAt = latest.LastCollectAt
	}
	if cerr := db.Model(&model.Cluster{}).Where("draft = 0 AND collect_status = ?", model.ClusterStatusHealthy).Count(&success).Error; cerr != nil {
		return nil, 0, 0, cerr
	}
	if cerr := db.Model(&model.Cluster{}).
		Where("draft = 0 AND collect_status IN ?", []string{model.ClusterStatusDegraded, model.ClusterStatusError}).
		Count(&failed).Error; cerr != nil {
		return nil, 0, 0, cerr
	}
	return lastAt, success, failed, nil
}

// SnapshotWrite 快照落库的完整入参。
//
// 该结构承载「实例 + 4 张快照表 + 集群计数」的全部数据，由 ApplySnapshot 在同一事务内全量替换。
type SnapshotWrite struct {
	ClusterID int64

	Version    string
	DeployMode string
	Arch       string

	DashboardURLs    string
	PrometheusURLs   string
	GrafanaURLs      string
	AlertmanagerURLs string

	ComponentCount int
	HostCount      int
	InstanceCount  int

	Status        string
	CollectStatus string
	FailStreak    int
	Success       bool
	ErrorMessage  string
	Capabilities  string
	// Source 元数据采集来源（display_json / display_text / meta_yaml）。
	Source string
	// Warnings 交叉校验告警（写入 clusters.collect_warnings，空则清空）。
	Warnings  []string
	FetchedAt time.Time

	// PDEndpoint 非空时表示需要把 PD 地址回填到 clusters（仅当库内为空，故不会覆盖手填值）。
	PDEndpoint string

	Hosts     []model.Host
	Instances []model.Instance
	Users     []model.TiDBUser
	RGs       []model.ResourceGroup
	Sessions  []model.Session
	Variables []model.Variable
}

// ApplySnapshot 在同一事务内全量替换快照并更新集群状态与计数。
//
// 事务一旦失败整体回滚，保留上一次成功采集的数据（ARCHITECTURE §11 事务约定）。
func (r *ClusterRepo) ApplySnapshot(ctx context.Context, w *SnapshotWrite) error {
	return Tx(ctx, r.db, func(tx *gorm.DB) error {
		// 1) upsert hosts，得到 ip → host_id 映射。
		hostIDs := make(map[string]int64, len(w.Hosts))
		for i := range w.Hosts {
			h := &w.Hosts[i]
			if h.IP == "" {
				continue
			}
			var existing model.Host
			err := tx.Where("ip = ?", h.IP).First(&existing).Error
			switch {
			case errors.Is(err, gorm.ErrRecordNotFound):
				if cerr := tx.Create(h).Error; cerr != nil {
					return cerr
				}
				hostIDs[h.IP] = h.ID
			case err != nil:
				return err
			default:
				hostIDs[h.IP] = existing.ID
				// 多集群共享同一台主机时，hosts 行按 IP 全局唯一：某套集群采不到硬件
				//（值为空 / 0）绝不能把另一套集群刚写入的值抹掉，因此只更新「本次确实有值」
				// 的字段；ssh_status 只由 SSH 检测任务写，快照永不覆盖。
				updates := map[string]any{"updated_at": time.Now()}
				if h.Hostname != "" {
					updates["hostname"] = h.Hostname
				}
				if h.OSUser != "" {
					updates["os_user"] = h.OSUser
				}
				if h.CPUArch != "" {
					updates["cpu_arch"] = h.CPUArch
				}
				if h.CPUCores > 0 {
					updates["cpu_cores"] = h.CPUCores
				}
				if h.MemoryGB > 0 {
					updates["memory_gb"] = h.MemoryGB
				}
				if h.DiskTotalGB > 0 {
					updates["disk_total_gb"] = h.DiskTotalGB
				}
				if h.CPUUsagePct > 0 {
					updates["cpu_usage_pct"] = h.CPUUsagePct
				}
				if h.MemUsagePct > 0 {
					updates["mem_usage_pct"] = h.MemUsagePct
				}
				if h.DiskUsagePct > 0 {
					updates["disk_usage_pct"] = h.DiskUsagePct
				}
				// resource_fetched_at 只在本批次确实带来硬件信息时推进，
				// 避免「这次只拿到 IP」把资源采集时间刷成最新、误导用户以为硬件刚采过。
				if h.CPUArch != "" || h.CPUCores > 0 || h.MemoryGB > 0 {
					at := h.ResourceFetchedAt
					if at == nil {
						now := time.Now()
						at = &now
					}
					updates["resource_fetched_at"] = at
				}
				if uerr := tx.Model(&model.Host{}).Where("id = ?", existing.ID).Updates(updates).Error; uerr != nil {
					return uerr
				}
			}
		}

		// 2) instances 全量替换。
		if err := tx.Where("cluster_id = ?", w.ClusterID).Delete(&model.Instance{}).Error; err != nil {
			return err
		}
		if len(w.Instances) > 0 {
			for i := range w.Instances {
				w.Instances[i].ClusterID = w.ClusterID
				if hid, ok := hostIDs[w.Instances[i].HostIP]; ok {
					id := hid
					w.Instances[i].HostID = &id
				}
			}
			if err := tx.CreateInBatches(w.Instances, 100).Error; err != nil {
				return err
			}
		}

		// 3) 四张快照表全量替换。
		if err := tx.Where("cluster_id = ?", w.ClusterID).Delete(&model.TiDBUser{}).Error; err != nil {
			return err
		}
		for i := range w.Users {
			w.Users[i].ClusterID = w.ClusterID
		}
		if len(w.Users) > 0 {
			if err := tx.CreateInBatches(w.Users, 100).Error; err != nil {
				return err
			}
		}

		if err := tx.Where("cluster_id = ?", w.ClusterID).Delete(&model.ResourceGroup{}).Error; err != nil {
			return err
		}
		for i := range w.RGs {
			w.RGs[i].ClusterID = w.ClusterID
		}
		if len(w.RGs) > 0 {
			if err := tx.CreateInBatches(w.RGs, 100).Error; err != nil {
				return err
			}
		}

		if err := tx.Where("cluster_id = ?", w.ClusterID).Delete(&model.Session{}).Error; err != nil {
			return err
		}
		for i := range w.Sessions {
			w.Sessions[i].ClusterID = w.ClusterID
		}
		if len(w.Sessions) > 0 {
			if err := tx.CreateInBatches(w.Sessions, 100).Error; err != nil {
				return err
			}
		}

		if err := tx.Where("cluster_id = ?", w.ClusterID).Delete(&model.Variable{}).Error; err != nil {
			return err
		}
		for i := range w.Variables {
			w.Variables[i].ClusterID = w.ClusterID
		}
		if len(w.Variables) > 0 {
			if err := tx.CreateInBatches(w.Variables, 200).Error; err != nil {
				return err
			}
		}

		// 4) 集群状态与计数（唯一写入口）。
		// 交叉校验告警每次都要写：本次没有矛盾时必须清空，否则旧的「状态存疑」
		// 提示会一直挂在界面上，反而变成新的误导。
		warnRaw := ""
		if len(w.Warnings) > 0 {
			if b, jerr := json.Marshal(w.Warnings); jerr == nil {
				warnRaw = string(b)
			}
		}
		fields := map[string]any{
			"status":              w.Status,
			"collect_status":      w.CollectStatus,
			"collect_fail_streak": w.FailStreak,
			"last_collect_at":     w.FetchedAt,
			"last_collect_error":  w.ErrorMessage,
			"collect_warnings":    warnRaw,
			"collecting":          0,
			"updated_at":          time.Now(),
		}
		if w.Success {
			fields["last_success_at"] = w.FetchedAt
			fields["fetched_at"] = w.FetchedAt
			fields["component_count"] = w.ComponentCount
			fields["host_count"] = w.HostCount
			fields["instance_count"] = w.InstanceCount
			if w.Version != "" {
				fields["tidb_version"] = w.Version
			}
			if w.DeployMode != "" && w.DeployMode != "unknown" {
				fields["deploy_mode"] = w.DeployMode
			}
			if w.Arch != "" {
				fields["cpu_arch"] = w.Arch
			}
			if w.DashboardURLs != "" {
				fields["dashboard_urls"] = w.DashboardURLs
			}
			if w.PrometheusURLs != "" {
				fields["prometheus_urls"] = w.PrometheusURLs
			}
			if w.GrafanaURLs != "" {
				fields["grafana_urls"] = w.GrafanaURLs
			}
			if w.AlertmanagerURLs != "" {
				fields["alertmanager_urls"] = w.AlertmanagerURLs
			}
			if w.Capabilities != "" {
				fields["capabilities"] = w.Capabilities
			}
			if w.Source != "" {
				fields["collect_source"] = w.Source
			}
			// PD 地址自愈：只在库内为空时写入（WHERE 条件保证），不覆盖用户手填值。
			if w.PDEndpoint != "" {
				if uerr := tx.Model(&model.Cluster{}).
					Where("id = ? AND (pd_endpoint IS NULL OR TRIM(pd_endpoint) = '')", w.ClusterID).
					Update("pd_endpoint", w.PDEndpoint).Error; uerr != nil {
					return uerr
				}
			}
		}
		return tx.Model(&model.Cluster{}).Where("id = ?", w.ClusterID).Updates(fields).Error
	})
}

// itoa 无符号整数转字符串（避免额外 import）。
func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [24]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
