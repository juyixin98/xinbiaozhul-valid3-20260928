// Package config holds broker resource limits and timing configuration.
//
// Every limit has an explicit, documented behaviour when exceeded — resource
// exhaustion is never silently reported as success.
package config

import (
	"encoding/json"
	"os"
	"time"
)

// Config is the complete broker configuration.
type Config struct {
	// Listen is the TCP address, e.g. "127.0.0.1:1883".
	Listen string `json:"listen"`

	// SQLite DSN: a file path or ":memory:".
	Database string `json:"database"`

	// MaxConnections is the hard cap on simultaneously connected clients.
	// New CONNECT beyond it receives CONNACK 0x03 (Server Unavailable).
	MaxConnections int `json:"max_connections"`

	// MaxInflightPerSession caps unacknowledged QoS1 messages delivered to
	// one session. When reached for an online subscriber the broker closes
	// that subscriber's connection (slow subscriber policy, documented in
	// README §7); inflight rows are persisted and redelivered on reconnect.
	MaxInflightPerSession int `json:"max_inflight_per_session"`

	// MaxOfflinePerSession caps queued messages for a disconnected durable
	// session. Overflow drops the OLDEST queued message (FIFO eviction);
	// inflight messages are never evicted.
	MaxOfflinePerSession int `json:"max_offline_per_session"`

	// SendQueueDepth is the per-connection outbound channel buffer. A slow
	// consumer whose buffer fills is closed (see MaxInflightPerSession).
	SendQueueDepth int `json:"send_queue_depth"`

	// MaxPacketBytes caps the accepted remaining length (protocol max is
	// 268435455; 1 MiB default for a local subset broker).
	MaxPacketBytes int `json:"max_packet_bytes"`

	// KeepAliveMultiplier: server disconnects if no control packet arrives
	// within KeepAlive * this value (MQTT-3.1.2-24; 1.5 per spec minimum).
	KeepAliveMultiplier float64 `json:"keepalive_multiplier"`

	// KeepAliveMax caps the Keep Alive seconds a client may request (0 in
	// CONNECT disables server-side keep alive).
	KeepAliveMax uint16 `json:"keepalive_max_seconds"`

	// WriteTimeout bounds a single network write.
	WriteTimeout time.Duration `json:"-"`
}

// Default returns the built-in local configuration.
func Default() Config {
	return Config{
		Listen:                "127.0.0.1:1883",
		Database:              "mqttlocal.db",
		MaxConnections:        100,
		MaxInflightPerSession: 32,
		MaxOfflinePerSession:  1000,
		SendQueueDepth:        64,
		MaxPacketBytes:        1 << 20,
		KeepAliveMultiplier:   1.5,
		KeepAliveMax:          600,
		WriteTimeout:          10 * time.Second,
	}
}

// LoadFile overlays a JSON file onto Default() (zero values are ignored by
// the caller pattern: start from Default, decode over it).
func LoadFile(path string) (Config, error) {
	c := Default()
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&c); err != nil {
		return c, err
	}
	return c, c.Validate()
}

// Validate rejects unusable configurations.
func (c *Config) Validate() error {
	if c.MaxConnections < 1 {
		return errBad("max_connections must be >= 1")
	}
	if c.MaxInflightPerSession < 1 {
		return errBad("max_inflight_per_session must be >= 1")
	}
	if c.MaxOfflinePerSession < 0 {
		return errBad("max_offline_per_session must be >= 0")
	}
	if c.SendQueueDepth < 1 {
		return errBad("send_queue_depth must be >= 1")
	}
	if c.MaxPacketBytes < 1 {
		return errBad("max_packet_bytes must be >= 1")
	}
	if c.KeepAliveMultiplier < 1.0 {
		return errBad("keepalive_multiplier must be >= 1.0 (MQTT-3.1.2-24)")
	}
	if c.WriteTimeout <= 0 {
		c.WriteTimeout = 10 * time.Second
	}
	return nil
}

type validationError string

func (e validationError) Error() string { return "config: " + string(e) }
func errBad(msg string) error           { return validationError(msg) }
