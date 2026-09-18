package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/ju4n97/hclapi/internal/manifest"
	"github.com/ju4n97/hclapi/internal/problem"
	"github.com/ju4n97/hclapi/internal/telemetry"
)

// parseTestExpr compiles an HCL expression string for test fixtures.
func parseTestExpr(t *testing.T, src string) manifest.Expr {
	t.Helper()
	expr, diags := hclsyntax.ParseExpression([]byte(src), "test.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("failed to parse expr %q: %s", src, diags.Error())
	}
	return manifest.NewExpr(expr, nil)
}

// testStepMock provides a mock step implementation for testing pipeline sequencing.
type testStepMock struct {
	name      string
	when      manifest.Expr
	terminal  bool
	executeFn func(ctx context.Context, ec manifest.StepExecutionContext) (any, error)
}

func (m *testStepMock) StepName() string                               { return m.name }
func (m *testStepMock) StepWhen() manifest.Expr                        { return m.when }
func (m *testStepMock) IsTerminal() bool                               { return m.terminal }
func (m *testStepMock) ValidateStep(manifest *manifest.Manifest) error { return nil }
func (m *testStepMock) ExecuteStep(ctx context.Context, ec manifest.StepExecutionContext) (any, error) {
	if m.executeFn != nil {
		return m.executeFn(ctx, ec)
	}
	return nil, nil
}

// TestBuildRouteHandler_ExecutionPipeline verifies step execution, conditional skipping, and terminal responses.
func TestBuildRouteHandler_ExecutionPipeline(t *testing.T) {
	t.Parallel()

	t.Run("executes steps in sequence and terminates cleanly", func(t *testing.T) {
		t.Parallel()

		var executed []string

		step1 := &testStepMock{
			name: "compute",
			executeFn: func(ctx context.Context, ec manifest.StepExecutionContext) (any, error) {
				executed = append(executed, "compute")
				return map[string]any{"val": 42}, nil
			},
		}

		step2 := &testStepMock{
			name:     "respond",
			terminal: true,
			executeFn: func(ctx context.Context, ec manifest.StepExecutionContext) (any, error) {
				executed = append(executed, "respond")
				w := ec.ResponseWriter()
				w.WriteHeader(http.StatusCreated)
				_, _ = w.Write([]byte(`{"status":"created"}`))
				return nil, manifest.ErrPipelineHalted
			},
		}

		route := manifest.Route{
			Method: "POST",
			Path:   "/items",
			Steps:  []manifest.Step{step1, step2},
		}

		handler := buildRouteHandler(route, Dependencies{}, 1024)
		req := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader(`{}`))
		rec := httptest.NewRecorder()

		handler(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("HTTP Status = %d, want 201", rec.Code)
		}

		expectedSeq := []string{"compute", "respond"}
		if len(executed) != 2 || executed[0] != expectedSeq[0] || executed[1] != expectedSeq[1] {
			t.Fatalf("executed sequence = %v, want %v", executed, expectedSeq)
		}
	})

	t.Run("records openTelemetry step spans during execution", func(t *testing.T) {
		t.Parallel()

		exporter := tracetest.NewInMemoryExporter()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
		t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })
		otel.SetTracerProvider(tp)

		tel := telemetry.New(manifest.Telemetry{})

		step1 := &testStepMock{
			name: "data_step",
			executeFn: func(ctx context.Context, ec manifest.StepExecutionContext) (any, error) {
				return "ok", nil
			},
		}

		step2 := &testStepMock{
			name:     "final_respond",
			terminal: true,
			executeFn: func(ctx context.Context, ec manifest.StepExecutionContext) (any, error) {
				w := ec.ResponseWriter()
				w.WriteHeader(http.StatusOK)
				return nil, manifest.ErrPipelineHalted
			},
		}

		route := manifest.Route{
			Method: "GET",
			Path:   "/spans",
			Steps:  []manifest.Step{step1, step2},
		}

		deps := Dependencies{Telemetry: tel}
		handler := buildRouteHandler(route, deps, 1024)
		req := httptest.NewRequest(http.MethodGet, "/spans", http.NoBody)
		rec := httptest.NewRecorder()

		handler(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Status = %d, want 200", rec.Code)
		}

		spans := exporter.GetSpans()
		if len(spans) != 2 {
			t.Fatalf("expected 2 step spans, got %d", len(spans))
		}

		if spans[0].Name != "step.teststepmock:data_step" {
			t.Errorf("span 0 = %q, want 'step.teststepmock:data_step'", spans[0].Name)
		}
		if spans[1].Name != "step.teststepmock:final_respond" {
			t.Errorf("span 1 = %q, want 'step.teststepmock:final_respond'", spans[1].Name)
		}
	})

	t.Run("skips step when conditional when evaluates to false", func(t *testing.T) {
		t.Parallel()

		skipped := true
		step1 := &testStepMock{
			name: "conditional",
			when: parseTestExpr(t, `ctx.request.query.active == "true"`),
			executeFn: func(ctx context.Context, ec manifest.StepExecutionContext) (any, error) {
				skipped = false
				return nil, nil
			},
		}

		step2 := &testStepMock{
			name:     "respond",
			terminal: true,
			executeFn: func(ctx context.Context, ec manifest.StepExecutionContext) (any, error) {
				w := ec.ResponseWriter()
				w.WriteHeader(http.StatusOK)
				return nil, manifest.ErrPipelineHalted
			},
		}

		route := manifest.Route{
			Method: "GET",
			Path:   "/check",
			Steps:  []manifest.Step{step1, step2},
		}

		handler := buildRouteHandler(route, Dependencies{}, 1024)
		req := httptest.NewRequest(http.MethodGet, "/check?active=false", http.NoBody)
		rec := httptest.NewRecorder()

		handler(rec, req)

		if !skipped {
			t.Fatal("expected step1 to be skipped when condition is false")
		}
		if rec.Code != http.StatusOK {
			t.Errorf("Status = %d, want 200", rec.Code)
		}
	})

	t.Run("pipeline fallthrough without terminal response returns 500 problem details", func(t *testing.T) {
		t.Parallel()

		step1 := &testStepMock{
			name: "data_only",
			executeFn: func(ctx context.Context, ec manifest.StepExecutionContext) (any, error) {
				return "data", nil
			},
		}

		route := manifest.Route{
			Method: "GET",
			Path:   "/unfinished",
			Steps:  []manifest.Step{step1},
		}

		handler := buildRouteHandler(route, Dependencies{}, 1024)
		req := httptest.NewRequest(http.MethodGet, "/unfinished", http.NoBody)
		rec := httptest.NewRecorder()

		handler(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("Status = %d, want 500 on unhandled fallthrough", rec.Code)
		}

		ct := rec.Header().Get("Content-Type")
		if ct != problem.ContentType {
			t.Errorf("Content-Type = %q, want %q", ct, problem.ContentType)
		}
	})
}
