package service

import (
	"context"
	"fmt"
	"time"

	"github.com/tmp-platform/backend/internal/pkg/apperr"
)

// AuditCSVHeader 审计日志导出表头（顺序与 ExportCSV 行数据一致）。
var AuditCSVHeader = []string{
	"时间", "操作者", "角色", "操作", "资源类型", "资源ID", "资源名称",
	"结果", "IP", "User-Agent", "RequestId", "详情",
}

// auditExportLimit 单次导出行数上限，防止大表导出撑爆内存。
const auditExportLimit = 50000

// ExportCSV 导出审计日志为 CSV 附件（UTF-8 BOM，单元格做公式注入防护）。
//
// 返回文件字节流与建议文件名（不含路径，可直接用于 Content-Disposition）。
func (s *AuditService) ExportCSV(ctx context.Context, f AuditFilter) ([]byte, string, error) {
	list, err := s.repo.ExportAll(ctx, f)
	if err != nil {
		return nil, "", apperr.Internal("导出审计日志失败")
	}
	if len(list) > auditExportLimit {
		list = list[:auditExportLimit]
	}
	rows := make([][]string, 0, len(list))
	for _, a := range list {
		rows = append(rows, []string{
			a.CreatedAt.Format("2006-01-02 15:04:05"),
			a.Username,
			a.Role,
			a.Action,
			a.ResourceType,
			a.ResourceID,
			a.ResourceName,
			a.Result,
			a.IP,
			a.UserAgent,
			a.RequestID,
			a.Detail,
		})
	}
	data, err := buildCSV(AuditCSVHeader, rows)
	if err != nil {
		return nil, "", apperr.Internal("生成审计 CSV 失败")
	}
	name := csvFilename(fmt.Sprintf("审计日志-%s", time.Now().Format("20060102-150405")))
	return data, name, nil
}
