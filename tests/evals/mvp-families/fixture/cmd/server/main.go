// Command server wires the demo HTTP surface: the admin routes, the
// password reset flow, and the session store they share.
package main

import (
	"log"
	"net/http"
	"time"

	"demo/internal/admin"
	"demo/internal/auth"
	"demo/internal/session"
)

func main() {
	store := session.NewStore(30 * time.Minute)

	mux := http.NewServeMux()
	mux.Handle("/admin/", admin.NewHandler(store))

	svc := auth.NewPasswordService(store)
	mux.HandleFunc("/reset", func(w http.ResponseWriter, r *http.Request) {
		user := r.URL.Query().Get("user")
		pass := r.URL.Query().Get("password")
		if err := svc.ResetPassword(user, pass); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	log.Println("demo server listening on :8080")
	log.Fatal(http.ListenAndServe(":8080", mux))
}
