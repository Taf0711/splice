package main

import (
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

// TestSessionLookupTable pins the /session GET handler's status codes in
// ONE table-driven test through a fresh mux with a controllable clock.
func TestSessionLookupTable(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		setup  func(*Store)
		want   int
	}{
		{
			name:   "live session reads 200",
			method: "GET",
			path:   "/session?id=live",
			setup: func(store *Store) {
				store.Create("live", "ada")
			},
			want: 200,
		},
		{
			name:   "missing session reads 404",
			method: "GET",
			path:   "/session?id=missing",
			setup:  func(store *Store) {},
			want:   404,
		},
		{
			name:   "wrong method answers 405",
			method: "PUT",
			path:   "/session?id=live",
			setup:  func(store *Store) {},
			want:   405,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewStore(time.Minute)
			store.now = func() time.Time { return time.Unix(1000, 0) }
			tc.setup(store)
			rec := httptest.NewRecorder()
			newSessionMux(store).ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != tc.want {
				t.Fatalf("%s: status = %d, want %d", tc.name, rec.Code, tc.want)
			}
		})
	}
}

var _ = errors.New
