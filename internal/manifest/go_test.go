package manifest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/valkey-io/valkey-go"

	"github.com/ju4n97/hclapi/internal/problem"
)

type testGoExecutionContext struct {
	handlers map[string]GoHandler
	scope    Scope
	recorder *httptest.ResponseRecorder
}

func (c *testGoExecutionContext) SQL(name string) (*sql.DB, error) {
	return nil, nil
}

func (c *testGoExecutionContext) Valkey(name string) (valkey.Client, error) {
	return nil, errors.New("valkey unconfigured")
}

func (c *testGoExecutionContext) GoHandler(name string) (GoHandler, error) {
	if c.handlers == nil {
		return nil, fmt.Errorf("unregistered handler %q", name)
	}
	h, ok := c.handlers[name]
	if !ok {
		return nil, fmt.Errorf("unregistered handler %q", name)
	}
	return h, nil
}

func (c *testGoExecutionContext) HTTPClient() *http.Client {
	return http.DefaultClient
}

func (c *testGoExecutionContext) ResponseWriter() http.ResponseWriter {
	return c.recorder
}

func (c *testGoExecutionContext) ResponseController() *http.ResponseController {
	return http.NewResponseController(c.recorder)
}

func (c *testGoExecutionContext) OpenAPISpec(format string) ([]byte, string, error) {
	return nil, "", nil
}

func (c *testGoExecutionContext) Scope() Scope {
	return c.scope
}

func (c *testGoExecutionContext) Schemas() map[string]Schema {
	return nil
}

// TestArgs_GenericMethods verifies typed extraction, slice coercion, fallback defaults, and struct binding.
func TestArgs_GenericMethods(t *testing.T) {
	t.Parallel()

	args := Args{
		"amount":     int64(100),
		"raw_int":    42,
		"rate":       3.14,
		"enabled":    "true",
		"currency":   "USD",
		"tags":       []any{"prod", "web"},
		"ports":      []any{int64(80), 443, "8080"},
		"empty_list": []any{},
	}

	t.Run("Has checks key presence and nil values", func(t *testing.T) {
		t.Parallel()

		if !args.Has("currency") {
			t.Error("expected Has('currency') to be true")
		}
		if args.Has("missing_key") {
			t.Error("expected Has('missing_key') to be false")
		}

		var nilArgs Args
		if nilArgs.Has("any") {
			t.Error("expected nil args.Has to be false")
		}
	})

	t.Run("Get with type coercion", func(t *testing.T) {
		t.Parallel()

		str, ok := args.Get[string]("currency")
		if !ok || str != "USD" {
			t.Fatalf("args.Get[string] = (%v, %v), want ('USD', true)", str, ok)
		}

		n64, ok := args.Get[int64]("raw_int")
		if !ok || n64 != 42 {
			t.Fatalf("args.Get[int64] = (%v, %v), want (42, true)", n64, ok)
		}

		b, ok := args.Get[bool]("enabled")
		if !ok || !b {
			t.Fatalf("args.Get[bool] = (%v, %v), want (true, true)", b, ok)
		}

		f, ok := args.Get[float64]("amount")
		if !ok || f != 100.0 {
			t.Fatalf("args.Get[float64] = (%v, %v), want (100.0, true)", f, ok)
		}

		_, bad := args.Get[int]("currency")
		if bad {
			t.Error("expected incompatible string-to-int conversion to return ok=false")
		}
	})

	t.Run("GetOr returns value or typed fallback", func(t *testing.T) {
		t.Parallel()

		if got := args.GetOr("amount", int64(0)); got != int64(100) {
			t.Errorf("args.GetOr('amount') = %v, want 100", got)
		}

		if got := args.GetOr("missing", "default_val"); got != "default_val" {
			t.Errorf("args.GetOr('missing') = %v, want 'default_val'", got)
		}

		if got := args.GetOr("currency", "EUR"); got != "USD" {
			t.Errorf("args.GetOr('currency') = %v, want 'USD'", got)
		}
	})

	t.Run("Slice coerces dynamic lists", func(t *testing.T) {
		t.Parallel()

		tags := args.Slice[string]("tags")
		expectedTags := []string{"prod", "web"}
		if !reflect.DeepEqual(tags, expectedTags) {
			t.Fatalf("args.Slice[string] = %v, want %v", tags, expectedTags)
		}

		ports := args.Slice[int]("ports")
		expectedPorts := []int{80, 443, 8080}
		if !reflect.DeepEqual(ports, expectedPorts) {
			t.Fatalf("args.Slice[int] = %v, want %v", ports, expectedPorts)
		}

		empty := args.Slice[string]("empty_list")
		if len(empty) != 0 {
			t.Errorf("expected empty slice, got %v", empty)
		}

		nonExistent := args.Slice[string]("missing")
		if nonExistent != nil {
			t.Errorf("expected nil for missing key, got %v", nonExistent)
		}
	})

	t.Run("Bind unmarshals args map into destination struct", func(t *testing.T) {
		t.Parallel()

		type ChargeParams struct {
			Amount   int64    `json:"amount"`
			Currency string   `json:"currency"`
			Tags     []string `json:"tags"`
		}

		var params ChargeParams
		if err := args.Bind(&params); err != nil {
			t.Fatalf("Bind() error: %v", err)
		}

		expected := ChargeParams{
			Amount:   100,
			Currency: "USD",
			Tags:     []string{"prod", "web"},
		}
		if !reflect.DeepEqual(params, expected) {
			t.Fatalf("Bind() = %+v, want %+v", params, expected)
		}
	})
}

// TestStepGo_ValidateStep verifies compile-time checks on callback identifiers.
func TestStepGo_ValidateStep(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		step      *StepGo
		wantError bool
		errSubstr string
	}{
		{
			name: "valid callback step",
			step: &StepGo{
				Name: "tax",
				Use:  "tax.calculate",
			},
			wantError: false,
		},
		{
			name: "missing use identifier",
			step: &StepGo{
				Name: "tax",
				Use:  "   ",
			},
			wantError: true,
			errSubstr: "missing handler identifier in 'use'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.step.ValidateStep(&Manifest{})
			if (err != nil) != tt.wantError {
				t.Fatalf("ValidateStep() error = %v, wantError = %v", err, tt.wantError)
			}

			if tt.wantError && tt.errSubstr != "" {
				if !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("error %q does not contain expected substring %q", err.Error(), tt.errSubstr)
				}
			}
		})
	}
}

// TestStepGo_ExecuteStep verifies execution, missing handlers, error problem streams, and panic recovery.
func TestStepGo_ExecuteStep(t *testing.T) {
	t.Parallel()

	t.Run("executes callback and maps result output using typed generic methods", func(t *testing.T) {
		t.Parallel()

		handler := func(ctx context.Context, req *GoRequest) (any, error) {
			base := req.Args.GetOr("base", int64(1))
			mult := req.Args.GetOr("mult", int64(1))
			return base * mult, nil
		}

		step := &StepGo{
			Name: "multiply",
			Use:  "math.multiply",
			Args: NewExpr(parseExpr(t, `{ base = 10, mult = 5 }`), nil),
		}

		exec := &testGoExecutionContext{
			handlers: map[string]GoHandler{"math.multiply": handler},
			recorder: httptest.NewRecorder(),
		}

		res, err := step.ExecuteStep(context.Background(), exec)
		if err != nil {
			t.Fatalf("ExecuteStep() unexpected error: %v", err)
		}

		resMap, ok := res.(map[string]any)
		if !ok || resMap["result"] != int64(50) {
			t.Fatalf("unexpected result map: %+v", res)
		}
	})

	t.Run("missing handler returns error", func(t *testing.T) {
		t.Parallel()

		step := &StepGo{
			Name: "missing",
			Use:  "unregistered.func",
		}

		exec := &testGoExecutionContext{
			handlers: map[string]GoHandler{},
			recorder: httptest.NewRecorder(),
		}

		_, err := step.ExecuteStep(context.Background(), exec)
		if err == nil || !strings.Contains(err.Error(), "unregistered handler") {
			t.Fatalf("expected unregistered handler error, got: %v", err)
		}
	})

	t.Run("recovers from panic and writes RFC 9457 HTTP 500 Problem Details", func(t *testing.T) {
		t.Parallel()

		panickingHandler := func(ctx context.Context, req *GoRequest) (any, error) {
			var ptr *int
			*ptr = 42
			return nil, nil
		}

		step := &StepGo{
			Name: "fail_step",
			Use:  "crash.panic",
		}

		rec := httptest.NewRecorder()
		exec := &testGoExecutionContext{
			handlers: map[string]GoHandler{"crash.panic": panickingHandler},
			recorder: rec,
		}

		_, err := step.ExecuteStep(context.Background(), exec)
		if !errors.Is(err, ErrPipelineHalted) {
			t.Fatalf("expected ErrPipelineHalted, got: %v", err)
		}

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("HTTP Status = %d, want 500", rec.Code)
		}

		var p problem.Problem
		if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
			t.Fatalf("failed to decode response problem details: %v", err)
		}

		if p.Status != 500 || !strings.Contains(p.Detail, "nil pointer") {
			t.Errorf("unexpected panic problem details: %+v", p)
		}
	})

	t.Run("callback returning Problem details streams response and halts", func(t *testing.T) {
		t.Parallel()

		problemHandler := func(ctx context.Context, req *GoRequest) (any, error) {
			return nil, problem.New(http.StatusPaymentRequired, "Insufficient credits")
		}

		step := &StepGo{
			Name: "bill",
			Use:  "bill.charge",
		}

		rec := httptest.NewRecorder()
		exec := &testGoExecutionContext{
			handlers: map[string]GoHandler{"bill.charge": problemHandler},
			recorder: rec,
		}

		_, err := step.ExecuteStep(context.Background(), exec)
		if !errors.Is(err, ErrPipelineHalted) {
			t.Fatalf("expected ErrPipelineHalted, got: %v", err)
		}

		if rec.Code != http.StatusPaymentRequired {
			t.Fatalf("HTTP Status = %d, want 402", rec.Code)
		}
	})
}

// TestDecodeStepGo verifies direct HCL keyword parsing into a [*StepGo] definition.
func TestDecodeStepGo(t *testing.T) {
	t.Parallel()

	hclBlock := `
		use  = "crypto.sign"
		args = {
			token = "token_value"
		}
	`

	expr, diags := hclsyntax.ParseConfig([]byte(hclBlock), "test.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("hcl parse error: %s", diags.Error())
	}

	step, err := decodeStepGo("sign_payload", expr.Body, nil, runtimeExprFunctions())
	if err != nil {
		t.Fatalf("decodeStepGo() error: %v", err)
	}

	goStep, ok := step.(*StepGo)
	if !ok {
		t.Fatalf("expected *StepGo, got %T", step)
	}

	if goStep.StepName() != "sign_payload" {
		t.Errorf("StepName() = %q, want 'sign_payload'", goStep.StepName())
	}
	if goStep.Use != "crypto.sign" {
		t.Errorf("Use = %q, want 'crypto.sign'", goStep.Use)
	}
	if goStep.IsTerminal() {
		t.Error("expected IsTerminal() to be false")
	}
}
