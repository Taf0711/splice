// Package web serves the local browser surfaces. The server binds to the
// loopback interface only. It starts on demand and stops when its context ends,
// when Stop is called, or after an idle period.
package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultIdleTimeout is how long the server waits without requests and without
// event streams before it stops.
const DefaultIdleTimeout = 30 * time.Minute

const (
	shutdownTimeout = 2 * time.Second
	maxIdleTick     = 10 * time.Second
	readHeaderLimit = 10 * time.Second
)

// Server is the local HTTP server for the browser surfaces. A Server runs once.
// After Stop, create a new Server.
type Server struct {
	idleTimeout time.Duration
	hub         *hub
	done        chan struct{}
	notices     atomic.Uint64
	changes     atomic.Uint64

	// Set once in Start, before the listener serves. Handlers read them without the lock.
	origin     string    // http://127.0.0.1:<port>
	hosts      [2]string // accepted Host header values
	cookieName string    // session cookie name for this server's port

	mu           sync.Mutex
	started      bool
	closed       bool
	addr         *net.TCPAddr
	httpServer   *http.Server
	lastActivity time.Time
	bootstrap    string // one-time URL token; empty after use
	session      string // secret that the session cookie must carry
	prefix       string // secret path segment that every route sits under
	action       string // per-session token that mutating surface requests must carry
	sessionID    string // the TUI session this server serves
	landing      string // the surface a redeemed bootstrap URL opens; empty opens the index
	// target is the page that the current bootstrap token opens, set by
	// BootstrapURLFor. It wins over landing, and it goes with the token.
	target   string
	surfaces map[string]Surface
	// afterFlush is a test seam. SetSession calls it between the flush and the
	// rotation, so a test can interleave a stop or a publish there.
	afterFlush func()
	// afterSubscribe is a test seam. handleEvents calls it after it subscribes.
	afterSubscribe func()
	// afterRedeem is a test seam. handleIndex calls it between the redeem and
	// the cookie, so a test can interleave a session change there.
	afterRedeem func()
}

// New returns a server that stops after idleTimeout. A zero or negative value
// uses DefaultIdleTimeout.
func New(idleTimeout time.Duration) *Server {
	if idleTimeout <= 0 {
		idleTimeout = DefaultIdleTimeout
	}
	return &Server{
		idleTimeout: idleTimeout,
		hub:         newHub(flushInterval),
		done:        make(chan struct{}),
		surfaces:    map[string]Surface{},
	}
}

// Start binds to 127.0.0.1 on a free port and returns the one-time bootstrap
// URL. The first navigation to that URL sets the session cookie. The server
// stops when ctx ends, when Stop is called, or after the idle timeout.
func (s *Server) Start(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.closed {
		return "", errors.New("web: server already started or stopped")
	}
	bootstrap, err := newSecret()
	if err != nil {
		return "", err
	}
	session, err := newSecret()
	if err != nil {
		return "", err
	}
	action, err := newSecret()
	if err != nil {
		return "", err
	}
	prefix, err := newPrefix()
	if err != nil {
		return "", err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("web: listen on loopback: %w", err)
	}
	s.addr = ln.Addr().(*net.TCPAddr)
	port := strconv.Itoa(s.addr.Port)
	s.origin = "http://127.0.0.1:" + port
	s.hosts = [2]string{"127.0.0.1:" + port, "localhost:" + port}
	s.cookieName = cookieNamePrefix + port
	s.session = session
	s.action = action
	s.bootstrap = bootstrap
	s.prefix = prefix
	s.lastActivity = time.Now()
	s.started = true
	srv := &http.Server{Handler: s.handler(), ReadHeaderTimeout: readHeaderLimit}
	s.httpServer = srv

	go srv.Serve(ln) // returns http.ErrServerClosed on Stop
	go s.hub.run(s.done)
	go s.watchIdle(min(max(s.idleTimeout/10, time.Millisecond), maxIdleTick))
	go func() {
		select {
		case <-ctx.Done():
			s.Stop()
		case <-s.done:
		}
	}()
	return s.origin + "/" + prefix + "/?token=" + bootstrap, nil
}

// Stop closes every event stream and shuts the server down. It is safe to call
// more than once.
func (s *Server) Stop() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	close(s.done)
	s.action = "" // a stopped server rejects every action token
	srv := s.httpServer
	s.mu.Unlock()

	s.hub.flush() // deliver the last events, such as a run's timeline, before the streams close
	s.hub.shutdown()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			_ = srv.Close()
		}
	}
}

// Publish queues an event for every connected client. See Event for coalescing.
func (s *Server) Publish(ev Event) {
	s.hub.publish(ev)
}

// watchIdle stops the server after the idle timeout. An open event stream
// counts as activity.
func (s *Server) watchIdle(tick time.Duration) {
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-t.C:
			s.mu.Lock()
			idle := time.Since(s.lastActivity) >= s.idleTimeout
			s.mu.Unlock()
			if idle && s.hub.count() == 0 {
				s.Stop()
				return
			}
		}
	}
}

func (s *Server) touch() {
	s.mu.Lock()
	s.lastActivity = time.Now()
	s.mu.Unlock()
}

func (s *Server) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /events", s.handleEvents)
	mux.HandleFunc("/s/", s.handleSurface)
	// An exact /s route, so the inner mux does not redirect /s to /s/ without
	// the prefix.
	mux.HandleFunc("/s", http.NotFound)
	root := http.NewServeMux()
	root.HandleFunc("GET /healthz", s.handleHealth)
	root.Handle("/", s.prefixed(mux))
	return secureHeaders(s.allowHost(s.guardMutations(root)))
}

// csp allows only same-origin resources. It blocks framing, base URL changes, and
// form posts to other origins.
const csp = "default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'"

// secureHeaders sets the response headers that apply to every response.
func secureHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", csp)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

// prefixed serves the routes under /<prefix>/. The prefix is a secret path segment.
// A request outside it gets 404, and the inner routes see the path without it.
// An inner path that is not clean, such as one from an escaped slash, also
// gets 404. The inner mux would redirect it to a path without the prefix.
func (s *Server) prefixed(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		base := "/" + s.prefix + "/"
		s.mu.Unlock()
		if !strings.HasPrefix(r.URL.Path, base) {
			http.NotFound(w, r)
			return
		}
		innerPath := "/" + strings.TrimPrefix(r.URL.Path, base)
		clean := path.Clean(innerPath)
		if strings.HasSuffix(innerPath, "/") && clean != "/" {
			clean += "/"
		}
		if clean != innerPath {
			http.NotFound(w, r)
			return
		}
		inner := r.Clone(r.Context())
		inner.URL.Path = innerPath
		inner.URL.RawPath = ""
		next.ServeHTTP(w, inner)
	})
}

// handleIndex redeems the bootstrap token, or serves the page to a session.
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if token := r.URL.Query().Get("token"); token != "" {
		// Only GET redeems the token. The GET pattern also serves HEAD, and a link
		// preview must not use up the one-time link.
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", http.MethodGet)
			http.Error(w, "web: method not allowed", http.StatusMethodNotAllowed)
			return
		}
		g, ok := s.redeem(token)
		if !ok {
			forbid(w)
			return
		}
		if s.afterRedeem != nil {
			s.afterRedeem()
		}
		s.touch()
		s.setSessionCookie(w, g)
		// Redirect so the token does not stay in the address bar or history.
		http.Redirect(w, r, g.target, http.StatusSeeOther)
		return
	}
	if !s.hasSession(r) {
		forbid(w)
		return
	}
	s.touch()
	page, err := assetFS.ReadFile(indexPath)
	if err != nil {
		http.Error(w, "web: read index page", http.StatusInternalServerError)
		return
	}
	s.mu.Lock()
	action := s.action
	s.mu.Unlock()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(bytes.ReplaceAll(page, []byte(actionPlaceholder), []byte(action)))
}

// handleHealth reports liveness. It needs no session and returns no secrets.
func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}` + "\n"))
}

// handleEvents streams events as Server-Sent Events. It requires the session
// cookie. A present Origin must match the bound origin.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Origin") != "" && !s.originAllowed(r) {
		forbid(w)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "web: streaming unsupported", http.StatusInternalServerError)
		return
	}
	// Subscribe before the session check. A session change that lands after the
	// check then closes this stream, so no client stays on a retired session.
	events, unsubscribe := s.hub.subscribe()
	defer unsubscribe()
	if s.afterSubscribe != nil {
		s.afterSubscribe()
	}
	if !s.hasSession(r) {
		forbid(w)
		return
	}
	s.touch()
	// The stream's end is activity too. Without this, the server could stop right
	// after a long-lived stream closes.
	defer s.touch()

	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write([]byte(": connected\n\n"))
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, open := <-events:
			if !open {
				return
			}
			writeEvent(w, ev)
			flusher.Flush()
		}
	}
}

// SetSession ends the current TUI session in the browser. It sends a
// session-changed event before any secret rotates, so an open page can say so.
// It then rotates the session cookie secret, the bootstrap token, and the action
// token, and closes every event stream. Old cookies and tokens fail after this
// call. It returns the new bootstrap URL, or "" when the server is not running.
// Before Start, it only records the ID.
func (s *Server) SetSession(id string) (string, error) {
	s.mu.Lock()
	s.sessionID = id
	running := s.started && !s.closed
	s.mu.Unlock()
	if !running {
		return "", nil
	}

	data, err := json.Marshal(sessionFrame{Session: id})
	if err != nil {
		return "", fmt.Errorf("web: encode session change: %w", err)
	}
	s.hub.publish(Event{
		Key:  "session-changed/" + strconv.FormatUint(s.changes.Add(1), 10),
		Name: "session-changed",
		Data: string(data),
	})
	s.hub.flush()
	if s.afterFlush != nil {
		s.afterFlush()
	}

	session, err := newSecret()
	if err != nil {
		return "", err
	}
	action, err := newSecret()
	if err != nil {
		return "", err
	}
	bootstrap, err := newSecret()
	if err != nil {
		return "", err
	}
	prefix, err := newPrefix()
	if err != nil {
		return "", err
	}
	s.mu.Lock()
	if s.closed {
		// Stop ran during the change. Writing new secrets now would leave a
		// URL that no server serves.
		s.mu.Unlock()
		return "", errors.New("web: server stopped during the session change")
	}
	s.session = session
	s.action = action
	s.bootstrap = bootstrap
	s.target = ""
	s.prefix = prefix
	s.mu.Unlock()

	s.hub.closeAll()
	return s.origin + "/" + prefix + "/?token=" + bootstrap, nil
}

// BootstrapURL issues a new one-time URL for the running server. A previous
// unused URL stops working. Existing sessions and action tokens do not change.
func (s *Server) BootstrapURL() (string, error) {
	return s.issueBootstrap("")
}

// BootstrapURLFor issues a new one-time URL that opens one surface page with
// a query, such as the editor with a draft. Like BootstrapURL, it ends a
// previous unused URL.
func (s *Server) BootstrapURLFor(surface string, query url.Values) (string, error) {
	if !validSurfaceName(surface) {
		return "", fmt.Errorf("web: invalid surface name %q", surface)
	}
	target := "s/" + surface + "/"
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	return s.issueBootstrap(target)
}

// issueBootstrap sets a new bootstrap token and the page it opens. An empty
// target opens the landing surface.
func (s *Server) issueBootstrap(target string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.closed {
		return "", errors.New("web: server is not running")
	}
	token, err := newSecret()
	if err != nil {
		return "", err
	}
	s.bootstrap, s.target = token, target
	return s.origin + "/" + s.prefix + "/?token=" + token, nil
}

// Done returns a channel that closes when the server stops. A caller uses it to
// tell a server that idled out from one that is still running.
func (s *Server) Done() <-chan struct{} {
	return s.done
}

// SessionID returns the TUI session ID last set with SetSession.
func (s *Server) SessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionID
}

// Surface is one browser page under /s/<name>/. Page serves GET requests, and
// receives the current action token to render into the page. Mutate serves
// POST, PUT, PATCH, and DELETE requests, and each one must carry the action token
// in the X-Splice-Action header. Sub serves GET requests below the page, such
// as data and assets, and receives the path below /s/<name>/. A nil handler
// means the surface has none.
type Surface struct {
	Page   func(w http.ResponseWriter, r *http.Request, action string)
	Mutate http.Handler
	Sub    func(w http.ResponseWriter, r *http.Request, sub string)
}

// Register adds a surface. The name uses lowercase letters, digits, and hyphens.
func (s *Server) Register(name string, surface Surface) error {
	if !validSurfaceName(name) {
		return fmt.Errorf("web: invalid surface name %q", name)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.surfaces[name]; exists {
		return fmt.Errorf("web: surface %q is already registered", name)
	}
	s.surfaces[name] = surface
	return nil
}

func (s *Server) hasSurface(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.surfaces[name]
	return ok
}

// SetLanding makes a redeemed bootstrap URL open the named surface instead of
// the index page. The index page stays at its own address.
func (s *Server) SetLanding(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.landing = name
}

func validSurfaceName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
			return false
		}
	}
	return true
}

// handleSurface routes /s/<name>/ to a registered surface. An unknown name is 404.
// A page needs the session cookie. A mutation needs the cookie, the Origin guard
// (applied before this handler), and the action token.
func (s *Server) handleSurface(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/s/")
	name, sub, found := strings.Cut(rest, "/")
	s.mu.Lock()
	surface, ok := s.surfaces[name]
	action := s.action
	s.mu.Unlock()
	if !found || !ok {
		http.NotFound(w, r)
		return
	}
	if !s.hasSession(r) {
		forbid(w)
		return
	}
	s.touch()
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		if surface.Mutate == nil {
			http.Error(w, "web: surface takes no actions", http.StatusMethodNotAllowed)
			return
		}
		if !s.actionOK(r, action) {
			forbid(w)
			return
		}
		surface.Mutate.ServeHTTP(w, r)
	case http.MethodGet, http.MethodHead:
		switch {
		case sub == "" && surface.Page != nil:
			surface.Page(w, r, action)
		case sub != "" && surface.Sub != nil:
			surface.Sub(w, r, sub)
		default:
			http.NotFound(w, r)
		}
	default:
		w.Header().Set("Allow", "GET, HEAD, POST, PUT, PATCH, DELETE")
		http.Error(w, "web: method not allowed", http.StatusMethodNotAllowed)
	}
}
