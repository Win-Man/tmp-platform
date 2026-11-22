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

// identTestService 建一个只含集群相关表的最小服务实例。
//
// 只测「集群唯一键 = (中控机, 集群名)」这条规则，因此走 SaveAsDraft=true 的接管路径：
// 该路径不触发连接测试凭证校验、不发现 PD、不提交任务引擎，
// 依赖被压缩到 ClusterRepo + CredentialRepo 两个仓库，不需要构造 job.Engine。
func identTestService(t *testing.T) *ClusterService {
	t.Helper()
	path := filepath.Join(t.TempDir(), "identity.db")
	gdb, err := gorm.Open(sqlite.Open(path), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("打开临时库失败: %v", err)
	}
	if err := gdb.AutoMigrate(&model.Cluster{}, &model.ClusterCredential{}, &model.Host{}, &model.Instance{}); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	return NewClusterService(ClusterDeps{
		Clusters:  repo.NewClusterRepo(gdb),
		Instances: repo.NewInstanceRepo(gdb),
		Hosts:     repo.NewHostRepo(gdb),
		Creds:     repo.NewCredentialRepo(gdb),
	})
}

// draftInput 构造一次「仅存草稿」的接管入参。
func draftInput(name, tiupHost string) TakeoverInput {
	return TakeoverInput{
		Name:        name,
		TiUPHost:    tiupHost,
		TiUPPort:    22,
		TiUPUser:    "tidb",
		DBHost:      tiupHost,
		DBPort:      4000,
		DBUser:      "root",
		SaveAsDraft: true,
	}
}

// TestTakeover_SameNameDifferentTiUPHost 验证同名集群可以在不同中控机上共存。
//
// 业务场景：同一批机器上部署多套 TiDB 集群，各自有独立的中控机，
// 集群名重复是完全合法的部署形态（TiUP 的集群名只在单台中控机内唯一）。
func TestTakeover_SameNameDifferentTiUPHost(t *testing.T) {
	svc := identTestService(t)
	ctx := context.Background()

	first, err := svc.Takeover(ctx, draftInput("tidb-prod", "10.0.0.11"), nil)
	if err != nil {
		t.Fatalf("第一台中控机接管失败: %v", err)
	}
	if first.ClusterID == 0 || !first.Draft {
		t.Fatalf("接管结果异常: %+v", first)
	}

	// 不同中控机 + 同名（含大小写不同）→ 允许。
	second, err := svc.Takeover(ctx, draftInput("TiDB-Prod", "10.0.0.12"), nil)
	if err != nil {
		t.Fatalf("不同中控机的同名集群应允许创建: %v", err)
	}
	if second.ClusterID == first.ClusterID {
		t.Fatalf("两次接管不应落到同一条记录: %+v", second)
	}

	// 同一中控机 + 同名（大小写不敏感）→ 40001。
	_, err = svc.Takeover(ctx, draftInput("TIDB-PROD", "10.0.0.11"), nil)
	if err == nil {
		t.Fatal("同一中控机上重名集群应被拒绝")
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("错误类型 = %T, 期望 *apperr.Error: %v", err, err)
	}
	if ae.Code != apperr.CodeBadRequest {
		t.Errorf("错误码 = %d, 期望 %d（40001）", ae.Code, apperr.CodeBadRequest)
	}
	if ae.Message != "该中控机上已存在同名集群「TIDB-PROD」（不区分大小写）" {
		t.Errorf("错误文案 = %q, 期望点明「该中控机上」", ae.Message)
	}
}

// TestUpdate_RenameUniquenessByTargetHost 验证重命名时的唯一性按「本次生效的中控机」判断。
func TestUpdate_RenameUniquenessByTargetHost(t *testing.T) {
	svc := identTestService(t)
	ctx := context.Background()

	hostA, err := svc.Takeover(ctx, draftInput("tidb-a", "10.0.0.21"), nil)
	if err != nil {
		t.Fatalf("接管 tidb-a 失败: %v", err)
	}
	hostB, err := svc.Takeover(ctx, draftInput("tidb-b", "10.0.0.22"), nil)
	if err != nil {
		t.Fatalf("接管 tidb-b 失败: %v", err)
	}
	// 第三台中控机上也叫 tidb-a（合法）。
	if _, err := svc.Takeover(ctx, draftInput("tidb-a", "10.0.0.23"), nil); err != nil {
		t.Fatalf("第三台中控机接管 tidb-a 失败: %v", err)
	}

	// 场景 1：把 10.0.0.22 上的 tidb-b 改名为 tidb-a —— 同一中控机上无冲突，允许。
	if _, err := svc.Update(ctx, hostB.ClusterID, UpdateInput{Name: "tidb-a"}); err != nil {
		t.Fatalf("同一中控机内无冲突的重命名应允许: %v", err)
	}

	// 场景 2：把 10.0.0.22 上的集群（现名 tidb-a）改名为 tidb-a 之外的名字，
	// 但目标中控机填 10.0.0.21（那里已有 tidb-a）—— 冲突应被拒绝。
	_, err = svc.Update(ctx, hostB.ClusterID, UpdateInput{Name: "tidb-a", TiUPHost: "10.0.0.21"})
	if err == nil {
		t.Fatal("搬到已有同名集群的中控机应被拒绝")
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) || ae.Code != apperr.CodeBadRequest {
		t.Fatalf("错误 = %v, 期望 40001", err)
	}

	// 场景 3：改成一个该中控机上不存在的名字 —— 允许。
	if _, err := svc.Update(ctx, hostB.ClusterID, UpdateInput{Name: "tidb-c", TiUPHost: "10.0.0.21"}); err != nil {
		t.Fatalf("目标中控机无同名集群时应允许: %v", err)
	}

	// 场景 4：把自己改回自己的名字（大小写变化）不应与自己冲突。
	if _, err := svc.Update(ctx, hostA.ClusterID, UpdateInput{Name: "TIDB-A"}); err != nil {
		t.Fatalf("仅改变大小写的自身重命名应允许: %v", err)
	}
}
