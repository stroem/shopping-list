// Package logging builds the application's structured logger (log/slog) from
// configuration, so every entrypoint emits JSON logs at a configurable level.
package logging

import (
	"io"
	"log/slog"
	"strings"
)

// ParseLevel maps a LOG_LEVEL string to a slog.Level. Parsing is
// case-insensitive and tolerant of surrounding whitespace; empty or unknown
// values default to info.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// New returns a slog.Logger writing JSON records to w at the level named by the
// given LOG_LEVEL string.
func New(w io.Writer, level string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: ParseLevel(level)}))
}
