package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/tmp-platform/backend/internal/db"
	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/pkg/apperr"
	"github.com/tmp-platform/backend/internal/repo"
)

// svcTestDB 在 t.TempDir 下建立独立 SQLite 库。
func svcTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "qa.db")
	gdb, err := db.Open(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("打开临时库失败: %v", err)
	}
	if err := db.Migrate(gdb); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
	sqlDB, err := gdb.DB()
	if err != nil {
		t.Fatalf("获取连接失败: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return gdb
}

// createJob 直接落库一个指定状态的任务。
func createJob(t *testing.T, gdb *gorm.DB, status string) *model.Job {
	t.Helper()
	job := &model.Job{
		Type:      "collect",
		Status:    status,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := gdb.Create(job).Error; err != nil {
		t.Fatalf("创建任务失败: %v", err)
	}
	return job
}

// assertAppErr 断言错误是带指定业务码的领域错误。
func assertAppErr(t *testing.T, err error, wantCode int) {
	t.Helper()
	if err == nil {
		t.Fatalf("期望返回错误 %d，实际 nil", wantCode)
	}
	var ae *apperr.Error
	if !errors.As(err, &ae) {
		t.Fatalf("错误类型不是 *apperr.Error: %v", err)
	}
	if ae.Code != wantCode {
		t.Fatalf("业务码 = %d, want %d（message=%s）", ae.Code, wantCode, ae.Message)
	}
	if ae.Message == "" {
		t.Fatal("错误 message 不能为空（需可读）")
	}
}

// TestJobStateMachine_RetryOnlyFromTerminalFailure 断言任务状态机的非法跃迁被拒绝。
//
// 合法：failed / canceled / interrupted → 新建任务重试。
// 非法：pending / running / success → 40901（例如 success→running 必须被拒绝）。
func TestJobStateMachine_RetryOnlyFromTerminalFailure(t *testing.T) {
	gdb := svcTestDB(t)
	svc := NewJobService(repo.NewJobRepo(gdb))
	ctx := context.Background()

	// 非法跃迁：success / running / pending 均不可重试。
	for _, status := range []string{
		model.JobStatusSuccess,
		model.JobStatusRunning,
		model.JobStatusPending,
	} {
		job := createJob(t, gdb, status)
		_, err := svc.Retry(ctx, job.ID, nil)
		assertAppErr(t, err, apperr.CodeConflict)

		// 原任务状态不得被改动（success 不能因重试变成 running）。
		var got model.Job
		if err := gdb.First(&got, job.ID).Error; err != nil {
			t.Fatalf("读取任务失败: %v", err)
		}
		if got.Status != status {
			t.Fatalf("重试被拒后任务状态应由 %q 保持，实际 %q", status, got.Status)
		}
	}
}

// TestJobStateMachine_CancelIllegalOnFinished 已结束任务取消必须被拒绝。
func TestJobStateMachine_CancelIllegalOnFinished(t *testing.T) {
	gdb := svcTestDB(t)
	svc := NewJobService(repo.NewJobRepo(gdb))
	ctx := context.Background()

	for _, status := range []string{
		model.JobStatusSuccess,
		model.JobStatusFailed,
		model.JobStatusCanceled,
		model.JobStatusInterrupted,
	} {
		job := createJob(t, gdb, status)
		err := svc.Cancel(ctx, job.ID)
		assertAppErr(t, err, apperr.CodeConflict)

		var got model.Job
		if err := gdb.First(&got, job.ID).Error; err != nil {
			t.Fatalf("读取任务失败: %v", err)
		}
		if got.Status != status {
			t.Fatalf("成功拒绝取消后状态应保持 %q，实际 %q", status, got.Status)
		}
	}
}

// TestJobStateMachine_CancelPending 排队中任务取消 → 直接置 canceled 且 finished_at 非空。
func TestJobStateMachine_CancelPending(t *testing.T) {
	gdb := svcTestDB(t)
	svc := NewJobService(repo.NewJobRepo(gdb))
	ctx := context.Background()

	job := createJob(t, gdb, model.JobStatusPending)
	if err := svc.Cancel(ctx, job.ID); err != nil {
		t.Fatalf("取消排队中任务失败: %v", err)
	}
	var got model.Job
	if err := gdb.First(&got, job.ID).Error; err != nil {
		t.Fatalf("读取任务失败: %v", err)
	}
	if got.Status != model.JobStatusCanceled {
		t.Fatalf("状态 = %q, want canceled", got.Status)
	}
	if got.CancelRequested != 1 {
		t.Fatalf("cancel_requested = %d, want 1", got.CancelRequested)
	}
	if got.FinishedAt == nil {
		t.Fatal("canceled 任务必须写入 finished_at")
	}
}

// TestJobStateMachine_CancelRunning 进行中任务取消 → 仅置 cancel_requested，状态仍 running。
func TestJobStateMachine_CancelRunning(t *testing.T) {
	gdb := svcTestDB(t)
	svc := NewJobService(repo.NewJobRepo(gdb))
	ctx := context.Background()

	job := createJob(t, gdb, model.JobStatusRunning)
	if err := svc.Cancel(ctx, job.ID); err != nil {
		t.Fatalf("取消进行中任务失败: %v", err)
	}
	var got model.Job
	if err := gdb.First(&got, job.ID).Error; err != nil {
		t.Fatalf("读取任务失败: %v", err)
	}
	if got.CancelRequested != 1 {
		t.Fatalf("cancel_requested = %d, want 1", got.CancelRequested)
	}
	if got.Status != model.JobStatusRunning {
		t.Fatalf("进行中任务取消后状态应仍为 running（由 worker 收尾），实际 %q", got.Status)
	}
}

// TestJobStateMachine_NotFound 不存在的任务返回 40401。
func TestJobStateMachine_NotFound(t *testing.T) {
	gdb := svcTestDB(t)
	svc := NewJobService(repo.NewJobRepo(gdb))
	ctx := context.Background()

	_, err := svc.Retry(ctx, 99999, nil)
	assertAppErr(t, err, apperr.CodeNotFound)
	assertAppErr(t, svc.Cancel(ctx, 99999), apperr.CodeNotFound)
}
