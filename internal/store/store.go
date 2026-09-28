// Package store is the SQLite-backed persistence boundary for the broker:
// durable sessions, subscriptions, retained messages and unacknowledged
// QoS 1 deliveries ("inflight").
//
// Concurrency: a single *sql.DB with a write mutex is used; WAL mode allows
// readers while a write transaction is open. All methods are safe for
// concurrent use.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"sync"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a requested row does not exist.
var ErrNotFound = errors.New("store: not found")

// InflightRow is one unacknowledged QoS 1 delivery for a client.
type InflightRow struct {
	ClientID string
	PacketID uint16
	Topic    string
	Payload  []byte
	QoS      byte
	Retain   bool
	// Sent records whether the broker has already attempted to deliver this
	// message. Re-sending a Sent message must set the MQTT DUP flag.
	Sent bool
}

// SubRow is one persisted subscription.
type SubRow struct {
	ClientID string
	Filter   string
	QoS      byte
}

// RetainedRow is one retained message.
type RetainedRow struct {
	Topic   string
	Payload []byte
	QoS     byte
}

// Store wraps the SQLite database.
type Store struct {
	db   *sql.DB
	wrMu sync.Mutex
}

// New opens (creating if needed) the database at path and applies the schema
// and pragmas. Use ":memory:" for tests.
func New(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	// Single writer avoids "database is locked" under concurrent handlers.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS sessions(
			client_id  TEXT PRIMARY KEY,
			clean      INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS subscriptions(
			client_id TEXT NOT NULL,
			filter    TEXT NOT NULL,
			qos       INTEGER NOT NULL,
			PRIMARY KEY(client_id, filter)
		)`,
		`CREATE TABLE IF NOT EXISTS inflight(
			client_id TEXT NOT NULL,
			packet_id INTEGER NOT NULL,
			topic     TEXT NOT NULL,
			payload   BLOB NOT NULL,
			qos       INTEGER NOT NULL,
			retain    INTEGER NOT NULL DEFAULT 0,
			sent      INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			PRIMARY KEY(client_id, packet_id)
		)`,
		`CREATE TABLE IF NOT EXISTS retained(
			topic      TEXT PRIMARY KEY,
			payload    BLOB NOT NULL,
			qos        INTEGER NOT NULL,
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_inflight_client ON inflight(client_id)`,
		`CREATE INDEX IF NOT EXISTS idx_subs_client ON subscriptions(client_id)`,
	}
	for _, q := range stmts {
		if _, err := s.db.Exec(q); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// --- sessions --------------------------------------------------------------

// CreateSession records a session. clean marks a clean session; its rows are
// removed on disconnect by the broker via WipeClient.
func (s *Store) CreateSession(clientID string, clean bool, nowUnix int64) error {
	s.wrMu.Lock()
	defer s.wrMu.Unlock()
	c := 0
	if clean {
		c = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO sessions(client_id, clean, created_at, updated_at)
		 VALUES(?,?,?,?)
		 ON CONFLICT(client_id) DO UPDATE SET clean=excluded.clean, updated_at=excluded.updated_at`,
		clientID, c, nowUnix, nowUnix)
	return err
}

// TouchSession updates the session timestamp.
func (s *Store) TouchSession(clientID string, nowUnix int64) error {
	_, err := s.db.Exec(`UPDATE sessions SET updated_at=? WHERE client_id=?`, nowUnix, clientID)
	return err
}

// SessionClean reports the clean flag; ErrNotFound if absent.
func (s *Store) SessionClean(clientID string) (bool, error) {
	var c int
	err := s.db.QueryRow(`SELECT clean FROM sessions WHERE client_id=?`, clientID).Scan(&c)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	return c != 0, err
}

// CountDurableSessions reports how many non-clean sessions are persisted.
func (s *Store) CountDurableSessions() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE clean=0`).Scan(&n)
	return n, err
}

// WipeClient removes all state for a client (clean-session teardown).
func (s *Store) WipeClient(clientID string) error {
	s.wrMu.Lock()
	defer s.wrMu.Unlock()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM inflight WHERE client_id=?`,
		`DELETE FROM subscriptions WHERE client_id=?`,
		`DELETE FROM sessions WHERE client_id=?`,
	} {
		if _, err := tx.Exec(q, clientID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// --- subscriptions ---------------------------------------------------------

// UpsertSubscription stores (or updates the QoS of) one subscription.
func (s *Store) UpsertSubscription(clientID, filter string, qos byte) error {
	s.wrMu.Lock()
	defer s.wrMu.Unlock()
	_, err := s.db.Exec(
		`INSERT INTO subscriptions(client_id, filter, qos) VALUES(?,?,?)
		 ON CONFLICT(client_id, filter) DO UPDATE SET qos=excluded.qos`,
		clientID, filter, qos)
	return err
}

// CountSubscriptions returns the number of persisted subscriptions.
func (s *Store) CountSubscriptions(clientID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM subscriptions WHERE client_id=?`, clientID).
		Scan(&n)
	return n, err
}

// ListSubscriptions returns all subscriptions of a client ordered by filter
// (deterministic order for replaying retained messages).
func (s *Store) ListSubscriptions(clientID string) ([]SubRow, error) {
	rows, err := s.db.Query(
		`SELECT client_id, filter, qos FROM subscriptions WHERE client_id=? ORDER BY filter`,
		clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSubs(rows)
}

// AllSubscriptions returns every subscription of every client (used for
// routing). Rows are grouped by client.
func (s *Store) AllSubscriptions() ([]SubRow, error) {
	rows, err := s.db.Query(`SELECT client_id, filter, qos FROM subscriptions`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSubs(rows)
}

func scanSubs(rows *sql.Rows) ([]SubRow, error) {
	var out []SubRow
	for rows.Next() {
		var r SubRow
		var q int
		if err := rows.Scan(&r.ClientID, &r.Filter, &q); err != nil {
			return nil, err
		}
		r.QoS = byte(q)
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- inflight --------------------------------------------------------------

// InsertInflight persists a new unacknowledged delivery. A duplicate
// (clientID, packetID) overwrites the row — this is the at-least-once
// duplicate path (a publisher re-using an identifier with DUP=1).
func (s *Store) InsertInflight(r InflightRow, nowUnix int64) error {
	s.wrMu.Lock()
	defer s.wrMu.Unlock()
	rn, rt := 0, 0
	if r.Retain {
		rt = 1
	}
	if r.Sent {
		rn = 1
	}
	_, err := s.db.Exec(
		`INSERT INTO inflight(client_id, packet_id, topic, payload, qos, retain, sent, created_at)
		 VALUES(?,?,?,?,?,?,?,?)
		 ON CONFLICT(client_id, packet_id) DO UPDATE SET
		   topic=excluded.topic, payload=excluded.payload, qos=excluded.qos,
		   retain=excluded.retain, sent=excluded.sent, created_at=excluded.created_at`,
		r.ClientID, int(r.PacketID), r.Topic, r.Payload, int(r.QoS), rt, rn, nowUnix)
	return err
}

// MarkInflightSent flags one delivery as attempted.
func (s *Store) MarkInflightSent(clientID string, packetID uint16) error {
	_, err := s.db.Exec(
		`UPDATE inflight SET sent=1 WHERE client_id=? AND packet_id=?`,
		clientID, int(packetID))
	return err
}

// DeleteInflight acknowledges and removes one delivery. The sql.Result lets
// the caller tell an acknowledged row from a duplicate/unknown PUBACK.
func (s *Store) DeleteInflight(clientID string, packetID uint16) (sql.Result, error) {
	return s.db.Exec(
		`DELETE FROM inflight WHERE client_id=? AND packet_id=?`,
		clientID, int(packetID))
}

// CountInflight reports how many deliveries are awaiting PUBACK.
func (s *Store) CountInflight(clientID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM inflight WHERE client_id=?`, clientID).
		Scan(&n)
	return n, err
}

// CountInflightTotal reports broker-wide unacknowledged rows (startup metric).
func (s *Store) CountInflightTotal() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM inflight`).Scan(&n)
	return n, err
}

// ListInflight returns unacknowledged deliveries for a client, oldest first.
func (s *Store) ListInflight(clientID string) ([]InflightRow, error) {
	rows, err := s.db.Query(
		`SELECT client_id, packet_id, topic, payload, qos, retain, sent
		 FROM inflight WHERE client_id=? ORDER BY created_at, packet_id`,
		clientID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InflightRow
	for rows.Next() {
		var r InflightRow
		var pid, qos, retain, sent int
		if err := rows.Scan(&r.ClientID, &pid, &r.Topic, &r.Payload, &qos, &retain, &sent); err != nil {
			return nil, err
		}
		r.PacketID, r.QoS, r.Retain, r.Sent = uint16(pid), byte(qos), retain != 0, sent != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// --- retained --------------------------------------------------------------

// SetRetained stores a retained message. An empty payload deletes it.
func (s *Store) SetRetained(topic string, payload []byte, qos byte, nowUnix int64) error {
	s.wrMu.Lock()
	defer s.wrMu.Unlock()
	if len(payload) == 0 {
		_, err := s.db.Exec(`DELETE FROM retained WHERE topic=?`, topic)
		return err
	}
	_, err := s.db.Exec(
		`INSERT INTO retained(topic, payload, qos, created_at) VALUES(?,?,?,?)
		 ON CONFLICT(topic) DO UPDATE SET
		   payload=excluded.payload, qos=excluded.qos, created_at=excluded.created_at`,
		topic, payload, int(qos), nowUnix)
	return err
}

// RetainedMatching returns retained messages whose topic matches the filter
// (matching is done in Go, not SQL). The store itself has no wildcard
// dependency; callers pass the match predicate so this layer stays decoupled.
func (s *Store) RetainedMatching(match func(filter, topic string) bool, filter string) ([]RetainedRow, error) {
	rows, err := s.db.Query(`SELECT topic, payload, qos FROM retained ORDER BY topic`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RetainedRow
	for rows.Next() {
		var r RetainedRow
		var q int
		if err := rows.Scan(&r.Topic, &r.Payload, &q); err != nil {
			return nil, err
		}
		r.QoS = byte(q)
		if match(filter, r.Topic) {
			out = append(out, r)
		}
	}
	return out, rows.Err()
}

// CountRetained reports the number of retained messages.
func (s *Store) CountRetained() (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM retained`).Scan(&n)
	return n, err
}
