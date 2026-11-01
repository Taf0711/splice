package main

import (
	"testing"
	"time"
)

// clockCase is one row of a fake-clock table test.
type clockCase struct {
	name    string
	ttl     time.Duration
	advance time.Duration
	wantErr bool
	wantLen int
}

// runClockTable drives one fresh store per case with a fake clock that never
// sleeps, so expiry is tested deterministically. New expiry tests should reuse
// this helper instead of duplicating the fake-clock plumbing.
func runClockTable(t *testing.T, cases []clockCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := NewStore(tc.ttl)
			clock := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			store.now = func() time.Time { return clock }
			store.Create("abc", "ada")
			clock = clock.Add(tc.advance)
			_, err := store.Get("abc")
			if tc.wantErr && err == nil {
				t.Fatalf("Get after %s: err = nil, want an error", tc.advance)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("Get after %s: %v", tc.advance, err)
			}
			if got := store.Len(); got != tc.wantLen {
				t.Fatalf("Len after %s = %d, want %d", tc.advance, got, tc.wantLen)
			}
		})
	}
}

// TestClockTableHelperSelfCheck proves the helper itself works.
func TestClockTableHelperSelfCheck(t *testing.T) {
	runClockTable(t, []clockCase{
		{name: "under-ttl", ttl: time.Minute, advance: 30 * time.Second, wantLen: 1},
		{name: "over-ttl", ttl: time.Minute, advance: 2 * time.Minute, wantErr: true, wantLen: 0},
	})
}
