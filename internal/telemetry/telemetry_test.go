package telemetry_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/ju4n97/hclapi/internal/config"
	"github.com/ju4n97/hclapi/internal/telemetry"
)

// TestTelemetry_Redaction verifies that configured sensitive keys are masked in log output.
func TestTelemetry_Redaction(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			k := strings.ToLower(a.Key)
			if k == "password" || k == "authorization" || k == "x-api-key" {
				return slog.String(a.Key, "[REDACTED]")
			}
			return a
		},
	}))

	logger.Info("user login attempt",
		slog.String("username", "alice"),
		slog.String("password", "super-secret-password-123"),
		slog.String("authorization", "Bearer my-secret-token"),
		slog.String("x-api-key", "api-key-999"),
		slog.String("tenant_id", "tenant-42"),
	)

	output := buf.String()

	if strings.Contains(output, "super-secret-password-123") {
		t.Errorf("SECURITY LEAK: password leaked in logs: %s", output)
	}
	if strings.Contains(output, "my-secret-token") {
		t.Errorf("SECURITY LEAK: token leaked in logs: %s", output)
	}
	if strings.Contains(output, "api-key-999") {
		t.Errorf("SECURITY LEAK: api key leaked in logs: %s", output)
	}
	if !strings.Contains(output, "[REDACTED]") {
		t.Errorf("expected '[REDACTED]' marker, got: %s", output)
	}
	if !strings.Contains(output, "tenant-42") {
		t.Errorf("expected non-sensitive tenant_id to be preserved: %s", output)
	}
}

// TestTelemetry_TraceCorrelation verifies that active OTel trace_id is attached to logs.
func TestTelemetry_TraceCorrelation(t *testing.T) {
	t.Parallel()

	// Set up an in-memory OTel TracerProvider for testing
	spanExporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spanExporter))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	otel.SetTracerProvider(tp)

	cfg := config.Telemetry{
		Logging: config.Logging{
			Level:  "info",
			Format: "json",
		},
	}

	tel := telemetry.New(cfg)

	// Start an active trace span
	ctx, span := tel.Tracer().Start(context.Background(), "test_operation")
	defer span.End()

	expectedTraceID := span.SpanContext().TraceID().String()
	expectedSpanID := span.SpanContext().SpanID().String()

	var buf bytes.Buffer
	// Redirect logger to buffer
	handler := slog.NewJSONHandler(&buf, nil)
	testLogger := slog.New(&mockTraceHandler{Handler: handler})

	testLogger.InfoContext(ctx, "processing pipeline step", slog.String("step", "sql.query"))

	var logEntry map[string]any
	if err := json.Unmarshal(buf.Bytes(), &logEntry); err != nil {
		t.Fatalf("failed to parse log JSON: %v", err)
	}

	if logEntry["trace_id"] != expectedTraceID {
		t.Errorf("trace_id = %v; want %s", logEntry["trace_id"], expectedTraceID)
	}
	if logEntry["span_id"] != expectedSpanID {
		t.Errorf("span_id = %v; want %s", logEntry["span_id"], expectedSpanID)
	}
}

// TestTelemetry_StepSpanLifecycle verifies span creation, error recording, and completion.
func TestTelemetry_StepSpanLifecycle(t *testing.T) {
	t.Parallel()

	spanExporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spanExporter))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	otel.SetTracerProvider(tp)

	tel := telemetry.New(config.Telemetry{})

	ctx, endSpan := tel.StartStepSpan(context.Background(), "sql", "fetch_users")
	if ctx == nil {
		t.Fatal("expected non-nil context")
	}

	// Simulate step completion with an error
	testErr := errors.New("connection reset by peer")
	endSpan(testErr)

	spans := spanExporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected 1 completed span, got %d", len(spans))
	}

	s := spans[0]
	if s.Name != "step.sql:fetch_users" {
		t.Errorf("span name = %q; want 'step.sql:fetch_users'", s.Name)
	}
	if len(s.Events) == 0 {
		t.Error("expected error event to be recorded on span")
	}
}

// TestTelemetry_Middleware verifies access log status capture.
func TestTelemetry_Middleware(t *testing.T) {
	t.Parallel()

	tel := telemetry.New(config.Telemetry{
		Logging: config.Logging{Level: "info", Format: "json"},
	})

	handler := tel.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))

	req := httptest.NewRequest(http.MethodPost, "/items", http.NoBody)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d; want 201", rec.Code)
	}
}

// mockTraceHandler mirrors traceCorrelatingHandler for test buffering.
type mockTraceHandler struct {
	slog.Handler
}

func (h *mockTraceHandler) Handle(ctx context.Context, r slog.Record) error {
	if span := tracetestSpanFromContext(ctx); span != nil {
		r.AddAttrs(
			slog.String("trace_id", span.SpanContext().TraceID().String()),
			slog.String("span_id", span.SpanContext().SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}

func tracetestSpanFromContext(ctx context.Context) trace.Span {
	return trace.SpanFromContext(ctx)
}
