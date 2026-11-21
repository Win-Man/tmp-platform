package job

import (
	"context"
	"log/slog"
	"time"

	"github.com/tmp-platform/backend/internal/repo"
)

// Recover 进程重启恢复（ARCHITECTURE §7.4）。必须完成三件事：
//
//  1. running → interrupted，其 running 步骤 → failed；
//  2. pending 任务重新入队（视为同一任务继续，不写 retry_of）；
//  3. clusters.collecting 全部重置为 0，避免刷新永久"复用"死任务。
//
// 顺序不可颠倒：先重置 collecting，再重新入队，最后才可能被 worker 置为 running。
func Recover(ctx context.Context, jobs *repo.JobRepo, clusters *repo.ClusterRepo, eng *Engine, log *slog.Logger) error {
	// 1) 残留 running → interrupted
	affected, err := jobs.MarkRunningInterrupted(ctx, "进程重启，任务中断")
	if err != nil {
		return err
	}
	if affected > 0 {
		log.Warn("检测到中断的任务", "count", affected, "action", "running→interrupted")
	}

	// 2) pending 重新入队
	ids, err := jobs.PendingIDs(ctx)
	if err != nil {
		return err
	}
	requeued := 0
	for _, id := range ids {
		if err := eng.Requeue(ctx, id); err != nil {
			log.Warn("重新入队失败", "job_id", id, "error", err)
			// 入队失败则直接标记 interrupted，避免永久 pending 占位。
			_ = jobs.UpdateFields(ctx, id, map[string]any{
				"status":        "interrupted",
				"error_summary": "进程重启后重新入队失败",
				"finished_at":   time.Now(),
			})
			continue
		}
		requeued++
	}
	if requeued > 0 {
		log.Info("已重新入队 pending 任务", "count", requeued)
	}

	// 3) 重置 collecting 并发标记
	reset, err := clusters.ResetAllCollecting(ctx)
	if err != nil {
		return err
	}
	if reset > 0 {
		log.Info("已重置采集并发标记", "count", reset)
	}
	return nil
}
