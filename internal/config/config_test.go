package config_test

import (
	"testing"
	"time"

	"h1parse/internal/config"
)

func TestFromEnv(t *testing.T) {
	t.Setenv("H1PARSE_ADDR", "127.0.0.1:9999")
	t.Setenv("H1PARSE_DB", ":memory:")
	t.Setenv("H1PARSE_MAX_HEADER_BYTES", "2048")
	t.Setenv("H1PARSE_MAX_BODY_BYTES", "4096")
	t.Setenv("H1PARSE_MAX_CHUNK_BYTES", "512")
	t.Setenv("H1PARSE_IDLE_TIMEOUT", "12s")
	t.Setenv("H1PARSE_LOG_JSON", "1")

	cfg, err := config.FromEnv(config.Default())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Addr != "127.0.0.1:9999" || cfg.DB != ":memory:" {
		t.Fatalf("addr/db: %+v", cfg)
	}
	if cfg.Limits.MaxHeaderBytes != 2048 || cfg.Limits.MaxBodyBytes != 4096 ||
		cfg.Limits.MaxChunkSize != 512 {
		t.Fatalf("limits: %+v", cfg.Limits)
	}
	if cfg.IdleTimeout != 12*time.Second || !cfg.LogJSON {
		t.Fatalf("idle/json: %+v", cfg)
	}
}

func TestFromEnvRejectsBadSize(t *testing.T) {
	t.Setenv("H1PARSE_MAX_BODY_BYTES", "-1")
	if _, err := config.FromEnv(config.Default()); err == nil {
		t.Fatal("negative size must error")
	}
	t.Setenv("H1PARSE_MAX_BODY_BYTES", "abc")
	if _, err := config.FromEnv(config.Default()); err == nil {
		t.Fatal("non-numeric size must error")
	}
}

func TestDefaultIsUsable(t *testing.T) {
	cfg := config.Default()
	if cfg.Limits.MaxHeaderBytes <= 0 || cfg.Limits.MaxBodyBytes <= 0 ||
		cfg.Limits.MaxChunkSize <= 0 || cfg.IdleTimeout <= 0 {
		t.Fatalf("defaults must be positive: %+v", cfg)
	}
}
