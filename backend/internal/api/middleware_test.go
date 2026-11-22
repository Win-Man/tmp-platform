package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// 接口响应禁止缓存：Web 端「刷新」按钮拿到的必须是新数据，
// 否则用户只能整页 F5 才能看到最新集群状态。
func TestNoCacheMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(NoCacheMiddleware())
	r.GET("/probe", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})

	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/probe", nil))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	cc := w.Header().Get("Cache-Control")
	if cc != "no-store, no-cache, must-revalidate" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
	if w.Header().Get("Pragma") != "no-cache" {
		t.Fatalf("Pragma = %q, want no-cache", w.Header().Get("Pragma"))
	}
}
