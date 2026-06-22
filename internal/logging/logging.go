// Package logging configures the process-wide structured (JSON) logger.
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

// Setup installs a JSON slog logger as the default and returns it. The level is
// controlled by the LOG_LEVEL env var (debug|info|warn|error), defaulting to info.
func Setup() *slog.Logger {
	var level slog.Level
	if err := level.UnmarshalText([]byte(strings.ToLower(os.Getenv("LOG_LEVEL")))); err != nil {
		level = slog.LevelInfo
	}

	handler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})
	logger := slog.New(&traceHandler{Handler: handler})
	slog.SetDefault(logger)
	return logger
}

// traceHandler decorates every record with the active trace and span IDs so logs
// can be correlated with traces.
type traceHandler struct {
	slog.Handler
}

func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}

func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithAttrs(attrs)}
}

func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithGroup(name)}
}
