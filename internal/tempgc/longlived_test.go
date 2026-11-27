package tempgc

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// waitIdle waits until the background collection in this process ends.
func waitIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for collecting.Load() {
		if time.Now().After(deadline) {
			t.Fatal("background collection did not finish within 5 s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestMaybeCollectAsyncCollectsAgainInLongLivedProcess(t *testing.T) {
	root := t.TempDir()
	skipUnderTests = false
	collectRoot = root
	t.Cleanup(func() {
		skipUnderTests = true
		collectRoot = ""
	})

	stamp := filepath.Join(root, spliceDirName, stampName)
	writeSized(t, stamp, 0)
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stamp, past, past); err != nil {
		t.Fatal(err)
	}

	// First run: the stale stamp lets a collection start, and it touches the stamp.
	MaybeCollectAsync()
	waitIdle(t)
	info, err := os.Stat(stamp)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(info.ModTime()) > time.Minute {
		t.Fatal("first background collection did not touch the stamp")
	}

	// Second run in the same process: the stamp is 2 hours old again, so it runs again.
	past = time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stamp, past, past); err != nil {
		t.Fatal(err)
	}
	MaybeCollectAsync()
	waitIdle(t)
	info, err = os.Stat(stamp)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(info.ModTime()) > time.Minute {
		t.Fatal("second background collection in the same process did not run")
	}
}
