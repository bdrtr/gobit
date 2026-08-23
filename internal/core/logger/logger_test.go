package logger_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/turkbirdev/gobit/internal/core/logger"
)

func TestNewJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	log := logger.New(logger.Options{Level: slog.LevelInfo, Format: "json", Output: &buf})

	log.Info("sipariş oluşturuldu", "order_id", "order_01")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("çıktı geçerli JSON değil: %v\nçıktı: %s", err, buf.String())
	}
	if rec["msg"] != "sipariş oluşturuldu" {
		t.Errorf("msg = %v, beklenen %q", rec["msg"], "sipariş oluşturuldu")
	}
	if rec["order_id"] != "order_01" {
		t.Errorf("order_id = %v, beklenen %q", rec["order_id"], "order_01")
	}
	if rec["level"] != "INFO" {
		t.Errorf("level = %v, beklenen INFO", rec["level"])
	}
}

func TestNewTextFormat(t *testing.T) {
	var buf bytes.Buffer
	log := logger.New(logger.Options{Level: slog.LevelInfo, Format: "text", Output: &buf})

	log.Info("merhaba", "k", "v")

	out := buf.String()
	if json.Valid(buf.Bytes()) {
		t.Errorf("text biçimi JSON üretti: %s", out)
	}
	if !strings.Contains(out, "k=v") {
		t.Errorf("çıktıda k=v yok: %s", out)
	}
}

func TestNewLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	log := logger.New(logger.Options{Level: slog.LevelWarn, Format: "json", Output: &buf})

	log.Debug("görünmemeli")
	log.Info("bu da görünmemeli")
	if buf.Len() != 0 {
		t.Errorf("seviye altındaki kayıtlar yazıldı: %s", buf.String())
	}

	log.Warn("görünmeli")
	if !strings.Contains(buf.String(), "görünmeli") {
		t.Errorf("warn kaydı yazılmadı: %s", buf.String())
	}
}

func TestNewAddSource(t *testing.T) {
	var buf bytes.Buffer
	log := logger.New(logger.Options{Level: slog.LevelInfo, Format: "json", Output: &buf, AddSource: true})

	log.Info("kaynaklı")

	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("çıktı geçerli JSON değil: %v", err)
	}
	if _, ok := rec["source"]; !ok {
		t.Errorf("AddSource=true iken source alanı yok: %s", buf.String())
	}
}

func TestNewDefaultsToStdout(t *testing.T) {
	// Output nil verildiğinde panik olmamalı (os.Stdout'a düşer).
	log := logger.New(logger.Options{Level: slog.LevelError, Format: "json"})
	if log == nil {
		t.Fatal("New() nil logger döndü")
	}
	log.Debug("yazılmaz")
}
