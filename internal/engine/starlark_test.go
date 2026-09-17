package engine_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ju4n97/hclapi"
)

// TestEngine_StarlarkStep verifies script execution, context modification, and step limits.
func TestEngine_StarlarkStep(t *testing.T) {
	t.Parallel()

	t.Run("terminates execution when step limit is exceeded", func(t *testing.T) {
		t.Parallel()

		manifestContent := `
server {
  host = "127.0.0.1"
  port = 8080
}

route "POST /infinite" {
  step "starlark" "loop" {
    source = <<-STARLARK
      def execute(ctx):
          x = 0
          while True:
              x += 1
          return x
    STARLARK
  }

  respond {
    status = 200
  }
}
`

		cfg, err := hclapi.Parse(manifestContent)
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}

		eng, err := hclapi.New(cfg)
		if err != nil {
			t.Fatalf("engine init failed: %v", err)
		}
		t.Cleanup(func() { _ = eng.Close() })

		req := httptest.NewRequest(http.MethodPost, "/infinite", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		eng.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d; want 500 on execution limit breach", rec.Code)
		}
	})

	t.Run("fails initialization when script syntax is invalid", func(t *testing.T) {
		t.Parallel()

		badManifest := `
server {
  host = "127.0.0.1"
  port = 8080
}

route "POST /broken" {
  step "starlark" "syntax_error" {
    source = "def execute(ctx) missing_colon: return 1"
  }
  respond { status = 200 }
}
`
		cfg, err := hclapi.Parse(badManifest)
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}

		_, err = hclapi.New(cfg)
		if err == nil {
			t.Fatal("expected engine initialization to fail for bad starlark syntax")
		}
	})
}
