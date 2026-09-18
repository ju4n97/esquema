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
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/ju4n97/hclapi/internal/manifest"
	"github.com/ju4n97/hclapi/internal/problem"
)

const instrumentationName = "github.com/ju4n97/hclapi"

// Telemetry coordinates structured logging, OpenTelemetry distributed tracing,
// attribute redaction, and HTTP middleware instrumentation.
type Telemetry struct {
	logger *slog.Logger
	tracer trace.Tracer
}

// New initializes an agnostic Telemetry instance from the manifest configuration.
// It configures a structured logger with trace correlation, binds an OpenTelemetry tracer,
// and installs W3C TraceContext and Baggage as global text map propagators.
func New(cfg manifest.Telemetry) *Telemetry {
	logger := BuildLogger(cfg)
	tracer := otel.GetTracerProvider().Tracer(instrumentationName)

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
	if t == nil || t.logger == nil {
		return slog.Default()
	}
	return t.logger
}

// Tracer returns the OpenTelemetry tracer instance.
func (t *Telemetry) Tracer() trace.Tracer {
	if t == nil || t.tracer == nil {
		return otel.GetTracerProvider().Tracer(instrumentationName)
	}
	return t.tracer
}

// StartStepSpan starts an OpenTelemetry span for an executing pipeline step and returns
// an updated [context.Context] along with an end closure to record completion or error status.
func (t *Telemetry) StartStepSpan(ctx context.Context, stepType, stepName string) (context.Context, func(err error)) {
	if t == nil || t.tracer == nil {
		return ctx, func(error) {}
	}

	spanName := fmt.Sprintf("step.%s:%s", stepType, stepName)
	ctx, span := t.tracer.Start(ctx, spanName,
		trace.WithAttributes(
			attribute.String("step.type", stepType),
			attribute.String("step.name", stepName),
		),
	)

	return ctx, func(err error) {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else {
			span.SetStatus(codes.Ok, "OK")
		}
		span.End()
	}
}

// Middleware wraps an [http.Handler] with request-level panic recovery, distributed trace
// propagation, and structured access logging.
func (t *Telemetry) Middleware(next http.Handler) http.Handler {
	if t == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rc := &responseCapture{ResponseWriter: w}

		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		spanName := fmt.Sprintf("HTTP %s %s", r.Method, r.URL.Path)
		ctx, span := t.tracer.Start(ctx, spanName,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.method", r.Method),
				attribute.String("http.target", r.URL.Path),
				attribute.String("http.client_ip", r.RemoteAddr),
			),
		)
		defer span.End()

		r = r.WithContext(ctx)

		defer func() {
			if rec := recover(); rec != nil {
				stack := string(debug.Stack())
				t.logger.Log(r.Context(), slog.LevelError, "panic_recovered",
					slog.Any("panic", rec),
					slog.String("stack", stack),
					slog.String("method", r.Method),
					slog.String("path", r.URL.Path),
				)

				span.RecordError(fmt.Errorf("panic: %v", rec))
				span.SetStatus(codes.Error, "panic recovered")

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

		span.SetAttributes(attribute.Int("http.status_code", statusCode))
		if statusCode >= 500 {
			span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", statusCode))
		} else {
			span.SetStatus(codes.Ok, "OK")
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

// responseCapture captures the HTTP response status code and total bytes written
// while preserving access to underlying transport interfaces like [http.Flusher].
type responseCapture struct {
	http.ResponseWriter
	statusCode   int
	bytesWritten int64
}

// Unwrap returns the underlying [http.ResponseWriter], enabling standard library
// tools such as [http.ResponseController] to access advanced transport capabilities.
func (rc *responseCapture) Unwrap() http.ResponseWriter {
	return rc.ResponseWriter
}

// Flush flushes buffered data to the client if the underlying [http.ResponseWriter] implements [http.Flusher].
func (rc *responseCapture) Flush() {
	if f, ok := rc.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// WriteHeader records the status code if not already written and forwards to the underlying writer.
func (rc *responseCapture) WriteHeader(code int) {
	if rc.statusCode == 0 {
		rc.statusCode = code
	}
	rc.ResponseWriter.WriteHeader(code)
}

// Write records the byte count written and defaults the status code to 200 OK if WriteHeader was not called.
func (rc *responseCapture) Write(b []byte) (int, error) {
	if rc.statusCode == 0 {
		rc.statusCode = http.StatusOK
	}
	n, err := rc.ResponseWriter.Write(b)
	rc.bytesWritten += int64(n)
	return n, err
}

// BuildLogger creates a [slog.Logger] configured with log level thresholds, output format,
// sensitive attribute redaction, and OpenTelemetry trace correlation.
func BuildLogger(cfg manifest.Telemetry) *slog.Logger {
	level := slog.LevelInfo
	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		level = slog.LevelDebug
	case "warn", "warning":
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

	replaceAttr := func(groups []string, a slog.Attr) slog.Attr {
		key := strings.ToLower(a.Key)
		if _, shouldRedact := redactKeys[key]; shouldRedact {
			return slog.String(a.Key, "[REDACTED]")
		}
		return a
	}

	opts := &slog.HandlerOptions{
		Level: level,
	}
	if len(redactKeys) > 0 {
		opts.ReplaceAttr = replaceAttr
	}

	var baseHandler slog.Handler = slog.NewTextHandler(os.Stdout, opts)
	if strings.EqualFold(cfg.LogFormat, "json") {
		baseHandler = slog.NewJSONHandler(os.Stdout, opts)
	}

	logger := slog.New(&traceCorrelatingHandler{Handler: baseHandler})
	if cfg.ServiceName != "" {
		logger = logger.With(slog.String("service.name", cfg.ServiceName))
	}

	return logger
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

// WithAttrs returns a new handler with the given attributes, preserving trace correlation.
func (h *traceCorrelatingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceCorrelatingHandler{Handler: h.Handler.WithAttrs(attrs)}
}

// WithGroup returns a new handler with the given group name, preserving trace correlation.
func (h *traceCorrelatingHandler) WithGroup(name string) slog.Handler {
	return &traceCorrelatingHandler{Handler: h.Handler.WithGroup(name)}
}
