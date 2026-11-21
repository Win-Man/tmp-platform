package job

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/tmp-platform/backend/internal/db"
	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/repo"
)

// newTestDB 在 t.TempDir 下建立独立 SQLite 库并完成迁移。
func newTestDB(t *testing.T) *gorm.DB {
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

// discardLogger 静默 logger，避免测试输出噪声。
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError}))
}

// TestRecover_RunningToInterruptedAndCollectingReset 断言重启恢复三件事：
// running→interrupted、running 步骤→failed、clusters.collecting 全部归零。
func TestRecover_RunningToInterruptedAndCollectingReset(t *testing.T) {
	gdb := newTestDB(t)
	jobs := repo.NewJobRepo(gdb)
	clusters := repo.NewClusterRepo(gdb)
	eng := NewEngine(Deps{Repo: jobs, Clusters: clusters, Log: discardLogger()}, 1)
	ctx := context.Background()

	// 准备：一个 running 任务（含 1 个 running 步骤 + 1 个 pending 步骤）。
	runningJob := &model.Job{
		Type:        TypeCollect,
		Status:      model.JobStatusRunning,
		ClusterID:   int64Ptr(1),
		TargetLabel: "c1",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	steps := []model.JobStep{
		{Seq: stepSeqBase, Name: "能力探测", Status: model.StepStatusRunning, CreatedAt: time.Now()},
		{Seq: 2 * stepSeqBase, Name: "SSH 拓扑采集", Status: model.StepStatusPending, CreatedAt: time.Now()},
	}
	if err := jobs.CreateWithSteps(ctx, runningJob, steps); err != nil {
		t.Fatalf("创建任务失败: %v", err)
	}

	// 准备：一个 pending 任务（应被重新入队而不是置为 interrupted）。
	pendingJob := &model.Job{
		Type:      TypeCollect,
		Status:    model.JobStatusPending,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := jobs.CreateWithSteps(ctx, pendingJob, nil); err != nil {
		t.Fatalf("创建 pending 任务失败: %v", err)
	}

	// 准备：两个集群 collecting=1（模拟采集中进程被杀）。
	for _, name := range []string{"c1", "c2"} {
		c := &model.Cluster{
			Name:       name,
			NameLower:  name,
			TiUPHost:   "10.0.0.1",
			TiUPUser:   "tidb",
			Status:     model.ClusterStatusHealthy,
			Collecting: 1,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}
		if err := clusters.Create(ctx, c); err != nil {
			t.Fatalf("创建集群失败: %v", err)
		}
	}

	if err := Recover(ctx, jobs, clusters, eng, discardLogger()); err != nil {
		t.Fatalf("Recover 失败: %v", err)
	}

	// 1) running → interrupted，且 error 摘要非空
	got, err := jobs.GetByID(ctx, runningJob.ID)
	if err != nil {
		t.Fatalf("读取任务失败: %v", err)
	}
	if got.Status != model.JobStatusInterrupted {
		t.Fatalf("running 任务状态 = %q, want interrupted", got.Status)
	}
	if got.ErrorSummary == "" {
		t.Fatal("interrupted 任务必须写入 error_summary")
	}
	if got.FinishedAt == nil {
		t.Fatal("interrupted 任务必须写入 finished_at")
	}

	// 2) running 步骤 → failed，pending 步骤保持 pending
	gotSteps, err := jobs.Steps(ctx, runningJob.ID)
	if err != nil {
		t.Fatalf("读取步骤失败: %v", err)
	}
	if len(gotSteps) != 2 {
		t.Fatalf("步骤数 = %d, want 2", len(gotSteps))
	}
	if gotSteps[0].Status != model.StepStatusFailed {
		t.Fatalf("running 步骤应置为 failed，实际 %q", gotSteps[0].Status)
	}
	if gotSteps[1].Status != model.StepStatusPending {
		t.Fatalf("pending 步骤不应被改动，实际 %q", gotSteps[1].Status)
	}

	// 3) pending 任务重新入队（队列长度 ≥1），状态仍为 pending
	if eng.QueueLength() < 1 {
		t.Fatalf("pending 任务应被重新入队，当前队列长度 = %d", eng.QueueLength())
	}
	pendingGot, err := jobs.GetByID(ctx, pendingJob.ID)
	if err != nil {
		t.Fatalf("读取 pending 任务失败: %v", err)
	}
	if pendingGot.Status != model.JobStatusPending {
		t.Fatalf("重新入队的任务状态应仍为 pending，实际 %q", pendingGot.Status)
	}

	// 4) clusters.collecting 全部归零
	var remaining int64
	if err := gdb.Model(&model.Cluster{}).Where("collecting <> 0").Count(&remaining).Error; err != nil {
		t.Fatalf("统计 collecting 失败: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("Recover 后仍存在 %d 个 collecting<>0 的集群", remaining)
	}
}

// TestRecover_Idempotent 重复执行 Recover 不报错、不产生副作用。
func TestRecover_Idempotent(t *testing.T) {
	gdb := newTestDB(t)
	jobs := repo.NewJobRepo(gdb)
	clusters := repo.NewClusterRepo(gdb)
	eng := NewEngine(Deps{Repo: jobs, Clusters: clusters, Log: discardLogger()}, 1)
	ctx := context.Background()

	if err := Recover(ctx, jobs, clusters, eng, discardLogger()); err != nil {
		t.Fatalf("首次 Recover 失败: %v", err)
	}
	if err := Recover(ctx, jobs, clusters, eng, discardLogger()); err != nil {
		t.Fatalf("二次 Recover 失败（应幂等）: %v", err)
	}
}

// TestSpecOf_UnknownTypeFallsBack 未注册类型回退 collect 规格。
func TestSpecOf_UnknownTypeFallsBack(t *testing.T) {
	if IsKnownType("nope") {
		t.Fatal("nope 不应被判定为已知类型")
	}
	if got := SpecOf("nope"); got.Type != TypeCollect {
		t.Fatalf("未知类型应回退 collect 规格，实际 %q", got.Type)
	}
	for typ := range specs {
		spec := SpecOf(typ)
		if len(spec.Steps) == 0 {
			t.Fatalf("任务类型 %q 未定义步骤", typ)
		}
		if spec.Timeout <= 0 {
			t.Fatalf("任务类型 %q 超时配置非法", typ)
		}
	}
}

// TestEngineSubmit_UnknownTypeRejected 提交未知类型任务必须被拒绝。
func TestEngineSubmit_UnknownTypeRejected(t *testing.T) {
	gdb := newTestDB(t)
	jobs := repo.NewJobRepo(gdb)
	clusters := repo.NewClusterRepo(gdb)
	eng := NewEngine(Deps{Repo: jobs, Clusters: clusters, Log: discardLogger()}, 1)
	if _, err := eng.Submit(context.Background(), &model.Job{Type: "bogus"}); err == nil {
		t.Fatal("提交未知类型任务应返回错误")
	}
}

func int64Ptr(v int64) *int64 { return &v }
