package web

import (
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
	"time"
)

// statusVia sends a GET through the client, and returns the status code.
func statusVia(t *testing.T, client *http.Client, rawURL string) int {
	t.Helper()
	resp, err := client.Get(rawURL)
	if err != nil {
		t.Fatalf("GET %s: %v", rawURL, err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

// TestPrefixScopesCookieAndRoutes models a browser's cookie jar. The jar follows
// the cookie path rules, as a browser does. A server on the same host and another
// port shares that jar, so the path is what keeps sessions apart.
func TestPrefixScopesCookieAndRoutes(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	client := &http.Client{Jar: jar, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// Redeem the bootstrap URL. The browser stores the cookie for the prefix path.
	resp, err := client.Get(boot)
	if err != nil {
		t.Fatalf("redeem: %v", err)
	}
	resp.Body.Close()
	prefixURL, _ := url.Parse(s.origin + s.prefixPath())
	stored := jar.Cookies(prefixURL)
	if len(stored) != 1 {
		t.Fatalf("jar holds %d cookies for the prefix, want 1", len(stored))
	}
	oldValue := stored[0].Value
	if got := statusVia(t, client, s.at("/")); got != http.StatusOK {
		t.Fatalf("index under the prefix: status %d, want 200", got)
	}

	// Outside the prefix the jar sends nothing, so the root gets no cookie.
	rootURL, _ := url.Parse(s.origin + "/")
	if n := len(jar.Cookies(rootURL)); n != 0 {
		t.Fatalf("jar sends %d cookies to /, which is outside the prefix", n)
	}
	if got := statusVia(t, client, s.origin+"/events"); got != http.StatusNotFound {
		t.Fatalf("/events outside the prefix: status %d, want 404", got)
	}

	// A session change rotates the prefix. The old cookie no longer matches it.
	oldPrefix := s.prefixPath()
	newBoot, err := s.SetSession("tui-session-2")
	if err != nil {
		t.Fatalf("SetSession: %v", err)
	}
	newURL, _ := url.Parse(newBoot)
	if n := len(jar.Cookies(newURL)); n != 0 {
		t.Fatalf("old cookie matches the new prefix (%d cookies)", n)
	}
	if got := statusVia(t, client, s.at("/")); got != http.StatusForbidden {
		t.Fatalf("new prefix without a cookie: status %d, want 403", got)
	}
	if got := statusVia(t, client, s.origin+oldPrefix); got != http.StatusNotFound {
		t.Fatalf("retired prefix: status %d, want 404", got)
	}

	// Replay the old cookie value against the new prefix's event stream.
	req, _ := http.NewRequest(http.MethodGet, s.at("/events"), nil)
	req.AddCookie(&http.Cookie{Name: stored[0].Name, Value: oldValue})
	replay, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	replay.Body.Close()
	if replay.StatusCode != http.StatusForbidden {
		t.Fatalf("replayed cookie on the new prefix's /events: status %d, want 403", replay.StatusCode)
	}
}

// TestPrefixedRoutesNeverRedirectOutOfThePrefix: the inner router must not
// redirect to a path without the prefix. /s (no slash) and an escaped slash
// get 404. A path with dot segments may redirect, but only inside the prefix.
func TestPrefixedRoutesNeverRedirectOutOfThePrefix(t *testing.T) {
	s, boot := startServer(t, time.Hour)
	cookie := sessionFor(t, s, boot)
	p := s.prefixPath()
	for _, rel := range []string{"s", "%2Fevents", "s/%2F..%2Fevents", "s/../events", "./events", "s//pipeline/"} {
		resp := send(t, http.MethodGet, s.origin+p+rel, nil, cookie)
		if loc := resp.Header.Get("Location"); loc != "" && !strings.HasPrefix(loc, p) {
			t.Fatalf("GET %s redirected to %q, outside the prefix", rel, loc)
		}
		if (rel == "s" || strings.Contains(rel, "%2F")) && resp.StatusCode != http.StatusNotFound {
			t.Fatalf("GET %s: status %d, want 404", rel, resp.StatusCode)
		}
	}
}
