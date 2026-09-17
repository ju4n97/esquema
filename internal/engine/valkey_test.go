package engine_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ju4n97/hclapi"
)

// TestEngine_Valkey_UnitErrors verifies configuration error handling without network dependencies.
func TestEngine_Valkey_UnitErrors(t *testing.T) {
	t.Parallel()

	t.Run("returns 500 when valkey connection is missing from runtime", func(t *testing.T) {
		t.Parallel()

		// Valid manifest syntax, but connection "cache" is not in the active engine
		manifest := `
server { 
  host = "127.0.0.1" 
  port = 8080 
}

connection "valkey" "cache" {
  url = "redis://127.0.0.1:6379"
}

route "GET /cache" {
  step "valkey" "lookup" {
    connection = "cache"
    op         = "get"
    key        = "user:1"
  }
  respond { 
  	status = 200 
  }
}
`
		cfg, err := hclapi.Parse(manifest)
		if err != nil {
			t.Fatalf("parse failed: %v", err)
		}

		// Delete connection from runtime to simulate an uninitialized pool
		delete(cfg.Connections, "cache")

		eng, err := hclapi.New(cfg)
		if err != nil {
			t.Fatalf("engine init failed: %v", err)
		}
		t.Cleanup(func() { _ = eng.Close() })

		req := httptest.NewRequest(http.MethodGet, "/cache", http.NoBody)
		rec := httptest.NewRecorder()

		eng.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Errorf("status = %d; want 500 on missing connection", rec.Code)
		}
	})
}
