package web

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// actionOf returns the current action token. Tests read it under the lock.
func actionOf(s *Server) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.action
}

func TestSurfaceRouting(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	if err := s.Register("demo", Surface{
		Page:   func(w http.ResponseWriter, _ *http.Request, action string) { io.WriteString(w, "page:"+action) },
		Mutate: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	cookie := sessionFor(t, s, boot)

	resp := send(t, http.MethodGet, s.at("/s/demo/"), nil, cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("surface page: status %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if got, want := string(body), "page:"+actionOf(s); got != want {
		t.Fatalf("page body = %q, want %q", got, want)
	}

	if resp := send(t, http.MethodGet, s.at("/s/unknown/"), nil, cookie); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown surface: status %d, want 404", resp.StatusCode)
	}
	if resp := send(t, http.MethodGet, s.at("/s/demo/extra"), nil, cookie); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown subpath: status %d, want 404", resp.StatusCode)
	}
	if resp := send(t, http.MethodGet, s.at("/s/demo/"), nil, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("surface page without cookie: status %d, want 403", resp.StatusCode)
	}
}

func TestRegisterRejectsBadAndDuplicateNames(t *testing.T) {
	s := New(time.Hour)
	for _, name := range []string{"", "Demo", "a/b", "a b", "../x"} {
		if err := s.Register(name, Surface{}); err == nil {
			t.Errorf("Register(%q) succeeded, want error", name)
		}
	}
	if err := s.Register("demo", Surface{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := s.Register("demo", Surface{}); err == nil {
		t.Fatal("duplicate Register succeeded, want error")
	}
}

func TestSurfaceMutationNeedsActionToken(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	mutated := false
	if err := s.Register("demo", Surface{
		Mutate: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			mutated = true
			w.WriteHeader(http.StatusNoContent)
		}),
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := s.Register("reader", Surface{}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	cookie := sessionFor(t, s, boot)
	action := actionOf(s)
	good := map[string]string{"Origin": s.origin, "X-Splice-Action": action}

	tests := []struct {
		name string
		url  string
		hdr  map[string]string
		want int
	}{
		{"no action header", "/s/demo/go", map[string]string{"Origin": s.origin}, http.StatusForbidden},
		{"wrong action header", "/s/demo/go", map[string]string{"Origin": s.origin, "X-Splice-Action": "nope"}, http.StatusForbidden},
		{"action token from another run", "/s/demo/go", map[string]string{"Origin": s.origin, "X-Splice-Action": action + "0"}, http.StatusForbidden},
		{"action without matching origin", "/s/demo/go", map[string]string{"Origin": "http://evil.example", "X-Splice-Action": action}, http.StatusForbidden},
		{"surface without actions", "/s/reader/go", good, http.StatusMethodNotAllowed},
		{"valid action", "/s/demo/go", good, http.StatusNoContent},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mutated = false
			resp := send(t, http.MethodPost, s.at(tc.url), tc.hdr, cookie)
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
			if tc.want == http.StatusNoContent && !mutated {
				t.Fatal("valid action did not reach the Mutate handler")
			}
			if tc.want != http.StatusNoContent && mutated {
				t.Fatal("Mutate ran without a valid action token")
			}
		})
	}
}

func TestIndexRendersActionTokenMeta(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	cookie := sessionFor(t, s, boot)
	resp := send(t, http.MethodGet, s.at("/"), nil, cookie)
	body, _ := io.ReadAll(resp.Body)
	want := `<meta name="splice-action" content="` + actionOf(s) + `">`
	if !strings.Contains(string(body), want) {
		t.Fatalf("index page lacks the action meta tag %q", want)
	}
	if strings.Contains(string(body), actionPlaceholder) {
		t.Fatal("index page still shows the placeholder")
	}
}

func TestSetSessionBroadcastsThenRotatesAndClosesStreams(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	oldCookie := sessionFor(t, s, boot)
	oldAction := actionOf(s)
	if err := s.Register("demo", Surface{
		Mutate: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.at("/events"), nil)
	req.AddCookie(oldCookie)
	stream, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("events: %v", err)
	}
	defer stream.Body.Close()
	reader := bufio.NewReader(stream.Body)
	received := make(chan string, 1)
	go func() {
		var all strings.Builder
		for {
			line, err := reader.ReadString('\n')
			all.WriteString(line)
			if err != nil { // the server closes the stream after the change
				received <- all.String()
				return
			}
		}
	}()

	newURL, err := s.SetSession("tui-session-2")
	if err != nil {
		t.Fatalf("SetSession: %v", err)
	}
	if !strings.HasPrefix(newURL, s.at("/?token=")) {
		t.Fatalf("SetSession URL = %q", newURL)
	}
	if s.SessionID() != "tui-session-2" {
		t.Fatalf("SessionID = %q", s.SessionID())
	}

	var got string
	select {
	case got = <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("stream was not closed after SetSession")
	}
	if !strings.Contains(got, "event: session-changed\n") || !strings.Contains(got, `data: {"session":"tui-session-2"}`) {
		t.Fatalf("stream did not carry session-changed before closing: %q", got)
	}

	// Old cookie and old action token fail after rotation.
	if resp := send(t, http.MethodGet, s.at("/"), nil, oldCookie); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("old cookie after rotation: status %d, want 403", resp.StatusCode)
	}
	if resp := send(t, http.MethodPost, s.at("/s/demo/go"), map[string]string{"Origin": s.origin, "X-Splice-Action": oldAction}, oldCookie); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("old cookie and action after rotation: status %d, want 403", resp.StatusCode)
	}
	// The original bootstrap token was already used, and the new URL works once.
	if resp := send(t, http.MethodGet, newURL, nil, nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("new bootstrap URL: status %d, want 303", resp.StatusCode)
	}
	if resp := send(t, http.MethodGet, newURL, nil, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("new bootstrap URL reused: status %d, want 403", resp.StatusCode)
	}
}

func TestSetSessionBeforeStartOnlyRecordsID(t *testing.T) {
	s := New(time.Hour)
	url, err := s.SetSession("early")
	if err != nil || url != "" {
		t.Fatalf("SetSession before Start = (%q, %v), want empty URL", url, err)
	}
	if s.SessionID() != "early" {
		t.Fatalf("SessionID = %q", s.SessionID())
	}
}

func TestBootstrapURLReissuesOneTimeURL(t *testing.T) {
	s, first := startServer(t, time.Hour)
	second, err := s.BootstrapURL()
	if err != nil {
		t.Fatalf("BootstrapURL: %v", err)
	}
	if second == first {
		t.Fatal("BootstrapURL returned the old token")
	}
	if resp := send(t, http.MethodGet, first, nil, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("superseded URL: status %d, want 403", resp.StatusCode)
	}
	if resp := send(t, http.MethodGet, second, nil, nil); resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("new URL: status %d, want 303", resp.StatusCode)
	}
}

func TestStopInvalidatesActionTokens(t *testing.T) {
	s, _ := startServer(t, time.Hour)
	action := actionOf(s)
	s.Stop()
	req, _ := http.NewRequest(http.MethodPost, s.at("/s/demo/go"), nil)
	req.Header.Set("X-Splice-Action", action)
	if s.actionOK(req, actionOf(s)) {
		t.Fatal("action token still accepted after Stop")
	}
	if _, err := s.BootstrapURL(); err == nil {
		t.Fatal("BootstrapURL succeeded on a stopped server")
	}
}
