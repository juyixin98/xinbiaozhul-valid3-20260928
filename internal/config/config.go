// Package config 承载服务的全部运行参数与解析逻辑。配置只有三个来源：
// 默认值、环境变量覆盖、显式构造（测试用）。不存在远程配置或账号依赖。
package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"h1parse/internal/protocol"
)

// Config 是一次服务运行的完整配置。
type Config struct {
	Addr        string          // 监听地址，如 127.0.0.1:8080
	DB          string          // SQLite DSN；":memory:" 或文件路径
	Limits      protocol.Limits // 协议资源上限
	IdleTimeout time.Duration   // 持久连接空闲超时
	ShutdownTO  time.Duration   // 优雅关闭等待
	LogJSON     bool            // 日志是否使用 JSON Lines（默认人类可读）
}

// Default 返回本地可复现的默认配置。
func Default() Config {
	return Config{
		Addr:        "127.0.0.1:8080",
		DB:          "h1parse.db",
		Limits:      protocol.DefaultLimits(),
		IdleTimeout: 30 * time.Second,
		ShutdownTO:  5 * time.Second,
		LogJSON:     false,
	}
}

// FromEnv 在给定基准配置上应用 H1PARSE_* 环境变量覆盖。
func FromEnv(base Config) (Config, error) {
	cfg := base
	var err error
	if v := os.Getenv("H1PARSE_ADDR"); v != "" {
		cfg.Addr = v
	}
	if v := os.Getenv("H1PARSE_DB"); v != "" {
		cfg.DB = v
	}
	if v := os.Getenv("H1PARSE_MAX_HEADER_BYTES"); v != "" {
		if cfg.Limits.MaxHeaderBytes, err = parseSize(v); err != nil {
			return cfg, fmt.Errorf("H1PARSE_MAX_HEADER_BYTES: %w", err)
		}
	}
	if v := os.Getenv("H1PARSE_MAX_BODY_BYTES"); v != "" {
		if cfg.Limits.MaxBodyBytes, err = parseSize(v); err != nil {
			return cfg, fmt.Errorf("H1PARSE_MAX_BODY_BYTES: %w", err)
		}
	}
	if v := os.Getenv("H1PARSE_MAX_CHUNK_BYTES"); v != "" {
		if cfg.Limits.MaxChunkSize, err = parseSize(v); err != nil {
			return cfg, fmt.Errorf("H1PARSE_MAX_CHUNK_BYTES: %w", err)
		}
	}
	if v := os.Getenv("H1PARSE_IDLE_TIMEOUT"); v != "" {
		if cfg.IdleTimeout, err = time.ParseDuration(v); err != nil {
			return cfg, fmt.Errorf("H1PARSE_IDLE_TIMEOUT: %w", err)
		}
	}
	if v := os.Getenv("H1PARSE_LOG_JSON"); v == "1" || v == "true" {
		cfg.LogJSON = true
	}
	return cfg, nil
}

// parseSize 解析正十进制字节数（不引入 k/m 后缀魔法，避免歧义）。
func parseSize(v string) (int64, error) {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("must be a positive integer byte count, got %q", v)
	}
	return n, nil
}
