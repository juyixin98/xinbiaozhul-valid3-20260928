package storage_test

import (
	"context"
	"testing"

	"h1parse/internal/storage"
)

func TestSQLitePutGetIdempotent(t *testing.T) {
	s, err := storage.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()

	r1, err := s.Put(ctx, "text/plain", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if r1.Size != 5 || r1.ID == "" || len(r1.SHA256) != 64 {
		t.Fatalf("bad record: %+v", r1)
	}
	// 同内容幂等：ID 相同。
	r2, err := s.Put(ctx, "text/plain", []byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if r2.ID != r1.ID {
		t.Fatalf("content-addressed id not stable: %s vs %s", r1.ID, r2.ID)
	}
	got, err := s.Get(ctx, r1.ID)
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Body) != "hello" || got.ContentType != "text/plain" {
		t.Fatalf("get mismatch: %+v", got)
	}
	if _, err := s.Get(ctx, "deadbeefdeadbeef"); err != storage.ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestSQLiteContentTypesDistinct(t *testing.T) {
	s, _ := storage.Open(":memory:")
	defer s.Close()
	// 同内容不同 content-type 仍是同一内容 ID（内容寻址只看 body）。
	a, _ := s.Put(context.Background(), "text/plain", []byte("xyz"))
	b, _ := s.Put(context.Background(), "application/json", []byte("xyz"))
	if a.ID != b.ID {
		t.Fatalf("id should be body-addressed, got %s vs %s", a.ID, b.ID)
	}
}

func TestSQLitePing(t *testing.T) {
	s, _ := storage.Open(":memory:")
	defer s.Close()
	if err := s.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
}
