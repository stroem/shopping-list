// Package config loads server configuration from the environment.
package config

import (
	"errors"
	"os"
	"time"
)

// defaultRequestTimeout bounds each request when REQUEST_TIMEOUT is unset or
// unparseable. It must be comfortably under API Gateway's 29s limit.
const defaultRequestTimeout = 5 * time.Second

// Config is the server's runtime configuration, sourced entirely from env vars.
type Config struct {
	DatabaseURL    string        // required; standard postgres:// URL
	Port           string        // listen port, default "8080"
	LogLevel       string        // slog level: debug|info|warn|error, default "info"
	RequestTimeout time.Duration // per-request deadline, default 5s (REQUEST_TIMEOUT)
}

// Load reads configuration from the environment. DATABASE_URL is required.
func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		Port:           os.Getenv("PORT"),
		LogLevel:       os.Getenv("LOG_LEVEL"),
		RequestTimeout: requestTimeout(os.Getenv("REQUEST_TIMEOUT")),
	}
	if cfg.DatabaseURL == "" {
		return Config{}, errors.New("DATABASE_URL is required")
	}
	if cfg.Port == "" {
		cfg.Port = "8080"
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "info"
	}
	return cfg, nil
}

// requestTimeout parses a Go duration string, falling back to the default on an
// empty or invalid value so a typo can never disable the deadline.
func requestTimeout(raw string) time.Duration {
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	return defaultRequestTimeout
}
