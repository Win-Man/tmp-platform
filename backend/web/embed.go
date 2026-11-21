// Package web 内嵌前端构建产物（dist），使后端可作为单二进制交付。
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Assets 返回以 dist 为根的文件系统；读取失败返回 nil（由调用方降级）。
func Assets() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil
	}
	return sub
}
