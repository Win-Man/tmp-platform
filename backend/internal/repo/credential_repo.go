package repo

import (
	"context"
	"errors"
	"time"

	"gorm.io/gorm"

	"github.com/tmp-platform/backend/internal/model"
)

// CredentialRepo 集群凭据数据访问（1 集群 1 份，密文存储）。
type CredentialRepo struct {
	db *gorm.DB
}

// NewCredentialRepo 构造仓库。
func NewCredentialRepo(db *gorm.DB) *CredentialRepo { return &CredentialRepo{db: db} }

// GetByClusterID 读取集群凭据；不存在返回 ErrNotFound。
func (r *CredentialRepo) GetByClusterID(ctx context.Context, clusterID int64) (*model.ClusterCredential, error) {
	var c model.ClusterCredential
	err := r.db.WithContext(ctx).Where("cluster_id = ?", clusterID).First(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// Upsert 写入或更新凭据。
func (r *CredentialRepo) Upsert(ctx context.Context, c *model.ClusterCredential) error {
	var existing model.ClusterCredential
	err := r.db.WithContext(ctx).Where("cluster_id = ?", c.ClusterID).First(&existing).Error
	now := time.Now()
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.CreatedAt = now
		c.UpdatedAt = now
		return r.db.WithContext(ctx).Create(c).Error
	}
	if err != nil {
		return err
	}
	return r.db.WithContext(ctx).Model(&model.ClusterCredential{}).Where("cluster_id = ?", c.ClusterID).Updates(map[string]any{
		"ssh_password_enc": c.SSHPasswordEnc,
		"ssh_key_enc":      c.SSHKeyEnc,
		"db_password_enc":  c.DBPasswordEnc,
		"key_version":      c.KeyVersion,
		"updated_by":       c.UpdatedBy,
		"updated_at":       now,
	}).Error
}

// ListWithCluster 按集群列出凭据（仅掩码信息，不含密文）。
type CredentialView struct {
	ClusterID   int64      `json:"clusterId"`
	ClusterName string     `json:"clusterName"`
	TiUPHost    string     `json:"tiupHost"`
	TiUPUser    string     `json:"tiupUser"`
	DBHost      string     `json:"dbHost"`
	DBPort      int        `json:"dbPort"`
	DBUser      string     `json:"dbUser"`
	HasSSHPwd   bool       `json:"hasSshPassword"`
	HasSSHKey   bool       `json:"hasSshKey"`
	HasDBPwd    bool       `json:"hasDbPassword"`
	UpdatedAt   *time.Time `json:"updatedAt"`
	UpdatedBy   *int64     `json:"updatedBy"`
}

// ListWithCluster 返回全部集群的凭据掩码视图。
func (r *CredentialRepo) ListWithCluster(ctx context.Context) ([]CredentialView, error) {
	var clusters []model.Cluster
	if err := r.db.WithContext(ctx).Order("id ASC").Find(&clusters).Error; err != nil {
		return nil, err
	}
	var creds []model.ClusterCredential
	if err := r.db.WithContext(ctx).Find(&creds).Error; err != nil {
		return nil, err
	}
	byCluster := make(map[int64]model.ClusterCredential, len(creds))
	for _, c := range creds {
		byCluster[c.ClusterID] = c
	}
	out := make([]CredentialView, 0, len(clusters))
	for _, cl := range clusters {
		v := CredentialView{
			ClusterID:   cl.ID,
			ClusterName: cl.Name,
			TiUPHost:    cl.TiUPHost,
			TiUPUser:    cl.TiUPUser,
			DBHost:      cl.DBHost,
			DBPort:      cl.DBPort,
			DBUser:      cl.DBUser,
		}
		if c, ok := byCluster[cl.ID]; ok {
			v.HasSSHPwd = c.SSHPasswordEnc != ""
			v.HasSSHKey = c.SSHKeyEnc != ""
			v.HasDBPwd = c.DBPasswordEnc != ""
			updated := c.UpdatedAt
			v.UpdatedAt = &updated
			v.UpdatedBy = c.UpdatedBy
		}
		out = append(out, v)
	}
	return out, nil
}
