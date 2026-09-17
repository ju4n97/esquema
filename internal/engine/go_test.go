package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ju4n97/hclapi"
	"github.com/ju4n97/hclapi/internal/problem"
)

// TestEngine_GoStep_Execution verifies dynamic argument evaluation and pipeline result passing.
func TestEngine_GoStep_Execution(t *testing.T) {
	t.Parallel()

	manifest := `
server {
  host = "127.0.0.1"
  port = 8080
}

route "POST /calc/{factor}" {
  request {
    path "factor" {
      type     = "integer"
      required = true
    }
    body {
      field "base" {
        type     = "integer"
        required = true
      }
    }
  }

  step "go" "multiply" {
    use = "math.mult"
    args = {
      factor = ctx.request.path.factor
      base   = ctx.request.body.base
    }
  }

  respond {
    status = 200
    body   = steps.multiply.result
  }
}
`

	cfg, err := hclapi.Parse(manifest)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	multHandler := func(ctx context.Context, step *hclapi.Step) (any, error) {
		factor := step.Args.GetOr("factor", int64(1))
		base := step.Args.GetOr("base", int64(0))

		if step.Request.Header.Get("X-Tenant") != "tenant-1" {
			return nil, errors.New("missing or invalid X-Tenant header")
		}

		return map[string]any{"product": factor * base}, nil
	}

	eng, err := hclapi.New(cfg, hclapi.WithStep("math.mult", multHandler))
	if err != nil {
		t.Fatalf("engine init failed: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	req := httptest.NewRequest(http.MethodPost, "/calc/5", strings.NewReader(`{"base": 20}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tenant", "tenant-1")
	rec := httptest.NewRecorder()

	eng.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 OK. Body: %s", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if body["product"] != float64(100) && body["product"] != int64(100) {
		t.Errorf("product = %v; want 100", body["product"])
	}
}

// TestEngine_GoStep_Errors verifies proper RFC 9457 error propagation when handlers fail or panic.
func TestEngine_GoStep_Errors(t *testing.T) {
	t.Parallel()

	manifest := `
server {
  host = "127.0.0.1"
  port = 8080
}

route "POST /unregistered" {
  step "go" "missing" {
    use = "nonexistent.handler"
  }
  respond { status = 200 }
}

route "POST /handler-error" {
  step "go" "failing" {
    use = "fail.error"
  }
  respond { status = 200 }
}

route "POST /handler-panic" {
  step "go" "panicking" {
    use = "fail.panic"
  }
  respond { status = 200 }
}
`

	cfg, err := hclapi.Parse(manifest)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	failingHandler := func(ctx context.Context, step *hclapi.Step) (any, error) {
		return nil, errors.New("upstream service connection refused")
	}

	panickingHandler := func(ctx context.Context, step *hclapi.Step) (any, error) {
		var ptr *int
		*ptr = 42
		return nil, nil
	}

	eng, err := hclapi.New(cfg,
		hclapi.WithStep("fail.error", failingHandler),
		hclapi.WithStep("fail.panic", panickingHandler),
	)
	if err != nil {
		t.Fatalf("engine init failed: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	t.Run("returns 500 when handler is not registered", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/unregistered", http.NoBody)
		rec := httptest.NewRecorder()

		eng.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d; want 500", rec.Code)
		}
	})

	t.Run("returns 500 when handler returns standard error", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/handler-error", http.NoBody)
		rec := httptest.NewRecorder()

		eng.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d; want 500", rec.Code)
		}
	})

	t.Run("recovers from panic and returns 500 problem details", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/handler-panic", http.NoBody)
		rec := httptest.NewRecorder()

		eng.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d; want 500", rec.Code)
		}

		var p problem.Problem
		_ = json.NewDecoder(rec.Body).Decode(&p)
		if !strings.Contains(p.Detail, "panicking") || !strings.Contains(p.Detail, "panic:") {
			t.Errorf("expected detail to mention panic, got: %q", p.Detail)
		}
	})
}

// TestEngine_GoStep_ContextCancellation verifies that canceled request contexts abort handler work.
func TestEngine_GoStep_ContextCancellation(t *testing.T) {
	t.Parallel()

	manifest := `
server {
  host = "127.0.0.1"
  port = 8080
}

route "GET /slow" {
  step "go" "wait" {
    use = "slow.worker"
  }
  respond { status = 200 }
}
`

	cfg, err := hclapi.Parse(manifest)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	canceledCh := make(chan struct{})
	slowHandler := func(ctx context.Context, step *hclapi.Step) (any, error) {
		select {
		case <-ctx.Done():
			close(canceledCh)
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
			return map[string]any{"done": true}, nil
		}
	}

	eng, err := hclapi.New(cfg, hclapi.WithStep("slow.worker", slowHandler))
	if err != nil {
		t.Fatalf("engine init failed: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/slow", http.NoBody).WithContext(ctx)
	rec := httptest.NewRecorder()

	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()

	eng.ServeHTTP(rec, req)

	select {
	case <-canceledCh:
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for context cancellation")
	}

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d; want 500 on context cancel", rec.Code)
	}
}
