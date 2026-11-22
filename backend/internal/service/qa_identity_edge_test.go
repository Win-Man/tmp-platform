package service

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/pkg/apperr"
	"github.com/tmp-platform/backend/internal/repo"
)

// QA 补充用例（独立于实现者）：验证集群唯一键在「只改大小写」这一边界上的两个方向。
//
// 唯一键是 (tiup_host, name_lower)，而 Update 的唯一性校验必须排除「自己」。
// 这里同时锁住两个方向，避免出现「排自己排过头」或「干脆不校验」两种退化实现：
//   - 正的：把自己从 tidb-demo 改成 TiDB-Demo（name_lower 不变）不得判成与自己冲突；
//   - 反的：把同一中控机上的另一套集群改成 TIDB-DEMO 仍必须被拒绝。

// qaIdentityService 返回一个最小可用的 ClusterService 与其底层库，便于直接读回落库结果。
func qaIdentityService(t *testing.T) (*ClusterService, *gorm.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "qa_identity.db")
	gdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开临时库失败: %v", err)
	}
	if err := gdb.AutoMigrate(&model.Cluster{}, &model.ClusterCredential{}, &model.Host{}, &model.Instance{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	svc := NewClusterService(ClusterDeps{
		Clusters:  repo.NewClusterRepo(gdb),
		Instances: repo.NewInstanceRepo(gdb),
		Hosts:     repo.NewHostRepo(gdb),
		Creds:     repo.NewCredentialRepo(gdb),
	})
	return svc, gdb
}

// TestUpdate_CaseOnlyRenameIsNotSelfConflict 验证「只改大小写」的重命名不与自身冲突。
func TestUpdate_CaseOnlyRenameIsNotSelfConflict(t *testing.T) {
	svc, gdb := qaIdentityService(t)
	ctx := context.Background()

	created, err := svc.Takeover(ctx, draftInput("tidb-demo", "10.0.0.31"), nil)
	if err != nil {
		t.Fatalf("接管 tidb-demo 失败: %v", err)
	}

	// 同一台中控机上，"TiDB-Demo" 的 name_lower 与原名完全一致：不构成新冲突。
	if _, err := svc.Update(ctx, created.ClusterID, UpdateInput{Name: "TiDB-Demo"}); err != nil {
		t.Fatalf("仅改大小写的自身重命名不应失败，实际: %v", err)
	}

	var got model.Cluster
	if err := gdb.Where("id = ?", created.ClusterID).First(&got).Error; err != nil {
		t.Fatalf("读取集群失败: %v", err)
	}
	if got.Name != "TiDB-Demo" {
		t.Errorf("Name = %q, 期望 TiDB-Demo（展示名应保留新的大小写）", got.Name)
	}
	if got.NameLower != "tidb-demo" {
		t.Errorf("NameLower = %q, 期望 tidb-demo（唯一键列必须仍为小写）", got.NameLower)
	}
}

// TestUpdate_CaseOnlyRenameStillRejectsOtherCluster 验证排除「自己」不能退化成「不校验」。
func TestUpdate_CaseOnlyRenameStillRejectsOtherCluster(t *testing.T) {
	svc, _ := qaIdentityService(t)
	ctx := context.Background()

	first, err := svc.Takeover(ctx, draftInput("tidb-demo", "10.0.0.41"), nil)
	if err != nil {
		t.Fatalf("接管 tidb-demo 失败: %v", err)
	}
	other, err := svc.Takeover(ctx, draftInput("tidb-other", "10.0.0.41"), nil)
	if err != nil {
		t.Fatalf("接管 tidb-other 失败: %v", err)
	}

	// 同一中控机上把 tidb-other 改成 TIDB-DEMO：大小写不同但 name_lower 相同 → 必须拒绝。
	_, err = svc.Update(ctx, other.ClusterID, UpdateInput{Name: "TIDB-DEMO"})
	if err == nil {
		t.Fatalf("同一中控机上改成已存在的同名集群（大小写不同）应被拒绝，第一个集群 ID=%d", first.ClusterID)
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("错误类型 = %T, 期望 *apperr.Error: %v", err, err)
	}
	if ae.Code != apperr.CodeBadRequest {
		t.Errorf("错误码 = %d, 期望 %d（40001）", ae.Code, apperr.CodeBadRequest)
	}
}
