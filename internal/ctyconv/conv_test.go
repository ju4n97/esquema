package ctyconv_test

import (
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/ju4n97/hclapi/internal/ctyconv"
)

// TestToCtyAndNative verifies bidirectional conversion between Go native types and cty.Value.
func TestToCtyAndNative(t *testing.T) {
	t.Parallel()

	input := map[string]any{
		"title":    "Build API",
		"count":    int64(42),
		"active":   true,
		"tags":     []any{"web", "prod"},
		"metadata": map[string]any{"version": "1.0"},
	}

	ctyVal := ctyconv.ToCty(input)
	if !ctyVal.Type().IsObjectType() {
		t.Fatalf("expected cty object, got: %s", ctyVal.Type().FriendlyName())
	}

	native := ctyconv.ToNative(ctyVal)
	nativeMap, ok := native.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got: %T", native)
	}

	if nativeMap["title"] != "Build API" {
		t.Errorf("title = %v; want 'Build API'", nativeMap["title"])
	}
	if nativeMap["count"] != int64(42) {
		t.Errorf("count = %v; want 42", nativeMap["count"])
	}
	if nativeMap["active"] != true {
		t.Errorf("active = %v; want true", nativeMap["active"])
	}
}

// TestEvalExpressions verifies HCL runtime expression evaluation and built-in functions.
func TestEvalExpressions(t *testing.T) {
	t.Parallel()

	evalCtx := &hcl.EvalContext{
		Variables: map[string]cty.Value{
			"ctx": cty.ObjectVal(map[string]cty.Value{
				"status": cty.NumberIntVal(200),
				"role":   cty.StringVal("admin"),
			}),
		},
		Functions: ctyconv.BuiltinFunctions(),
	}

	t.Run("evaluates boolean conditional", func(t *testing.T) {
		t.Parallel()

		expr, diags := hclsyntax.ParseExpression([]byte(`ctx.status == 200 && ctx.role == "admin"`), "test.hcl", hcl.InitialPos)
		if diags.HasErrors() {
			t.Fatalf("parse error: %v", diags)
		}

		result, err := ctyconv.EvalBool(expr, evalCtx)
		if err != nil {
			t.Fatalf("EvalBool failed: %v", err)
		}
		if !result {
			t.Errorf("expected true condition, got false")
		}
	})

	t.Run("evaluates string helper function upper", func(t *testing.T) {
		t.Parallel()

		expr, diags := hclsyntax.ParseExpression([]byte("upper(ctx.role)"), "test.hcl", hcl.InitialPos)
		if diags.HasErrors() {
			t.Fatalf("parse error: %v", diags)
		}

		result, err := ctyconv.EvalAny(expr, evalCtx)
		if err != nil {
			t.Fatalf("EvalAny failed: %v", err)
		}
		if result != "ADMIN" {
			t.Errorf("result = %v; want 'ADMIN'", result)
		}
	})
}

// TestToCty_CustomStructSlices verifies that slices of custom structs convert to cty tuples with json tags.
func TestToCty_CustomStructSlices(t *testing.T) {
	t.Parallel()

	type ResolvedItem struct {
		URL      string `json:"url"`
		FreshURL string `json:"fresh_url,omitempty"`
		Error    string `json:"error,omitempty"`
	}

	input := map[string]any{
		"total": 1,
		"resolved": []ResolvedItem{
			{
				URL:   "http://localhost:8080/assets?id=42",
				Error: "study not found in cfaz",
			},
		},
	}

	ctyVal := ctyconv.ToCty(input)
	native := ctyconv.ToNative(ctyVal)

	nativeMap, ok := native.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any, got: %T", native)
	}

	// Verify resolved is a real slice of maps, not a stringified struct
	resolvedSlice, ok := nativeMap["resolved"].([]any)
	if !ok {
		t.Fatalf("expected nativeMap['resolved'] to be []any, got: %T (%v)", nativeMap["resolved"], nativeMap["resolved"])
	}

	if len(resolvedSlice) != 1 {
		t.Fatalf("expected 1 item, got %d", len(resolvedSlice))
	}

	itemMap, ok := resolvedSlice[0].(map[string]any)
	if !ok {
		t.Fatalf("expected item to be map[string]any, got: %T", resolvedSlice[0])
	}

	if itemMap["url"] != "http://localhost:8080/assets?id=42" {
		t.Errorf("url = %v; want 'http://localhost:8080/assets?id=42'", itemMap["url"])
	}
	if itemMap["error"] != "study not found in cfaz" {
		t.Errorf("error = %v; want 'study not found in cfaz'", itemMap["error"])
	}
}
