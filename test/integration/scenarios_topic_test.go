package integration_test

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"mqttlocal/internal/packet"
	"mqttlocal/internal/testsupport"
)

// TestWildcardBoundariesAndRetained covers wildcard edge delivery plus
// retained-message rules: store-on-retain, retain delivered ONLY on
// subscribe with RETAIN flag set, empty retained payload clears, no
// retained on a fresh non-matching filter.
func TestWildcardBoundariesAndRetained(t *testing.T) {
	h := testsupport.StartHarness(t, defaultConfig(t))
	pub := connectDurable(t, h, "pub-w")
	sub := connectDurable(t, h, "sub-w")

	mkSub := func(id uint16, filter string, qos byte) byte {
		if err := sub.SendSubscribe(id, filter, qos); err != nil {
			t.Fatal(err)
		}
		_, granted, err := sub.ReadSuback(2 * time.Second)
		if err != nil || len(granted) != 1 {
			t.Fatalf("suback filter=%s: granted=%v err=%v", filter, granted, err)
		}
		return granted[0]
	}

	// Lay down retained messages at boundary topics.
	logStep(t, pub, "RETAIN-SET", "retained at a, a/b, a/b/c, and empty-clear target a/x")
	retained := []struct {
		topic   string
		payload string
		qos     byte
	}{
		{"a", "root", 0},
		{"a/b", "mid", 1},
		{"a/b/c", "deep", 0},
	}
	for i, r := range retained {
		if err := pub.SendPublish(r.topic, []byte(r.payload), r.qos, uint16(200+i), false, true); err != nil {
			t.Fatal(err)
		}
		if r.qos == 1 {
			f, err := pub.ReadFrame(2 * time.Second)
			if err != nil || f.Type != packet.TypePUBACK {
				t.Fatalf("retained qos1 puback: %v %s", err, f.Type)
			}
		}
	}
	// Empty retained payload deletes (MQTT-3.3.1-5 convention).
	if err := pub.SendPublish("a/x", []byte{}, 0, 0, false, true); err != nil {
		t.Fatal(err)
	}
	// Drain: retained messages must NOT be delivered to existing subscribers
	// at storage time (no subscription yet).
	if f, err := sub.ReadFrame(300 * time.Millisecond); err == nil {
		t.Fatalf("retained message unexpectedly delivered pre-subscribe: %s", f.Type)
	}

	// # matches a, a/b, a/b/c (all retained) on fresh subscribe.
	if g := mkSub(1, "#", 1); g != 1 {
		t.Fatalf("granted %d", g)
	}
	pubs, _ := collectPublishes(t, sub, 400*time.Millisecond, 20)
	gotTopics := map[string]*testsupport.PublishFrame{}
	for _, p := range pubs {
		if !p.Retain {
			t.Fatalf("retained delivery must carry RETAIN flag: %+v", p)
		}
		gotTopics[p.Topic] = p
	}
	for _, want := range []string{"a", "a/b", "a/b/c"} {
		if _, ok := gotTopics[want]; !ok {
			t.Fatalf("filter # missing retained %q, got %v", want, topicsOf(pubs))
		}
	}
	if _, ok := gotTopics["a/x"]; ok {
		t.Fatalf("deleted retained message delivered")
	}
	// Retained QoS 1 message downgraded/capped at granted QoS 1 keeps its id
	// non-zero; qos0 retained are qos0.
	if p := gotTopics["a/b"]; p.QoS != 1 || p.PacketID == 0 {
		t.Fatalf("retained qos1 delivery wrong: %+v", p)
	}

	// Re-subscribing to a narrower filter delivers only that subtree.
	if g := mkSub(2, "a/+", 1); g != 1 {
		t.Fatalf("granted %d", g)
	}
	pubs2, _ := collectPublishes(t, sub, 400*time.Millisecond, 20)
	got2 := map[string]bool{}
	for _, p := range pubs2 {
		if !p.Retain {
			t.Fatalf("non-retained frame on subscribe: %+v", p)
		}
		got2[p.Topic] = true
	}
	// a/+ matches a/b only (not "a" — + needs a level; not a/b/c).
	if !got2["a/b"] || got2["a"] || got2["a/b/c"] {
		t.Fatalf("a/+ retained set wrong: %v", got2)
	}

	// Runtime wildcard fan-out boundary cases. A FRESH subscribing
	// connection per case prevents earlier broad filters (e.g. "#") from
	// matching a later case's topic.
	cases := []struct {
		pubTopic string
		filter   string
		want     bool
	}{
		{"a", "#", true},
		{"a/", "#", true},
		{"a/b", "a/+", true},
		{"a", "a/+", false},
		{"a/b/c", "a/+", false},
		{"a/b/c", "a/#", true},
		{"b/x", "a/#", false},
		{"/x", "+/x", true},
	}
	for i, tc := range cases {
		pfx := fmt.Sprintf("e%d", i)
		filter := pfx + "/" + tc.filter
		pubT := pfx + "/" + tc.pubTopic

		edgeSub := h.NewClient()
		if ca, err := edgeSub.Connect(testsupport.BasicConnectOpts("edge-sub-"+strconv.Itoa(i), true, 0)); err != nil || ca.Code != 0 {
			t.Fatalf("edge sub %d connect: %+v %v", i, ca, err)
		}
		if err := edgeSub.SendSubscribe(uint16(100+i), filter, 0); err != nil {
			t.Fatal(err)
		}
		if id, granted, err := edgeSub.ReadSuback(2 * time.Second); err != nil || int(id) != 100+i || len(granted) != 1 || granted[0] != 0 {
			t.Fatalf("case %d suback: id=%d granted=%v err=%v", i, id, granted, err)
		}
		if err := pub.SendPublish(pubT, []byte("e"), 0, 0, false, false); err != nil {
			t.Fatal(err)
		}
		f, err := edgeSub.ReadFrame(300 * time.Millisecond)
		if tc.want && err != nil {
			t.Fatalf("filter=%s topic=%s expected delivery, got err %v", tc.filter, tc.pubTopic, err)
		}
		if !tc.want && err == nil {
			t.Fatalf("filter=%s topic=%s must NOT match, got %s", tc.filter, tc.pubTopic, f.Type)
		}
		if tc.want {
			p, _ := f.ParsePublish()
			if p.Topic != pubT || p.Retain {
				t.Fatalf("normal delivery must not be retained: %+v", p)
			}
		}
		_ = edgeSub.Close()
	}
}

func topicsOf(pubs []*testsupport.PublishFrame) []string {
	var out []string
	for _, p := range pubs {
		out = append(out, p.Topic)
	}
	return out
}
