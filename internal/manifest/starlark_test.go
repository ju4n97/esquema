package manifest

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/valkey-io/valkey-go"
)

type testStarlarkContext struct {
	scope Scope
}

func (c *testStarlarkContext) SQL(name string) (*sql.DB, error) {
	return nil, nil
}

func (c *testStarlarkContext) Valkey(name string) (valkey.Client, error) {
	return nil, errors.New("valkey unconfigured")
}

func (c *testStarlarkContext) GoHandler(name string) (GoHandler, error) {
	return nil, errors.New("go unconfigured")
}

func (c *testStarlarkContext) HTTPClient() *http.Client {
	return http.DefaultClient
}

func (c *testStarlarkContext) ResponseWriter() http.ResponseWriter {
	return httptest.NewRecorder()
}

func (c *testStarlarkContext) ResponseController() *http.ResponseController {
	return http.NewResponseController(httptest.NewRecorder())
}

func (c *testStarlarkContext) OpenAPISpec(format string) ([]byte, string, error) {
	return nil, "", nil
}

func (c *testStarlarkContext) Scope() Scope {
	return c.scope
}

func (c *testStarlarkContext) Schemas() map[string]Schema {
	return nil
}

// TestStepStarlark_ValidateStep verifies compile-time syntax checks and entrypoint validation.
func TestStepStarlark_ValidateStep(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		step      *StepStarlark
		wantError bool
		errSubstr string
	}{
		{
			name: "valid script with execute entrypoint",
			step: &StepStarlark{
				Name: "clean",
				Source: `
def execute(ctx):
    return {"status": "ok"}
`,
			},
			wantError: false,
		},
		{
			name: "empty source code returns error",
			step: &StepStarlark{
				Name:   "empty",
				Source: "   ",
			},
			wantError: true,
			errSubstr: "source cannot be empty",
		},
		{
			name: "syntax error missing colon",
			step: &StepStarlark{
				Name:   "broken_syntax",
				Source: "def execute(ctx) return 1",
			},
			wantError: true,
			errSubstr: "syntax error",
		},
		{
			name: "missing execute entrypoint",
			step: &StepStarlark{
				Name: "missing_func",
				Source: `
def process(data):
    return data
`,
			},
			wantError: true,
			errSubstr: "script must define an 'execute(ctx)' function",
		},
		{
			name: "execute identifier is not a callable",
			step: &StepStarlark{
				Name:   "not_callable",
				Source: `execute = "not_a_function"`,
			},
			wantError: true,
			errSubstr: "must be a callable function",
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

// TestStepStarlark_ExecuteStep verifies script execution, context inspection, and budget limits.
func TestStepStarlark_ExecuteStep(t *testing.T) {
	t.Parallel()

	refTime := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	scope := Scope{
		Request: map[string]any{
			"body": map[string]any{
				"email":  "  USER@DOMAIN.COM  ",
				"tags":   []any{"  DEV  ", "prod", " "},
				"prefix": "org",
			},
		},
		Steps: map[string]any{
			"db": map[string]any{
				"role": "admin",
			},
		},
		Now: refTime,
	}

	t.Run("transforms payload and accesses context coordinates", func(t *testing.T) {
		t.Parallel()

		step := &StepStarlark{
			Name: "normalize",
			Source: `
def execute(ctx):
    req_body = ctx["request"]["body"]
    clean_email = req_body["email"].strip().lower()
    prefix = req_body.get("prefix", "app")
    role = ctx["steps"]["db"]["role"]

    tags = [prefix + ":" + t.strip().lower() for t in req_body["tags"] if len(t.strip()) > 0]

    return {
        "email": clean_email,
        "tags": tags,
        "count": len(tags),
        "role": role,
        "timestamp": ctx["timestamp"]
    }
`,
		}

		exec := &testStarlarkContext{scope: scope}
		res, err := step.ExecuteStep(context.Background(), exec)
		if err != nil {
			t.Fatalf("ExecuteStep() unexpected error: %v", err)
		}

		resMap, ok := res.(map[string]any)
		if !ok {
			t.Fatalf("expected map[string]any output, got %T", res)
		}

		result, ok := resMap["result"].(map[string]any)
		if !ok {
			t.Fatalf("expected result key of type map[string]any, got %T", resMap["result"])
		}

		if result["email"] != "user@domain.com" {
			t.Errorf("email = %v, want 'user@domain.com'", result["email"])
		}
		if result["role"] != "admin" {
			t.Errorf("role = %v, want 'admin'", result["role"])
		}
		if result["count"] != int64(2) {
			t.Errorf("count = %v, want 2", result["count"])
		}
		if result["timestamp"] != refTime.Unix() {
			t.Errorf("timestamp = %v, want %d", result["timestamp"], refTime.Unix())
		}

		expectedTags := []any{"org:dev", "org:prod"}
		if !reflect.DeepEqual(result["tags"], expectedTags) {
			t.Errorf("tags = %v, want %v", result["tags"], expectedTags)
		}
	})

	t.Run("halts execution when instruction limit is exceeded", func(t *testing.T) {
		t.Parallel()

		step := &StepStarlark{
			Name: "infinite_loop",
			Source: `
def execute(ctx):
    x = 0
    while True:
        x += 1
    return x
`,
		}

		exec := &testStarlarkContext{scope: scope}
		_, err := step.ExecuteStep(context.Background(), exec)
		if err == nil || !strings.Contains(err.Error(), "too many") {
			t.Fatalf("expected instruction limit error, got: %v", err)
		}
	})

	t.Run("handles None return value as nil", func(t *testing.T) {
		t.Parallel()

		step := &StepStarlark{
			Name: "void_script",
			Source: `
def execute(ctx):
    return None
`,
		}

		exec := &testStarlarkContext{scope: scope}
		res, err := step.ExecuteStep(context.Background(), exec)
		if err != nil {
			t.Fatalf("ExecuteStep() error: %v", err)
		}

		resMap := res.(map[string]any)
		if resMap["result"] != nil {
			t.Errorf("expected result = nil, got %v", resMap["result"])
		}
	})
}

// TestDecodeStepStarlark verifies direct HCL keyword parsing into a [*StepStarlark] definition.
func TestDecodeStepStarlark(t *testing.T) {
	t.Parallel()

	hclBlock := `
		source = <<-STARLARK
			def execute(ctx):
				return {"ok": True}
		STARLARK
	`

	expr, diags := hclsyntax.ParseConfig([]byte(hclBlock), "test.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("hcl parse error: %s", diags.Error())
	}

	step, err := decodeStepStarlark("clean_data", expr.Body, nil, runtimeExprFunctions())
	if err != nil {
		t.Fatalf("decodeStepStarlark error: %v", err)
	}

	starlarkStep, ok := step.(*StepStarlark)
	if !ok {
		t.Fatalf("expected *StepStarlark, got %T", step)
	}

	if starlarkStep.StepName() != "clean_data" {
		t.Errorf("StepName() = %q, want 'clean_data'", starlarkStep.StepName())
	}
	if !strings.Contains(starlarkStep.Source, "def execute(ctx):") {
		t.Errorf("source did not retain function code: %q", starlarkStep.Source)
	}
	if starlarkStep.IsTerminal() {
		t.Error("expected IsTerminal() to be false")
	}
}
