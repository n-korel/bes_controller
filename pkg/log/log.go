package log

import (
	"log/slog"
	"os"
	"strings"
)

func levelFromEnv() slog.Level {
	raw := strings.TrimSpace(os.Getenv("LOG_LEVEL"))
	if raw == "" {
		return slog.LevelInfo
	}
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(raw)); err != nil {
		return slog.LevelInfo
	}
	return lvl
}

func Init(format string) {
	opts := &slog.HandlerOptions{Level: levelFromEnv()}
	if strings.EqualFold(format, "json") {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, opts)))
		return
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, opts)))
}

func With(args ...any) *slog.Logger {
	return slog.Default().With(args...)
}
