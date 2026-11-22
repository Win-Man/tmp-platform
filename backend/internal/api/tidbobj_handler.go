package api

import (
	"github.com/gin-gonic/gin"

	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/repo"
	"github.com/tmp-platform/backend/internal/service"
)

// ---------------------------------------------------------------------------
// TiDB 对象（F-33 ~ F-44）
// ---------------------------------------------------------------------------

// ListTiDBUsers GET /api/v1/clusters/:id/tidb-users
func (h *Handlers) ListTiDBUsers(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	f := repo.TiDBUserFilter{Keyword: rawQuery(c, "keyword"), Page: PageParams(c)}
	res, err := h.TiDBObj.ListUsers(c.Request.Context(), id, f)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, res)
}

// CreateTiDBUser POST /api/v1/clusters/:id/tidb-users
func (h *Handlers) CreateTiDBUser(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	var in service.CreateTiDBUserInput
	if !BindJSON(c, &in) {
		return
	}
	jobID, err := h.TiDBObj.CreateUser(c.Request.Context(), id, in, currentUser(c))
	if err != nil {
		h.RecordAudit(c, service.AuditTiDBUserCreate, "tidb_user", "", in.UserName, "failed", map[string]any{"error": err.Error()})
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditTiDBUserCreate, "tidb_user", "", in.UserName+"@"+in.Host, "success", map[string]any{
		"resourceGroup": in.ResourceGroup, "grants": in.Grants, "jobId": jobID,
	})
	Accepted(c, gin.H{"jobId": jobID, "mock": h.Mode.IsMock()})
}

// DeleteTiDBUser DELETE /api/v1/clusters/:id/tidb-users/:uid
func (h *Handlers) DeleteTiDBUser(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	uid, ok := Int64Param(c, "uid")
	if !ok {
		return
	}
	jobID, err := h.TiDBObj.DeleteUser(c.Request.Context(), id, uid, currentUser(c))
	if err != nil {
		h.RecordAudit(c, service.AuditTiDBUserDelete, "tidb_user", itoa(uid), "", "failed", map[string]any{"error": err.Error()})
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditTiDBUserDelete, "tidb_user", itoa(uid), "", "success", map[string]any{"jobId": jobID})
	Accepted(c, gin.H{"jobId": jobID, "mock": h.Mode.IsMock()})
}

// ResetTiDBUserPassword PUT /api/v1/clusters/:id/tidb-users/:uid/password
func (h *Handlers) ResetTiDBUserPassword(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	uid, ok := Int64Param(c, "uid")
	if !ok {
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if !BindJSON(c, &in) {
		return
	}
	jobID, err := h.TiDBObj.ResetPassword(c.Request.Context(), id, uid, in.Password, currentUser(c))
	if err != nil {
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditTiDBUserPwd, "tidb_user", itoa(uid), "", "success", map[string]any{"jobId": jobID})
	Accepted(c, gin.H{"jobId": jobID, "mock": h.Mode.IsMock()})
}

// GetTiDBUserGrants GET /api/v1/clusters/:id/tidb-users/:uid/grants
func (h *Handlers) GetTiDBUserGrants(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	uid, ok := Int64Param(c, "uid")
	if !ok {
		return
	}
	grants, err := h.TiDBObj.Grants(c.Request.Context(), id, uid)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, gin.H{"grants": grants})
}

// ChangeTiDBUserGrants POST /api/v1/clusters/:id/tidb-users/:uid/grants
func (h *Handlers) ChangeTiDBUserGrants(c *gin.Context) {
	h.changeGrants(c, true)
}

// RevokeTiDBUserGrants DELETE /api/v1/clusters/:id/tidb-users/:uid/grants
func (h *Handlers) RevokeTiDBUserGrants(c *gin.Context) {
	h.changeGrants(c, false)
}

// changeGrants 授权 / 回收公共实现。
func (h *Handlers) changeGrants(c *gin.Context, grant bool) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	uid, ok := Int64Param(c, "uid")
	if !ok {
		return
	}
	var in service.GrantInput
	if !BindJSON(c, &in) {
		return
	}
	grants, err := h.TiDBObj.ChangeGrant(c.Request.Context(), id, uid, in, grant)
	if err != nil {
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditTiDBUserGrant, "tidb_user", itoa(uid), "", "success", map[string]any{
		"scope": in.Scope, "db": in.DB, "privs": in.Privs, "grant": grant,
	})
	OK(c, gin.H{"grants": grants})
}

// ListResourceGroups GET /api/v1/clusters/:id/resource-groups
//
// live=1 时实时查询 information_schema.resource_groups（页面刷新按钮），
// 否则读取上次采集的 Resource Group 快照。
func (h *Handlers) ListResourceGroups(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	var list []model.ResourceGroup
	var err error
	if BoolQuery(c, "live") {
		list, err = h.TiDBObj.RefreshResourceGroups(c.Request.Context(), id)
	} else {
		list, err = h.TiDBObj.ListResourceGroups(c.Request.Context(), id)
	}
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, gin.H{"list": list})
}

// CreateResourceGroup POST /api/v1/clusters/:id/resource-groups
func (h *Handlers) CreateResourceGroup(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	var in service.CreateResourceGroupInput
	if !BindJSON(c, &in) {
		return
	}
	if err := h.TiDBObj.CreateResourceGroup(c.Request.Context(), id, in); err != nil {
		h.RecordAudit(c, service.AuditRGCreate, "resource_group", "", in.Name, "failed", map[string]any{"error": err.Error()})
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditRGCreate, "resource_group", "", in.Name, "success", map[string]any{
		"ruPerSec": in.RUPerSec, "priority": in.Priority, "burstable": in.Burstable,
	})
	NoContent(c)
}

// AlterResourceGroup PUT /api/v1/clusters/:id/resource-groups/:name
func (h *Handlers) AlterResourceGroup(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	name := c.Param("name")
	var in service.AlterResourceGroupInput
	if !BindJSON(c, &in) {
		return
	}
	if err := h.TiDBObj.AlterResourceGroup(c.Request.Context(), id, name, in); err != nil {
		h.RecordAudit(c, service.AuditRGAlter, "resource_group", "", name, "failed", map[string]any{"error": err.Error()})
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditRGAlter, "resource_group", "", name, "success", map[string]any{
		"ruPerSec": in.RUPerSec, "priority": in.Priority, "burstable": in.Burstable,
	})
	NoContent(c)
}

// DeleteResourceGroup DELETE /api/v1/clusters/:id/resource-groups/:name
func (h *Handlers) DeleteResourceGroup(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	name := c.Param("name")
	if err := h.TiDBObj.DeleteResourceGroup(c.Request.Context(), id, name); err != nil {
		h.RecordAudit(c, service.AuditRGDrop, "resource_group", "", name, "failed", map[string]any{"error": err.Error()})
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditRGDrop, "resource_group", "", name, "success", nil)
	NoContent(c)
}

// BindResourceGroup POST /api/v1/clusters/:id/resource-groups/bind
func (h *Handlers) BindResourceGroup(c *gin.Context) {
	h.bindRG(c, true)
}

// UnbindResourceGroup POST /api/v1/clusters/:id/resource-groups/unbind
func (h *Handlers) UnbindResourceGroup(c *gin.Context) {
	h.bindRG(c, false)
}

// bindRG 绑定 / 解绑公共实现。
func (h *Handlers) bindRG(c *gin.Context, bind bool) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	var in service.BindRGInput
	if !BindJSON(c, &in) {
		return
	}
	if err := h.TiDBObj.BindResourceGroup(c.Request.Context(), id, in, bind); err != nil {
		Fail(c, err)
		return
	}
	action := service.AuditRGUnbind
	if bind {
		action = service.AuditRGBind
	}
	h.RecordAudit(c, action, "tidb_user", "", in.UserName+"@"+in.Host, "success", map[string]any{"resourceGroup": in.ResourceGroup})
	NoContent(c)
}

// ListSessions GET /api/v1/clusters/:id/sessions
//
// live=1 时实时查询 information_schema.cluster_processlist（页面刷新按钮），
// 否则读取上次采集的会话快照。
func (h *Handlers) ListSessions(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	f := repo.SessionFilter{
		Keyword: rawQuery(c, "keyword"),
		SortBy:  rawQuery(c, "sortBy"),
		Page:    PageParams(c),
	}
	if BoolQuery(c, "live") {
		res, err := h.TiDBObj.RefreshSessions(c.Request.Context(), id, f)
		if err != nil {
			Fail(c, err)
			return
		}
		OK(c, res)
		return
	}
	res, err := h.TiDBObj.ListSessions(c.Request.Context(), id, f)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, res)
}

// KillSessions POST /api/v1/clusters/:id/sessions/kill
func (h *Handlers) KillSessions(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	var in struct {
		SessionIDs []int64 `json:"sessionIds"`
	}
	if !BindJSON(c, &in) {
		return
	}
	jobID, err := h.TiDBObj.KillSessions(c.Request.Context(), id, in.SessionIDs, currentUser(c))
	if err != nil {
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditSessionKill, "session", "", "", "success", map[string]any{"sessionIds": in.SessionIDs, "jobId": jobID})
	Accepted(c, gin.H{"jobId": jobID, "mock": h.Mode.IsMock()})
}

// ListVariables GET /api/v1/clusters/:id/variables
//
// live=1 时实时执行 SHOW GLOBAL VARIABLES（页面刷新按钮），
// 否则读取上次采集的参数快照。
func (h *Handlers) ListVariables(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	f := repo.VariableFilter{
		Keyword:  rawQuery(c, "keyword"),
		OnlyDiff: BoolQuery(c, "onlyDiff"),
		Page:     PageParams(c),
	}
	if BoolQuery(c, "live") {
		res, err := h.TiDBObj.RefreshVariables(c.Request.Context(), id, f)
		if err != nil {
			Fail(c, err)
			return
		}
		OK(c, res)
		return
	}
	res, err := h.TiDBObj.ListVariables(c.Request.Context(), id, f)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, res)
}

// ExportVariables GET /api/v1/clusters/:id/variables/export
func (h *Handlers) ExportVariables(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	data, filename, err := h.TiDBObj.ExportVariables(c.Request.Context(), id, BoolQuery(c, "onlyDiff"))
	if err != nil {
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditVariableExport, "variable", itoa(id), filename, "success", nil)
	attachment(c, filename, "text/csv; charset=utf-8", data)
}

// UpdateVariable PUT /api/v1/clusters/:id/variables
func (h *Handlers) UpdateVariable(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	var in struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if !BindJSON(c, &in) {
		return
	}
	if err := h.TiDBObj.UpdateVariable(c.Request.Context(), id, in.Name, in.Value); err != nil {
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditVariableUpdate, "variable", itoa(id), in.Name, "success", map[string]any{"value": in.Value})
	NoContent(c)
}

// SlowQueries GET /api/v1/clusters/:id/slow-queries（P2，MVP 返回空）
func (h *Handlers) SlowQueries(c *gin.Context) {
	OK(c, gin.H{"list": []any{}, "total": 0})
}
