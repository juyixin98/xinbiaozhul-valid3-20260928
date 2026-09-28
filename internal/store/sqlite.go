// Package store persists durable MQTT session state in SQLite:
//
//   - sessions:      CleanSession=false clients, Will message (only while
//     registered), last used packet identifier hint
//   - subscriptions: durable topic filters
//   - inflight:      QoS 1 messages delivered to a session but not PUBACKed
//   - offline:       QoS 1 messages published while the session was offline
//   - retained:      retained messages keyed by topic (latest writer wins)
//
// Semantics needed by the broker (and asserted by tests):
//
//   - inflight rows are keyed by (client_id, packet_id); persisting with an
//     identifier already present is an upsert (reconnect redelivery).
//   - offline rows carry a monotonic per-client queue position so delivery
//     order is FIFO across broker restarts.
//   - deleting a session cascades to its subscriptions/inflight/offline.
//
// A single *sql.DB with SetMaxOpenConns(1) serializes access; workloads here
// are small and local. All Store methods are safe for concurrent use.
package store

import (
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned for missing rows.
var ErrNotFound = errors.New("mqttlocal/store: not found")

// StoredMessage is one persisted PUBLISH (inflight, offline or retained).
type StoredMessage struct {
	Topic    string
	Payload  []byte
	QoS      byte
	Retain   bool
	PacketID uint16 // 0 for retained/offline before id assignment
	Dup      bool
}

// WillState is a session's stored Last Will.
type WillState struct {
	Topic   string
	Payload []byte
	QoS     byte
	Retain  bool
}

// SessionRow is one durable session record.
type SessionRow struct {
	ClientID     string
	NextPacketID uint16 // hint; actual allocation happens in broker memory
	Will         *WillState
}

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the SQLite file at dsn, applies PRAGMAs and
// the schema. Use dsn ":memory:" for tests.
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		return nil, fmt.Errorf("pragma wal: %w", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		return nil, fmt.Errorf("pragma fk: %w", err)
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

func (s *Store) migrate() error {
	_, err := s.db.Exec(schema)
	return err
}

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
    client_id      TEXT PRIMARY KEY,
    next_packet_id INTEGER NOT NULL DEFAULT 1,
    will_topic     TEXT NOT NULL DEFAULT '',
    will_payload   BLOB NOT NULL DEFAULT X'',
    will_qos       INTEGER NOT NULL DEFAULT 0,
    will_retain    INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS subscriptions (
    client_id TEXT NOT NULL,
    filter    TEXT NOT NULL,
    qos       INTEGER NOT NULL,
    PRIMARY KEY (client_id, filter),
    FOREIGN KEY (client_id) REFERENCES sessions(client_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS inflight (
    client_id TEXT NOT NULL,
    packet_id INTEGER NOT NULL,
    topic     TEXT NOT NULL,
    payload   BLOB NOT NULL,
    qos       INTEGER NOT NULL,
    retain    INTEGER NOT NULL,
    dup       INTEGER NOT NULL,
    PRIMARY KEY (client_id, packet_id),
    FOREIGN KEY (client_id) REFERENCES sessions(client_id) ON DELETE CASCADE
);
CREATE TABLE IF NOT EXISTS offline (
    id        INTEGER PRIMARY KEY AUTOINCREMENT,
    client_id TEXT NOT NULL,
    topic     TEXT NOT NULL,
    payload   BLOB NOT NULL,
    qos       INTEGER NOT NULL,
    retain    INTEGER NOT NULL,
    FOREIGN KEY (client_id) REFERENCES sessions(client_id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_offline_client ON offline(client_id, id);
CREATE TABLE IF NOT EXISTS retained (
    topic   TEXT PRIMARY KEY,
    payload BLOB NOT NULL,
    qos     INTEGER NOT NULL
);
`

// --- sessions ---------------------------------------------------------------

// SaveSession upserts the session row (without Will; use SetWill).
func (s *Store) SaveSession(clientID string, nextPacketID uint16) error {
	_, err := s.db.Exec(
		`INSERT INTO sessions(client_id, next_packet_id) VALUES(?, ?)
		 ON CONFLICT(client_id) DO UPDATE SET next_packet_id = excluded.next_packet_id`,
		clientID, nextPacketID)
	return err
}

// SessionExists reports whether a durable session exists.
func (s *Store) SessionExists(clientID string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE client_id = ?`, clientID).Scan(&n)
	return n > 0, err
}

// GetSession loads a session including its Will (nil Will when absent).
func (s *Store) GetSession(clientID string) (*SessionRow, error) {
	row := s.db.QueryRow(
		`SELECT client_id, next_packet_id, will_topic, will_payload, will_qos, will_retain
		 FROM sessions WHERE client_id = ?`, clientID)
	var r SessionRow
	var willTopic string
	var willPayload []byte
	var willQoS, willRetain int
	if err := row.Scan(&r.ClientID, &r.NextPacketID, &willTopic, &willPayload, &willQoS, &willRetain); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if willTopic != "" {
		r.Will = &WillState{Topic: willTopic, Payload: willPayload, QoS: byte(willQoS), Retain: willRetain != 0}
	}
	return &r, nil
}

// SetWill stores or clears (nil) the Will for a session.
func (s *Store) SetWill(clientID string, w *WillState) error {
	if w == nil {
		_, err := s.db.Exec(
			`UPDATE sessions SET will_topic='', will_payload=X'', will_qos=0, will_retain=0
			 WHERE client_id=?`, clientID)
		return err
	}
	_, err := s.db.Exec(
		`UPDATE sessions SET will_topic=?, will_payload=?, will_qos=?, will_retain=?
		 WHERE client_id=?`, w.Topic, w.Payload, w.QoS, w.Retain, clientID)
	return err
}

// DeleteSession removes the session and (via cascade) its subscriptions,
// inflight and offline queues.
func (s *Store) DeleteSession(clientID string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE client_id = ?`, clientID)
	return err
}

// SetNextPacketID persists the packet-id allocation hint.
func (s *Store) SetNextPacketID(clientID string, id uint16) error {
	_, err := s.db.Exec(`UPDATE sessions SET next_packet_id=? WHERE client_id=?`, id, clientID)
	return err
}

// --- subscriptions ----------------------------------------------------------

// PutSubscription upserts one subscription filter/granted QoS.
func (s *Store) PutSubscription(clientID, filter string, qos byte) error {
	_, err := s.db.Exec(
		`INSERT INTO subscriptions(client_id, filter, qos) VALUES(?, ?, ?)
		 ON CONFLICT(client_id, filter) DO UPDATE SET qos = excluded.qos`,
		clientID, filter, qos)
	return err
}

// Subscriptions returns all durable subscriptions of a session.
func (s *Store) Subscriptions(clientID string) (map[string]byte, error) {
	rows, err := s.db.Query(`SELECT filter, qos FROM subscriptions WHERE client_id=?`, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]byte{}
	for rows.Next() {
		var f string
		var q int
		if err := rows.Scan(&f, &q); err != nil {
			return nil, err
		}
		out[f] = byte(q)
	}
	return out, rows.Err()
}

// --- inflight ---------------------------------------------------------------

// PutInflight persists (or overwrites, e.g. redelivery marks DUP) one
// unacknowledged QoS 1 message.
func (s *Store) PutInflight(clientID string, m StoredMessage) error {
	_, err := s.db.Exec(
		`INSERT INTO inflight(client_id, packet_id, topic, payload, qos, retain, dup)
		 VALUES(?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(client_id, packet_id)
		 DO UPDATE SET topic=excluded.topic, payload=excluded.payload,
		               qos=excluded.qos, retain=excluded.retain, dup=excluded.dup`,
		clientID, m.PacketID, m.Topic, m.Payload, m.QoS, m.Retain, m.Dup)
	return err
}

// DeleteInflight removes a message on PUBACK.
func (s *Store) DeleteInflight(clientID string, packetID uint16) error {
	_, err := s.db.Exec(`DELETE FROM inflight WHERE client_id=? AND packet_id=?`, clientID, packetID)
	return err
}

// Inflight returns all unacknowledged messages ordered by packet id for
// stable, observable redelivery ordering.
func (s *Store) Inflight(clientID string) ([]StoredMessage, error) {
	rows, err := s.db.Query(
		`SELECT packet_id, topic, payload, qos, retain, dup FROM inflight
		 WHERE client_id=? ORDER BY packet_id ASC`, clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMessages(rows, true)
}

// InflightCount returns the number of stored inflight messages.
func (s *Store) InflightCount(clientID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM inflight WHERE client_id=?`, clientID).Scan(&n)
	return n, err
}

// --- offline queue ----------------------------------------------------------

// EnqueueOffline appends a message to a disconnected durable session's queue.
func (s *Store) EnqueueOffline(clientID string, m StoredMessage) error {
	_, err := s.db.Exec(
		`INSERT INTO offline(client_id, topic, payload, qos, retain) VALUES(?, ?, ?, ?, ?)`,
		clientID, m.Topic, m.Payload, m.QoS, m.Retain)
	return err
}

// OfflineCount returns the queued message count.
func (s *Store) OfflineCount(clientID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM offline WHERE client_id=?`, clientID).Scan(&n)
	return n, err
}

// PopOffline removes and returns up to max oldest queued messages in FIFO
// order. Messages beyond max stay queued (used by reconnection, where QoS1
// messages can only be promoted while inflight window capacity exists).
func (s *Store) PopOffline(clientID string, max int) ([]StoredMessage, error) {
	if max <= 0 {
		return nil, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(
		`SELECT id, topic, payload, qos, retain FROM offline
		 WHERE client_id=? ORDER BY id ASC LIMIT ?`, clientID, max)
	if err != nil {
		return nil, err
	}
	type rec struct {
		id int64
		m  StoredMessage
	}
	var recs []rec
	for rows.Next() {
		var r rec
		if err := rows.Scan(&r.id, &r.m.Topic, &r.m.Payload, &r.m.QoS, &r.m.Retain); err != nil {
			rows.Close()
			return nil, err
		}
		recs = append(recs, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for _, r := range recs {
		if _, err := tx.Exec(`DELETE FROM offline WHERE id=?`, r.id); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	out := make([]StoredMessage, len(recs))
	for i, r := range recs {
		out[i] = r.m
	}
	return out, nil
}

// AllDurableClients returns client ids that have persisted sessions (startup
// recovery: their Wills document sessions killed by a broker crash).
func (s *Store) AllDurableClients() ([]string, error) {
	rows, err := s.db.Query(`SELECT client_id FROM sessions ORDER BY client_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// OfflineSubscriptions returns (clientID, filter, qos) rows for durable
// sessions that are NOT currently online; the broker passes the online set.
func (s *Store) OfflineSubscriptions(online map[string]bool) ([]OfflineSub, error) {
	rows, err := s.db.Query(`SELECT client_id, filter, qos FROM subscriptions ORDER BY client_id, filter`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []OfflineSub
	for rows.Next() {
		var r OfflineSub
		if err := rows.Scan(&r.ClientID, &r.Filter, &r.QoS); err != nil {
			return nil, err
		}
		if !online[r.ClientID] {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

// OfflineSub is one durable subscription of an offline session.
type OfflineSub struct {
	ClientID string
	Filter   string
	QoS      byte
}

// TrimOfflineOldest deletes the oldest queued messages until at most keep
// remain; it returns how many were removed.
func (s *Store) TrimOfflineOldest(clientID string, keep int) (int, error) {
	n, err := s.OfflineCount(clientID)
	if err != nil || n <= keep {
		return 0, err
	}
	drop := n - keep
	res, err := s.db.Exec(
		`DELETE FROM offline WHERE id IN (
		    SELECT id FROM offline WHERE client_id=? ORDER BY id ASC LIMIT ?
		 )`, clientID, drop)
	if err != nil {
		return 0, err
	}
	d, _ := res.RowsAffected()
	return int(d), nil
}

// --- retained ---------------------------------------------------------------

// SetRetained stores the retained message for topic. An empty payload clears
// it (MQTT-3.3.1-3/4 retained deletion convention).
func (s *Store) SetRetained(topic string, payload []byte, qos byte) error {
	if len(payload) == 0 {
		_, err := s.db.Exec(`DELETE FROM retained WHERE topic=?`, topic)
		return err
	}
	_, err := s.db.Exec(
		`INSERT INTO retained(topic, payload, qos) VALUES(?, ?, ?)
		 ON CONFLICT(topic) DO UPDATE SET payload=excluded.payload, qos=excluded.qos`,
		topic, payload, qos)
	return err
}

// RetainedMessage returns the retained message for an exact topic.
func (s *Store) RetainedMessage(topic string) (*StoredMessage, error) {
	row := s.db.QueryRow(`SELECT payload, qos FROM retained WHERE topic=?`, topic)
	var m StoredMessage
	m.Topic = topic
	m.Retain = true
	if err := row.Scan(&m.Payload, &m.QoS); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &m, nil
}

// AllRetained returns every retained message; the broker matches filters in
// memory.
func (s *Store) AllRetained() ([]StoredMessage, error) {
	rows, err := s.db.Query(`SELECT topic, payload, qos FROM retained ORDER BY topic ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredMessage
	for rows.Next() {
		var m StoredMessage
		m.Retain = true
		if err := rows.Scan(&m.Topic, &m.Payload, &m.QoS); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// RetainedMatching returns retained messages whose topic matches filter,
// ordered by topic for deterministic retained-delivery tests.
func (s *Store) RetainedMatching(matcher func(filter, topic string) bool, filter string) ([]StoredMessage, error) {
	all, err := s.AllRetained()
	if err != nil {
		return nil, err
	}
	var out []StoredMessage
	for _, m := range all {
		if matcher(filter, m.Topic) {
			out = append(out, m)
		}
	}
	return out, nil
}

// --- helpers ----------------------------------------------------------------

type rowScanner interface {
	Scan(dest ...any) error
	Next() bool
	Close() error
	Err() error
}

func scanMessages(rows *sql.Rows, withID bool) ([]StoredMessage, error) {
	var out []StoredMessage
	for rows.Next() {
		var m StoredMessage
		var packetID int
		var qos, retain, dup int
		if withID {
			if err := rows.Scan(&packetID, &m.Topic, &m.Payload, &qos, &retain, &dup); err != nil {
				return nil, err
			}
			m.PacketID = uint16(packetID)
			m.Dup = dup != 0
		}
		m.QoS = byte(qos)
		m.Retain = retain != 0
		out = append(out, m)
	}
	return out, rows.Err()
}
