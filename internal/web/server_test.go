package web

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"
)

// startServer starts a server with the given idle timeout and returns it with
// its bootstrap URL. Cleanup stops it.
func startServer(t *testing.T, idle time.Duration) (*Server, string) {
	t.Helper()
	s := New(idle)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		s.Stop()
	})
	boot, err := s.Start(ctx)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return s, boot
}

// at returns the URL for path under the server's current prefix. /healthz is the
// one route outside the prefix, so it stays on the origin.
func (s *Server) at(path string) string {
	if path == "/healthz" {
		return s.origin + path
	}
	s.mu.Lock()
	prefix := s.prefix
	s.mu.Unlock()
	return s.origin + "/" + prefix + path
}

// prefixPath returns the server's path prefix, such as "/<hex>/".
func (s *Server) prefixPath() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return "/" + s.prefix + "/"
}

// noRedirect returns a client that reports redirects instead of following them.
func noRedirect() *http.Client {
	return &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// send issues a request. An empty host keeps the one the URL implies.
func send(t *testing.T, method, rawURL string, hdr map[string]string, cookie *http.Cookie) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	for k, v := range hdr {
		if k == "Host" {
			req.Host = v
			continue
		}
		req.Header.Set(k, v)
	}
	if cookie != nil {
		req.AddCookie(cookie)
	}
	resp, err := noRedirect().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, rawURL, err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// sessionFor redeems the bootstrap URL and returns the session cookie.
func sessionFor(t *testing.T, s *Server, boot string) *http.Cookie {
	t.Helper()
	resp := send(t, http.MethodGet, boot, nil, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("bootstrap status = %d, want 303", resp.StatusCode)
	}
	for _, c := range resp.Cookies() {
		if c.Name == s.cookieName {
			return c
		}
	}
	t.Fatal("bootstrap response set no session cookie")
	return nil
}

func TestStartBindsLoopbackOnly(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	if !s.addr.IP.IsLoopback() {
		t.Fatalf("bound to %v, want loopback", s.addr.IP)
	}
	u, err := url.Parse(boot)
	if err != nil {
		t.Fatalf("parse bootstrap URL: %v", err)
	}
	host, _, err := net.SplitHostPort(u.Host)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		t.Fatalf("bootstrap host = %q, want a loopback IP", u.Host)
	}
}

func TestStartTwiceFails(t *testing.T) {
	s, _ := startServer(t, time.Hour)
	if _, err := s.Start(context.Background()); err == nil {
		t.Fatal("second Start succeeded, want error")
	}
}

func TestBadHostRejectedOnEveryRoute(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	cookie := sessionFor(t, s, boot)
	base := s.origin
	port := strconv.Itoa(s.addr.Port)

	routes := []string{"/", "/healthz", "/events"}
	badHosts := []string{
		"evil.example", "evil.example:" + port, "127.0.0.1", "[::1]:" + port,
		"LOCALHOST:" + port, "127.0.0.1.:" + port, // exact match only: case and trailing dot change the host
	}
	for _, route := range routes {
		for _, host := range badHosts {
			resp := send(t, http.MethodGet, base+route, map[string]string{"Host": host}, cookie)
			if resp.StatusCode != http.StatusForbidden {
				t.Errorf("GET %s with Host %q: status %d, want 403", route, host, resp.StatusCode)
			}
		}
	}
}

func TestLocalhostHostAccepted(t *testing.T) {
	s, _ := startServer(t, time.Hour)
	host := "localhost:" + strconv.Itoa(s.addr.Port)
	resp := send(t, http.MethodGet, s.at("/healthz"), map[string]string{"Host": host}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health with Host %q: status %d, want 200", host, resp.StatusCode)
	}
}

func TestBootstrapTokenWorksOnce(t *testing.T) {
	s, boot := startServer(t, time.Hour)

	if resp := send(t, http.MethodGet, s.at("/?token=wrong"), nil, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("wrong token: status %d, want 403", resp.StatusCode)
	}

	resp := send(t, http.MethodGet, boot, nil, nil)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("first use: status %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != s.prefixPath() {
		t.Fatalf("redirect Location = %q, want %q (token dropped)", loc, s.prefixPath())
	}

	if resp := send(t, http.MethodGet, boot, nil, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("second use: status %d, want 403", resp.StatusCode)
	}
}

func TestSessionCookieAttributes(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	c := sessionFor(t, s, boot)
	if !c.HttpOnly {
		t.Error("session cookie is not HttpOnly")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", c.SameSite)
	}
	if c.Path != s.prefixPath() {
		t.Errorf("Path = %q, want the prefix path %q", c.Path, s.prefixPath())
	}
}

func TestTwoServersKeepSeparateCookies(t *testing.T) {
	a, bootA := startServer(t, time.Hour)
	b, bootB := startServer(t, time.Hour)
	cookieA := sessionFor(t, a, bootA)
	cookieB := sessionFor(t, b, bootB)
	if cookieA.Name == cookieB.Name {
		t.Fatalf("both servers use cookie name %q", cookieA.Name)
	}

	// A client holding both cookies is accepted by each server with its own cookie.
	both := func(s *Server) *http.Response {
		req, _ := http.NewRequest(http.MethodGet, s.at("/"), nil)
		req.AddCookie(cookieA)
		req.AddCookie(cookieB)
		resp, err := noRedirect().Do(req)
		if err != nil {
			t.Fatalf("GET %s: %v", s.origin, err)
		}
		t.Cleanup(func() { resp.Body.Close() })
		return resp
	}
	if resp := both(a); resp.StatusCode != http.StatusOK {
		t.Fatalf("server A with both cookies: status %d, want 200", resp.StatusCode)
	}
	if resp := both(b); resp.StatusCode != http.StatusOK {
		t.Fatalf("server B with both cookies: status %d, want 200", resp.StatusCode)
	}

	// Server A's secret under server B's cookie name is rejected by server B.
	forged := &http.Cookie{Name: b.cookieName, Value: a.session}
	if resp := send(t, http.MethodGet, b.at("/"), nil, forged); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("server B with server A's secret: status %d, want 403", resp.StatusCode)
	}
	// Server A's cookie alone is not accepted by server B.
	if resp := send(t, http.MethodGet, b.at("/"), nil, cookieA); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("server B with only server A's cookie: status %d, want 403", resp.StatusCode)
	}
}

func TestPageNeedsSession(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	if resp := send(t, http.MethodGet, s.at("/"), nil, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("page without cookie: status %d, want 403", resp.StatusCode)
	}
	cookie := sessionFor(t, s, boot)
	resp := send(t, http.MethodGet, s.at("/"), nil, cookie)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("page with cookie: status %d, want 200", resp.StatusCode)
	}
}

func TestMutationRequiresCookieAndOrigin(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	cookie := sessionFor(t, s, boot)
	good := map[string]string{"Origin": s.origin}

	tests := []struct {
		name   string
		hdr    map[string]string
		cookie *http.Cookie
		want   int
	}{
		{"no cookie, no origin", nil, nil, http.StatusForbidden},
		{"no cookie, matching origin", good, nil, http.StatusForbidden},
		{"cookie, foreign origin", map[string]string{"Origin": "http://evil.example"}, cookie, http.StatusForbidden},
		{"cookie, null origin", map[string]string{"Origin": "null"}, cookie, http.StatusForbidden},
		{"cookie, foreign referer only", map[string]string{"Referer": "http://evil.example/x"}, cookie, http.StatusForbidden},
		{"cookie, no origin or referer", nil, cookie, http.StatusForbidden},
		// The mux only routes GET, so a passing guard shows as 405.
		{"cookie, matching origin", good, cookie, http.StatusMethodNotAllowed},
		{"cookie, matching referer only", map[string]string{"Referer": s.at("/page")}, cookie, http.StatusMethodNotAllowed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := send(t, http.MethodPost, s.at("/"), tc.hdr, tc.cookie)
			if resp.StatusCode != tc.want {
				t.Fatalf("POST status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestSSERequiresSession(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	if resp := send(t, http.MethodGet, s.at("/events"), nil, nil); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("SSE without cookie: status %d, want 403", resp.StatusCode)
	}
	cookie := sessionFor(t, s, boot)
	if resp := send(t, http.MethodGet, s.at("/events"), map[string]string{"Origin": "http://evil.example"}, cookie); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("SSE with foreign origin: status %d, want 403", resp.StatusCode)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.at("/events"), nil)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE with cookie: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE with cookie: status %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("SSE Content-Type = %q", ct)
	}
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil && err != io.EOF {
		t.Fatalf("read SSE: %v", err)
	}
	if line != ": connected\n" {
		t.Fatalf("first SSE line = %q", line)
	}
}

func TestSSEDeliversPublishedEvent(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	cookie := sessionFor(t, s, boot)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.at("/events"), nil)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE: %v", err)
	}
	defer resp.Body.Close()
	r := bufio.NewReader(resp.Body)
	if _, err := r.ReadString('\n'); err != nil { // ": connected"
		t.Fatalf("read preamble: %v", err)
	}
	// The hub flushes on a 50 ms tick, so publish until the client sees the event.
	deadline := time.After(5 * time.Second)
	go func() {
		for {
			select {
			case <-deadline:
				return
			case <-time.After(20 * time.Millisecond):
				s.Publish(Event{Key: "k", Name: "tick", Data: "hello"})
			}
		}
	}()
	var got strings.Builder
	for !strings.Contains(got.String(), "data: hello\n") {
		line, err := r.ReadString('\n')
		if err != nil {
			t.Fatalf("read event: %v (got %q)", err, got.String())
		}
		got.WriteString(line)
	}
	if !strings.Contains(got.String(), "event: tick\n") {
		t.Fatalf("missing event name in %q", got.String())
	}
}

func TestHealthNeedsNoSessionAndLeaksNoSecret(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	resp := send(t, http.MethodGet, s.at("/healthz"), nil, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("health: status %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), `"ok"`) {
		t.Fatalf("health body = %q", body)
	}
	token := boot[strings.Index(boot, "token=")+len("token="):]
	if strings.Contains(string(body), token) || strings.Contains(string(body), s.session) {
		t.Fatal("health body contains a secret")
	}
}

func TestResponsesCarrySecurityHeaders(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	paths := []struct {
		path   string
		cookie *http.Cookie
	}{
		{"/healthz", nil},
		{"/", nil},
		{"/", sessionFor(t, s, boot)},
	}
	for _, p := range paths {
		resp := send(t, http.MethodGet, s.at(p.path), nil, p.cookie)
		want := map[string]string{
			"Content-Security-Policy": "default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
			"X-Content-Type-Options":  "nosniff",
			"Referrer-Policy":         "no-referrer",
			"Cache-Control":           "no-store",
		}
		for k, v := range want {
			if got := resp.Header.Get(k); got != v {
				t.Errorf("GET %s: %s = %q, want %q", p.path, k, got, v)
			}
		}
	}
}

func TestIdleTimeoutStopsServer(t *testing.T) {
	s, _ := startServer(t, 50*time.Millisecond)
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		t.Fatal("server still running after idle timeout")
	}
}

func TestOpenEventStreamKeepsServerAlive(t *testing.T) {
	s, boot := startServer(t, 50*time.Millisecond)
	cookie := sessionFor(t, s, boot)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, s.at("/events"), nil)
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("SSE: %v", err)
	}
	defer resp.Body.Close()

	time.Sleep(300 * time.Millisecond) // several idle periods
	select {
	case <-s.done:
		t.Fatal("server stopped while an event stream was open")
	default:
	}
}

func TestContextCancelStopsServer(t *testing.T) {
	s := New(time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	if _, err := s.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	cancel()
	select {
	case <-s.done:
	case <-time.After(5 * time.Second):
		t.Fatal("server still running after context cancel")
	}
	s.Stop() // a second Stop is a no-op
	if _, err := s.Start(context.Background()); err == nil {
		t.Fatal("Start after Stop succeeded, want error")
	}
}
