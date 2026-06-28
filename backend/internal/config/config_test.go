package config_test

import (
	"testing"
	"time"

	"github.com/stroem/shopping-list/backend/internal/config"
)

func TestLoadRequiresDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	if _, err := config.Load(); err == nil {
		t.Fatal("expected error when DATABASE_URL is unset, got nil")
	}
}

func TestLoadDefaultsPort(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("PORT", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != "8080" {
		t.Fatalf("Port = %q, want 8080", cfg.Port)
	}
	if cfg.DatabaseURL != "postgres://localhost/db" {
		t.Fatalf("DatabaseURL = %q", cfg.DatabaseURL)
	}
}

func TestLoadDefaultsLogLevel(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("LOG_LEVEL", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LogLevel != "info" {
		t.Fatalf("LogLevel = %q, want info", cfg.LogLevel)
	}
}

func TestLoadReadsLogLevel(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("LOG_LEVEL", "debug")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LogLevel != "debug" {
		t.Fatalf("LogLevel = %q, want debug", cfg.LogLevel)
	}
}

func TestLoadDefaultsRequestTimeout(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("REQUEST_TIMEOUT", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RequestTimeout != 5*time.Second {
		t.Fatalf("RequestTimeout = %v, want 5s", cfg.RequestTimeout)
	}
}

func TestLoadReadsRequestTimeout(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("REQUEST_TIMEOUT", "250ms")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RequestTimeout != 250*time.Millisecond {
		t.Fatalf("RequestTimeout = %v, want 250ms", cfg.RequestTimeout)
	}
}

func TestLoadInvalidRequestTimeoutFallsBackToDefault(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("REQUEST_TIMEOUT", "not-a-duration")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.RequestTimeout != 5*time.Second {
		t.Fatalf("RequestTimeout = %v, want 5s fallback", cfg.RequestTimeout)
	}
}

func TestLoadDefaultsSuggestRateLimit(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("SUGGEST_RATE_LIMIT", "")
	t.Setenv("SUGGEST_RATE_WINDOW", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SuggestRateLimit != 60 {
		t.Fatalf("SuggestRateLimit = %d, want 60", cfg.SuggestRateLimit)
	}
	if cfg.SuggestRateWindow != time.Minute {
		t.Fatalf("SuggestRateWindow = %v, want 1m", cfg.SuggestRateWindow)
	}
}

func TestLoadReadsSuggestRateLimit(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("SUGGEST_RATE_LIMIT", "120")
	t.Setenv("SUGGEST_RATE_WINDOW", "30s")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SuggestRateLimit != 120 {
		t.Fatalf("SuggestRateLimit = %d, want 120", cfg.SuggestRateLimit)
	}
	if cfg.SuggestRateWindow != 30*time.Second {
		t.Fatalf("SuggestRateWindow = %v, want 30s", cfg.SuggestRateWindow)
	}
}

func TestLoadInvalidSuggestRateLimitFallsBackToDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("SUGGEST_RATE_LIMIT", "nope")
	t.Setenv("SUGGEST_RATE_WINDOW", "not-a-duration")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SuggestRateLimit != 60 {
		t.Fatalf("SuggestRateLimit = %d, want 60 fallback", cfg.SuggestRateLimit)
	}
	if cfg.SuggestRateWindow != time.Minute {
		t.Fatalf("SuggestRateWindow = %v, want 1m fallback", cfg.SuggestRateWindow)
	}
}

func TestLoadDefaultsCORSAllowedOriginsEmpty(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.CORSAllowedOrigins) != 0 {
		t.Fatalf("CORSAllowedOrigins = %v, want empty", cfg.CORSAllowedOrigins)
	}
}

func TestLoadParsesCORSAllowedOrigins(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://localhost/db")
	t.Setenv("CORS_ALLOWED_ORIGINS", " https://app.example.com , ,https://admin.example.com ")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"https://app.example.com", "https://admin.example.com"}
	if len(cfg.CORSAllowedOrigins) != len(want) {
		t.Fatalf("CORSAllowedOrigins = %v, want %v", cfg.CORSAllowedOrigins, want)
	}
	for i, o := range want {
		if cfg.CORSAllowedOrigins[i] != o {
			t.Fatalf("CORSAllowedOrigins[%d] = %q, want %q", i, cfg.CORSAllowedOrigins[i], o)
		}
	}
}
