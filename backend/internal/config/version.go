package config

// 版本信息，由构建时的 ldflags 注入：
//
//	go build -ldflags "-X github.com/tmp-platform/backend/internal/config.Version=1.0.0 ..."
var (
	// Version 平台版本号。
	Version = "1.0.0-mvp"
	// BuildTime 构建时间（RFC3339）。
	BuildTime = "unknown"
	// Commit 构建对应的 git commit。
	Commit = "unknown"
)
