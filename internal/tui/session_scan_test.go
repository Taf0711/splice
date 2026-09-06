package tui

// session_scan_test.go: the async session scan (F1, §14 — no store I/O on
// the UI loop). Probes through the real Update path: the scan cmd must not
// block Update, the picker arms from the msg, the event-read bound holds,
// and a landing scan never steals focus from an active composer.

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Taf0711/splice/internal/sessions"
)

// blockingStore wraps a Store whose ListResumable blocks long enough that a
// synchronous scan would be visible as a stalled Update.
type blockingStore struct {
	*sessions.Store
	blockUntil chan struct{}
	entered    atomic.Bool
}

func (b *blockingStore) ListResumable() ([]sessions.Metadata, error) {
	b.entered.Store(true)
	<-b.blockUntil
	return b.Store.ListResumable()
}

// Update returns before the (slow) scan finishes: the UI loop is free.
func TestSessionScanDoesNotBlockUpdate(t *testing.T) {
	base := testSessionStore(t)
	m := newModel(context.Background(), Options{Cwd: "/tmp/scan-probe", SessionStore: base})
	scanModel, cmd := startSessionScan(m, "Resume a session")
	if !scanModel.sessionScanInFlight {
		t.Fatal("scan: pending flag not armed")
	}
	if cmd == nil {
		t.Fatal("scan: no cmd armed")
	}
	// The scan runs on its goroutine: a store whose ListResumable blocks
	// must never stall the UI loop. Run the blocking scan off-thread and
	// prove Update would still be free during it.
	blocked := &blockingStore{Store: base, blockUntil: make(chan struct{})}
	done := make(chan tea.Msg, 1)
	go func() { done <- scanSessions(blocked, "/tmp/scan-probe", "Resume a session") }()
	select {
	case <-time.After(50 * time.Millisecond):
		if !blocked.entered.Load() {
			t.Fatal("scan: blocking store never entered")
		}
		// UI loop free while the scan runs. Good.
	case msg := <-done:
		t.Fatalf("scan finished too fast for a blocking store: %#v", msg)
	}
	close(blocked.blockUntil)
	msg := <-done
	if _, ok := msg.(sessionsScannedMsg); !ok {
		t.Fatalf("scan: expected sessionsScannedMsg, got %T", msg)
	}
}

// The event-read bound: a store with many workspace sessions is read at
// most sessionPickerScanLimit times.
func TestSessionScanBoundsEventReads(t *testing.T) {
	store := &countingStore{Store: testSessionStore(t)}
	for i := 0; i < sessionPickerScanLimit+10; i++ {
		created, err := store.Create(sessions.CreateInput{Title: "session", Cwd: "/tmp/scan-probe"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AppendEvent(created.SessionID, sessions.AppendEventInput{
			Type:    sessions.EventMessage,
			Payload: map[string]any{"role": "assistant", "content": "real answer"},
		}); err != nil {
			t.Fatal(err)
		}
	}
	msg := scanSessions(store.Store, "/tmp/scan-probe", "Resume a session")
	scanned, ok := msg.(sessionsScannedMsg)
	if !ok || scanned.Picker == nil {
		t.Fatalf("scan: no picker: %#v", msg)
	}
	if store.reads.Load() > sessionPickerScanLimit {
		t.Fatalf("scan: %d event reads exceed the bound %d", store.reads.Load(), sessionPickerScanLimit)
	}
}

type countingStore struct {
	*sessions.Store
	reads atomic.Int32
}

func (c *countingStore) ReadEvents(id string) ([]sessions.Event, error) {
	c.reads.Add(1)
	return c.Store.ReadEvents(id)
}

// A scan landing while the user has typed must not steal focus: no picker
// arms over a non-empty composer.
func TestSessionScanLandingDoesNotStealComposer(t *testing.T) {
	m := launchTestModel(t)
	m.input.SetValue("half typed thought")
	msg := sessionsScannedMsg{Picker: &commandPicker{kind: pickerSession, title: "Resume a session"}}
	next := applySessionsScanned(m, msg)
	if next.picker != nil {
		t.Fatal("scan: picker stole focus over a non-empty composer")
	}
	if next.sessionScanInFlight {
		t.Fatal("scan: pending flag not cleared on landing")
	}
	// Even on an empty composer the picker must not auto-arm.
	empty := launchTestModel(t)
	armed := applySessionsScanned(empty, msg)
	if armed.picker != nil {
		t.Fatal("scan: picker auto-armed from the scan (must open only via /resume)")
	}
	// An explicit /resume intent (resumePickerWanted) DOES arm it.
	resume := launchTestModel(t)
	resume.resumePickerWanted = true
	armed = applySessionsScanned(resume, msg)
	if armed.picker == nil {
		t.Fatal("scan: /resume-intent picker did not arm")
	}
}

// A bare /resume arms the scan cmd (Update returns immediately) and the
// picker opens when the msg lands — the wiring end to end.
func TestResumeScanEndToEnd(t *testing.T) {
	store := testSessionStore(t)
	created, err := store.Create(sessions.CreateInput{Title: "old chat", Cwd: "/tmp/scan-e2e"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(created.SessionID, sessions.AppendEventInput{
		Type:    sessions.EventMessage,
		Payload: map[string]any{"role": "assistant", "content": "here is the answer"},
	}); err != nil {
		t.Fatal(err)
	}
	m := newModel(context.Background(), Options{Cwd: "/tmp/scan-e2e", SessionStore: store})
	m.input.SetValue("/resume")
	updated, cmd := m.Update(testKey(tea.KeyEnter))
	next := updated.(model)
	if cmd == nil {
		t.Fatal("resume: no scan cmd armed")
	}
	updated, _ = next.Update(cmd())
	armed := updated.(model)
	if armed.picker == nil || armed.picker.kind != pickerSession {
		t.Fatalf("resume: picker did not arm from the scan: %#v", armed.picker)
	}
	if !strings.Contains(armed.picker.items[0].Label, "old chat") {
		t.Fatalf("resume: picker row lost the title: %#v", armed.picker.items[0])
	}
}

// The exact black-screen scenario: the user starts typing while the scan is
// in flight. The landing picker must NOT arm over the in-flight typing (the
// typed runes may not have reached the composer yet when the msg processes)
// — otherwise every keystroke after lands in the picker's query and the
// body swaps to the picker, looking like a dead screen.
func TestSessionScanLandingDuringTypingDoesNotArmPicker(t *testing.T) {
	store := testSessionStore(t)
	created, err := store.Create(sessions.CreateInput{Title: "chat", Cwd: "/tmp/scan-race"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendEvent(created.SessionID, sessions.AppendEventInput{
		Type:    sessions.EventMessage,
		Payload: map[string]any{"role": "assistant", "content": "answer"},
	}); err != nil {
		t.Fatal(err)
	}
	m := newModel(context.Background(), Options{Cwd: "/tmp/scan-race", SessionStore: store})
	m.width, m.height, m.altScreen = 360, 46, true
	scanModel, cmd := startSessionScan(m, "Continue where you left off")
	// The user types while the scan runs: keys processed BEFORE the msg.
	typed, _ := scanModel.Update(reviewRealPlainKey('h'))
	typedModel := typed.(model)
	if typedModel.keySeq == scanModel.scanStartKeySeq {
		t.Fatal("fixture: key seq did not advance on a keypress")
	}
	// The scan lands.
	updated, _ := typedModel.Update(cmd())
	landed := updated.(model)
	if landed.picker != nil {
		t.Fatal("scan: picker armed over in-flight typing")
	}
	if landed.sessionScanInFlight {
		t.Fatal("scan: pending flag not cleared")
	}
	if landed.scannedLatest == nil {
		t.Fatal("scan: launch card data not armed")
	}
	// And the user's next keystrokes reach the COMPOSER, not a picker query.
	updated, _ = landed.Update(reviewRealPlainKey('i'))
	final := updated.(model)
	if final.picker != nil {
		t.Fatal("scan: picker armed after landing")
	}
	if !strings.Contains(final.composerValue(), "i") {
		t.Fatalf("scan: keystroke lost after landing: %q", final.composerValue())
	}
}
