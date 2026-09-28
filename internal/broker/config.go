package broker

import "time"

// Config is the explicit input contract of the broker. Zero values are not
// valid; use DefaultConfig and override.
type Config struct {
	// Listen is the TCP address, e.g. "127.0.0.1:1883".
	Listen string
	// DBPath is the SQLite DSN path (":memory:" for ephemeral tests).
	DBPath string
	// MaxPacketBytes bounds the advertised remaining length of one packet.
	MaxPacketBytes int
	// MaxSessions bounds the number of persisted sessions.
	MaxSessions int
	// MaxSubscriptionsPerClient bounds subscriptions per client.
	MaxSubscriptionsPerClient int
	// MaxInflightPerClient bounds unacknowledged QoS 1 deliveries per client
	// (in either direction for an online client).
	MaxInflightPerClient int
	// MaxRetained bounds retained messages broker-wide.
	MaxRetained int
	// SendQueueDepth is the number of messages buffered per slow client
	// before QoS 0 messages are dropped.
	SendQueueDepth int
	// SendWriteTimeout bounds how long one queued frame may block in the
	// kernel waiting for TCP send space. A client whose receive window stays
	// closed past this is a dead slow consumer; the connection is closed so
	// it cannot pin a writer goroutine forever. 0 disables the bound.
	SendWriteTimeout time.Duration
	// KeepAliveFactor is multiplied by the client's keep-alive seconds to
	// derive the broker-side read deadline (MQTT recommends 1.5).
	KeepAliveFactor float64
	// KeepAliveMax caps the negotiated timeout regardless of client request;
	// 0 disables the cap.
	KeepAliveMax time.Duration
	// RetransmitInterval is the unacknowledged-QoS1 re-send interval.
	RetransmitInterval time.Duration
	// AcceptAnonymous allows CONNECT without credentials. This subset has no
	// authentication database; anonymous is the only real mode.
	AcceptAnonymous bool
}

// DefaultConfig returns conservative local-only defaults with explicit
// resource bounds.
func DefaultConfig() Config {
	return Config{
		Listen:                    "127.0.0.1:1883",
		DBPath:                    "mqttd.db",
		MaxPacketBytes:            256 * 1024,
		MaxSessions:               1024,
		MaxSubscriptionsPerClient: 64,
		MaxInflightPerClient:      128,
		MaxRetained:               4096,
		SendQueueDepth:            256,
		SendWriteTimeout:          30 * time.Second,
		KeepAliveFactor:           1.5,
		KeepAliveMax:              10 * time.Minute,
		RetransmitInterval:        2 * time.Second,
		AcceptAnonymous:           true,
	}
}
