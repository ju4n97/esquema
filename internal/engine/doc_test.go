package engine_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ju4n97/hclapi"
)

// TestEngine_DocsStep verifies interactive documentation viewer portals.
func TestEngine_DocsStep(t *testing.T) {
	t.Parallel()

	renderers := []string{"scalar", "swagger", "redoc", "elements"}

	for _, renderer := range renderers {
		t.Run("renders "+renderer+" portal", func(t *testing.T) {
			t.Parallel()

			manifest := `
server {
  host = "127.0.0.1"
  port = 8080
}

openapi {
  title = "Storefront API"
}

route "GET /docs" {
  docs {
    renderer = "` + renderer + `"
    spec_url = "/openapi.json"
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

			req := httptest.NewRequest(http.MethodGet, "/docs", http.NoBody)
			rec := httptest.NewRecorder()

			eng.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d; want 200 OK", rec.Code)
			}

			ct := rec.Header().Get("Content-Type")
			if !strings.Contains(ct, "text/html") {
				t.Errorf("Content-Type = %q; want text/html", ct)
			}

			body := rec.Body.String()
			if !strings.Contains(body, "/openapi.json") {
				t.Errorf("expected HTML to contain spec_url '/openapi.json', body: %s", body)
			}
		})
	}
}
