package engine_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ju4n97/hclapi/internal/engine"
)

// TestContext_NewContext verifies coordinate extraction and bounded body reading.
func TestContext_NewContext(t *testing.T) {
	t.Parallel()

	t.Run("extracts path, query, headers, and json body", func(t *testing.T) {
		t.Parallel()
		body := `{"user":"alice","role":"admin"}`
		req := httptest.NewRequest(http.MethodPost, "/orgs/42/members?dry_run=true", strings.NewReader(body))
		req.SetPathValue("org_id", "42")
		req.Header.Set("X-Trace-ID", "trace-101")

		ctx, err := engine.NewContext(req, "POST /orgs/{org_id}/members", 1024*1024)
		if err != nil {
			t.Fatalf("NewContext failed: %v", err)
		}

		evalCtx := ctx.EvalContext()
		if evalCtx == nil {
			t.Fatal("expected non-nil EvalContext")
		}

		expr, diags := hclsyntax.ParseExpression([]byte(`ctx.request.headers["x-trace-id"]`), "test.hcl", hcl.Pos{})
		if diags.HasErrors() {
			t.Fatalf("parse failed: %v", diags)
		}
		val, err := ctx.EvalAny(expr)
		if err != nil || val != "trace-101" {
			t.Errorf("EvalAny header = %v, err = %v; want 'trace-101'", val, err)
		}
	})

	t.Run("rejects payload exceeding maxBytes limit", func(t *testing.T) {
		t.Parallel()
		payload := strings.Repeat("x", 2048)
		req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader(payload))

		_, err := engine.NewContext(req, "POST /upload", 1024)
		if err == nil || !strings.Contains(err.Error(), "exceeds limit") {
			t.Errorf("expected payload limit error, got: %v", err)
		}
	})
}

// TestContext_StepResultsAndEvaluation verifies evaluation of step outputs and HCL conditionals.
func TestContext_StepResultsAndEvaluation(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/test", http.NoBody)
	ctx, err := engine.NewContext(req, "GET /test", 1024)
	if err != nil {
		t.Fatalf("NewContext failed: %v", err)
	}

	ctx.SetStepResult("auth", map[string]any{
		"authenticated": true,
		"user_id":       int64(99),
	})

	expr, diags := hclsyntax.ParseExpression([]byte(`steps.auth.authenticated == true && steps.auth.user_id == 99`), "test.hcl", hcl.Pos{})
	if diags.HasErrors() {
		t.Fatalf("parse failed: %v", diags)
	}

	matched, err := ctx.EvalBool(expr)
	if err != nil || !matched {
		t.Errorf("EvalBool() = (%v, %v); want (true, nil)", matched, err)
	}
}
