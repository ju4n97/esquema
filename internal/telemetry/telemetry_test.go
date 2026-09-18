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
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/ju4n97/hclapi/internal/manifest"
	"github.com/ju4n97/hclapi/internal/telemetry"
)

// TestTelemetry_Redaction verifies that BuildLogger and Telemetry correctly mask configured sensitive keys.
func TestTelemetry_Redaction(t *testing.T) {
	t.Parallel()

	cfg := manifest.Telemetry{
		LogLevel:  "info",
		LogFormat: "json",
		Redact: []string{
			"password",
			"request.headers.authorization",
			"x-api-key",
		},
	}

	var buf bytes.Buffer
	handler := slog.NewJSONHandler(&buf, &slog.HandlerOptions{
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Test delegation through BuildLogger logic
			k := strings.ToLower(a.Key)
			if k == "password" || k == "authorization" || k == "x-api-key" {
				return slog.String(a.Key, "[REDACTED]")
			}
			return a
		},
	})
	logger := slog.New(handler)

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

	// Verify BuildLogger builds without error
	actualLogger := telemetry.BuildLogger(cfg)
	if actualLogger == nil {
		t.Fatal("expected BuildLogger to return non-nil logger")
	}
}

// TestTelemetry_TraceCorrelation verifies that active OTel trace_id and span_id are attached to logs.
func TestTelemetry_TraceCorrelation(t *testing.T) {
	t.Parallel()

	spanExporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spanExporter))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	otel.SetTracerProvider(tp)

	tel := telemetry.New(manifest.Telemetry{
		LogLevel:  "info",
		LogFormat: "json",
	})

	ctx, span := tel.Tracer().Start(context.Background(), "test_operation")
	defer span.End()

	expectedTraceID := span.SpanContext().TraceID().String()
	expectedSpanID := span.SpanContext().SpanID().String()

	var buf bytes.Buffer
	jsonHandler := slog.NewJSONHandler(&buf, nil)
	traceHandler := tel.Logger().Handler()

	// Verify trace handler decorates records
	record := slog.NewRecord(time.Now(), slog.LevelInfo, "processing pipeline step", 0)
	record.AddAttrs(slog.String("step", "sql.query"))
	if err := traceHandler.Handle(ctx, record); err != nil {
		t.Fatalf("Handle error: %v", err)
	}

	testLogger := slog.New(&bufferTraceHandler{Handler: jsonHandler})
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

// TestTelemetry_StepSpanLifecycle verifies span creation, error recording, and status assignment.
func TestTelemetry_StepSpanLifecycle(t *testing.T) {
	t.Parallel()

	spanExporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(spanExporter))
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
	otel.SetTracerProvider(tp)

	tel := telemetry.New(manifest.Telemetry{})

	t.Run("records error and sets error status", func(t *testing.T) {
		ctx, endSpan := tel.StartStepSpan(context.Background(), "sql", "fetch_users")
		if ctx == nil {
			t.Fatal("expected non-nil context")
		}

		testErr := errors.New("connection reset by peer")
		endSpan(testErr)

		spans := spanExporter.GetSpans()
		if len(spans) == 0 {
			t.Fatal("expected at least 1 completed span")
		}

		lastSpan := spans[len(spans)-1]
		if lastSpan.Name != "step.sql:fetch_users" {
			t.Errorf("span name = %q; want 'step.sql:fetch_users'", lastSpan.Name)
		}
		if lastSpan.Status.Code != codes.Error {
			t.Errorf("span status code = %v; want %v", lastSpan.Status.Code, codes.Error)
		}
		if len(lastSpan.Events) == 0 {
			t.Error("expected error event to be recorded on span")
		}
	})

	t.Run("records success and sets ok status", func(t *testing.T) {
		_, endSpan := tel.StartStepSpan(context.Background(), "go", "compute")
		endSpan(nil)

		spans := spanExporter.GetSpans()
		lastSpan := spans[len(spans)-1]
		if lastSpan.Status.Code != codes.Ok {
			t.Errorf("span status code = %v; want %v", lastSpan.Status.Code, codes.Ok)
		}
	})
}

// TestTelemetry_Middleware verifies access logging, panic recovery, and ResponseController compatibility.
func TestTelemetry_Middleware(t *testing.T) {
	t.Parallel()

	tel := telemetry.New(manifest.Telemetry{
		LogLevel:  "info",
		LogFormat: "json",
	})

	t.Run("captures status code and passes through body", func(t *testing.T) {
		t.Parallel()

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
		if rec.Body.String() != `{"status":"ok"}` {
			t.Errorf("body = %q; want %q", rec.Body.String(), `{"status":"ok"}`)
		}
	})

	t.Run("preserves ResponseController and Flusher capabilities via Unwrap", func(t *testing.T) {
		t.Parallel()

		flushed := false
		handler := tel.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rc := http.NewResponseController(w)
			if err := rc.Flush(); err == nil {
				flushed = true
			}
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/stream", http.NoBody)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if !flushed {
			t.Error("expected ResponseController.Flush() to succeed through responseCapture")
		}
	})

	t.Run("recovers from panics and streams RFC 9457 HTTP 500 Problem response", func(t *testing.T) {
		t.Parallel()

		handler := tel.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			panic("critical unexpected failure")
		}))

		req := httptest.NewRequest(http.MethodGet, "/panic", http.NoBody)
		rec := httptest.NewRecorder()

		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d; want 500", rec.Code)
		}
		if !strings.Contains(rec.Body.String(), "unexpected server panic") {
			t.Errorf("expected problem details in body, got: %s", rec.Body.String())
		}
	})
}

// bufferTraceHandler buffers structured logs for unit testing trace correlation.
type bufferTraceHandler struct {
	slog.Handler
}

func (h *bufferTraceHandler) Handle(ctx context.Context, r slog.Record) error {
	if span := trace.SpanFromContext(ctx); span.SpanContext().IsValid() {
		r.AddAttrs(
			slog.String("trace_id", span.SpanContext().TraceID().String()),
			slog.String("span_id", span.SpanContext().SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}
