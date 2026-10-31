package main

import (
	"testing"
	"time"
)

// TestSessionExpiryTable covers the expiry boundary cases against a
// controllable clock in one table.
func TestSessionExpiryTable(t *testing.T) {
	cases := []struct {
		name    string
		advance time.Duration
		live    bool
	}{
		{"fresh-readable", 0, true},
		{"exactly-ttl-live", time.Minute, true},
		{"one-second-past-expired", time.Minute + time.Second, false},
	}
	for _, tc := range cases {
		store := NewStore(time.Minute)
		store.now = func() time.Time { return time.Unix(1000, 0) }
		store.Create("abc", "ada")
		store.now = func() time.Time { return time.Unix(1000, 0).Add(tc.advance) }
		_, err := store.Get("abc")
		if tc.live && err != nil {
			t.Fatalf("%s: Get = %v, want live", tc.name, err)
		}
		if !tc.live && err == nil {
			t.Fatalf("%s: Get = nil, want expired", tc.name)
		}
	}
}
