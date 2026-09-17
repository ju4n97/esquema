package engine_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ju4n97/hclapi"
)

// TestEngine_SpecStep verifies OpenAPI specification document serving and ETag caching.
func TestEngine_SpecStep(t *testing.T) {
	t.Parallel()

	manifest := `
server {
  host = "127.0.0.1"
  port = 8080
}

openapi {
  title   = "Inventory API"
  version = "1.0.0"
}

route "GET /openapi.json" {
  spec {
    format = "json"
  }
}

route "GET /openapi.yaml" {
  spec {
    format = "yaml"
  }
}
`

	cfg, err := hclapi.Parse(manifest)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	eng, err := hclapi.New(cfg)
	if err != nil {
		t.Fatalf("engine init failed: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	t.Run("serves json spec with etag caching", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/openapi.json", http.NoBody)
		rec := httptest.NewRecorder()

		eng.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200 OK", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
			t.Errorf("Content-Type = %q; want application/json", ct)
		}

		etag := rec.Header().Get("ETag")
		if etag == "" {
			t.Fatal("expected non-empty ETag header")
		}

		// Conditional request returning 304 Not Modified
		cachedReq := httptest.NewRequest(http.MethodGet, "/openapi.json", http.NoBody)
		cachedReq.Header.Set("If-None-Match", etag)
		cachedRec := httptest.NewRecorder()

		eng.ServeHTTP(cachedRec, cachedReq)

		if cachedRec.Code != http.StatusNotModified {
			t.Errorf("status = %d; want 304 Not Modified", cachedRec.Code)
		}
	})

	t.Run("serves yaml spec", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/openapi.yaml", http.NoBody)
		rec := httptest.NewRecorder()

		eng.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200 OK", rec.Code)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "yaml") {
			t.Errorf("Content-Type = %q; want application/yaml", ct)
		}
	})
}
