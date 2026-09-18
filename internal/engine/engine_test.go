package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ju4n97/hclapi/internal/manifest"
)

// TestEngine_LifecycleAndEndToEndExecution verifies engine boot, routing, pools, and clean shutdown.
func TestEngine_LifecycleAndEndToEndExecution(t *testing.T) {
	t.Parallel()

	hclContent := `
server {
  host          = "127.0.0.1"
  port          = 8080
  max_body_size = "10MB"
}

openapi {
  title   = "Test Engine API"
  version = "1.0.0"
}

connection "sql" "primary" {
  engine = "sqlite"
  source = "file::memory:?cache=shared"
}

route "POST /compute" {
  go "multiply" {
    use = "math.multiply"
    args = {
      val = 21
    }
  }

  respond {
    status = 200
    body   = steps.multiply.result
  }
}

route "GET /spec.json" {
  spec {
    format = "json"
  }
}
`

	m, err := manifest.Parse(hclContent)
	if err != nil {
		t.Fatalf("Parse() error: %v", err)
	}

	multHandler := func(ctx context.Context, req *manifest.StepInput) (any, error) {
		val := req.Args.GetOr("val", int64(1))
		return map[string]any{"doubled": val * 2}, nil
	}

	eng, err := New(m, WithGoHandler("math.multiply", multHandler))
	if err != nil {
		t.Fatalf("New() unexpected error: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	t.Run("executes pipeline endpoint through ServeHTTP", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest(http.MethodPost, "/compute", strings.NewReader(`{}`))
		rec := httptest.NewRecorder()

		eng.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Status = %d, want 200", rec.Code)
		}

		if !strings.Contains(rec.Body.String(), `"doubled":42`) {
			t.Errorf("expected response to contain doubled:42, got %s", rec.Body.String())
		}
	})

	t.Run("serves precompiled openapi specification", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest(http.MethodGet, "/spec.json", http.NoBody)
		rec := httptest.NewRecorder()

		eng.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Status = %d, want 200", rec.Code)
		}

		var spec map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
			t.Fatalf("failed to decode specification JSON: %v", err)
		}

		if spec["openapi"] != "3.1.0" {
			t.Errorf("expected openapi '3.1.0', got %v", spec["openapi"])
		}
	})

	t.Run("provides direct access to managed SQL pool", func(t *testing.T) {
		t.Parallel()

		db, ok := eng.SQL("primary")
		if !ok || db == nil {
			t.Fatal("expected 'primary' SQL pool to be accessible")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		if err := db.PingContext(ctx); err != nil {
			t.Fatalf("ping database failed: %v", err)
		}
	})

	t.Run("Close cleanly terminates connection pools", func(t *testing.T) {
		t.Parallel()

		mCopy, pErr := manifest.Parse(hclContent)
		if pErr != nil {
			t.Fatalf("parse error: %v", pErr)
		}

		tempEng, err := New(mCopy)
		if err != nil {
			t.Fatalf("New() error: %v", err)
		}

		db, _ := tempEng.SQL("primary")

		if err := tempEng.Close(); err != nil {
			t.Fatalf("Close() error: %v", err)
		}

		// Pool should be closed
		if err := db.Ping(); err == nil {
			t.Fatal("expected closed database pool to reject ping, got nil")
		}
	})
}
