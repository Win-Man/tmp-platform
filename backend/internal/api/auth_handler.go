package api

import (
	"github.com/gin-gonic/gin"

	"github.com/tmp-platform/backend/internal/auth"
	"github.com/tmp-platform/backend/internal/pkg/apperr"
	"github.com/tmp-platform/backend/internal/repo"
	"github.com/tmp-platform/backend/internal/service"
)

// ---------------------------------------------------------------------------
// 认证（F-01/02/07/08）
// ---------------------------------------------------------------------------

// SetupStatus GET /api/v1/setup/status
func (h *Handlers) SetupStatus(c *gin.Context) {
	st, err := h.Auth.SetupStatus(c.Request.Context())
	if err != nil {
		Fail(c, err)
		return
	}
	st.MockMode = h.Mode.IsMock()
	OK(c, st)
}

// SetupInit POST /api/v1/setup/init
func (h *Handlers) SetupInit(c *gin.Context) {
	var in service.SetupInitInput
	if !BindJSON(c, &in) {
		return
	}
	u, err := h.Auth.SetupInit(c.Request.Context(), in)
	if err != nil {
		Fail(c, err)
		return
	}
	h.RecordAuditAs(c, in.Username, "auth.setup", "success", map[string]any{"userId": u.ID})
	OK(c, u)
}

// Login POST /api/v1/auth/login
func (h *Handlers) Login(c *gin.Context) {
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !BindJSON(c, &in) {
		return
	}
	ip, ua, rid := AuditMeta(c)
	res, err := h.Auth.Login(c.Request.Context(), service.LoginInput{
		Username:  in.Username,
		Password:  in.Password,
		IP:        ip,
		UserAgent: ua,
		RequestID: rid,
	})
	if err != nil {
		h.RecordAuditAs(c, in.Username, service.AuditLogin, "failed", map[string]any{"reason": apperr.From(err).Message})
		Fail(c, err)
		return
	}
	h.Audit.Record(c.Request.Context(), service.AuditInput{
		ActorName: res.User.Username,
		Action:    service.AuditLogin,
		Result:    "success",
		Detail:    map[string]any{"username": res.User.Username, "role": res.User.Role},
		IP:        ip,
		UserAgent: ua,
		RequestID: rid,
	})
	OK(c, res)
}

// Logout POST /api/v1/auth/logout
//
// 客户端丢弃 token；服务端记录审计。不做内存黑名单（吊销依赖 token_version）。
func (h *Handlers) Logout(c *gin.Context) {
	h.RecordAudit(c, service.AuditLogout, "user", "", "", "success", nil)
	NoContent(c)
}

// Me GET /api/v1/auth/me
func (h *Handlers) Me(c *gin.Context) {
	u := currentUser(c)
	OK(c, h.Auth.Me(u))
}

// ChangePassword PUT /api/v1/auth/password
func (h *Handlers) ChangePassword(c *gin.Context) {
	var in struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if !BindJSON(c, &in) {
		return
	}
	u := currentUser(c)
	if err := h.Auth.ChangePassword(c.Request.Context(), u, in.OldPassword, in.NewPassword); err != nil {
		h.RecordAudit(c, service.AuditPasswordChange, "user", "", u.Username, "failed", nil)
		Fail(c, err)
		return
	}
	// 改密后旧 token 立即失效，这里为新 token 供前端无缝续用。
	token, exp, terr := h.Auth.IssueTokenFor(c.Request.Context(), u.ID)
	h.RecordAudit(c, service.AuditPasswordChange, "user", "", u.Username, "success", nil)
	if terr != nil {
		NoContent(c)
		return
	}
	OK(c, gin.H{"token": token, "expiresAt": exp})
}

// ---------------------------------------------------------------------------
// 平台用户（F-03/04/05/06）
// ---------------------------------------------------------------------------

// ListUsers GET /api/v1/users
func (h *Handlers) ListUsers(c *gin.Context) {
	f := repo.UserFilter{Keyword: rawQuery(c, "keyword"), Page: PageParams(c)}
	res, err := h.Users.List(c.Request.Context(), f)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, res)
}

// CreateUser POST /api/v1/users
func (h *Handlers) CreateUser(c *gin.Context) {
	var in service.CreateUserInput
	if !BindJSON(c, &in) {
		return
	}
	u, err := h.Users.Create(c.Request.Context(), in)
	if err != nil {
		h.RecordAudit(c, service.AuditUserCreate, "user", "", in.Username, "failed", map[string]any{"error": err.Error()})
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditUserCreate, "user", itoa(u.ID), u.Username, "success", map[string]any{"role": u.Role})
	OK(c, u)
}

// UpdateUser PUT /api/v1/users/:id
func (h *Handlers) UpdateUser(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	var in service.UpdateUserInput
	if !BindJSON(c, &in) {
		return
	}
	u, err := h.Users.Update(c.Request.Context(), id, in)
	if err != nil {
		h.RecordAudit(c, service.AuditUserUpdate, "user", itoa(id), "", "failed", map[string]any{"error": err.Error()})
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditUserUpdate, "user", itoa(id), u.Username, "success", map[string]any{"role": u.Role})
	OK(c, u)
}

// SetUserStatus PATCH /api/v1/users/:id/status
func (h *Handlers) SetUserStatus(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	var in struct {
		Status string `json:"status"`
	}
	if !BindJSON(c, &in) {
		return
	}
	u, err := h.Users.SetStatus(c.Request.Context(), id, in.Status)
	if err != nil {
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditUserStatus, "user", itoa(id), u.Username, "success", map[string]any{"status": in.Status})
	OK(c, u)
}

// ResetUserPassword POST /api/v1/users/:id/reset-password
func (h *Handlers) ResetUserPassword(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	var in struct {
		Password string `json:"password"`
	}
	if !BindJSON(c, &in) {
		return
	}
	if err := h.Users.ResetPassword(c.Request.Context(), id, in.Password); err != nil {
		Fail(c, err)
		return
	}
	h.RecordAudit(c, service.AuditUserResetPwd, "user", itoa(id), "", "success", nil)
	NoContent(c)
}

// RoleMatrix GET /api/v1/roles/matrix
func (h *Handlers) RoleMatrix(c *gin.Context) {
	OK(c, gin.H{"roles": []string{auth.RoleAdmin, auth.RoleOperator, auth.RoleViewer}, "items": service.RoleMatrix()})
}

// GetUser GET /api/v1/users/:id
func (h *Handlers) GetUser(c *gin.Context) {
	id, ok := Int64Param(c, "id")
	if !ok {
		return
	}
	u, err := h.Users.GetByID(c.Request.Context(), id)
	if err != nil {
		Fail(c, err)
		return
	}
	OK(c, service.ToUserProfile(u))
}
