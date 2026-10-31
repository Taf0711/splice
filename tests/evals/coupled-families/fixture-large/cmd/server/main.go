// Command server wires the demo HTTP surface: the admin routes, the
// password reset flow, and the session store they share.
package main

import (
	"log"
	"net/http"
	"time"

	"demo/internal/admin"
	"demo/internal/audit"
	"demo/internal/auth"
	"demo/internal/billing"
	"demo/internal/notifications"
	"demo/internal/session"
	"demo/internal/storage"
	"demo/internal/webhooks"
	"demo/internal/worker"
)

func main() {
	store := session.NewStore(30 * time.Minute)
	svc := auth.NewPasswordService(store)
	trail := audit.NewTrail()
	dispatch := notifications.NewDispatcher()
	hooks := webhooks.NewRegistry()
	blobs := storage.NewStore()
	pool := &worker.Pool{}
	biller := billing.NewService()

	mux := http.NewServeMux()
	mux.Handle("/admin/", admin.NewHandler(store))

	mux.HandleFunc("/reset", func(w http.ResponseWriter, r *http.Request) {
		user := r.URL.Query().Get("user")
		pass := r.URL.Query().Get("password")
		if err := svc.ResetPassword(user, pass); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		trail.Record(user, "password.reset", user, "ok")
		w.WriteHeader(http.StatusOK)
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})

	_ = dispatch
	_ = hooks
	_ = blobs
	_ = pool
	_ = biller

	log.Println("demo server listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}

// mainPage0 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func mainPage0(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// mainNormalize1 drops empty entries and keeps the first occurrence of
// each value, in input order.
func mainNormalize1(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// mainWindow2 counts uses inside one period and refuses uses past the
// limit until the period resets.
type mainWindow2 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *mainWindow2) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *mainWindow2) reset() {
	w.used = 0
}

// mainClamp3 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func mainClamp3(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// mainDescribe4 renders a status code for operator logs.
func mainDescribe4(code int) string {
	switch {
	case code < 0:
		return "invalid"
	case code == 0:
		return "idle"
	case code < 10:
		return "active"
	case code < 100:
		return "degraded"
	default:
		return "saturated"
	}
}

// mainPage5 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func mainPage5(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// mainNormalize6 drops empty entries and keeps the first occurrence of
// each value, in input order.
func mainNormalize6(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// mainWindow7 counts uses inside one period and refuses uses past the
// limit until the period resets.
type mainWindow7 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *mainWindow7) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *mainWindow7) reset() {
	w.used = 0
}
