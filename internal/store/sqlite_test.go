package store

import (
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func TestSessionAndSubscriptions(t *testing.T) {
	st := openTemp(t)
	if err := st.SaveSession("c1", 1); err != nil {
		t.Fatal(err)
	}
	exists, err := st.SessionExists("c1")
	if err != nil || !exists {
		t.Fatalf("exists=%v err=%v", exists, err)
	}
	if err := st.PutSubscription("c1", "a/+", 1); err != nil {
		t.Fatal(err)
	}
	if err := st.PutSubscription("c1", "b/#", 0); err != nil {
		t.Fatal(err)
	}
	subs, err := st.Subscriptions("c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(subs) != 2 || subs["a/+"] != 1 || subs["b/#"] != 0 {
		t.Fatalf("subs=%v", subs)
	}
	// Upsert same filter changes qos.
	if err := st.PutSubscription("c1", "a/+", 0); err != nil {
		t.Fatal(err)
	}
	subs, _ = st.Subscriptions("c1")
	if subs["a/+"] != 0 {
		t.Fatalf("upsert failed: %v", subs)
	}
}

func TestInflightUpsertAndDelete(t *testing.T) {
	st := openTemp(t)
	_ = st.SaveSession("c1", 1)
	m1 := StoredMessage{Topic: "a", Payload: []byte("x"), QoS: 1, PacketID: 7}
	if err := st.PutInflight("c1", m1); err != nil {
		t.Fatal(err)
	}
	// Re-put with DUP (reconnect redelivery semantics at storage layer).
	m1.Dup = true
	if err := st.PutInflight("c1", m1); err != nil {
		t.Fatal(err)
	}
	got, err := st.Inflight("c1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].PacketID != 7 || !got[0].Dup {
		t.Fatalf("inflight=%+v", got)
	}
	if err := st.DeleteInflight("c1", 7); err != nil {
		t.Fatal(err)
	}
	got, _ = st.Inflight("c1")
	if len(got) != 0 {
		t.Fatalf("delete failed: %+v", got)
	}
}

func TestOfflineFIFOAndCap(t *testing.T) {
	st := openTemp(t)
	_ = st.SaveSession("c1", 1)
	for i := 0; i < 5; i++ {
		if err := st.EnqueueOffline("c1", StoredMessage{Topic: "t", Payload: []byte{byte('a' + i)}, QoS: 1}); err != nil {
			t.Fatal(err)
		}
	}
	n, err := st.OfflineCount("c1")
	if err != nil || n != 5 {
		t.Fatalf("count=%d err=%v", n, err)
	}
	// Pop up to 2 preserves FIFO order and keeps the rest.
	msgs, err := st.PopOffline("c1", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 2 || msgs[0].Payload[0] != 'a' || msgs[1].Payload[0] != 'b' {
		t.Fatalf("pop=%+v", msgs)
	}
	n, _ = st.OfflineCount("c1")
	if n != 3 {
		t.Fatalf("remaining=%d", n)
	}
	// TrimOldest.
	dropped, err := st.TrimOfflineOldest("c1", 1)
	if err != nil || dropped != 2 {
		t.Fatalf("dropped=%d err=%v", dropped, err)
	}
	msgs, _ = st.PopOffline("c1", 10)
	if len(msgs) != 1 || msgs[0].Payload[0] != 'e' {
		t.Fatalf("oldest eviction wrong: %+v", msgs)
	}
}

func TestRetainedDeleteAndMatch(t *testing.T) {
	st := openTemp(t)
	if err := st.SetRetained("a/b", []byte("hi"), 1); err != nil {
		t.Fatal(err)
	}
	if err := st.SetRetained("a/c", []byte("yo"), 0); err != nil {
		t.Fatal(err)
	}
	if r, err := st.RetainedMessage("a/b"); err != nil || string(r.Payload) != "hi" {
		t.Fatalf("retained get: %+v err=%v", r, err)
	}
	// Empty payload deletes.
	if err := st.SetRetained("a/b", nil, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RetainedMessage("a/b"); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	// Filtered retrieval uses an injected matcher (independent of the topic
	// package at this layer).
	called := false
	matcher := func(filter, topic string) bool { called = true; return topic == "a/c" }
	got, err := st.RetainedMatching(matcher, "a/+")
	if err != nil || len(got) != 1 || got[0].Topic != "a/c" || !called {
		t.Fatalf("matching got=%+v err=%v called=%v", got, err, called)
	}
}

func TestDeleteSessionCascades(t *testing.T) {
	st := openTemp(t)
	_ = st.SaveSession("c1", 1)
	_ = st.PutSubscription("c1", "a", 0)
	_ = st.PutInflight("c1", StoredMessage{Topic: "a", PacketID: 1, QoS: 1})
	_ = st.EnqueueOffline("c1", StoredMessage{Topic: "a", QoS: 1})
	if err := st.DeleteSession("c1"); err != nil {
		t.Fatal(err)
	}
	exists, _ := st.SessionExists("c1")
	if exists {
		t.Fatal("session still exists")
	}
	if subs, _ := st.Subscriptions("c1"); len(subs) != 0 {
		t.Fatalf("subs remain: %v", subs)
	}
	if n, _ := st.InflightCount("c1"); n != 0 {
		t.Fatalf("inflight remain: %d", n)
	}
	if n, _ := st.OfflineCount("c1"); n != 0 {
		t.Fatalf("offline remain: %d", n)
	}
}

// TestPersistenceAcrossReopen verifies durable state survives closing and
// reopening the database file (broker restart simulation at storage level).
func TestPersistenceAcrossReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "s.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.SaveSession("c1", 9)
	_ = st.PutSubscription("c1", "a/#", 1)
	_ = st.PutInflight("c1", StoredMessage{Topic: "a", Payload: []byte("z"), QoS: 1, PacketID: 3, Dup: true})
	_ = st.EnqueueOffline("c1", StoredMessage{Topic: "a", Payload: []byte("q"), QoS: 1})
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}

	st2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	row, err := st2.GetSession("c1")
	if err != nil || row.NextPacketID != 9 {
		t.Fatalf("session: %+v err=%v", row, err)
	}
	subs, _ := st2.Subscriptions("c1")
	if subs["a/#"] != 1 {
		t.Fatalf("subs lost: %v", subs)
	}
	inflight, _ := st2.Inflight("c1")
	if len(inflight) != 1 || inflight[0].PacketID != 3 || !inflight[0].Dup {
		t.Fatalf("inflight lost/wrong: %+v", inflight)
	}
	if n, _ := st2.OfflineCount("c1"); n != 1 {
		t.Fatalf("offline lost: %d", n)
	}
}
