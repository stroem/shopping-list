// Package config loads server configuration from the environment.
package config

import (
	"errors"
	"os"
	"strings"
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
	// CORSAllowedOrigins lists the cross-origin web origins permitted to call the
	// API, parsed from CORS_ALLOWED_ORIGINS (comma-separated). Empty means the
	// router applies its safe local-dev default; set it in production.
	CORSAllowedOrigins []string
}

// Load reads configuration from the environment. DATABASE_URL is required.
func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:        os.Getenv("DATABASE_URL"),
		Port:               os.Getenv("PORT"),
		LogLevel:           os.Getenv("LOG_LEVEL"),
		RequestTimeout:     requestTimeout(os.Getenv("REQUEST_TIMEOUT")),
		CORSAllowedOrigins: ParseCORSOrigins(os.Getenv("CORS_ALLOWED_ORIGINS")),
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

// ParseCORSOrigins splits a comma-separated CORS_ALLOWED_ORIGINS value, trimming
// spaces and dropping empties. It returns nil when nothing usable is present, so
// the router can apply its local-dev default. Exported so cmd/lambda, which does
// not call Load, can parse the same env var consistently.
func ParseCORSOrigins(raw string) []string {
	var origins []string
	for _, part := range strings.Split(raw, ",") {
		if o := strings.TrimSpace(part); o != "" {
			origins = append(origins, o)
		}
	}
	return origins
}

// requestTimeout parses a Go duration string, falling back to the default on an
// empty or invalid value so a typo can never disable the deadline.
func requestTimeout(raw string) time.Duration {
	if d, err := time.ParseDuration(raw); err == nil && d > 0 {
		return d
	}
	return defaultRequestTimeout
}
