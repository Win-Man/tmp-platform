package api

import (
	"github.com/gin-gonic/gin"

	"github.com/tmp-platform/backend/internal/repo"
	"github.com/tmp-platform/backend/internal/service"
)

// ---------------------------------------------------------------------------
// 集群（F-14 ~ F-32）
// ---------------------------------------------------------------------------

// ListClusters GET /api/v1/clusters
func (h *Handlers) ListClusters(c *gin.Context) {
	f := repo.ClusterFilter{
		Keyword:      rawQuery(c, "keyword"),
		Version:      rawQuery(c, "version"),
		DeployMode:   rawQuery(c, "deployMode"),
		Status:       rawQuery(c, "status"),
		Arch:         rawQuery(c, "arch"),
		SortBy:       rawQuery(c, "sortBy"),
		SortOrder:    rawQuery(c, "sortOrder"),
		IncludeDraft: true,
		Page:         PageParams(c),
	}
	res, err := h.Clusters.List(c.Request.Context(), f)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, res)
}

// ClusterOptions GET /api/v1/clusters/options
func (h *Handlers) ClusterOptions(c *gin.Context) {
	list, err := h.Clusters.Options(c.Request.Context())
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, list)
}

// TestConnection POST /api/v1/clusters/test-connection
//
// 三通道并行探测，整体 15s 内返回三项独立结果，绝不整体 hang 死。
// 新建接管场景额外返回 testToken（10 分钟有效），POST /clusters 时校验。
func (h *Handlers) TestConnection(c *gin.Context) {
	var in service.TestConnectionInput
	if !BindJSON(c, &in) {
		return
	}
	res, err := h.Clusters.TestConnection(c.Request.Context(), in)
	if err != nil {
		Fail(c, err)
		return
	}
	mockMode := h.Mode.IsMock()
	notice := ""
	if mockMode {
		notice = "当前为演示模式：连接测试与后续采集均返回内置样例数据，未连接真实集群。需要真实数据请以 TMP_MOCK=false 启动并关闭系统设置中的演示模式。"
	}
	OK(c, gin.H{
		"results":    res.Results,
		"testToken":  res.TestToken,
		"mock":       mockMode,
		"demo":       mockMode,
		"notice":     notice,
		"discovered": res.Discovered,
	})
}

// DiscoverPD POST /api/v1/clusters/discover-pd
//
// 自动发现 PD 地址：优先读 TiUP 元数据（meta.yaml 的 pd_servers），
// 其次查 TiDB 的 information_schema.cluster_info。接管表单的「自动获取」按钮调用。
func (h *Handlers) DiscoverPD(c *gin.Context) {
	var in service.TestConnectionInput
	if !BindJSON(c, &in) {
		return
	}
	res, err := h.Clusters.DiscoverPD(c.Request.Context(), in)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, res)
}

// CreateCluster POST /api/v1/clusters（接管）
func (h *Handlers) CreateCluster(c *gin.Context) {
	var in service.TakeoverInput
	if !BindJSON(c, &in) {
		return
	}
	res, err := h.Clusters.Takeover(c.Request.Context(), in, currentUser(c))
	if err != nil {
		h.RecordAudit(c, service.AuditClusterTakeover, "cluster", "", in.Name, "failed", map[string]any{"error": err.Error()})
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditClusterTakeover, "cluster", itoa(res.ClusterID), in.Name, "success", map[string]any{
		"tiupHost": in.TiUPHost,
		"dbHost":   in.DBHost,
		"draft":    in.SaveAsDraft,
		"force":    res.Forced,
	})
	mockMode := h.Mode.IsMock()
	payload := gin.H{
		"clusterId": res.ClusterID,
		"jobId":     res.JobID,
		"draft":     res.Draft,
		"forced":    res.Forced,
		"mock":      mockMode,
		"demo":      res.Demo,
	}
	if mockMode {
		payload["notice"] = "演示模式接管：该集群信息由内置样例生成，界面会标记为「演示数据」，不代表真实集群。"
	}
	Accepted(c, payload)
}

// GetCluster GET /api/v1/clusters/:id
func (h *Handlers) GetCluster(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	d, err := h.Clusters.Get(c.Request.Context(), id)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, d)
}

// UpdateCluster PUT /api/v1/clusters/:id
func (h *Handlers) UpdateCluster(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	var in service.UpdateInput
	if !BindJSON(c, &in) {
		return
	}
	d, err := h.Clusters.Update(c.Request.Context(), id, in)
	if err != nil {
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditClusterUpdate, "cluster", itoa(id), d.Name, "success", map[string]any{
		"tiupHost": in.TiUPHost, "dbHost": in.DBHost,
	})
	OK(c, d)
}

// DeleteCluster DELETE /api/v1/clusters/:id
func (h *Handlers) DeleteCluster(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	var in struct {
		ConfirmName string `json:"confirmName"`
	}
	if !BindJSON(c, &in) {
		return
	}
	if err := h.Clusters.Delete(c.Request.Context(), id, in.ConfirmName); err != nil {
		h.RecordAudit(c, service.AuditClusterDelete, "cluster", itoa(id), in.ConfirmName, "failed", map[string]any{"error": err.Error()})
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditClusterDelete, "cluster", itoa(id), in.ConfirmName, "success", map[string]any{"note": "仅解除纳管，不影响真实集群"})
	NoContent(c)
}

// RefreshCluster POST /api/v1/clusters/:id/refresh
func (h *Handlers) RefreshCluster(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	res, err := h.Clusters.Refresh(c.Request.Context(), id, currentUser(c))
	if err != nil {
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditClusterRefresh, "cluster", itoa(id), "", "success", map[string]any{"jobId": res.JobID, "reused": res.Reused})
	Accepted(c, res)
}

// BatchRefreshClusters POST /api/v1/clusters/batch-refresh
func (h *Handlers) BatchRefreshClusters(c *gin.Context) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if !BindJSON(c, &in) {
		return
	}
	res, err := h.Clusters.BatchRefresh(c.Request.Context(), in.IDs, currentUser(c))
	if err != nil {
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditClusterRefresh, "cluster", "", "", "success", map[string]any{"count": len(in.IDs)})
	Accepted(c, gin.H{"results": res})
}

// ClusterOverview GET /api/v1/clusters/:id/overview
func (h *Handlers) ClusterOverview(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	ov, err := h.Clusters.Overview(c.Request.Context(), id)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, ov)
}

// ClusterTopology GET /api/v1/clusters/:id/topology
func (h *Handlers) ClusterTopology(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	topo, err := h.Clusters.Topology(c.Request.Context(), id)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, gin.H{
		"groups":    topo.Groups,
		"hosts":     topo.Hosts,
		"nodeCount": topo.NodeCount,
		"hostCount": topo.HostCount,
	})
}

// ClusterInstances GET /api/v1/clusters/:id/instances
func (h *Handlers) ClusterInstances(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	f := repo.InstanceFilter{
		Component: rawQuery(c, "component"),
		Status:    rawQuery(c, "status"),
		Keyword:   rawQuery(c, "keyword"),
		Page:      PageParams(c),
	}
	res, err := h.Clusters.Instances(c.Request.Context(), id, f)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, res)
}

// ClusterComponents GET /api/v1/clusters/:id/components
func (h *Handlers) ClusterComponents(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	info, err := h.Clusters.Components(c.Request.Context(), id)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, info)
}

// ClusterCapabilities GET /api/v1/clusters/:id/capabilities
func (h *Handlers) ClusterCapabilities(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	info, err := h.Clusters.Capabilities(c.Request.Context(), id)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, info)
}

// ClusterChanges GET /api/v1/clusters/:id/changes
func (h *Handlers) ClusterChanges(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	list, err := h.Clusters.Changes(c.Request.Context(), id, 0)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, gin.H{"list": list})
}

// ClusterJobs GET /api/v1/clusters/:id/jobs
func (h *Handlers) ClusterJobs(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	limit := 5
	if v := Int64Query(c, "limit"); v > 0 && v <= 50 {
		limit = int(v)
	}
	list, err := h.Jobs.List(c.Request.Context(), repo.JobFilter{
		ClusterID: id,
		Page:      repo.Page{Page: 1, PageSize: limit},
	})
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, list)
}

// ClusterMetrics GET /api/v1/clusters/:id/metrics
func (h *Handlers) ClusterMetrics(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	if err := h.Clusters.Metrics(c.Request.Context(), id, rawQuery(c, "range")); err != nil {
		Fail(c, err)
		return
	}
	OK(c, gin.H{"series": []any{}})
}

// ClusterAlerts GET /api/v1/clusters/:id/alerts
func (h *Handlers) ClusterAlerts(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	list, err := h.Clusters.Alerts(c.Request.Context(), id)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, list)
}
