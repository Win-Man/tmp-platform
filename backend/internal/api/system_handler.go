package api

import (
	"sort"

	"github.com/gin-gonic/gin"

	"github.com/tmp-platform/backend/internal/job"
	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/repo"
	"github.com/tmp-platform/backend/internal/service"
)

// ---------------------------------------------------------------------------
// 总览
// ---------------------------------------------------------------------------

// OverviewSummary GET /api/v1/overview/summary
func (h *Handlers) OverviewSummary(c *gin.Context) {
	res, err := h.Overview.Summary(c.Request.Context())
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, res)
}

// OverviewHealth GET /api/v1/overview/health-distribution
func (h *Handlers) OverviewHealth(c *gin.Context) {
	res, err := h.Overview.HealthDistribution(c.Request.Context())
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, res)
}

// OverviewRecentJobs GET /api/v1/overview/recent-jobs
func (h *Handlers) OverviewRecentJobs(c *gin.Context) {
	limit := int(Int64Query(c, "limit"))
	res, err := h.Overview.RecentJobs(c.Request.Context(), limit)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, res)
}

// OverviewLatestInspection GET /api/v1/overview/latest-inspection
func (h *Handlers) OverviewLatestInspection(c *gin.Context) {
	res, err := h.Overview.LatestInspection(c.Request.Context())
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, res)
}

// OverviewCollectStatus GET /api/v1/overview/collect-status
func (h *Handlers) OverviewCollectStatus(c *gin.Context) {
	res, err := h.Overview.CollectStatus(c.Request.Context())
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, res)
}

// ---------------------------------------------------------------------------
// 枚举字典
// ---------------------------------------------------------------------------

// Enums GET /api/v1/meta/enums
func (h *Handlers) Enums(c *gin.Context) {
	types := make([]string, 0, len(job.TypeLabels))
	for t := range job.TypeLabels {
		types = append(types, t)
	}
	sort.Strings(types)
	OK(c, service.Enums(types))
}

// ---------------------------------------------------------------------------
// 系统设置
// ---------------------------------------------------------------------------

// GetCollectionSettings GET /api/v1/settings/collection
func (h *Handlers) GetCollectionSettings(c *gin.Context) {
	res, err := h.Settings.GetCollection(c.Request.Context())
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, res)
}

// UpdateCollectionSettings PUT /api/v1/settings/collection
func (h *Handlers) UpdateCollectionSettings(c *gin.Context) {
	var in service.CollectionSettings
	if !BindJSON(c, &in) {
		return
	}
	u := currentUser(c)
	var by *int64
	if u != nil {
		id := u.ID
		by = &id
	}
	res, err := h.Settings.UpdateCollection(c.Request.Context(), in, by)
	if err != nil {
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditSettingUpdate, "setting", "collection", "采集配置", "success", map[string]any{
		"intervalSec":   in.IntervalSec,
		"timeoutSec":    in.TimeoutSec,
		"concurrency":   in.Concurrency,
		"retry":         in.Retry,
		"sshTimeoutSec": in.SSHTimeoutSec,
		"cpuThreshold":  in.ThresholdCPUPct,
		"diskThreshold": in.ThresholdDiskPct,
	})
	OK(c, res)
}

// GetDemoMode GET /api/v1/settings/demo-mode
func (h *Handlers) GetDemoMode(c *gin.Context) {
	OK(c, h.Settings.GetDemoMode(c.Request.Context()))
}

// SetDemoMode PUT /api/v1/settings/demo-mode
func (h *Handlers) SetDemoMode(c *gin.Context) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if !BindJSON(c, &in) {
		return
	}
	u := currentUser(c)
	var by *int64
	if u != nil {
		id := u.ID
		by = &id
	}
	res, err := h.Settings.SetDemoMode(c.Request.Context(), in.Enabled, by)
	if err != nil {
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditDemoMode, "setting", "demo-mode", "演示模式", "success",
		map[string]any{"enabled": in.Enabled})
	OK(c, res)
}

// About GET /api/v1/settings/about
func (h *Handlers) About(c *gin.Context) {
	res, err := h.Settings.About(c.Request.Context())
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, res)
}

// ---------------------------------------------------------------------------
// 审计日志
// ---------------------------------------------------------------------------

// auditFilter 由查询参数构造审计筛选条件。
func auditFilter(c *gin.Context) repo.AuditFilter {
	return repo.AuditFilter{
		Username:     rawQuery(c, "username"),
		Action:       rawQuery(c, "action"),
		ResourceType: rawQuery(c, "resourceType"),
		ResourceID:   rawQuery(c, "resourceId"),
		Result:       rawQuery(c, "result"),
		From:         TimeQuery(c, "from"),
		To:           TimeQuery(c, "to"),
		Page:         PageParams(c),
	}
}

// ListAuditLogs GET /api/v1/audit-logs
func (h *Handlers) ListAuditLogs(c *gin.Context) {
	f := auditFilter(c)
	list, total, err := h.Audit.List(c.Request.Context(), f)
	if err != nil {
		Fail(c, err)
		return
	}
	if list == nil {
		list = []model.AuditLog{}
	}
	OK(c, repo.NewListResult(list, total, f.Page))
}

// ExportAuditLogs GET /api/v1/audit-logs/export
func (h *Handlers) ExportAuditLogs(c *gin.Context) {
	f := auditFilter(c)
	data, filename, err := h.Audit.ExportCSV(c.Request.Context(), f)
	if err != nil {
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditAuditExport, "audit_log", "", filename, "success", nil)
	attachment(c, filename, "text/csv; charset=utf-8", data)
}

// ---------------------------------------------------------------------------
// 凭据管理
// ---------------------------------------------------------------------------

// ListCredentials GET /api/v1/settings/credentials
func (h *Handlers) ListCredentials(c *gin.Context) {
	list, err := h.Creds.List(c.Request.Context())
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, list)
}

// UpdateCredential PUT /api/v1/settings/credentials/:clusterId
func (h *Handlers) UpdateCredential(c *gin.Context) {
	id, ok := Int64Param(c, "clusterId")
	if !ok {
		return
	}
	var in service.CredentialUpdateInput
	if !BindJSON(c, &in) {
		return
	}
	u := currentUser(c)
	var by *int64
	if u != nil {
		uid := u.ID
		by = &uid
	}
	if err := h.Creds.Update(c.Request.Context(), id, in, by); err != nil {
		Fail(c, err)
		return
	}
	// 审计只记录「更新了哪几类凭据」，绝不记录明文。
	detail := map[string]any{
		"sshPasswordUpdated": in.SSHPassword != "",
		"sshKeyUpdated":      in.SSHKey != "",
		"dbPasswordUpdated":  in.DBPassword != "",
	}
	h.RecordAudit(c, service.AuditCredential, "credential", itoa(id), "", "success", detail)
	NoContent(c)
}

// TestCredential POST /api/v1/settings/credentials/:clusterId/test
func (h *Handlers) TestCredential(c *gin.Context) {
	id, ok := Int64Param(c, "clusterId")
	if !ok {
		return
	}
	results, err := h.Creds.Test(c.Request.Context(), id)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, gin.H{"results": results, "mock": h.MockMode})
}
