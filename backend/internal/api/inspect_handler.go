package api

import (
	"github.com/gin-gonic/gin"

	"github.com/tmp-platform/backend/internal/repo"
	"github.com/tmp-platform/backend/internal/service"
)

// ---------------------------------------------------------------------------
// 巡检（F-50 ~ F-58）
// ---------------------------------------------------------------------------

// ListInspectTemplates GET /api/v1/inspection/templates
func (h *Handlers) ListInspectTemplates(c *gin.Context) {
	list, err := h.Inspect.Templates(c.Request.Context())
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, gin.H{"list": list})
}

// GetInspectTemplate GET /api/v1/inspection/templates/:id
func (h *Handlers) GetInspectTemplate(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	tpl, err := h.Inspect.TemplateDetail(c.Request.Context(), id)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, tpl)
}

// ListInspectItems GET /api/v1/inspection/items
func (h *Handlers) ListInspectItems(c *gin.Context) {
	items, err := h.Inspect.Items(c.Request.Context(), Int64Query(c, "templateId"), rawQuery(c, "category"))
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, gin.H{"list": items})
}

// StartInspectionRuns POST /api/v1/inspection/runs
func (h *Handlers) StartInspectionRuns(c *gin.Context) {
	var in service.StartRunInput
	if !BindJSON(c, &in) {
		return
	}
	results, err := h.Inspect.StartRuns(c.Request.Context(), in, currentUser(c))
	if err != nil {
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditInspectRun, "inspection", "", "", "success", map[string]any{
		"clusterIds": in.ClusterIDs, "templateId": in.TemplateID, "runs": len(results),
	})
	Accepted(c, gin.H{"results": results, "mock": h.Mode.IsMock()})
}

// GetInspectionRun GET /api/v1/inspection/runs/:id
func (h *Handlers) GetInspectionRun(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	detail, err := h.Inspect.GetRun(c.Request.Context(), id)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, detail)
}

// ListInspectionReports GET /api/v1/inspection/reports
func (h *Handlers) ListInspectionReports(c *gin.Context) {
	f := repo.ReportFilter{
		ClusterID: Int64Query(c, "clusterId"),
		Keyword:   rawQuery(c, "keyword"),
		From:      TimeQuery(c, "from"),
		To:        TimeQuery(c, "to"),
		Page:      PageParams(c),
	}
	res, err := h.Inspect.Reports(c.Request.Context(), f)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, res)
}

// GetInspectionReport GET /api/v1/inspection/reports/:id
func (h *Handlers) GetInspectionReport(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	detail, err := h.Inspect.GetReport(c.Request.Context(), id)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, detail)
}

// DownloadInspectionReport GET /api/v1/inspection/reports/:id/download
func (h *Handlers) DownloadInspectionReport(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	format := c.DefaultQuery("format", "html")
	data, filename, contentType, err := h.Inspect.Download(c.Request.Context(), id, format)
	if err != nil {
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditReportDownload, "inspection", itoa(id), filename, "success", map[string]any{"format": format})
	attachment(c, filename, contentType, data)
}

// ListSchedules GET /api/v1/inspection/schedules（P2 占位）
func (h *Handlers) ListSchedules(c *gin.Context) {
	OK(c, gin.H{"list": []any{}, "message": "定时巡检为 P2 能力，MVP 未启用"})
}

// CreateSchedule POST /api/v1/inspection/schedules（P2 占位）
func (h *Handlers) CreateSchedule(c *gin.Context) {
	Fail(c, conflicted("定时巡检为 P2 能力，MVP 未启用"))
}
