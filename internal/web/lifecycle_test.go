package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Taf0711/splice/internal/agent"
)

// pendingCount returns how many events wait for the next flush.
func pendingCount(s *Server) int {
	s.hub.mu.Lock()
	defer s.hub.mu.Unlock()
	return len(s.hub.pending)
}

func TestPublishIsNoOpAfterStop(t *testing.T) {
	s, _ := startServer(t, time.Hour)
	s.Stop()
	for i := 0; i < 1000; i++ {
		s.Publish(Event{Key: fmt.Sprint(i), Data: "late"})
	}
	if n := pendingCount(s); n != 0 {
		t.Fatalf("pending = %d after Stop, want 0", n)
	}
}

func TestSetSessionAfterStopReturnsErrorAndNoURL(t *testing.T) {
	s, _ := startServer(t, time.Hour)
	s.afterFlush = func() { s.Stop() } // Stop lands between the flush and the rotation
	url, err := s.SetSession("tui-session-2")
	if err == nil {
		t.Fatal("SetSession after Stop succeeded, want an error")
	}
	if url != "" {
		t.Fatalf("SetSession after Stop returned URL %q", url)
	}
}

func TestSessionChangeDropsOldSessionEvents(t *testing.T) {
	s, _ := startServer(t, time.Hour)
	stale := s.Sink() // a run started in the old session
	s.afterFlush = func() {
		// An event from the old session lands after the flush, while the change is in progress.
		stale(agent.StageEvent{Name: "code_writer", Status: "running", Detail: "old-session-detail"})
	}
	if _, err := s.SetSession("tui-session-2"); err != nil {
		t.Fatalf("SetSession: %v", err)
	}
	if n := pendingCount(s); n != 0 {
		t.Fatalf("pending = %d after the session change, want 0 (old events must not reach the new session)", n)
	}
	// The old run keeps publishing after the change. Its events must stay out.
	stale(agent.StageEvent{Name: "code_writer", Status: "completed", Detail: "late-old-session"})
	if n := pendingCount(s); n != 0 {
		t.Fatalf("stale sink published %d events into the new session", n)
	}
}

func TestStreamCloseCountsAsActivity(t *testing.T) {
	s, boot := startServer(t, 100*time.Millisecond)
	cookie := sessionFor(t, s, boot)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.at("/events"), nil)
	req.AddCookie(cookie)
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	time.Sleep(300 * time.Millisecond) // longer than the idle timeout, but the stream is open
	stream.Body.Close()
	time.Sleep(50 * time.Millisecond) // a few ticks after the stream closed
	select {
	case <-s.done:
		t.Fatal("server stopped right after its event stream closed")
	default:
	}
}

func TestSSESubscribesBeforeSessionCheck(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	cookie := sessionFor(t, s, boot)
	// The session changes after the handler subscribes, and before it checks the cookie.
	s.afterSubscribe = func() {
		if _, err := s.SetSession("tui-session-2"); err != nil {
			t.Errorf("SetSession: %v", err)
		}
	}
	resp := send(t, http.MethodGet, s.at("/events"), nil, cookie)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("stream opened on a retired session: status %d, want 403", resp.StatusCode)
	}
}

func TestUnauthenticatedAndHealthRequestsDoNotKeepServerAlive(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		hdr  map[string]string
	}{
		{"health checks", "/healthz", nil},
		{"requests without a session", "/", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := startServer(t, 100*time.Millisecond)
			deadline := time.Now().Add(400 * time.Millisecond)
			for time.Now().Before(deadline) {
				select {
				case <-s.done:
					return // stopped, as an idle server must be
				default:
				}
				req, _ := http.NewRequest(http.MethodGet, s.at(tc.path), nil)
				for k, v := range tc.hdr {
					req.Header.Set(k, v)
				}
				if resp, err := noRedirect().Do(req); err == nil {
					resp.Body.Close()
				}
				time.Sleep(10 * time.Millisecond)
			}
			// The idle timeout is 100 ms and the traffic lasted 400 ms, so a server that
			// ignores these requests has stopped by now.
			select {
			case <-s.done:
			default:
				t.Fatal("server still running after 400 ms of unauthenticated traffic")
			}
		})
	}
}

func TestHeadDoesNotRedeemBootstrapToken(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	if resp := send(t, http.MethodHead, boot, nil, nil); resp.StatusCode == http.StatusSeeOther {
		t.Fatal("HEAD redeemed the bootstrap token")
	}
	if resp := send(t, http.MethodGet, boot, nil, nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("GET after a HEAD preview: status %d, want 303 (the link must survive a preview)", resp.StatusCode)
	}
	_ = s
}

func TestEveryMutatingMethodIsGuarded(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	cookie := sessionFor(t, s, boot)
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			if resp := send(t, method, s.at("/"), nil, nil); resp.StatusCode != http.StatusForbidden {
				t.Fatalf("no cookie: status %d, want 403", resp.StatusCode)
			}
			if resp := send(t, method, s.at("/"), map[string]string{"Origin": "http://evil.example"}, cookie); resp.StatusCode != http.StatusForbidden {
				t.Fatalf("foreign origin: status %d, want 403", resp.StatusCode)
			}
			// A passing guard reaches the router, which has no such route: 405.
			if resp := send(t, method, s.at("/"), map[string]string{"Origin": s.origin}, cookie); resp.StatusCode != http.StatusMethodNotAllowed {
				t.Fatalf("cookie and matching origin: status %d, want 405 (the guard passed)", resp.StatusCode)
			}
		})
	}
}

func TestSSEAcceptedWithCookieAndMatchingOrigin(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	cookie := sessionFor(t, s, boot)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.at("/events"), nil)
	req.AddCookie(cookie)
	req.Header.Set("Origin", s.origin)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE with cookie and matching origin: status %d, want 200", resp.StatusCode)
	}
}

// TestRedeemRacingSessionChangeKeepsTheOldSession: a session change that lands
// after the redeem and before the cookie must not give the old link a cookie
// for the new session. The cookie and the redirect use what the redeem read.
func TestRedeemRacingSessionChangeKeepsTheOldSession(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	oldPrefix := s.prefixPath()
	s.afterRedeem = func() {
		if _, err := s.SetSession("session-2"); err != nil {
			t.Errorf("SetSession: %v", err)
		}
	}
	resp := send(t, http.MethodGet, boot, nil, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("bootstrap status = %d, want 303", resp.StatusCode)
	}
	s.mu.Lock()
	newSession := s.session
	s.mu.Unlock()
	for _, c := range resp.Cookies() {
		if c.Name != s.cookieName {
			continue
		}
		if c.Value == newSession || c.Path != oldPrefix {
			t.Fatalf("old link got cookie path %q for the new session (new=%v)", c.Path, c.Value == newSession)
		}
	}
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, oldPrefix) {
		t.Fatalf("redirect = %q, want the old prefix %q", loc, oldPrefix)
	}
}

// TestAuthenticatedIndexCountsAsActivity: a page load with the session cookie
// keeps the server alive, as a surface page does.
func TestAuthenticatedIndexCountsAsActivity(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	cookie := sessionFor(t, s, boot)
	old := time.Now().Add(-time.Minute)
	s.mu.Lock()
	s.lastActivity = old
	s.mu.Unlock()
	if resp := send(t, http.MethodGet, s.at("/"), nil, cookie); resp.StatusCode != http.StatusOK {
		t.Fatalf("index with a session: status %d, want 200", resp.StatusCode)
	}
	s.mu.Lock()
	last := s.lastActivity
	s.mu.Unlock()
	if !last.After(old) {
		t.Fatal("an authenticated index GET did not count as activity")
	}
}

// TestOtherMethodsAreGuardedAndRefused: a method other than GET, HEAD, POST,
// PUT, PATCH, and DELETE needs the session cookie and the origin, as a
// mutation does. A surface then refuses it with 405. It never reaches the
// page or the sub handler.
func TestOtherMethodsAreGuardedAndRefused(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	served := 0
	if err := s.Register("probe", Surface{
		Page: func(w http.ResponseWriter, _ *http.Request, _ string) { served++ },
		Sub:  func(w http.ResponseWriter, _ *http.Request, _ string) { served++ },
	}); err != nil {
		t.Fatal(err)
	}
	cookie := sessionFor(t, s, boot)
	for _, method := range []string{"PROPFIND", http.MethodOptions, "BREW"} {
		t.Run(method, func(t *testing.T) {
			for _, rel := range []string{"/s/probe/", "/s/probe/data"} {
				if resp := send(t, method, s.at(rel), nil, nil); resp.StatusCode != http.StatusForbidden {
					t.Fatalf("%s %s with no cookie: status %d, want 403", method, rel, resp.StatusCode)
				}
				if resp := send(t, method, s.at(rel), map[string]string{"Origin": "http://evil.example"}, cookie); resp.StatusCode != http.StatusForbidden {
					t.Fatalf("%s %s from a foreign origin: status %d, want 403", method, rel, resp.StatusCode)
				}
				if resp := send(t, method, s.at(rel), map[string]string{"Origin": s.origin}, cookie); resp.StatusCode != http.StatusMethodNotAllowed {
					t.Fatalf("%s %s with a session: status %d, want 405", method, rel, resp.StatusCode)
				}
			}
		})
	}
	if served != 0 {
		t.Fatalf("a page or sub handler served %d non-read requests", served)
	}
}
