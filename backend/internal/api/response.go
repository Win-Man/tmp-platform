// Package api 接入层：Gin 路由、统一响应、中间件与各业务 Handler。
package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/tmp-platform/backend/internal/pkg/apperr"
	"github.com/tmp-platform/backend/internal/repo"
)

// Response 统一响应包 {code,message,data}。
type Response struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

// OK 返回 200 + code=0。
func OK(c *gin.Context, data any) {
	c.JSON(http.StatusOK, Response{Code: apperr.CodeOK, Message: "ok", Data: data})
}

// Accepted 返回 202（异步任务受理）。
func Accepted(c *gin.Context, data any) {
	c.JSON(http.StatusAccepted, Response{Code: apperr.CodeOK, Message: "accepted", Data: data})
}

// NoContent OK 但无 data。
func NoContent(c *gin.Context) {
	c.JSON(http.StatusOK, Response{Code: apperr.CodeOK, Message: "ok", Data: nil})
}

// Fail 把错误转换为统一错误响应。
func Fail(c *gin.Context, err error) {
	ae := apperr.From(err)
	if ae == nil {
		NoContent(c)
		return
	}
	status := ae.HTTP
	if status == 0 {
		status = http.StatusInternalServerError
	}
	c.AbortWithStatusJSON(status, Response{Code: ae.Code, Message: ae.Message, Data: ae.Data})
}

// BindJSON 绑定并校验请求体；失败时直接输出 40001 并返回 false。
func BindJSON(c *gin.Context, out any) bool {
	if err := c.ShouldBindJSON(out); err != nil {
		Fail(c, apperr.BadRequest("请求参数格式错误: "+trimErr(err.Error())))
		return false
	}
	return true
}

// trimErr 截断过长的校验错误信息。
func trimErr(s string) string {
	if len(s) > 240 {
		return s[:240]
	}
	return s
}

// PageParams 解析 page/pageSize 查询参数。
func PageParams(c *gin.Context) repo.Page {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	pageSize, _ := strconv.Atoi(c.DefaultQuery("pageSize", "20"))
	p := repo.Page{Page: page, PageSize: pageSize}
	p.Normalize()
	return p
}

// Int64Param 解析路径中的 int64 参数。
func Int64Param(c *gin.Context, name string) (int64, bool) {
	v, err := strconv.ParseInt(c.Param(name), 10, 64)
	if err != nil || v <= 0 {
		Fail(c, apperr.BadRequest("路径参数 "+name+" 不合法"))
		return 0, false
	}
	return v, true
}

// Int64Query 解析查询参数中的 int64。
func Int64Query(c *gin.Context, name string) int64 {
	v, err := strconv.ParseInt(c.Query(name), 10, 64)
	if err != nil {
		return 0
	}
	return v
}

// BoolQuery 解析布尔查询参数。
func BoolQuery(c *gin.Context, name string) bool {
	v := strings.ToLower(c.DefaultQuery(name, "false"))
	return v == "true" || v == "1"
}

// TimeQuery 解析 RFC3339 或 yyyy-MM-dd 时间查询参数。
func TimeQuery(c *gin.Context, name string) *time.Time {
	raw := strings.TrimSpace(c.Query(name))
	if raw == "" {
		return nil
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, raw, time.Local); err == nil {
			return &t
		}
	}
	return nil
}

// rawQuery 读取原始查询参数。
func rawQuery(c *gin.Context, name string) string {
	return strings.TrimSpace(c.Query(name))
}
