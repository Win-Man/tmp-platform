package api

import (
	"github.com/gin-gonic/gin"

	"github.com/tmp-platform/backend/internal/repo"
	"github.com/tmp-platform/backend/internal/service"
)

// ListHosts GET /api/v1/hosts
func (h *Handlers) ListHosts(c *gin.Context) {
	f := repo.HostFilter{
		ClusterID: Int64Query(c, "clusterId"),
		Arch:      rawQuery(c, "arch"),
		SSHStatus: rawQuery(c, "sshStatus"),
		Keyword:   rawQuery(c, "keyword"),
		Page:      PageParams(c),
	}
	res, err := h.Hosts.List(c.Request.Context(), f)
	if err != nil {
		Fail(c, err)
		return
	}
	archs, _ := h.Hosts.DistinctArch(c.Request.Context())
	OK(c, gin.H{
		"list":     res.List,
		"total":    res.Total,
		"page":     res.Page,
		"pageSize": res.PageSize,
		"arches":   archs,
	})
}

// GroupedHosts GET /api/v1/hosts/grouped
func (h *Handlers) GroupedHosts(c *gin.Context) {
	groups, err := h.Hosts.Grouped(c.Request.Context())
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, gin.H{"groups": groups})
}

// GetHost GET /api/v1/hosts/:id
func (h *Handlers) GetHost(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	d, err := h.Hosts.Get(c.Request.Context(), id)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, d)
}

// HostMetrics GET /api/v1/hosts/:id/metrics
func (h *Handlers) HostMetrics(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	if err := h.Hosts.Metrics(c.Request.Context(), id); err != nil {
		Fail(c, err)
		return
	}
	OK(c, gin.H{"series": []any{}})
}

// SSHCheck POST /api/v1/hosts/ssh-check
func (h *Handlers) SSHCheck(c *gin.Context) {
	var in struct {
		HostIDs []int64 `json:"hostIds"`
	}
	if !BindJSON(c, &in) {
		return
	}
	jobID, err := h.Hosts.SSHCheck(c.Request.Context(), in.HostIDs, currentUser(c))
	if err != nil {
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditHostSSHCheck, "host", "", "", "success", map[string]any{"hostIds": in.HostIDs, "jobId": jobID})
	Accepted(c, gin.H{"jobId": jobID, "mock": h.Mode.IsMock()})
}

// HostSSHChecks GET /api/v1/hosts/:id/ssh-checks
func (h *Handlers) HostSSHChecks(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	list, err := h.Hosts.SSHChecks(c.Request.Context(), id, 20)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, gin.H{"list": list})
}
