package logging

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

func TestNewRejectsUnknownLevel(t *testing.T) {
	if _, err := New("verbose"); err == nil {
		t.Fatal("expected error for unknown level")
	}
}

func TestNewAcceptsValidLevels(t *testing.T) {
	for _, lvl := range []string{"", "debug", "info", "warn", "warning", "error"} {
		if _, err := New(lvl); err != nil {
			t.Errorf("level %q: %v", lvl, err)
		}
	}
}

// captureLogger builds a logger that writes JSON to a buffer, mirroring the
// production encoder so we can assert on field shape.
func captureLogger(t *testing.T, level zapcore.Level) (*zap.Logger, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	encCfg := zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		MessageKey:     "message",
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.RFC3339NanoTimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
	}
	core := zapcore.NewCore(zapcore.NewJSONEncoder(encCfg), zapcore.AddSync(buf), level)
	return zap.New(core), buf
}

func TestEntryShape(t *testing.T) {
	l, buf := captureLogger(t, zapcore.InfoLevel)
	WithComponent(l, "discovery").Info("cycle starting", zap.String("cycle_id", "abc"))

	var entry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("log line is not valid JSON: %v\n%s", err, buf.String())
	}
	for _, key := range []string{"timestamp", "level", "message", "component", "cycle_id"} {
		if _, ok := entry[key]; !ok {
			t.Errorf("missing field %q in %v", key, entry)
		}
	}
	if entry["component"] != "discovery" {
		t.Errorf("component field mismatch: %v", entry["component"])
	}
	if entry["level"] != "info" {
		t.Errorf("level field mismatch: %v", entry["level"])
	}
}

func TestLevelFiltering(t *testing.T) {
	l, buf := captureLogger(t, zapcore.WarnLevel)
	l.Info("ignored")
	l.Warn("kept")
	got := buf.String()
	if strings.Contains(got, "ignored") {
		t.Errorf("warn-level logger should not emit info: %s", got)
	}
	if !strings.Contains(got, "kept") {
		t.Errorf("warn-level logger missed warn: %s", got)
	}
}

// silence stdout so the production New() test doesn't pollute test output.
func TestNewWritesToStdout(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	l, err := New("info")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	l.Info("smoke")
	_ = l.Sync()
	w.Close()

	out, _ := io.ReadAll(r)
	if !strings.Contains(string(out), `"message":"smoke"`) {
		t.Errorf("stdout did not receive log: %s", out)
	}
}
