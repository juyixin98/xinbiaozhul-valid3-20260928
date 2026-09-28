// Package storage persists small JSON records in a local SQLite
// database via the pure-Go modernc.org/sqlite driver (no CGO).
package storage

import (
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"
)

// Store is a SQLite-backed record store.
type Store struct {
	db *sql.DB
}

// ErrNotFound is returned for an unknown id.
var ErrNotFound = errors.New("record not found")

// Record is one persisted row.
type Record struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Payload string `json:"payload"`
}

// Open opens (creating the schema in) the database at path. Use
// ":memory:" for ephemeral tests.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", path, err)
	}
	// A single connection avoids "database is locked" under local
	// serial/HTTP concurrency; WAL is enabled for file databases.
	db.SetMaxOpenConns(1)
	s := &Store{db: db}
	if err := s.init(path); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) init(path string) error {
	if path != ":memory:" {
		if _, err := s.db.Exec(`PRAGMA journal_mode=WAL;`); err != nil {
			return fmt.Errorf("set WAL: %w", err)
		}
	}
	if _, err := s.db.Exec(`PRAGMA foreign_keys=ON;`); err != nil {
		return fmt.Errorf("enable foreign keys: %w", err)
	}
	const schema = `
CREATE TABLE IF NOT EXISTS records (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    title   TEXT    NOT NULL,
    payload TEXT    NOT NULL,
    created TEXT    NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}
	return nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// Create inserts a record and returns its new id.
func (s *Store) Create(title, payload string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO records(title, payload) VALUES(?, ?);`, title, payload)
	if err != nil {
		return 0, fmt.Errorf("insert record: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}
	return id, nil
}

// Get fetches a record by id.
func (s *Store) Get(id int64) (*Record, error) {
	row := s.db.QueryRow(
		`SELECT id, title, payload FROM records WHERE id = ?;`, id)
	var r Record
	if err := row.Scan(&r.ID, &r.Title, &r.Payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("select record: %w", err)
	}
	return &r, nil
}

// Count returns the number of stored records (used in tests / health).
func (s *Store) Count() (int64, error) {
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM records;`).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}
