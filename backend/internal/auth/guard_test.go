package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tmp-platform/backend/internal/model"
)

// guardRouter 构造一个「Authenticate + MustChangePasswordGuard」的最小路由，
// 注册白名单路径与一条典型业务路径，用于验证强制改密守卫。
func guardRouter(t *testing.T, mw *Middleware) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/v1", mw.Authenticate(), MustChangePasswordGuard())
	okHandler := func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"code": 0, "message": "ok"}) }
	g.GET("/auth/me", okHandler)
	g.PUT("/auth/password", okHandler)
	g.POST("/auth/logout", okHandler)
	g.GET("/clusters", okHandler)
	g.POST("/users", okHandler)
	return r
}

// guardCall 用指定 token 请求指定路径，返回 HTTP 状态码与业务码。
func guardCall(t *testing.T, r *gin.Engine, method, path, token string) (int, int) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var body struct {
		Code int `json:"code"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return w.Code, body.Code
}

// issueFor 为指定用户签一个与其 token_version 匹配的 token。
func issueFor(t *testing.T, m *Manager, u *model.PlatformUser) string {
	t.Helper()
	raw, _, err := m.Issue(u.ID, u.Username, u.TokenVersion)
	if err != nil {
		t.Fatalf("Issue 失败: %v", err)
	}
	return raw
}

// TestMustChangePasswordGuard_BlocksBusinessEndpoints 未改密时业务接口一律 403/40302。
func TestMustChangePasswordGuard_BlocksBusinessEndpoints(t *testing.T) {
	m := NewManager("unit-test-secret", time.Hour)
	u := &model.PlatformUser{ID: 7, Username: "eve", Role: RoleAdmin, Status: "active", MustChangePassword: 1}
	r := guardRouter(t, NewMiddleware(m, &fakeUsers{user: u}))
	token := issueFor(t, m, u)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/clusters"},
		{http.MethodPost, "/api/v1/users"},
	}
	for _, tc := range cases {
		code, biz := guardCall(t, r, tc.method, tc.path, token)
		if code != http.StatusForbidden || biz != 40302 {
			t.Fatalf("%s %s 未改密时应 403/40302，实际 %d/%d", tc.method, tc.path, code, biz)
		}
	}
}

// TestMustChangePasswordGuard_AllowList 白名单三接口在未改密状态下必须放行。
func TestMustChangePasswordGuard_AllowList(t *testing.T) {
	m := NewManager("unit-test-secret", time.Hour)
	u := &model.PlatformUser{ID: 7, Username: "eve", Role: RoleAdmin, Status: "active", MustChangePassword: 1}
	r := guardRouter(t, NewMiddleware(m, &fakeUsers{user: u}))
	token := issueFor(t, m, u)

	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/auth/me"},
		{http.MethodPut, "/api/v1/auth/password"},
		{http.MethodPost, "/api/v1/auth/logout"},
	}
	for _, tc := range cases {
		code, biz := guardCall(t, r, tc.method, tc.path, token)
		if code != http.StatusOK || biz != 0 {
			t.Fatalf("%s %s 在白名单内应 200/0，实际 %d/%d", tc.method, tc.path, code, biz)
		}
	}
}

// TestMustChangePasswordGuard_PassThroughWhenAlreadyChanged 已改密用户不受影响。
func TestMustChangePasswordGuard_PassThroughWhenAlreadyChanged(t *testing.T) {
	m := NewManager("unit-test-secret", time.Hour)
	u := &model.PlatformUser{ID: 8, Username: "frank", Role: RoleAdmin, Status: "active", MustChangePassword: 0}
	r := guardRouter(t, NewMiddleware(m, &fakeUsers{user: u}))
	token := issueFor(t, m, u)

	if code, biz := guardCall(t, r, http.MethodGet, "/api/v1/clusters", token); code != http.StatusOK || biz != 0 {
		t.Fatalf("已改密用户应正常访问，实际 %d/%d", code, biz)
	}
}

// TestMustChangePasswordGuard_NoUserPassThrough 无上下文用户时不 panic、直接放行，
// 由后续 Require 兜底（守卫不承担鉴权职责）。
func TestMustChangePasswordGuard_NoUserPassThrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", MustChangePasswordGuard(), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"code": 0})
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/x", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("无用户上下文应直接放行，实际 %d", w.Code)
	}
}

// TestMustChangePasswordAllowList_Content 白名单必须是「查自己 / 改密 / 退出 / 探针」四类，
// 防止后续误把业务接口加进白名单。
func TestMustChangePasswordAllowList_Content(t *testing.T) {
	want := map[string]bool{
		"/api/v1/auth/me":       true,
		"/api/v1/auth/password": true,
		"/api/v1/auth/logout":   true,
		"/healthz":              true,
		"/readyz":               true,
	}
	if len(MustChangePasswordAllowList) != len(want) {
		t.Fatalf("白名单条目数 = %d, want %d（不允许擅自扩容）",
			len(MustChangePasswordAllowList), len(want))
	}
	for k := range MustChangePasswordAllowList {
		if !want[k] {
			t.Fatalf("白名单出现非预期路径 %q，请确认不是把业务接口放行", k)
		}
	}
}
