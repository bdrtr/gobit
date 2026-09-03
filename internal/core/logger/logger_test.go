package logger_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/bdrtr/gobit/internal/core/logger"
)

func TestNewJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	log := logger.New(logger.Options{Level: slog.LevelInfo, Format: "json", Output: &buf})

	log.Info("the order was placed", "order_id", "order_01")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("the output is not valid JSON: %v\noutput: %s", err, buf.String())
	}
	if rec["msg"] != "the order was placed" {
		t.Errorf("msg = %v, want %q", rec["msg"], "the order was placed")
	}
	if rec["order_id"] != "order_01" {
		t.Errorf("order_id = %v, want %q", rec["order_id"], "order_01")
	}
	if rec["level"] != "INFO" {
		t.Errorf("level = %v, want INFO", rec["level"])
	}
}

func TestNewTextFormat(t *testing.T) {
	var buf bytes.Buffer
	log := logger.New(logger.Options{Level: slog.LevelInfo, Format: "text", Output: &buf})

	log.Info("hello", "k", "v")

	out := buf.String()
	if json.Valid(buf.Bytes()) {
		t.Errorf("the text format produced JSON: %s", out)
	}
	if !strings.Contains(out, "k=v") {
		t.Errorf("k=v is missing from the output: %s", out)
	}
}

func TestNewLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	log := logger.New(logger.Options{Level: slog.LevelWarn, Format: "json", Output: &buf})

	log.Debug("must not appear")
	log.Info("must not appear either")
	if buf.Len() != 0 {
		t.Errorf("records below the level were written: %s", buf.String())
	}

	log.Warn("must appear")
	if !strings.Contains(buf.String(), "must appear") {
		t.Errorf("the warn record was not written: %s", buf.String())
	}
}

func TestNewAddSource(t *testing.T) {
	var buf bytes.Buffer
	log := logger.New(logger.Options{Level: slog.LevelInfo, Format: "json", Output: &buf, AddSource: true})

	log.Info("with a source")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("the output is not valid JSON: %v", err)
	}
	if _, ok := rec["source"]; !ok {
		t.Errorf("the source field is missing while AddSource=true: %s", buf.String())
	}
}

func TestNewDefaultsToStdout(t *testing.T) {
	// A nil Output must not panic (it falls back to os.Stdout).
	log := logger.New(logger.Options{Level: slog.LevelError, Format: "json"})
	if log == nil {
		t.Fatal("New() returned a nil logger")
	}
	log.Debug("not written")
}
