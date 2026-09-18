package hclapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ju4n97/hclapi"
)

// TestPublicAPI_EndToEnd verifies manifest parsing, engine mounting, and execution through the public package.
func TestPublicAPI_EndToEnd(t *testing.T) {
	t.Parallel()

	hclContent := `
server {
  host = "127.0.0.1"
  port = 8080
}

openapi {
  title   = "Public API Test"
  version = "1.0.0"
}

route "POST /calculate" {
  go "compute" {
    use = "math.calc"
    args = {
      base = 10
      mult = 3
    }
  }

  respond {
    status = 200
    body   = steps.compute.result
  }
}
`

	m, err := hclapi.Parse(hclContent)
	if err != nil {
		t.Fatalf("hclapi.Parse() error: %v", err)
	}

	handler := func(ctx context.Context, req *hclapi.StepInput) (any, error) {
		base := req.Args.GetOr("base", int64(1))
		mult := req.Args.GetOr("mult", int64(1))
		return map[string]any{"total": base * mult}, nil
	}

	app, err := hclapi.New(m, hclapi.WithStep("math.calc", handler))
	if err != nil {
		t.Fatalf("hclapi.New() error: %v", err)
	}
	t.Cleanup(func() { _ = app.Close() })

	t.Run("executes route via ServeHTTP", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest(http.MethodPost, "/calculate", strings.NewReader(`{}`))
		rec := httptest.NewRecorder()

		app.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Status = %d, want 200", rec.Code)
		}

		if !strings.Contains(rec.Body.String(), `"total":30`) {
			t.Errorf("expected response to contain total:30, got %s", rec.Body.String())
		}
	})

	t.Run("compiles openapi specification", func(t *testing.T) {
		t.Parallel()

		spec, err := hclapi.CompileSpec(m)
		if err != nil {
			t.Fatalf("hclapi.CompileSpec() error: %v", err)
		}

		if spec.JSONETag == "" {
			t.Fatal("expected non-empty JSON ETag")
		}

		if !strings.Contains(string(spec.JSON), `"title": "Public API Test"`) {
			t.Errorf("expected spec JSON to contain title, got: %s", string(spec.JSON))
		}
	})
}
