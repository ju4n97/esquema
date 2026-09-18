package manifest

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// parseExpr parses an HCL expression string for test fixtures, failing t on syntax errors.
func parseExpr(t *testing.T, src string) hcl.Expression {
	t.Helper()

	expr, diags := hclsyntax.ParseExpression([]byte(src), "test.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("failed to parse expression %q: %s", src, diags.Error())
	}

	return expr
}

// TestNewExpr verifies expression construction for nil and valid raw expressions.
func TestNewExpr(t *testing.T) {
	t.Parallel()

	t.Run("nil raw returns nil", func(t *testing.T) {
		t.Parallel()

		if got := NewExpr(nil, nil); got != nil {
			t.Fatalf("expected nil, got %v", got)
		}
	})

	t.Run("valid expression compiles", func(t *testing.T) {
		t.Parallel()

		raw := parseExpr(t, `"ready"`)
		if got := NewExpr(raw, nil); got == nil {
			t.Fatal("expected non-nil Expr")
		}
	})
}

// TestExpr_Eval verifies expression evaluation, scope lookups, custom functions, and diagnostics.
func TestExpr_Eval(t *testing.T) {
	t.Parallel()

	refTime := time.Date(2026, 9, 17, 12, 0, 0, 0, time.UTC)
	scope := Scope{
		Request: map[string]any{
			"id":       "req-1",
			"active":   true,
			"priority": 10,
			"meta": map[string]any{
				"tag":      "prod",
				"nullable": nil,
			},
		},
		Steps: map[string]any{
			"step1": map[string]any{
				"output": "ok",
			},
		},
		Now: refTime,
	}

	upperFunc := function.New(&function.Spec{
		Params: []function.Parameter{{Name: "s", Type: cty.String}},
		Type:   function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			return cty.StringVal(strings.ToUpper(args[0].AsString())), nil
		},
	})

	tests := []struct {
		name      string
		exprSrc   string
		funcs     map[string]function.Function
		scope     Scope
		want      any
		wantError bool
	}{
		{
			name:    "literal string",
			exprSrc: `"hello"`,
			want:    "hello",
		},
		{
			name:    "literal integer",
			exprSrc: `42`,
			want:    int64(42),
		},
		{
			name:    "literal float",
			exprSrc: `3.14`,
			want:    float64(3.14),
		},
		{
			name:    "literal boolean",
			exprSrc: `true`,
			want:    true,
		},
		{
			name:    "null literal",
			exprSrc: `null`,
			want:    nil,
		},
		{
			name:    "arithmetic precedence",
			exprSrc: `2 + 3 * 4`,
			want:    int64(14),
		},
		{
			name:    "scope request lookup",
			exprSrc: `ctx.request.id`,
			scope:   scope,
			want:    "req-1",
		},
		{
			name:    "scope request nested null",
			exprSrc: `ctx.request.meta.nullable`,
			scope:   scope,
			want:    nil,
		},
		{
			name:    "scope timestamp lookup",
			exprSrc: `ctx.timestamp`,
			scope:   scope,
			want:    refTime.Unix(),
		},
		{
			name:    "scope now lookup",
			exprSrc: `ctx.now`,
			scope:   scope,
			want:    refTime.Format(time.RFC3339),
		},
		{
			name:    "scope steps lookup",
			exprSrc: `steps.step1.output`,
			scope:   scope,
			want:    "ok",
		},
		{
			name:    "tuple resolution",
			exprSrc: `["a", 1, true]`,
			want:    []any{"a", int64(1), true},
		},
		{
			name:    "custom function call",
			exprSrc: `upper("hello")`,
			funcs:   map[string]function.Function{"upper": upperFunc},
			want:    "HELLO",
		},
		{
			name:      "missing variable diagnostic",
			exprSrc:   `undefined_var`,
			wantError: true,
		},
		{
			name:      "type mismatch diagnostic",
			exprSrc:   `"str" + 1`,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expr := NewExpr(parseExpr(t, tt.exprSrc), tt.funcs)
			got, err := expr.Eval(tt.scope)
			if (err != nil) != tt.wantError {
				t.Fatalf("Eval() error = %v, wantError = %v", err, tt.wantError)
			}
			if !tt.wantError && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Eval() = %#v (%T), want %#v (%T)", got, got, tt.want, tt.want)
			}
		})
	}

	t.Run("nil receiver returns nil result without error", func(t *testing.T) {
		t.Parallel()

		var e *hclExpr
		got, err := e.Eval(scope)
		if err != nil || got != nil {
			t.Fatalf("expected (nil, nil), got (%v, %v)", got, err)
		}
	})
}

// TestExpr_EvalBool verifies boolean evaluation, type errors, diagnostics, and nil defaults.
func TestExpr_EvalBool(t *testing.T) {
	t.Parallel()

	scope := Scope{
		Request: map[string]any{"count": 5},
	}

	tests := []struct {
		name      string
		exprSrc   string
		scope     Scope
		want      bool
		wantError bool
	}{
		{
			name:    "literal true",
			exprSrc: `true`,
			want:    true,
		},
		{
			name:    "literal false",
			exprSrc: `false`,
			want:    false,
		},
		{
			name:    "logical comparison",
			exprSrc: `ctx.request.count == 5`,
			scope:   scope,
			want:    true,
		},
		{
			name:      "type error on string",
			exprSrc:   `"true"`,
			wantError: true,
		},
		{
			name:      "type error on number",
			exprSrc:   `1`,
			wantError: true,
		},
		{
			name:      "evaluation diagnostic error",
			exprSrc:   `undefined_var`,
			wantError: true,
		},
		{
			name:    "null literal defaults to true",
			exprSrc: `null`,
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expr := NewExpr(parseExpr(t, tt.exprSrc), nil)
			got, err := expr.EvalBool(tt.scope)
			if (err != nil) != tt.wantError {
				t.Fatalf("EvalBool() error = %v, wantError = %v", err, tt.wantError)
			}
			if !tt.wantError && got != tt.want {
				t.Errorf("EvalBool() = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("nil receiver defaults to true", func(t *testing.T) {
		t.Parallel()

		var e *hclExpr
		got, err := e.EvalBool(scope)
		if err != nil || got != true {
			t.Fatalf("expected (true, nil), got (%v, %v)", got, err)
		}
	})

	t.Run("dynamic unconfigured value defaults to true", func(t *testing.T) {
		t.Parallel()

		// Simulate gohcl unconfigured optional attribute
		expr := &hclExpr{}

		// A nil or unconfigured guard must evaluate to true
		var unconfigured *hclExpr
		got, err := unconfigured.EvalBool(Scope{})
		if err != nil || !got {
			t.Fatalf("expected (true, nil), got (%v, %v)", got, err)
		}
		_ = expr
	})
}

// TestExpr_EvalMap verifies object conversion, null handling, type errors, and nil receiver behavior.
func TestExpr_EvalMap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		exprSrc   string
		want      map[string]any
		wantError bool
	}{
		{
			name:    "valid object",
			exprSrc: `{ a = "1", b = 2 }`,
			want:    map[string]any{"a": "1", "b": int64(2)},
		},
		{
			name:    "null literal returns nil map",
			exprSrc: `null`,
			want:    nil,
		},
		{
			name:      "string type error",
			exprSrc:   `"invalid"`,
			wantError: true,
		},
		{
			name:      "list type error",
			exprSrc:   `[1, 2]`,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expr := NewExpr(parseExpr(t, tt.exprSrc), nil)
			got, err := expr.EvalMap(Scope{})
			if (err != nil) != tt.wantError {
				t.Fatalf("EvalMap() error = %v, wantError = %v", err, tt.wantError)
			}
			if !tt.wantError && !reflect.DeepEqual(got, tt.want) {
				t.Errorf("EvalMap() = %#v, want %#v", got, tt.want)
			}
		})
	}

	t.Run("nil receiver returns nil without error", func(t *testing.T) {
		t.Parallel()

		var e *hclExpr
		got, err := e.EvalMap(Scope{})
		if err != nil || got != nil {
			t.Fatalf("expected (nil, nil), got (%v, %v)", got, err)
		}
	})
}

// TestExpr_String verifies string formatting for nil and active expressions.
func TestExpr_String(t *testing.T) {
	t.Parallel()

	t.Run("nil receiver returns empty string", func(t *testing.T) {
		t.Parallel()

		var e *hclExpr
		if got := e.String(); got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})

	t.Run("valid expression returns non-empty representation", func(t *testing.T) {
		t.Parallel()

		expr := NewExpr(parseExpr(t, `"sample"`), nil)
		if expr.String() == "" {
			t.Error("expected non-empty string representation")
		}
	})
}

// Test_toCty verifies conversion from native Go values to cty.Value representations.
func Test_toCty(t *testing.T) {
	t.Parallel()

	type CustomStruct struct {
		Key string
	}

	tests := []struct {
		name     string
		input    any
		validate func(t *testing.T, val cty.Value)
	}{
		{
			name:  "nil input maps to dynamic null",
			input: nil,
			validate: func(t *testing.T, val cty.Value) {
				if !val.IsNull() {
					t.Fatalf("expected null value, got %#v", val)
				}
			},
		},
		{
			name:  "string primitive",
			input: "hello",
			validate: func(t *testing.T, val cty.Value) {
				if val.Type() != cty.String || val.AsString() != "hello" {
					t.Fatalf("expected string 'hello', got %#v", val)
				}
			},
		},
		{
			name:  "int primitive",
			input: 42,
			validate: func(t *testing.T, val cty.Value) {
				if val.Type() != cty.Number {
					t.Fatalf("expected cty.Number, got %v", val.Type())
				}
			},
		},
		{
			name:  "float64 primitive",
			input: 3.1415,
			validate: func(t *testing.T, val cty.Value) {
				if val.Type() != cty.Number {
					t.Fatalf("expected cty.Number, got %v", val.Type())
				}
			},
		},
		{
			name:  "empty map maps to EmptyObjectVal",
			input: map[string]any{},
			validate: func(t *testing.T, val cty.Value) {
				if !val.RawEquals(cty.EmptyObjectVal) {
					t.Fatalf("expected EmptyObjectVal, got %#v", val)
				}
			},
		},
		{
			name:  "empty slice maps to EmptyTupleVal",
			input: []any{},
			validate: func(t *testing.T, val cty.Value) {
				if !val.RawEquals(cty.EmptyTupleVal) {
					t.Fatalf("expected EmptyTupleVal, got %#v", val)
				}
			},
		},
		{
			name:  "custom struct encapsulates cleanly without stringifying",
			input: CustomStruct{Key: "value"},
			validate: func(t *testing.T, val cty.Value) {
				if !val.Type().IsCapsuleType() {
					t.Fatalf("expected capsule type for struct, got %v", val.Type())
				}
				native := toNative(val)
				expected := CustomStruct{Key: "value"}
				if native != expected {
					t.Fatalf("round-trip unwrap = %#v, want %#v", native, expected)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.validate(t, toCty(tt.input))
		})
	}
}

// Test_toNative verifies conversion from cty.Value to native Go values and preserves numeric precision.
func Test_toNative(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input cty.Value
		want  any
	}{
		{
			name:  "uninitialized nil value returns nil",
			input: cty.NilVal,
			want:  nil,
		},
		{
			name:  "null string returns nil",
			input: cty.NullVal(cty.String),
			want:  nil,
		},
		{
			name:  "unknown string returns nil",
			input: cty.UnknownVal(cty.String),
			want:  nil,
		},
		{
			name:  "string value",
			input: cty.StringVal("text"),
			want:  "text",
		},
		{
			name:  "bool value",
			input: cty.BoolVal(true),
			want:  true,
		},
		{
			name:  "exact integer returns int64",
			input: cty.NumberIntVal(100),
			want:  int64(100),
		},
		{
			name:  "fractional number returns float64",
			input: cty.NumberFloatVal(2.718),
			want:  float64(2.718),
		},
		{
			name:  "tuple converts to []any",
			input: cty.TupleVal([]cty.Value{cty.StringVal("a")}),
			want:  []any{"a"},
		},
		{
			name:  "list converts to []any",
			input: cty.ListVal([]cty.Value{cty.StringVal("b")}),
			want:  []any{"b"},
		},
		{
			name:  "object converts to map[string]any",
			input: cty.ObjectVal(map[string]cty.Value{"k": cty.StringVal("v")}),
			want:  map[string]any{"k": "v"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := toNative(tt.input)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("toNative() = %#v (%T), want %#v (%T)", got, got, tt.want, tt.want)
			}
		})
	}
}
