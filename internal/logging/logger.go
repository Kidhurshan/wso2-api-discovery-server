// Package logging wires zap into the daemon's structured-logging contract per
// claude/specs/operations_guide.md §7. Output is JSON to stdout (Kubernetes
// collects); systemd journals capture the same lines via stdout redirection.
package logging

import (
	"fmt"
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New builds a zap.Logger configured for production use:
//   - JSON encoder
//   - RFC3339 nano timestamps in field "timestamp"
//   - lowercase level names
//   - level filtered by cfg (debug | info | warn | error)
//
// Per operations_guide.md §7.3, secrets and PII are never logged — that
// responsibility lives at every call site; this constructor sets no default
// fields that could leak.
func New(level string) (*zap.Logger, error) {
	lvl, err := parseLevel(level)
	if err != nil {
		return nil, err
	}

	encCfg := zapcore.EncoderConfig{
		TimeKey:        "timestamp",
		LevelKey:       "level",
		MessageKey:     "message",
		CallerKey:      "caller",
		StacktraceKey:  "stacktrace",
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.RFC3339NanoTimeEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.ShortCallerEncoder,
	}

	core := zapcore.NewCore(
		zapcore.NewJSONEncoder(encCfg),
		zapcore.Lock(os.Stdout),
		lvl,
	)

	return zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel)), nil
}

// WithComponent attaches a `component` field to every entry from the returned
// logger. Per operations_guide.md §7.1 every log line should carry the
// component (e.g., "discovery", "managed", "bff").
func WithComponent(l *zap.Logger, component string) *zap.Logger {
	return l.With(zap.String("component", component))
}

func parseLevel(s string) (zapcore.Level, error) {
	switch s {
	case "debug":
		return zapcore.DebugLevel, nil
	case "info", "":
		return zapcore.InfoLevel, nil
	case "warn", "warning":
		return zapcore.WarnLevel, nil
	case "error":
		return zapcore.ErrorLevel, nil
	default:
		return 0, fmt.Errorf("unknown log level %q", s)
	}
}
