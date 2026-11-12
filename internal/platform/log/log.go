// Package log provides structured logging via log/slog.
package log

import (
	"log/slog"
	"os"
)

// New returns a JSON slog logger suitable for production; call once at startup.
func New(service string) *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})).With("service", service)
}
