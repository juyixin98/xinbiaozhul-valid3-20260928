// Package storage 用 SQLite 持久化业务记录。它位于协议层之下：
// 业务处理把已经完整读出、长度可信的消息体交给 Store，Store 只负责
// 事务、去重与查询，不接触任何 HTTP 线协议概念。
package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	_ "modernc.org/sqlite" // 纯 Go 驱动，注册到 database/sql
)

// Record 是一条已存储的请求体记录。
type Record struct {
	ID          string `json:"id"`     // 内容寻址 ID：sha256(body) 前 16 位十六进制
	SHA256      string `json:"sha256"` // 完整 sha256 十六进制
	Size        int    `json:"size"`   // 字节数
	ContentType string `json:"content_type"`
	Body        []byte `json:"body,omitempty"`
	CreatedAt   string `json:"created_at"` // UTC RFC3339
}

// ErrNotFound 表示内容寻址 ID 无对应记录。
var ErrNotFound = errors.New("record not found")

// Store 是存储能力边界（接口，方便业务层测试用内存假实现替换）。
type Store interface {
	// Put 幂等写入一条记录，返回最终记录（内容寻址：同内容同 ID）。
	Put(ctx context.Context, contentType string, body []byte) (*Record, error)
	// Get 按内容寻址 ID 查询；不存在返回 ErrNotFound。
	Get(ctx context.Context, id string) (*Record, error)
	// Ping 验证数据库可用。
	Ping(ctx context.Context) error
	Close() error
}

// SQLiteStore 是 Store 的 SQLite 实现。
type SQLiteStore struct {
	db *sql.DB
}

const ddl = `
CREATE TABLE IF NOT EXISTS records (
	id           TEXT PRIMARY KEY,
	sha256       TEXT NOT NULL UNIQUE,
	size         INTEGER NOT NULL,
	content_type TEXT NOT NULL,
	body         BLOB NOT NULL,
	created_at   TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now'))
);`

// Open 打开（必要时创建）SQLite 数据库。dsn 可以是文件路径或 ":memory:"。
func Open(dsn string) (*SQLiteStore, error) {
	// _pragma 让文件模式也具备合理的并发/耐久设置；busy_timeout 防止
	// 偶发 SQLITE_BUSY 直接冒到业务层。
	dsnWith := dsn
	if dsn != ":memory:" {
		dsnWith = dsn + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(on)"
	}
	db, err := sql.Open("sqlite", dsnWith)
	if err != nil {
		return nil, fmt.Errorf("open sqlite %q: %w", dsn, err)
	}
	// 单连接：modernc 的内存模式与 WAL 行为在单连接下最简单确定。
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite %q: %w", dsn, err)
	}
	if _, err := db.Exec(ddl); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("init schema: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *SQLiteStore) Close() error                   { return s.db.Close() }

// Put 幂等写入。
func (s *SQLiteStore) Put(ctx context.Context, contentType string, body []byte) (*Record, error) {
	sum := sha256.Sum256(body)
	full := hex.EncodeToString(sum[:])
	id := full[:16]
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO records (id, sha256, size, content_type, body)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO NOTHING`,
		id, full, len(body), contentType, body)
	if err != nil {
		return nil, fmt.Errorf("insert record: %w", err)
	}
	return s.Get(ctx, id)
}

// Get 查询。
func (s *SQLiteStore) Get(ctx context.Context, id string) (*Record, error) {
	var r Record
	err := s.db.QueryRowContext(ctx,
		`SELECT id, sha256, size, content_type, body, created_at
		   FROM records WHERE id = ?`, id).
		Scan(&r.ID, &r.SHA256, &r.Size, &r.ContentType, &r.Body, &r.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get record: %w", err)
	}
	return &r, nil
}
