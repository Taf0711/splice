package update

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	// NoticeRefreshAfter is the maximum age of a cached update check.
	NoticeRefreshAfter = 24 * time.Hour
	updateCacheEnv     = "SPLICE_UPDATE_CACHE_PATH"
	updateNoticeEnv    = "SPLICE_DISABLE_UPDATE_NOTICE"
	updatesEnv         = "SPLICE_DISABLE_UPDATES"
)

type noticeCache struct {
	Result *Result `json:"result,omitempty"`
}

var noticePathLocks struct {
	sync.Mutex
	values map[string]*sync.Mutex
}

// NoticeOptions configures a cached background update check.
type NoticeOptions struct {
	// Check overrides Check. It is used by tests and by the CLI dependency seam.
	Check   func(context.Context, Options) (Result, error)
	Options Options
	// ExecutablePath is used to select the update command for the install method.
	ExecutablePath string
	// Now overrides the clock. A nil value uses time.Now.
	Now func() time.Time
	// CachePath overrides the user cache path. An empty value uses the default path
	// or SPLICE_UPDATE_CACHE_PATH when set.
	CachePath string
}

// UpdatesDisabled reports whether all update behavior is disabled.
func UpdatesDisabled() bool {
	return strings.TrimSpace(os.Getenv(updatesEnv)) != ""
}

// NoticeDisabled reports whether background update notices are disabled.
func NoticeDisabled() bool {
	return UpdatesDisabled() || strings.TrimSpace(os.Getenv(updateNoticeEnv)) != ""
}

// CachedNotice checks for an update at most once per NoticeRefreshAfter. It
// stores the last result on disk, including failed checks, so a failed or slow
// network cannot cause repeated work on every startup. Errors are returned to
// the caller, which can ignore them for a courtesy background check.
func CachedNotice(ctx context.Context, options NoticeOptions) (string, error) {
	if NoticeDisabled() {
		return "", nil
	}
	path, err := noticeCachePath(options.CachePath)
	if err != nil {
		return "", err
	}
	lock := noticeLock(path)
	lock.Lock()
	defer lock.Unlock()
	now := time.Now
	if options.Now != nil {
		now = options.Now
	}
	nowValue := now()
	if cached, fresh, readErr := readNoticeCache(path, nowValue); readErr == nil && fresh {
		if cached.Result == nil || !cached.Result.UpdateAvailable {
			return "", nil
		}
		return FormatNotice(*cached.Result, DetectInstallMethod(options.ExecutablePath)), nil
	}

	check := options.Check
	if check == nil {
		check = Check
	}
	result, checkErr := check(ctx, options.Options)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return "", ctxErr
	}
	cacheErr := writeNoticeCache(path, noticeCache{Result: func() *Result {
		if checkErr != nil {
			return nil
		}
		return &result
	}()})
	if checkErr != nil {
		return "", checkErr
	}
	if cacheErr != nil {
		return "", cacheErr
	}
	if !result.UpdateAvailable {
		return "", nil
	}
	return FormatNotice(result, DetectInstallMethod(options.ExecutablePath)), nil
}

func noticeLock(path string) *sync.Mutex {
	noticePathLocks.Lock()
	defer noticePathLocks.Unlock()
	if noticePathLocks.values == nil {
		noticePathLocks.values = make(map[string]*sync.Mutex)
	}
	if lock := noticePathLocks.values[path]; lock != nil {
		return lock
	}
	lock := &sync.Mutex{}
	noticePathLocks.values[path] = lock
	return lock
}

// FormatNotice returns the single-line notice shown on stderr for a newer
// release. It uses npm for npm-managed installs and the standalone command for
// all other installs.
func FormatNotice(result Result, method InstallMethod) string {
	command := "splice update --apply"
	if method == InstallMethodNpm {
		command = "npm install -g " + npmPackageName + "@latest"
	}
	return fmt.Sprintf("[splice] Update available: %s -> %s. Run `%s` to update.", result.CurrentVersion, result.LatestVersion, command)
}

func noticeCachePath(override string) (string, error) {
	if value := strings.TrimSpace(override); value != "" {
		return value, nil
	}
	if value := strings.TrimSpace(os.Getenv(updateCacheEnv)); value != "" {
		return value, nil
	}
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "splice", "update-check.json"), nil
}

func readNoticeCache(path string, now time.Time) (noticeCache, bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return noticeCache{}, false, err
	}
	if now.Sub(info.ModTime()) >= NoticeRefreshAfter {
		return noticeCache{}, false, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return noticeCache{}, false, err
	}
	var cached noticeCache
	if err := json.Unmarshal(data, &cached); err != nil {
		return noticeCache{}, false, err
	}
	return cached, true, nil
}

func writeNoticeCache(path string, cached noticeCache) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), "update-check-*.json")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer func() {
		_ = os.Remove(tempPath)
	}()
	data, err := json.Marshal(cached)
	if err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}
