package ui

import (
	"context"
	"log/slog"
)

// LogHandler is a slog.Handler that forwards records to a Sink as LogLine
// events, so pipeline warnings surface in the TUI journal instead of
// corrupting the alternate screen.
type LogHandler struct {
	Sink Sink
	Min  slog.Level
}

// Enabled implements slog.Handler.
func (h LogHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= h.Min
}

// Handle implements slog.Handler.
func (h LogHandler) Handle(_ context.Context, r slog.Record) error {
	msg := r.Message
	r.Attrs(func(a slog.Attr) bool {
		msg += " " + a.Key + "=" + a.Value.String()
		return true
	})
	h.Sink.Emit(LogLine{Level: r.Level, Message: msg})
	return nil
}

// WithAttrs implements slog.Handler.
func (h LogHandler) WithAttrs([]slog.Attr) slog.Handler { return h }

// WithGroup implements slog.Handler.
func (h LogHandler) WithGroup(string) slog.Handler { return h }
