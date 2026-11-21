package service

import (
	"context"
	"errors"

	"github.com/tmp-platform/backend/internal/collector"
	"github.com/tmp-platform/backend/internal/crypto"
	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/pkg/apperr"
	"github.com/tmp-platform/backend/internal/repo"
)

// CredentialService 凭据管理服务（仅 admin 可写，接口一律返回掩码）。
type CredentialService struct {
	creds    *repo.CredentialRepo
	clusters *repo.ClusterRepo
	cipher   *crypto.Cipher
	mode     *ModeManager
}

// NewCredentialService 构造凭据服务。
func NewCredentialService(creds *repo.CredentialRepo, clusters *repo.ClusterRepo, cipher *crypto.Cipher, mode *ModeManager) *CredentialService {
	return &CredentialService{creds: creds, clusters: clusters, cipher: cipher, mode: mode}
}

// List 按集群列出凭据掩码信息。
func (s *CredentialService) List(ctx context.Context) ([]repo.CredentialView, error) {
	list, err := s.creds.ListWithCluster(ctx)
	if err != nil {
		return nil, apperr.Internal("查询凭据列表失败")
	}
	if list == nil {
		list = []repo.CredentialView{}
	}
	return list, nil
}

// UpdateInput 更新凭据入参（必须重输明文）。
type CredentialUpdateInput struct {
	SSHPassword string `json:"sshPassword"`
	SSHKey      string `json:"sshKey"`
	DBPassword  string `json:"dbPassword"`
}

// Update 更新集群凭据；仅覆盖本次提交的非空字段。
func (s *CredentialService) Update(ctx context.Context, clusterID int64, in CredentialUpdateInput, by *int64) error {
	if _, err := s.clusters.GetByID(ctx, clusterID); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return apperr.NotFound("集群不存在")
		}
		return apperr.Internal("查询集群失败")
	}
	existing, err := s.creds.GetByClusterID(ctx, clusterID)
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return apperr.Internal("读取凭据失败")
	}
	cred := &model.ClusterCredential{ClusterID: clusterID, KeyVersion: 1, UpdatedBy: by}
	if existing != nil {
		cred.SSHPasswordEnc = existing.SSHPasswordEnc
		cred.SSHKeyEnc = existing.SSHKeyEnc
		cred.DBPasswordEnc = existing.DBPasswordEnc
		cred.KeyVersion = existing.KeyVersion
	}
	if in.SSHPassword != "" {
		enc, eerr := s.cipher.Encrypt(in.SSHPassword)
		if eerr != nil {
			return apperr.Internal("OS 凭据加密失败")
		}
		cred.SSHPasswordEnc = enc
	}
	if in.SSHKey != "" {
		enc, eerr := s.cipher.Encrypt(in.SSHKey)
		if eerr != nil {
			return apperr.Internal("SSH 私钥加密失败")
		}
		cred.SSHKeyEnc = enc
	}
	if in.DBPassword != "" {
		enc, eerr := s.cipher.Encrypt(in.DBPassword)
		if eerr != nil {
			return apperr.Internal("数据库凭据加密失败")
		}
		cred.DBPasswordEnc = enc
	}
	if err := s.creds.Upsert(ctx, cred); err != nil {
		return apperr.Internal("保存凭据失败")
	}
	return nil
}

// Test 用已存凭据测试连通性。
func (s *CredentialService) Test(ctx context.Context, clusterID int64) ([]collector.ProbeResult, error) {
	c, err := s.clusters.GetByID(ctx, clusterID)
	if err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			return nil, apperr.NotFound("集群不存在")
		}
		return nil, apperr.Internal("查询集群失败")
	}
	cred, err := s.creds.GetByClusterID(ctx, clusterID)
	if err != nil && !errors.Is(err, repo.ErrNotFound) {
		return nil, apperr.Internal("读取凭据失败")
	}
	target := collector.NewTarget(c, cred, s.cipher)
	probeCtx, cancel := context.WithTimeout(ctx, testConnectionTimeout)
	defer cancel()
	results, err := s.mode.Current().Probe(probeCtx, target,
		[]collector.ProbeScope{collector.ScopeSSH, collector.ScopeMySQL, collector.ScopePD})
	if err != nil {
		return nil, apperr.CollectFailed("连通性测试失败: "+err.Error(), nil)
	}
	return results, nil
}
