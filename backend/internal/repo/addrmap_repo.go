package repo

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/tmp-platform/backend/internal/model"
)

// AddrMapRepo 集群地址映射仓储。
type AddrMapRepo struct {
	db *gorm.DB
}

// NewAddrMapRepo 构造仓储。
func NewAddrMapRepo(db *gorm.DB) *AddrMapRepo { return &AddrMapRepo{db: db} }

// List 返回集群下全部规则（含停用），按源地址排序。
func (r *AddrMapRepo) List(ctx context.Context, clusterID int64) ([]model.ClusterAddrMap, error) {
	var list []model.ClusterAddrMap
	err := r.db.WithContext(ctx).
		Where("cluster_id = ?", clusterID).
		Order("source ASC, id ASC").
		Find(&list).Error
	return list, err
}

// ListEnabled 返回集群下启用的规则（构建映射器用）。
func (r *AddrMapRepo) ListEnabled(ctx context.Context, clusterID int64) ([]model.ClusterAddrMap, error) {
	var list []model.ClusterAddrMap
	err := r.db.WithContext(ctx).
		Where("cluster_id = ? AND enabled = 1", clusterID).
		Order("source ASC, id ASC").
		Find(&list).Error
	return list, err
}

// ListAllEnabled 返回所有集群的启用规则（主机列表批量标注用，一次查询避免 N+1）。
func (r *AddrMapRepo) ListAllEnabled(ctx context.Context) ([]model.ClusterAddrMap, error) {
	var list []model.ClusterAddrMap
	err := r.db.WithContext(ctx).
		Where("enabled = 1").
		Order("cluster_id ASC, source ASC").
		Find(&list).Error
	return list, err
}

// GetByID 读取单条规则；不存在返回 ErrNotFound。
func (r *AddrMapRepo) GetByID(ctx context.Context, id int64) (*model.ClusterAddrMap, error) {
	var m model.ClusterAddrMap
	err := r.db.WithContext(ctx).Where("id = ?", id).First(&m).Error
	if err == gorm.ErrRecordNotFound {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &m, nil
}

// Create 写入规则并回填自增 ID。
func (r *AddrMapRepo) Create(ctx context.Context, m *model.ClusterAddrMap) error {
	return r.db.WithContext(ctx).Create(m).Error
}

// UpdateFields 局部更新规则。
func (r *AddrMapRepo) UpdateFields(ctx context.Context, id int64, fields map[string]any) error {
	fields["updated_at"] = time.Now()
	return r.db.WithContext(ctx).Model(&model.ClusterAddrMap{}).
		Where("id = ?", id).Updates(fields).Error
}

// Delete 删除规则（映射是配置数据，物理删除，不留软删版本）。
func (r *AddrMapRepo) Delete(ctx context.Context, id int64) error {
	return r.db.WithContext(ctx).Where("id = ?", id).Delete(&model.ClusterAddrMap{}).Error
}

// CountBySource 统计同集群下相同源地址的规则数（excludeID > 0 时排除自身，用于更新判重）。
func (r *AddrMapRepo) CountBySource(ctx context.Context, clusterID int64, source string, excludeID int64) (int64, error) {
	q := r.db.WithContext(ctx).Model(&model.ClusterAddrMap{}).
		Where("cluster_id = ? AND source = ?", clusterID, source)
	if excludeID > 0 {
		q = q.Where("id <> ?", excludeID)
	}
	var n int64
	err := q.Count(&n).Error
	return n, err
}
