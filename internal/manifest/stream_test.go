package manifest

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/valkey-io/valkey-go"
)

type testStreamContext struct {
	scope    Scope
	recorder *httptest.ResponseRecorder
	schemas  map[string]Schema
}

func (c *testStreamContext) SQL(name string) (*sql.DB, error) {
	return nil, nil
}

func (c *testStreamContext) Valkey(name string) (valkey.Client, error) {
	return nil, errors.New("valkey unconfigured")
}

func (c *testStreamContext) GoHandler(name string) (StepHandler, error) {
	return nil, errors.New("go unconfigured")
}

func (c *testStreamContext) HTTPClient() *http.Client {
	return http.DefaultClient
}

func (c *testStreamContext) ResponseWriter() http.ResponseWriter {
	return c.recorder
}

func (c *testStreamContext) ResponseController() *http.ResponseController {
	return http.NewResponseController(c.recorder)
}

func (c *testStreamContext) OpenAPISpec(format string) ([]byte, string, error) {
	return nil, "", nil
}

func (c *testStreamContext) Scope() Scope {
	return c.scope
}

func (c *testStreamContext) Schemas() map[string]Schema {
	return c.schemas
}

// makeMockRecordSeq creates a standard iter.Seq2 yielding items followed by completion.
func makeMockRecordSeq(items []any) RecordSeq {
	return func(yield func(any, error) bool) {
		for _, it := range items {
			if !yield(it, nil) {
				return
			}
		}
	}
}

// TestStepStream_ValidateStep verifies compile-time checks on decoupled stream targets.
func TestStepStream_ValidateStep(t *testing.T) {
	t.Parallel()

	manifest := &Manifest{
		Schemas: map[string]Schema{
			"Event": {Name: "Event"},
		},
	}

	tests := []struct {
		name      string
		step      *StepStream
		wantError bool
		errSubstr string
	}{
		{
			name: "unsupported format",
			step: &StepStream{
				Format: "websocket",
				Source: NewExpr(parseExpr(t, `null`), nil),
			},
			wantError: true,
			errSubstr: "unsupported stream format \"websocket\"",
		},
		{
			name: "missing source expression",
			step: &StepStream{
				Format: "ndjson",
			},
			wantError: true,
			errSubstr: "requires a 'source' expression",
		},
		{
			name: "valid sse stream definition",
			step: &StepStream{
				Format:    "sse",
				Source:    NewExpr(parseExpr(t, `steps.events.stream`), nil),
				Heartbeat: Duration(15 * time.Second),
			},
			wantError: false,
		},
		{
			name: "valid csv stream definition",
			step: &StepStream{
				Format: "csv",
				Source: NewExpr(parseExpr(t, `steps.db.stream`), nil),
			},
			wantError: false,
		},
		{
			name: "schema references unknown model",
			step: &StepStream{
				Format: "ndjson",
				Source: NewExpr(parseExpr(t, `steps.db.stream`), nil),
				Schema: &TypeSpec{Type: TypeObject, SchemaRef: "MissingModel"},
			},
			wantError: true,
			errSubstr: "references unknown schema \"MissingModel\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.step.ValidateStep(manifest)
			if (err != nil) != tt.wantError {
				t.Fatalf("ValidateStep() error = %v, wantError = %v", err, tt.wantError)
			}

			if tt.wantError && tt.errSubstr != "" {
				if !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("error %q does not contain substring %q", err.Error(), tt.errSubstr)
				}
			}
		})
	}
}

// TestStepStream_ExecuteStep_NDJSON verifies consuming Go iterators into application/x-ndjson output.
func TestStepStream_ExecuteStep_NDJSON(t *testing.T) {
	t.Parallel()

	records := []any{
		map[string]any{"id": 1, "status": "active"},
		map[string]any{"id": 2, "status": "pending"},
	}

	step := &StepStream{
		Format: "ndjson",
		Source: NewExpr(parseExpr(t, `ctx.request.iterator`), nil),
	}

	rec := httptest.NewRecorder()
	exec := &testStreamContext{
		recorder: rec,
		scope: Scope{
			Request: map[string]any{
				"iterator": makeMockRecordSeq(records),
			},
		},
	}

	_, err := step.ExecuteStep(context.Background(), exec)
	if !errors.Is(err, ErrPipelineHalted) {
		t.Fatalf("expected ErrPipelineHalted, got: %v", err)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "application/x-ndjson" {
		t.Errorf("Content-Type = %q, want 'application/x-ndjson'", ct)
	}

	lines := strings.Split(strings.TrimSpace(rec.Body.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 NDJSON lines, got %d. Body:\n%s", len(lines), rec.Body.String())
	}

	if !strings.Contains(lines[0], `"id":1`) || !strings.Contains(lines[1], `"status":"pending"`) {
		t.Errorf("unexpected NDJSON output: %s", rec.Body.String())
	}
}

// TestStepStream_ExecuteStep_CSV verifies consuming Go iterators into tabular CSV formatting.
func TestStepStream_ExecuteStep_CSV(t *testing.T) {
	t.Parallel()

	records := []any{
		map[string]any{"sku": "SKU-A", "price": 10},
		map[string]any{"sku": "SKU-B", "price": 20},
	}

	step := &StepStream{
		Format: "csv",
		Source: NewExpr(parseExpr(t, `ctx.request.iterator`), nil),
	}

	rec := httptest.NewRecorder()
	exec := &testStreamContext{
		recorder: rec,
		scope: Scope{
			Request: map[string]any{
				"iterator": makeMockRecordSeq(records),
			},
		},
	}

	_, err := step.ExecuteStep(context.Background(), exec)
	if !errors.Is(err, ErrPipelineHalted) {
		t.Fatalf("expected ErrPipelineHalted, got: %v", err)
	}

	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("Content-Type = %q, want 'text/csv'", ct)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "SKU-A") || !strings.Contains(body, "SKU-B") {
		t.Fatalf("unexpected CSV body:\n%s", body)
	}
}

// TestStepStream_ExecuteStep_Raw verifies raw byte piping from io.Reader.
func TestStepStream_ExecuteStep_Raw(t *testing.T) {
	t.Parallel()

	chunkedData := "token-1|token-2|token-3"
	step := &StepStream{
		Format:      "raw",
		ContentType: "text/plain",
		Source:      NewExpr(parseExpr(t, `ctx.request.reader`), nil),
	}

	rec := httptest.NewRecorder()
	exec := &testStreamContext{
		recorder: rec,
		scope: Scope{
			Request: map[string]any{
				"reader": io.NopCloser(bytes.NewBufferString(chunkedData)),
			},
		},
	}

	_, err := step.ExecuteStep(context.Background(), exec)
	if !errors.Is(err, ErrPipelineHalted) {
		t.Fatalf("expected ErrPipelineHalted, got: %v", err)
	}

	if rec.Body.String() != chunkedData {
		t.Errorf("body = %q, want %q", rec.Body.String(), chunkedData)
	}
}
