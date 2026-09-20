package tui

// session_scan.go: the resume/session scan runs OFF the UI loop. On a
// machine with thousands of stored sessions, Store.ListResumable (metadata
// read per session) plus the per-session event reads used to block
// Update() for seconds at launch and on /resume — the F1 boundary (§14)
// says the TUI keeps filesystem access off the UI loop, exactly like the
// handoff card's worktree inputs. The scan goroutine touches only the
// store and immutable inputs; it never reads model fields. The result
// lands as a sessionsScannedMsg and the picker/launch card arm from it.

import (
	tea "charm.land/bubbletea/v2"
	"time"

	"github.com/Taf0711/splice/internal/sessions"
	splicerun "github.com/Taf0711/splice/internal/splice"
)

// scanNow is the clock the scan uses for label timestamps. A package var so
// tests can pin it; the scan goroutine must not read the model's clock.
var scanNow = time.Now

// sessionPickerScanLimit bounds how many of the newest workspace sessions
// get a full event read during the picker scan. The palette shows at most
// pickerOverlayMaxVisible rows; the limit keeps worst-case I/O bounded
// while leaving headroom for empty/failed sessions the content check
// drops. Sessions beyond the newest N are never read.
const sessionPickerScanLimit = 40

// sessionsScannedMsg carries the async scan result back to the UI loop.
// Latest is the newest qualifying workspace session (with its events and
// reconstructed design state) for the launch resume card; Picker is the
// session picker built from the same scan. Both are nil when nothing
// qualified.
type sessionsScannedMsg struct {
	Picker *commandPicker
	Latest *scannedSession
}

// initialCmdsBatchMsg carries commands armed during program construction
// (the async session scan) into the running event loop — program.Send
// needs a Msg, and Cmds only run once the loop consumes their messages.
type initialCmdsBatchMsg struct {
	cmds []tea.Cmd
}

// scannedSession is the launch card's input: the newest resumable
// workspace session with the design state reconstructed from its events.
type scannedSession struct {
	Meta  sessions.Metadata
	State splicerun.DesignState
}

// sessionScanPending reports that a scan is in flight (per model flag, so a
// second /resume does not spawn a duplicate scan).
func sessionScanPending(m model) bool {
	return m.sessionScanInFlight
}

// startSessionScan arms the pending flag and returns the scan cmd. The
// caller decides what the UI shows while the scan runs (nothing — honest
// absence — for the launch card; the picker appears when the msg lands).
// The title distinguishes the launch pass from a bare /resume.
func startSessionScan(m model, title string) (model, tea.Cmd) {
	if m.sessionStore == nil {
		return m, nil
	}
	m.sessionScanInFlight = true
	m.scanStartKeySeq = m.keySeq
	store := m.sessionStore
	cwd := m.cwd
	return m, func() tea.Msg { return scanSessions(store, cwd, title) }
}

// SessionLister is the store surface the scan needs (ReadEvents +
// ListResumable), so tests can inject a blocking or counting wrapper.
type SessionLister interface {
	ListResumable() ([]sessions.Metadata, error)
	ReadEvents(sessionID string) ([]sessions.Event, error)
}

// scanSessions is var-wrapped so tests can intercept the store seam.
var scanSessions = realScanSessions

// realScanSessions runs the full session discovery off the UI loop: metadata
// listing, workspace filter, bounded newest-first event reads, and design
// state reconstruction. It reads ONLY its arguments. The title distinguishes
// the launch pass ("Continue where you left off") from a bare /resume.
func realScanSessions(store SessionLister, cwd string, title string) tea.Msg {
	metas, err := store.ListResumable()
	if err != nil || len(metas) == 0 {
		return sessionsScannedMsg{}
	}
	now := scanNow()
	items := make([]pickerItem, 0, len(metas))
	planBearing := false
	reads := 0
	var latest *scannedSession
	for i := range metas {
		meta := metas[i]
		// Workspace-scoped: hide sessions from other project directories.
		// Checked BEFORE the event read so a large global history does not
		// pay per-session file reads. Sessions with no recorded Cwd stay
		// visible rather than vanishing; worktree sessions match through
		// their origin repo (TW4).
		if !sessionWorkspaceMatch(meta, cwd) {
			continue
		}
		if meta.EventCount == 0 {
			continue
		}
		// The bounded scan: sessions beyond the newest limit keep their
		// metadata row out of the picker rather than stalling the scan.
		if reads >= sessionPickerScanLimit {
			break
		}
		reads++
		events, readErr := store.ReadEvents(meta.SessionID)
		if readErr != nil {
			// Fail open, same as the old picker: keep the row, no plan status.
			events = nil
		}
		state, stateErr := splicerun.ReconstructDesignState(events)
		// The launch card's latest follows the OLD launch card's rule: any
		// session with events and a reconstructable state qualifies — the
		// decisions/open-question ledger is exactly what makes an otherwise
		// quiet design session worth resuming. The content check gates only
		// the picker row.
		if latest == nil && stateErr == nil {
			captured := meta
			latest = &scannedSession{Meta: captured, State: state}
		}
		hasContent := readErr != nil || eventsHaveResumableContent(events)
		if !hasContent {
			continue
		}
		status := ""
		if stateErr == nil {
			status = sessionPlanStatus(state)
			if status != "" {
				planBearing = true
			}
		}
		label := displayValue(meta.Title, "untitled")
		if when := sessionWhen(meta.UpdatedAt, now); when != "" {
			label = when + "  " + label
		}
		if status != "" {
			label += "  [" + status + "]"
		}
		if isWorktreeSession(meta) {
			label = "wt: " + label
		}
		items = append(items, pickerItem{Label: label, Value: meta.SessionID, Meta: meta.SessionID})
	}
	if len(items) == 0 {
		// No picker rows (every candidate failed the content check), but
		// the launch card's latest may still be valid — do not drop it.
		return sessionsScannedMsg{Latest: latest}
	}
	picker := &commandPicker{
		kind:        pickerSession,
		title:       title,
		items:       items,
		allItems:    append([]pickerItem{}, items...),
		selected:    0,
		planBearing: planBearing,
	}
	return sessionsScannedMsg{Picker: picker, Latest: latest}
}

// applySessionsScanned arms the launch card from the async scan result.
// The picker arms ONLY on an explicit /resume (resumePickerWanted, set by
// openSessionPicker): the launch never auto-opens a modal over a user who
// may already be typing — that swallowed their input and read as a dead
// screen (owner report 2026-09-06).
func applySessionsScanned(m model, msg sessionsScannedMsg) model {
	m.sessionScanInFlight = false
	m.scannedLatest = msg.Latest
	if msg.Picker != nil && m.resumePickerWanted && m.picker == nil &&
		m.composerValue() == "" && !m.pending {
		m.picker = msg.Picker
	}
	m.resumePickerWanted = false
	return m
}
