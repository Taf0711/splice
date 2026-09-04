package main

import (
	"net/http/httptest"
	"testing"
	"time"
)

// WRONG variant: three separate test functions instead of one table.
func TestLookupLive(t *testing.T) {
	store := NewStore(time.Minute)
	store.now = func() time.Time { return time.Unix(1000, 0) }
	store.Create("live", "ada")
	rec := httptest.NewRecorder()
	newSessionMux(store).ServeHTTP(rec, httptest.NewRequest("GET", "/session?id=live", nil))
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestLookupMissing(t *testing.T) {
	store := NewStore(time.Minute)
	rec := httptest.NewRecorder()
	newSessionMux(store).ServeHTTP(rec, httptest.NewRequest("GET", "/session?id=missing", nil))
	if rec.Code != 404 {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
}

func TestLookupWrongMethod(t *testing.T) {
	store := NewStore(time.Minute)
	rec := httptest.NewRecorder()
	newSessionMux(store).ServeHTTP(rec, httptest.NewRequest("PUT", "/session?id=x", nil))
	if rec.Code != 405 {
		t.Fatalf("status = %d, want 405", rec.Code)
	}
}

var _ = httptest.NewRequest
