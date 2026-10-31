package web

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

// drain returns every event waiting on ch without blocking.
func drain(ch <-chan Event) []Event {
	var out []Event
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		default:
			return out
		}
	}
}

func TestCoalescingKeepsLatestPerKey(t *testing.T) {
	h := newHub(time.Hour) // never ticks; the test flushes by hand
	ch, unsub := h.subscribe()
	defer unsub()

	h.publish(Event{Key: "a", Data: "v1"})
	h.publish(Event{Key: "a", Data: "v2"})
	h.publish(Event{Key: "b", Data: "v3"})
	h.flush()

	got := drain(ch)
	if len(got) != 2 {
		t.Fatalf("delivered %d events, want 2: %+v", len(got), got)
	}
	if got[0].Key != "a" || got[0].Data != "v2" {
		t.Errorf("first = %+v, want key a with latest data v2", got[0])
	}
	if got[1].Key != "b" || got[1].Data != "v3" {
		t.Errorf("second = %+v, want key b with v3", got[1])
	}
}

func TestSlowClientDroppedWithoutBlockingPublish(t *testing.T) {
	h := newHub(time.Hour)
	_, unsubSlow := h.subscribe() // never reads
	defer unsubSlow()
	fast, unsubFast := h.subscribe()
	defer unsubFast()

	const n = clientBuffer + 10
	done := make(chan struct{})
	var received int
	go func() {
		defer close(done)
		for i := 0; i < n; i++ {
			h.publish(Event{Key: strconv.Itoa(i), Data: "d"})
			h.flush()
			received += len(drain(fast))
		}
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publish and flush blocked on a slow client")
	}

	if received != n {
		t.Errorf("fast client received %d events, want %d", received, n)
	}
	if got := h.count(); got != 1 {
		t.Errorf("clients after flush = %d, want 1 (slow client dropped)", got)
	}
}

func TestUnsubscribeIsIdempotent(t *testing.T) {
	h := newHub(time.Hour)
	_, unsub := h.subscribe()
	unsub()
	unsub()
	if got := h.count(); got != 0 {
		t.Fatalf("clients = %d, want 0", got)
	}
}

func TestRunFlushesOnTick(t *testing.T) {
	h := newHub(5 * time.Millisecond)
	stop := make(chan struct{})
	defer close(stop)
	go h.run(stop)

	ch, unsub := h.subscribe()
	defer unsub()
	h.publish(Event{Key: "k", Data: "tick"})

	select {
	case ev := <-ch:
		if ev.Data != "tick" {
			t.Fatalf("event = %+v", ev)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no event delivered by the ticker")
	}
}

func TestWriteEventCannotInjectFields(t *testing.T) {
	var b strings.Builder
	writeEvent(&b, Event{Name: "state\nretry: 1", Data: "line one\r\nevent: forged\rline three"})
	want := "event: stateretry: 1\ndata: line one\ndata: event: forged\ndata: line three\n\n"
	if b.String() != want {
		t.Fatalf("frame = %q, want %q", b.String(), want)
	}
}
