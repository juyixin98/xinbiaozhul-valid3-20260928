// Package config loads local server configuration from a JSON file
// with environment variable overrides. No cloud services, no
// credentials.
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"http11subset/internal/httpx"
)

// Config is the process configuration.
type Config struct {
	Listen         string `json:"listen"`
	SQLitePath     string `json:"sqlite_path"`
	MaxHeaderBytes int    `json:"max_header_bytes"`
	MaxBodyBytes   int64  `json:"max_body_bytes"`
	MaxChunkLine   int    `json:"max_chunk_line_bytes"`
	ReadTimeoutMS  int    `json:"read_timeout_ms"`
	WriteTimeoutMS int    `json:"write_timeout_ms"`
	IdleTimeoutMS  int    `json:"idle_timeout_ms"`
	Continue100    bool   `json:"enable_100_continue"`
}

// Limits maps config to the parser's Limits.
func (c *Config) Limits() httpx.Limits {
	return httpx.Limits{
		MaxHeaderBytes:    c.MaxHeaderBytes,
		MaxBodyBytes:      c.MaxBodyBytes,
		MaxChunkLineBytes: c.MaxChunkLine,
	}
}

// Default returns local defaults.
func Default() *Config {
	return &Config{
		Listen:         "127.0.0.1:8080",
		SQLitePath:     "./data/app.db",
		MaxHeaderBytes: 64 * 1024,
		MaxBodyBytes:   1 << 20,
		MaxChunkLine:   8 * 1024,
		ReadTimeoutMS:  15000,
		WriteTimeoutMS: 15000,
		IdleTimeoutMS:  60000,
		Continue100:    true,
	}
}

// Load reads path if it exists (missing file is fine) and then applies
// environment overrides: HTTP11_LISTEN, HTTP11_SQLITE_PATH,
// HTTP11_MAX_HEADER_BYTES, HTTP11_MAX_BODY_BYTES, HTTP11_MAX_CHUNK_LINE.
func Load(path string) (*Config, error) {
	cfg := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		if err == nil {
			if err := json.Unmarshal(b, cfg); err != nil {
				return nil, fmt.Errorf("parse config %s: %w", path, err)
			}
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("read config %s: %w", path, err)
		}
	}
	applyEnvString("HTTP11_LISTEN", &cfg.Listen)
	applyEnvString("HTTP11_SQLITE_PATH", &cfg.SQLitePath)
	applyEnvInt("HTTP11_MAX_HEADER_BYTES", &cfg.MaxHeaderBytes)
	applyEnvInt64("HTTP11_MAX_BODY_BYTES", &cfg.MaxBodyBytes)
	applyEnvInt("HTTP11_MAX_CHUNK_LINE", &cfg.MaxChunkLine)
	applyEnvInt("HTTP11_READ_TIMEOUT_MS", &cfg.ReadTimeoutMS)
	applyEnvInt("HTTP11_WRITE_TIMEOUT_MS", &cfg.WriteTimeoutMS)
	applyEnvInt("HTTP11_IDLE_TIMEOUT_MS", &cfg.IdleTimeoutMS)
	if v := os.Getenv("HTTP11_ENABLE_100_CONTINUE"); v != "" {
		cfg.Continue100 = v == "1" || v == "true"
	}

	if cfg.MaxHeaderBytes <= 0 || cfg.MaxBodyBytes <= 0 || cfg.MaxChunkLine <= 0 {
		return nil, fmt.Errorf("limits must be positive")
	}
	return cfg, nil
}

func applyEnvString(key string, dst *string) {
	if v := os.Getenv(key); v != "" {
		*dst = v
	}
}

func applyEnvInt(key string, dst *int) {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return
		}
		*dst = n
	}
}

func applyEnvInt64(key string, dst *int64) {
	if v := os.Getenv(key); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return
		}
		*dst = n
	}
}
