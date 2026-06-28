package logging_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/stroem/shopping-list/backend/internal/logging"
)

func TestParseLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":   slog.LevelDebug,
		"DEBUG":   slog.LevelDebug,
		" info ":  slog.LevelInfo,
		"info":    slog.LevelInfo,
		"warn":    slog.LevelWarn,
		"warning": slog.LevelWarn,
		"error":   slog.LevelError,
		"":        slog.LevelInfo,
		"garbage": slog.LevelInfo,
	}
	for in, want := range cases {
		if got := logging.ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNewEmitsJSON(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(&buf, "info")
	log.Info("hello", "k", "v")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, buf.String())
	}
	if rec["msg"] != "hello" {
		t.Errorf("msg = %v, want hello", rec["msg"])
	}
	if rec["k"] != "v" {
		t.Errorf("k = %v, want v", rec["k"])
	}
}

func TestNewRespectsLevel(t *testing.T) {
	var buf bytes.Buffer
	log := logging.New(&buf, "info")
	log.Debug("suppressed")
	if buf.Len() != 0 {
		t.Errorf("debug record emitted at info level: %s", buf.String())
	}

	buf.Reset()
	log = logging.New(&buf, "debug")
	log.Debug("kept")
	if buf.Len() == 0 {
		t.Error("debug record suppressed at debug level")
	}
}
