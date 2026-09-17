package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/ju4n97/hclapi/internal/config"
	"github.com/ju4n97/hclapi/internal/problem"
)

const (
	instrumentationName = "github.com/ju4n97/hclapi"
)

// Telemetry provides vendor-agnostic logging, tracing, and metric instrumentation.
type Telemetry struct {
	logger *slog.Logger
	tracer trace.Tracer
}

// New initializes an agnostic Telemetry instance from manifest configuration.
func New(cfg config.Telemetry) *Telemetry {
	logger := buildLogger(cfg.Logging)
	tracer := otel.GetTracerProvider().Tracer(instrumentationName)

	// Set W3C TraceContext propagator as global default
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return &Telemetry{
		logger: logger,
		tracer: tracer,
	}
}

// Logger returns the configured structured logger.
func (t *Telemetry) Logger() *slog.Logger {
	return t.logger
}

// Tracer returns the OpenTelemetry tracer instance.
func (t *Telemetry) Tracer() trace.Tracer {
	return t.tracer
}

// StartStepSpan starts an OpenTelemetry span for a pipeline step and returns an end function.
func (t *Telemetry) StartStepSpan(ctx context.Context, stepType, stepName string) (context.Context, func(err error)) {
	ctx, span := t.tracer.Start(ctx, fmt.Sprintf("step.%s:%s", stepType, stepName),
		trace.WithAttributes(
			attribute.String("step.type", stepType),
			attribute.String("step.name", stepName),
		),
	)

	return ctx, func(err error) {
		if err != nil {
			span.RecordError(err)
		}
		span.End()
	}
}

// Middleware wraps an http.Handler with panic recovery, trace correlation, and access logging.
func (t *Telemetry) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rc := &responseCapture{ResponseWriter: w}

		defer func() {
			if rec := recover(); rec != nil {
				t.logger.Log(r.Context(), slog.LevelError, "panic_recovered",
					slog.Any("panic", rec),
					slog.String("stack", string(debug.Stack())),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
				)

				p := problem.New(http.StatusInternalServerError, "An unexpected server panic occurred")
				problem.Write(w, p)
			}
		}()

		next.ServeHTTP(rc, r)

		duration := time.Since(start)
		statusCode := rc.statusCode
		if statusCode == 0 {
			statusCode = http.StatusOK
		}

		level := slog.LevelInfo
		switch {
		case statusCode >= 500:
			level = slog.LevelError
		case statusCode >= 400:
			level = slog.LevelWarn
		}

		t.logger.Log(r.Context(), level, "http_request",
			slog.String("method", r.Method),
			slog.String("path", r.URL.Path),
			slog.Int("status", statusCode),
			slog.Duration("duration", duration),
			slog.Int64("bytes", rc.bytesWritten),
			slog.String("remote_addr", r.RemoteAddr),
		)
	})
}

// responseCapture captures status code and response byte size.
type responseCapture struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

// WriteHeader records status code.
func (rc *responseCapture) WriteHeader(code int) {
	if rc.statusCode == 0 {
		rc.statusCode = code
	}
	rc.ResponseWriter.WriteHeader(code)
}

// Write records byte length and defaults status to 200 OK.
func (rc *responseCapture) Write(b []byte) (int, error) {
	if rc.statusCode == 0 {
		rc.statusCode = http.StatusOK
	}
	n, err := rc.ResponseWriter.Write(b)
	rc.bytesWritten += int64(n)
	return n, err
}

// buildLogger creates a slog.Logger with automatic attribute redaction and trace correlation.
func buildLogger(cfg config.Logging) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(cfg.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}

	redactKeys := make(map[string]struct{}, len(cfg.Redact))
	for _, k := range cfg.Redact {
		clean := strings.ToLower(strings.TrimSpace(k))
		clean = strings.TrimPrefix(clean, "request.body.")
		clean = strings.TrimPrefix(clean, "request.headers.")
		clean = strings.TrimPrefix(clean, "headers.")
		clean = strings.TrimPrefix(clean, "body.")
		if clean != "" {
			redactKeys[clean] = struct{}{}
		}
	}

	// Redact sensitive attributes at log emission time
	replaceAttr := func(groups []string, a slog.Attr) slog.Attr {
		key := strings.ToLower(a.Key)
		if _, shouldRedact := redactKeys[key]; shouldRedact {
			return slog.String(a.Key, "[REDACTED]")
		}
		return a
	}

	opts := &slog.HandlerOptions{
		Level:       level,
		ReplaceAttr: replaceAttr,
	}

	var baseHandler slog.Handler = slog.NewTextHandler(os.Stdout, opts)
	if strings.EqualFold(cfg.Format, "json") {
		baseHandler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(&traceCorrelatingHandler{Handler: baseHandler})
}

// traceCorrelatingHandler injects active OTel trace_id and span_id into log records.
type traceCorrelatingHandler struct {
	slog.Handler
}

// Handle appends trace_id and span_id attributes if a valid span is in context.
func (h *traceCorrelatingHandler) Handle(ctx context.Context, r slog.Record) error {
	if span := trace.SpanFromContext(ctx); span.SpanContext().IsValid() {
		r.AddAttrs(
			slog.String("trace_id", span.SpanContext().TraceID().String()),
			slog.String("span_id", span.SpanContext().SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}
