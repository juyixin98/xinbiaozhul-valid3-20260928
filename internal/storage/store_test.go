package storage

import "testing"

// TestCreateGetCount exercises the SQLite store with the pure-Go
// driver in-memory.
func TestCreateGetCount(t *testing.T) {
	s, err := Open(":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer s.Close()

	if n, _ := s.Count(); n != 0 {
		t.Fatalf("initial count=%d want 0", n)
	}
	id, err := s.Create("title", "payload")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if id != 1 {
		t.Fatalf("first id=%d want 1", id)
	}
	got, err := s.Get(1)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Title != "title" || got.Payload != "payload" {
		t.Fatalf("record mismatch: %+v", got)
	}
	if n, _ := s.Count(); n != 1 {
		t.Fatalf("count=%d want 1", n)
	}
	if _, err := s.Get(999); err != ErrNotFound {
		t.Fatalf("missing id => %v want ErrNotFound", err)
	}
}
