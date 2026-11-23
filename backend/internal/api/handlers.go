package api

import (
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/tmp-platform/backend/internal/auth"
	"github.com/tmp-platform/backend/internal/config"
	"github.com/tmp-platform/backend/internal/model"
	"github.com/tmp-platform/backend/internal/service"
)

// Handlers 全部业务 Handler 的依赖容器。
type Handlers struct {
	Config   *config.Config
	Mode     *service.ModeManager
	Auth     *service.AuthService
	Users    *service.UserService
	Clusters *service.ClusterService
	Hosts    *service.HostService
	TiDBObj  *service.TiDBObjService
	Inspect  *service.InspectService
	Jobs     *service.JobService
	Settings *service.SettingService
	Creds    *service.CredentialService
	Overview *service.OverviewService
	Audit    *service.AuditService
	AddrMaps *service.AddrMapService
	MockMode bool
}

// AuditMeta 提取审计所需的请求元信息。
func AuditMeta(c *gin.Context) (ip, ua, requestID string) {
	return c.ClientIP(), c.GetHeader("User-Agent"), RequestID(c)
}

// RecordAudit 写入审计（便捷封装）。
func (h *Handlers) RecordAudit(c *gin.Context, action, resourceType, resourceID, resourceName, result string, detail map[string]any) {
	ip, ua, rid := AuditMeta(c)
	h.Audit.Record(c.Request.Context(), service.AuditInput{
		User:         auth.CurrentUser(c),
		Action:       action,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		ResourceName: resourceName,
		Result:       result,
		Detail:       detail,
		IP:           ip,
		UserAgent:    ua,
		RequestID:    rid,
	})
}

// RecordAuditAs 以指定账号名写入审计（登录失败场景）。
func (h *Handlers) RecordAuditAs(c *gin.Context, actor, action, result string, detail map[string]any) {
	ip, ua, rid := AuditMeta(c)
	h.Audit.Record(c.Request.Context(), service.AuditInput{
		ActorName: actor,
		Action:    action,
		Result:    result,
		Detail:    detail,
		IP:        ip,
		UserAgent: ua,
		RequestID: rid,
	})
}

// currentUser 取当前登录用户（中间件已保证非 nil）。
func currentUser(c *gin.Context) *model.PlatformUser {
	return auth.CurrentUser(c)
}

// attachment 输出附件下载响应；文件名同时提供 ASCII 回退与 RFC5987 编码。
func attachment(c *gin.Context, filename, contentType string, data []byte) {
	ascii := make([]rune, 0, len(filename))
	for _, r := range filename {
		if r < 128 {
			ascii = append(ascii, r)
		} else {
			ascii = append(ascii, '_')
		}
	}
	fallback := string(ascii)
	if strings.TrimSpace(fallback) == "" {
		fallback = "download"
	}
	c.Header("Content-Disposition",
		`attachment; filename="`+fallback+`"; filename*=UTF-8''`+url.PathEscape(filename))
	c.Data(200, contentType, data)
}

// itoa int64 转字符串（避免在 handler 中反复引入 strconv）。
func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var buf [24]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
