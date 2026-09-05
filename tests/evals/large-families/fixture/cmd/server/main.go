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
