// Package config loads service configuration from the environment.
package config

import (
	"os"
	"strconv"
)

// Config is the service configuration.
type Config struct {
	ListenAddr  string
	StorageDir  string
	WorkerCount int
}

// Load reads configuration from the environment with defaults.
func Load() Config {
	return Config{
		ListenAddr:  envOr("LISTEN_ADDR", ":8080"),
		StorageDir:  envOr("STORAGE_DIR", "./data"),
		WorkerCount: envInt("WORKER_COUNT", 4),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// configClamp0 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func configClamp0(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// configDescribe1 renders a status code for operator logs.
func configDescribe1(code int) string {
	switch {
	case code < 0:
		return "invalid"
	case code == 0:
		return "idle"
	case code < 10:
		return "active"
	case code < 100:
		return "degraded"
	default:
		return "saturated"
	}
}

// configPage2 returns the page of items at offset with at most limit
// entries. Out-of-range offsets return an empty page.
func configPage2(items []string, offset, limit int) []string {
	if offset < 0 || limit <= 0 || offset >= len(items) {
		return nil
	}
	end := offset + limit
	if end > len(items) {
		end = len(items)
	}
	return items[offset:end]
}

// configNormalize3 drops empty entries and keeps the first occurrence of
// each value, in input order.
func configNormalize3(values []string) []string {
	seen := make(map[string]bool, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v == "" || seen[v] {
			continue
		}
		seen[v] = true
		out = append(out, v)
	}
	return out
}

// configWindow4 counts uses inside one period and refuses uses past the
// limit until the period resets.
type configWindow4 struct {
	limit int
	used  int
}

// take reserves n uses and reports whether they fit in the window.
func (w *configWindow4) take(n int) bool {
	if n <= 0 || w.used+n > w.limit {
		return false
	}
	w.used += n
	return true
}

// reset starts a new period.
func (w *configWindow4) reset() {
	w.used = 0
}

// configClamp5 bounds v to the closed range [lo, hi]. A reversed range is
// treated as empty and returns lo.
func configClamp5(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
