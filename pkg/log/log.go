package log

import (
	"log/slog"
	"os"
	"strings"
)

func Init(format string) {
	opts := &slog.HandlerOptions{Level: slog.LevelDebug}
	if strings.EqualFold(format, "json") {
		slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, opts)))
		return
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, opts)))
}

func With(args ...any) *slog.Logger {
	return slog.Default().With(args...)
}

