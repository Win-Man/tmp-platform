// Package repo 数据访问层：只做 SQL，不含业务规则。
package repo

import (
	"strings"

	"gorm.io/gorm"
)

// 分页常量。
const (
	DefaultPage     = 1
	DefaultPageSize = 20
	MaxPageSize     = 200
)

// Page 分页参数。
type Page struct {
	Page     int
	PageSize int
}

// Normalize 归一化分页参数（默认 1/20，上限 200）。
func (p *Page) Normalize() {
	if p.Page < 1 {
		p.Page = DefaultPage
	}
	if p.PageSize < 1 {
		p.PageSize = DefaultPageSize
	}
	if p.PageSize > MaxPageSize {
		p.PageSize = MaxPageSize
	}
}

// Offset 计算偏移量。
func (p Page) Offset() int {
	return (p.Page - 1) * p.PageSize
}

// ListResult 通用分页结果。
type ListResult[T any] struct {
	List     []T   `json:"list"`
	Total    int64 `json:"total"`
	Page     int   `json:"page"`
	PageSize int   `json:"pageSize"`
}

// NewListResult 构造分页结果；保证 list 非 nil（前端渲染空态）。
func NewListResult[T any](list []T, total int64, p Page) ListResult[T] {
	if list == nil {
		list = []T{}
	}
	return ListResult[T]{List: list, Total: total, Page: p.Page, PageSize: p.PageSize}
}

// likePattern 构造 LIKE 模糊匹配串。
func likePattern(keyword string) string {
	return "%" + escapeLike(strings.TrimSpace(keyword)) + "%"
}

// escapeLike 转义 SQL LIKE 通配符。
func escapeLike(s string) string {
	s = strings.ReplaceAll(s, "\\", "\\\\")
	s = strings.ReplaceAll(s, "%", "\\%")
	s = strings.ReplaceAll(s, "_", "\\_")
	return s
}

// ApplyPagination 应用分页并统计总数。
func ApplyPagination[T any](q *gorm.DB, p Page, model any) ([]T, int64, error) {
	var total int64
	if err := q.Session(&gorm.Session{}).Model(model).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var list []T
	if err := q.Offset(p.Offset()).Limit(p.PageSize).Find(&list).Error; err != nil {
		return nil, 0, err
	}
	return list, total, nil
}
