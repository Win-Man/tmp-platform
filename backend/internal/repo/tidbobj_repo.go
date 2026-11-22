package repo

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/tmp-platform/backend/internal/model"
)

// TiDBObjRepo TiDB 对象快照（用户 / 参数 / 会话 / Resource Group）数据访问。
type TiDBObjRepo struct {
	db *gorm.DB
}

// NewTiDBObjRepo 构造仓库。
func NewTiDBObjRepo(db *gorm.DB) *TiDBObjRepo { return &TiDBObjRepo{db: db} }

// TiDBUserFilter TiDB 用户筛选。
type TiDBUserFilter struct {
	Keyword string
	Page    Page
}

// ListUsers 分页查询 TiDB 用户快照。
func (r *TiDBObjRepo) ListUsers(ctx context.Context, clusterID int64, f TiDBUserFilter) ([]model.TiDBUser, int64, error) {
	f.Page.Normalize()
	q := r.db.WithContext(ctx).Model(&model.TiDBUser{}).Where("cluster_id = ?", clusterID)
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		like := likePattern(kw)
		q = q.Where("user_name LIKE ? ESCAPE '\\' OR host LIKE ? ESCAPE '\\'", like, like)
	}
	q = q.Order("user_name ASC, host ASC")
	return ApplyPagination[model.TiDBUser](q, f.Page, &model.TiDBUser{})
}

// GetUserByID 按 ID 查询快照用户。
func (r *TiDBObjRepo) GetUserByID(ctx context.Context, clusterID, id int64) (*model.TiDBUser, error) {
	var u model.TiDBUser
	err := r.db.WithContext(ctx).Where("cluster_id = ? AND id = ?", clusterID, id).First(&u).Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// CountUsersByName 统计同名用户数（重名预检）。
func (r *TiDBObjRepo) CountUsersByName(ctx context.Context, clusterID int64, userName, host string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.TiDBUser{}).
		Where("cluster_id = ? AND user_name = ? AND host = ?", clusterID, userName, host).Count(&n).Error
	return n, err
}

// VariableFilter 参数筛选。
type VariableFilter struct {
	Keyword  string
	OnlyDiff bool
	Page     Page
}

// ListVariables 分页查询参数快照。
func (r *TiDBObjRepo) ListVariables(ctx context.Context, clusterID int64, f VariableFilter) ([]model.Variable, int64, error) {
	f.Page.Normalize()
	q := r.db.WithContext(ctx).Model(&model.Variable{}).Where("cluster_id = ?", clusterID)
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		like := likePattern(kw)
		q = q.Where("name LIKE ? ESCAPE '\\' OR value LIKE ? ESCAPE '\\'", like, like)
	}
	if f.OnlyDiff {
		q = q.Where("differs = 1")
	}
	q = q.Order("name ASC")
	return ApplyPagination[model.Variable](q, f.Page, &model.Variable{})
}

// AllVariables 返回全部参数（导出 CSV 用）。
func (r *TiDBObjRepo) AllVariables(ctx context.Context, clusterID int64, onlyDiff bool) ([]model.Variable, error) {
	q := r.db.WithContext(ctx).Where("cluster_id = ?", clusterID)
	if onlyDiff {
		q = q.Where("differs = 1")
	}
	var list []model.Variable
	err := q.Order("name ASC").Find(&list).Error
	return list, err
}

// CountDiffVariables 统计与默认值不同的参数数量。
func (r *TiDBObjRepo) CountDiffVariables(ctx context.Context, clusterID int64) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.Variable{}).
		Where("cluster_id = ? AND differs = 1", clusterID).Count(&n).Error
	return n, err
}

// GetVariableByName 按名字查询参数快照。
func (r *TiDBObjRepo) GetVariableByName(ctx context.Context, clusterID int64, name string) (*model.Variable, error) {
	var v model.Variable
	err := r.db.WithContext(ctx).Where("cluster_id = ? AND name = ?", clusterID, name).First(&v).Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// SessionFilter 会话筛选。
type SessionFilter struct {
	Keyword string
	SortBy  string
	Page    Page
}

// ListSessions 分页查询会话快照。
func (r *TiDBObjRepo) ListSessions(ctx context.Context, clusterID int64, f SessionFilter) ([]model.Session, int64, error) {
	f.Page.Normalize()
	q := r.db.WithContext(ctx).Model(&model.Session{}).Where("cluster_id = ?", clusterID)
	if kw := strings.TrimSpace(f.Keyword); kw != "" {
		like := likePattern(kw)
		q = q.Where("user_name LIKE ? ESCAPE '\\' OR host LIKE ? ESCAPE '\\' OR instance LIKE ? ESCAPE '\\' OR db LIKE ? ESCAPE '\\' OR sql_text LIKE ? ESCAPE '\\' OR sql_digest LIKE ? ESCAPE '\\'", like, like, like, like, like, like)
	}
	order := "time_sec DESC"
	if f.SortBy == "id" {
		order = "session_id ASC"
	}
	q = q.Order(order)
	return ApplyPagination[model.Session](q, f.Page, &model.Session{})
}

// GetSessionsByIDs 按会话 ID 批量查询。
func (r *TiDBObjRepo) GetSessionsByIDs(ctx context.Context, clusterID int64, ids []int64) ([]model.Session, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var list []model.Session
	err := r.db.WithContext(ctx).Where("cluster_id = ? AND session_id IN ?", clusterID, ids).Find(&list).Error
	return list, err
}

// ReplaceSessions 用实时结果整体替换某集群的会话缓存。
//
// 只动 sessions 表，不影响集群快照的其他部分与 last_collect_at；
// 目的是让「会话」页面刷新后，Kill 等后续动作能查到这批会话。
func (r *TiDBObjRepo) ReplaceSessions(ctx context.Context, clusterID int64, list []model.Session) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("cluster_id = ?", clusterID).Delete(&model.Session{}).Error; err != nil {
			return err
		}
		if len(list) == 0 {
			return nil
		}
		for i := range list {
			list[i].ClusterID = clusterID
		}
		return tx.CreateInBatches(list, 100).Error
	})
}

// ReplaceVariables 用实时结果整体替换某集群的参数缓存。
//
// 与 ReplaceSessions 同理：只动 variables 表，不影响集群快照其余部分。
func (r *TiDBObjRepo) ReplaceVariables(ctx context.Context, clusterID int64, list []model.Variable) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("cluster_id = ?", clusterID).Delete(&model.Variable{}).Error; err != nil {
			return err
		}
		if len(list) == 0 {
			return nil
		}
		for i := range list {
			list[i].ClusterID = clusterID
		}
		return tx.CreateInBatches(list, 200).Error
	})
}

// ListResourceGroups 查询 Resource Group 快照。
func (r *TiDBObjRepo) ListResourceGroups(ctx context.Context, clusterID int64) ([]model.ResourceGroup, error) {
	var list []model.ResourceGroup
	err := r.db.WithContext(ctx).Where("cluster_id = ?", clusterID).Order("name ASC").Find(&list).Error
	return list, err
}

// ReplaceResourceGroups 用实时结果整体替换某集群的 Resource Group 缓存。
//
// 与 ReplaceSessions / ReplaceVariables 同理：只动 resource_groups 表，
// 让「Resource Group」页刷新后能立刻看到新建 / 修改 / 删除的结果。
func (r *TiDBObjRepo) ReplaceResourceGroups(ctx context.Context, clusterID int64, list []model.ResourceGroup) error {
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("cluster_id = ?", clusterID).Delete(&model.ResourceGroup{}).Error; err != nil {
			return err
		}
		if len(list) == 0 {
			return nil
		}
		for i := range list {
			list[i].ClusterID = clusterID
		}
		return tx.CreateInBatches(list, 100).Error
	})
}

// CountUsersByResourceGroup 统计绑定到某 RG 的用户数。
func (r *TiDBObjRepo) CountUsersByResourceGroup(ctx context.Context, clusterID int64, rg string) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&model.TiDBUser{}).
		Where("cluster_id = ? AND resource_group = ?", clusterID, rg).Count(&n).Error
	return n, err
}

// ListUsersByResourceGroup 列出绑定到某 RG 的用户。
func (r *TiDBObjRepo) ListUsersByResourceGroup(ctx context.Context, clusterID int64, rg string) ([]model.TiDBUser, error) {
	var list []model.TiDBUser
	err := r.db.WithContext(ctx).Where("cluster_id = ? AND resource_group = ?", clusterID, rg).Order("user_name ASC").Find(&list).Error
	return list, err
}

// UpsertUser 写入 / 更新单个用户快照（DDL 执行后局部刷新，避免全量重采）。
func (r *TiDBObjRepo) UpsertUser(ctx context.Context, u *model.TiDBUser) error {
	if u.FetchedAt.IsZero() {
		u.FetchedAt = time.Now()
	}
	u.CreatedAt = time.Now()
	var existing model.TiDBUser
	err := r.db.WithContext(ctx).
		Where("cluster_id = ? AND user_name = ? AND host = ?", u.ClusterID, u.UserName, u.Host).
		First(&existing).Error
	if err == gorm.ErrRecordNotFound {
		return r.db.WithContext(ctx).Create(u).Error
	}
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&model.TiDBUser{}).Where("id = ?", existing.ID).Updates(map[string]any{
		"auth_plugin":      u.AuthPlugin,
		"account_locked":   u.AccountLocked,
		"password_expired": u.PasswordExpired,
		"resource_group":   u.ResourceGroup,
		"fetched_at":       u.FetchedAt,
	}).Error
}

// DeleteUserRow 删除单个用户快照。
func (r *TiDBObjRepo) DeleteUserRow(ctx context.Context, clusterID, id int64) error {
	return r.db.WithContext(ctx).Where("cluster_id = ? AND id = ?", clusterID, id).Delete(&model.TiDBUser{}).Error
}

// UpdateUserResourceGroup 更新用户快照绑定的 Resource Group。
func (r *TiDBObjRepo) UpdateUserResourceGroup(ctx context.Context, clusterID int64, userName, host, rg string) error {
	return r.db.WithContext(ctx).Model(&model.TiDBUser{}).
		Where("cluster_id = ? AND user_name = ? AND host = ?", clusterID, userName, host).
		Updates(map[string]any{"resource_group": rg, "fetched_at": time.Now()}).Error
}

// UpdateVariableValue 更新单个参数快照的当前值。
func (r *TiDBObjRepo) UpdateVariableValue(ctx context.Context, clusterID int64, name, value string, differs bool) error {
	d := 0
	if differs {
		d = 1
	}
	return r.db.WithContext(ctx).Model(&model.Variable{}).
		Where("cluster_id = ? AND name = ?", clusterID, name).
		Updates(map[string]any{"value": value, "differs": d, "fetched_at": time.Now()}).Error
}
