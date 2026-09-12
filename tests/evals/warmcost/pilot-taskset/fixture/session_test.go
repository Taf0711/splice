package main

import (
	"errors"
	"testing"
	"time"
)

func TestStoreCreateAndGet(t *testing.T) {
	store := NewStore(time.Minute)
	created := store.Create("abc", "ada")
	if created.User != "ada" {
		t.Fatalf("created user = %q, want ada", created.User)
	}
	found, err := store.Get("abc")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if found.ID != "abc" || found.User != "ada" {
		t.Fatalf("found session = %+v, want abc/ada", found)
	}
	if store.Len() != 1 {
		t.Fatalf("store length = %d, want 1", store.Len())
	}
}

func TestStoreExpiresIdleSession(t *testing.T) {
	store := NewStore(time.Minute)
	clock := time.Now()
	store.now = func() time.Time { return clock }
	store.Create("abc", "ada")

	clock = clock.Add(2 * time.Minute)
	if _, err := store.Get("abc"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get after ttl = %v, want ErrNotFound", err)
	}
	if store.Len() != 0 {
		t.Fatalf("expired session was not dropped: length = %d", store.Len())
	}
}
