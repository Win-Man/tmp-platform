package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config 平台运行时配置，全部来自环境变量并提供合理默认值。
type Config struct {
	// Addr HTTP 监听地址，TMP_ADDR，默认 :8080。
	Addr string
	// DataDir 运行时数据目录（存放 secret.key 与日志），TMP_DATA_DIR，默认 ./data。
	DataDir string
	// DBPath SQLite 元数据库文件路径，TMP_DB，默认工作目录下 tmp.db。
	DBPath string
	// Secret 主密钥，TMP_SECRET；为空时在 DataDir 下生成 secret.key（0600）。
	Secret string
	// Mock 是否强制启用演示模式（mock 采集器），TMP_MOCK=true。
	Mock bool
	// GinMode gin 运行模式，TMP_GIN_MODE，默认 release。
	GinMode string
	// LogLevel 日志级别 debug|info|warn|error，TMP_LOG_LEVEL，默认 info。
	LogLevel string
}

// Load 从环境变量加载配置。
func Load() *Config {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	cfg := &Config{
		Addr:     env("TMP_ADDR", ":8080"),
		DataDir:  env("TMP_DATA_DIR", filepath.Join(cwd, "data")),
		DBPath:   env("TMP_DB", filepath.Join(cwd, "tmp.db")),
		Secret:   os.Getenv("TMP_SECRET"),
		Mock:     envBool("TMP_MOCK", false),
		GinMode:  env("TMP_GIN_MODE", "release"),
		LogLevel: strings.ToLower(env("TMP_LOG_LEVEL", "info")),
	}
	if abs, err := filepath.Abs(cfg.DataDir); err == nil {
		cfg.DataDir = abs
	}
	if abs, err := filepath.Abs(cfg.DBPath); err == nil {
		cfg.DBPath = abs
	}
	return cfg
}

// env 读取字符串环境变量。
func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// envBool 读取布尔环境变量，支持 1/true/yes/on。
func envBool(key string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		return def
	}
	if b, err := strconv.ParseBool(v); err == nil {
		return b
	}
	switch v {
	case "yes", "on", "y":
		return true
	}
	return def
}
