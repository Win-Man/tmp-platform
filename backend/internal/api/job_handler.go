package api

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tmp-platform/backend/internal/pkg/apperr"
	"github.com/tmp-platform/backend/internal/repo"
	"github.com/tmp-platform/backend/internal/service"
)

// conflicted 快捷构造 40901。
func conflicted(msg string) error { return apperr.Conflict(msg) }

// ListJobs GET /api/v1/jobs
//
// 默认最近 7 天；支持 type/status/clusterId/mine/from/to 筛选。
func (h *Handlers) ListJobs(c *gin.Context) {
	from := TimeQuery(c, "from")
	to := TimeQuery(c, "to")
	if from == nil && to == nil {
		def := time.Now().AddDate(0, 0, -7)
		from = &def
	}
	f := repo.JobFilter{
		Type:      rawQuery(c, "type"),
		Status:    rawQuery(c, "status"),
		ClusterID: Int64Query(c, "clusterId"),
		Keyword:   rawQuery(c, "keyword"),
		From:      from,
		To:        to,
		Page:      PageParams(c),
	}
	if BoolQuery(c, "mine") {
		if u := currentUser(c); u != nil {
			f.MineBy = u.ID
		}
	}
	res, err := h.Jobs.List(c.Request.Context(), f)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, res)
}

// RunningJobCount GET /api/v1/jobs/running-count
func (h *Handlers) RunningJobCount(c *gin.Context) {
	n, err := h.Jobs.RunningCount(c.Request.Context())
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, gin.H{"count": n})
}

// GetJob GET /api/v1/jobs/:id
func (h *Handlers) GetJob(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	view, err := h.Jobs.Get(c.Request.Context(), id)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, view)
}

// JobSteps GET /api/v1/jobs/:id/steps
func (h *Handlers) JobSteps(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	steps, err := h.Jobs.Steps(c.Request.Context(), id)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, gin.H{"list": steps})
}

// JobLogs GET /api/v1/jobs/:id/logs
func (h *Handlers) JobLogs(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	afterSeq := Int64Query(c, "afterSeq")
	limit := int(Int64Query(c, "limit"))
	list, err := h.Jobs.Logs(c.Request.Context(), id, afterSeq, limit)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, gin.H{"list": list})
}

// RetryJob POST /api/v1/jobs/:id/retry
func (h *Handlers) RetryJob(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	jobID, err := h.Jobs.Retry(c.Request.Context(), id, currentUser(c))
	if err != nil {
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditJobRetry, "job", itoa(id), "", "success", map[string]any{"newJobId": jobID})
	Accepted(c, gin.H{"jobId": jobID})
}

// CancelJob POST /api/v1/jobs/:id/cancel
func (h *Handlers) CancelJob(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	if err := h.Jobs.Cancel(c.Request.Context(), id); err != nil {
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditJobCancel, "job", itoa(id), "", "success", nil)
	NoContent(c)
}
