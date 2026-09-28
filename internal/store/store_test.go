package store_test

import (
	"errors"
	"testing"

	"mqttd/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	// A file-backed database in a per-test temp dir keeps cases isolated and
	// also exercises the real (non ":memory:") open path.
	s, err := store.New(t.TempDir() + "/test.db")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSessionLifecycle(t *testing.T) {
	s := openStore(t)
	if err := s.CreateSession("c1", false, 1); err != nil {
		t.Fatal(err)
	}
	clean, err := s.SessionClean("c1")
	if err != nil || clean {
		t.Fatalf("SessionClean = %v,%v", clean, err)
	}
	if _, err := s.SessionClean("missing"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("missing session: want ErrNotFound, got %v", err)
	}
	if err := s.WipeClient("c1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionClean("c1"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("session not removed: %v", err)
	}
}

func TestInflightUpsertAndAck(t *testing.T) {
	s := openStore(t)
	if err := s.CreateSession("pub", false, 1); err != nil {
		t.Fatal(err)
	}
	row := store.InflightRow{ClientID: "pub", PacketID: 5, Topic: "t", Payload: []byte("m"), QoS: 1}
	if err := s.InsertInflight(row, 1); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountInflight("pub"); n != 1 {
		t.Fatalf("count = %d, want 1", n)
	}
	// Duplicate packet id overwrites rather than errors: at-least-once
	// retransmission path.
	row.Payload = []byte("m2")
	if err := s.InsertInflight(row, 2); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListInflight("pub")
	if err != nil || len(rows) != 1 || string(rows[0].Payload) != "m2" {
		t.Fatalf("upsert semantics wrong: %+v err=%v", rows, err)
	}
	res, err := s.DeleteInflight("pub", 5)
	if err != nil {
		t.Fatal(err)
	}
	if n, _ := res.RowsAffected(); n != 1 {
		t.Fatalf("deleted %d rows, want 1", n)
	}
	// Duplicate ack removes nothing.
	res, _ = s.DeleteInflight("pub", 5)
	if n, _ := res.RowsAffected(); n != 0 {
		t.Fatalf("duplicate ack removed %d rows", n)
	}
}

func TestMarkSentPersistsAcrossReopen(t *testing.T) {
	path := t.TempDir() + "/persist.db"
	s, err := store.New(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession("durable", false, 1); err != nil {
		t.Fatal(err)
	}
	row := store.InflightRow{ClientID: "durable", PacketID: 9, Topic: "t", Payload: []byte("x"), QoS: 1}
	if err := s.InsertInflight(row, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkInflightSent("durable", 9); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	// Reopen: the sent flag must survive — reconnect replay relies on it to
	// set DUP=1.
	s2, err := store.New(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	rows, err := s2.ListInflight("durable")
	if err != nil || len(rows) != 1 || !rows[0].Sent || rows[0].PacketID != 9 {
		t.Fatalf("durable inflight state wrong: %+v err=%v", rows, err)
	}
}

func TestSubscriptionsUpsert(t *testing.T) {
	s := openStore(t)
	if err := s.CreateSession("s", false, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSubscription("s", "a/+", 0); err != nil {
		t.Fatal(err)
	}
	// Re-subscribe with the same filter replaces the granted QoS.
	if err := s.UpsertSubscription("s", "a/+", 1); err != nil {
		t.Fatal(err)
	}
	subs, err := s.ListSubscriptions("s")
	if err != nil || len(subs) != 1 || subs[0].QoS != 1 {
		t.Fatalf("upsert wrong: %+v err=%v", subs, err)
	}
	if err := s.UpsertSubscription("s", "b/#", 1); err != nil {
		t.Fatal(err)
	}
	all, err := s.AllSubscriptions()
	if err != nil || len(all) != 2 {
		t.Fatalf("AllSubscriptions = %+v err=%v", all, err)
	}
}

func TestRetainedSetClearMatching(t *testing.T) {
	s := openStore(t)
	match := func(filter, topic string) bool { return filter == "#" || filter == topic }
	if err := s.SetRetained("a/b", []byte("v1"), 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRetained("x/y", []byte("v2"), 0, 1); err != nil {
		t.Fatal(err)
	}
	got, err := s.RetainedMatching(match, "#")
	if err != nil || len(got) != 2 {
		t.Fatalf("matching # = %+v err=%v", got, err)
	}
	// Empty payload clears.
	if err := s.SetRetained("a/b", nil, 1, 1); err != nil {
		t.Fatal(err)
	}
	got, _ = s.RetainedMatching(match, "#")
	if len(got) != 1 || got[0].Topic != "x/y" {
		t.Fatalf("clear wrong: %+v", got)
	}
	if n, _ := s.CountRetained(); n != 1 {
		t.Fatalf("count = %d", n)
	}
}
