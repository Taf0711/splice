package web

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
)

// cookieNamePrefix starts the session cookie name. Browsers do not isolate
// cookies by port, so each server appends its own port to the name.
const cookieNamePrefix = "splice_session_"

// actionHeader carries the per-session action token on surface mutations.
const actionHeader = "X-Splice-Action"

// newSecret returns 32 random bytes as hex. It is used for the bootstrap token,
// the session secret, and the action token.
func newSecret() (string, error) {
	return newHex(32)
}

// newPrefix returns the secret path segment that every route sits under. It is
// 128 random bits as hex.
func newPrefix() (string, error) {
	return newHex(16)
}

func newHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("web: generate secret: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// grant is what one redeemed bootstrap token gives: the session secret and the
// prefix of the session that issued the token, and the page it opens.
type grant struct {
	session string
	prefix  string
	target  string // the path to redirect to, under the prefix
}

// redeem consumes the one-time bootstrap token. It reports false when the token
// is wrong or was already used. The grant is read with the token, under one
// lock, so a session change that lands after the redeem cannot give the old
// link a cookie for the new session.
func (s *Server) redeem(token string) (grant, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.bootstrap == "" || subtle.ConstantTimeCompare([]byte(token), []byte(s.bootstrap)) != 1 {
		return grant{}, false
	}
	s.bootstrap = ""
	// The target is the landing surface when one is set, else the index.
	g := grant{session: s.session, prefix: s.prefix, target: "/" + s.prefix + "/"}
	switch {
	case s.target != "":
		g.target += s.target
	case s.landing != "":
		g.target += "s/" + s.landing + "/"
	}
	s.target = ""
	return g, true
}

// Scope rule. Two servers on 127.0.0.1 share one cookie jar, because browsers
// do not isolate cookies by port. Three parts keep their state apart:
//   - Every route sits under /<prefix>/, a random 128-bit segment that rotates
//     with the session. A request outside that prefix gets 404 (see prefixed).
//   - The session cookie has Path=/<prefix>/, so the browser sends it only to
//     this server's prefix. The name also carries the port (cookieName).
//   - The cookie value is checked in constant time against the current secret.
//
// /healthz is the one route outside the prefix. It returns no secret.

// setSessionCookie writes the session cookie of a grant. Its attributes block
// cross-site sends and script access.
func (s *Server) setSessionCookie(w http.ResponseWriter, g grant) {
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName,
		Value:    g.session,
		Path:     "/" + g.prefix + "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

// hasSession reports whether the request carries the session cookie.
func (s *Server) hasSession(r *http.Request) bool {
	c, err := r.Cookie(s.cookieName)
	if err != nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.session)) == 1
}

// actionOK reports whether the request carries the current action token in the
// X-Splice-Action header. An empty token never matches, so a stopped server
// rejects every action.
func (s *Server) actionOK(r *http.Request, action string) bool {
	got := r.Header.Get(actionHeader)
	return action != "" && subtle.ConstantTimeCompare([]byte(got), []byte(action)) == 1
}

// originAllowed checks the Origin header. When Origin is absent, it checks the
// Referer header. It reports false when neither header matches the bound origin.
// The bound origin is http://127.0.0.1:<port> only. The bootstrap URL uses that
// host, so a localhost tab never holds a session cookie and needs no match.
func (s *Server) originAllowed(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		return origin == s.origin
	}
	ref := r.Header.Get("Referer")
	if ref == "" {
		return false
	}
	u, err := url.Parse(ref)
	return err == nil && u.Scheme+"://"+u.Host == s.origin
}

// allowHost rejects any Host header that is not the bound loopback address. This
// blocks DNS rebinding. The fields it reads are set once in Start, before the
// listener serves.
func (s *Server) allowHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != s.hosts[0] && r.Host != s.hosts[1] {
			forbid(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// guardMutations requires the session cookie and a matching Origin or Referer
// on every method other than GET and HEAD. A method such as OPTIONS, PROPFIND,
// or a custom verb is treated as mutating.
func (s *Server) guardMutations(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !readMethod(r.Method) && (!s.hasSession(r) || !s.originAllowed(r)) {
			forbid(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// readMethod reports whether a method only reads: GET or HEAD.
func readMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

func forbid(w http.ResponseWriter) {
	http.Error(w, "forbidden", http.StatusForbidden)
}
