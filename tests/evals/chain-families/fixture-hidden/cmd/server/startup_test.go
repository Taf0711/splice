package main

import "testing"

func TestStartupChecksPass(t *testing.T) {
	if err := validate(); err != nil {
		t.Fatal(err)
	}
}
