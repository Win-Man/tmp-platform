package api

import (
	"github.com/gin-gonic/gin"

	"github.com/tmp-platform/backend/internal/service"
)

// ListAddrMaps GET /api/v1/clusters/:id/addr-maps
func (h *Handlers) ListAddrMaps(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	list, err := h.AddrMaps.List(c.Request.Context(), id)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, gin.H{"list": list})
}

// CreateAddrMap POST /api/v1/clusters/:id/addr-maps
func (h *Handlers) CreateAddrMap(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	var in service.AddrMapInput
	if !BindJSON(c, &in) {
		return
	}
	m, err := h.AddrMaps.Create(c.Request.Context(), id, in, currentUser(c))
	if err != nil {
		h.RecordAudit(c, service.AuditAddrMapCreate, "addr_map", "", in.Source, "failed",
			map[string]any{"target": in.Target, "error": err.Error()})
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditAddrMapCreate, "addr_map", itoa(m.ID), m.Source, "success",
		map[string]any{"target": m.Target, "enabled": m.Enabled == 1})
	OK(c, m)
}

// UpdateAddrMap PUT /api/v1/clusters/:id/addr-maps/:mapId
func (h *Handlers) UpdateAddrMap(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	mapID, ok := Int64Param(c, "mapId")
	if !ok {
		return
	}
	var in service.AddrMapInput
	if !BindJSON(c, &in) {
		return
	}
	m, err := h.AddrMaps.Update(c.Request.Context(), id, mapID, in)
	if err != nil {
		h.RecordAudit(c, service.AuditAddrMapUpdate, "addr_map", itoa(mapID), in.Source, "failed",
			map[string]any{"target": in.Target, "error": err.Error()})
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditAddrMapUpdate, "addr_map", itoa(m.ID), m.Source, "success",
		map[string]any{"target": m.Target, "enabled": m.Enabled == 1})
	OK(c, m)
}

// DeleteAddrMap DELETE /api/v1/clusters/:id/addr-maps/:mapId
func (h *Handlers) DeleteAddrMap(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	mapID, ok := Int64Param(c, "mapId")
	if !ok {
		return
	}
	if err := h.AddrMaps.Delete(c.Request.Context(), id, mapID); err != nil {
		h.RecordAudit(c, service.AuditAddrMapDelete, "addr_map", itoa(mapID), "", "failed",
			map[string]any{"error": err.Error()})
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditAddrMapDelete, "addr_map", itoa(mapID), "", "success", nil)
	NoContent(c)
}

// PreviewAddrMap POST /api/v1/clusters/:id/addr-maps/preview
//
// 用当前启用的规则试算一个地址，便于用户保存前确认规则生效范围。
func (h *Handlers) PreviewAddrMap(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	var in struct {
		Address string `json:"address"`
	}
	if !BindJSON(c, &in) {
		return
	}
	res, err := h.AddrMaps.Preview(c.Request.Context(), id, in.Address)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, res)
}
