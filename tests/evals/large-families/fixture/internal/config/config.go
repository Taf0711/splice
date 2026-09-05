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
