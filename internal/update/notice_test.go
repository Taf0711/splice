package update

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCachedNoticeUsesCacheWithin24Hours(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	var calls atomic.Int32
	check := func(context.Context, Options) (Result, error) {
		calls.Add(1)
		return Result{CurrentVersion: "1.0.0", LatestVersion: "1.1.0", UpdateAvailable: true}, nil
	}
	now := time.Now()
	options := NoticeOptions{Check: check, CachePath: cachePath, Now: func() time.Time { return now }}
	if _, err := CachedNotice(context.Background(), options); err != nil {
		t.Fatalf("first CachedNotice() error = %v", err)
	}
	options.Now = func() time.Time { return now.Add(time.Hour) }
	if _, err := CachedNotice(context.Background(), options); err != nil {
		t.Fatalf("second CachedNotice() error = %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("check call count = %d, want 1", got)
	}
}

func TestCachedNoticeNoNewerReleaseIsEmpty(t *testing.T) {
	notice, err := CachedNotice(context.Background(), NoticeOptions{
		CachePath: filepath.Join(t.TempDir(), "update-check.json"),
		Check: func(context.Context, Options) (Result, error) {
			return Result{CurrentVersion: "1.1.0", LatestVersion: "1.1.0"}, nil
		},
	})
	if err != nil {
		t.Fatalf("CachedNotice() error = %v", err)
	}
	if notice != "" {
		t.Fatalf("notice = %q, want empty", notice)
	}
}

func TestCachedNoticeFailedCheckIsSilentAndCached(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	var calls atomic.Int32
	check := func(context.Context, Options) (Result, error) {
		calls.Add(1)
		return Result{}, errors.New("timed out")
	}
	now := time.Now()
	options := NoticeOptions{Check: check, CachePath: cachePath, Now: func() time.Time { return now }}
	if notice, err := CachedNotice(context.Background(), options); err == nil || notice != "" {
		t.Fatalf("failed check = (%q, %v), want no notice and an error", notice, err)
	}
	options.Now = func() time.Time { return now.Add(time.Hour) }
	if notice, err := CachedNotice(context.Background(), options); err != nil || notice != "" {
		t.Fatalf("cached failed check = (%q, %v), want silent success", notice, err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("check call count = %d, want 1", got)
	}
}

func TestFormatNoticeNamesInstallCommand(t *testing.T) {
	result := Result{CurrentVersion: "1.0.0", LatestVersion: "1.1.0", UpdateAvailable: true}
	if got := FormatNotice(result, InstallMethodNpm); got != "[splice] Update available: 1.0.0 -> 1.1.0. Run `npm install -g @taf0711/splice@latest` to update." {
		t.Fatalf("npm notice = %q", got)
	}
	if got := FormatNotice(result, InstallMethodStandalone); got != "[splice] Update available: 1.0.0 -> 1.1.0. Run `splice update --apply` to update." {
		t.Fatalf("standalone notice = %q", got)
	}
}

func TestCachedNoticeDetectsNpmInstall(t *testing.T) {
	dir := t.TempDir()
	executable := filepath.Join(dir, "splice")
	if err := os.WriteFile(filepath.Join(dir, ".splice-binary-version"), []byte("1.0.0"), 0o600); err != nil {
		t.Fatal(err)
	}
	cachePath := filepath.Join(t.TempDir(), "update-check.json")
	notice, err := CachedNotice(context.Background(), NoticeOptions{
		CachePath:      cachePath,
		ExecutablePath: executable,
		Check: func(context.Context, Options) (Result, error) {
			return Result{CurrentVersion: "1.0.0", LatestVersion: "1.1.0", UpdateAvailable: true}, nil
		},
	})
	if err != nil {
		t.Fatalf("CachedNotice() error = %v", err)
	}
	if want := "npm install -g @taf0711/splice@latest"; !strings.Contains(notice, want) {
		t.Fatalf("notice = %q, want %q", notice, want)
	}
}
