package engine_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/ju4n97/hclapi"
)

// TestEngine_HTTPStep verifies outbound HTTP request execution, headers, and traceparent propagation.
func TestEngine_HTTPStep(t *testing.T) {
	t.Parallel()

	tp := sdktrace.NewTracerProvider(sdktrace.WithSampler(sdktrace.AlwaysSample()))
	otel.SetTracerProvider(tp)
	t.Cleanup(func() { _ = tp.Shutdown(context.Background()) })

	var receivedTraceparent string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Service-Auth") != "secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		receivedTraceparent = r.Header.Get("traceparent")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"upstream_id": 99, "status": "active"}`))
	}))
	t.Cleanup(upstream.Close)

	manifestContent := fmt.Sprintf(`
server {
  host = "127.0.0.1"
  port = 8080
}

route "GET /sync" {
  step "http" "fetch_remote" {
    method = "GET"
    url    = "%s"
    headers = {
      "X-Service-Auth" = "secret"
    }
  }

  respond {
    status = 200
    body   = steps.fetch_remote.body
  }
}
`, upstream.URL)

	cfg, err := hclapi.Parse(manifestContent)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	eng, err := hclapi.New(cfg)
	if err != nil {
		t.Fatalf("failed to initialize engine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	req := httptest.NewRequest(http.MethodGet, "/sync", http.NoBody)
	rec := httptest.NewRecorder()

	eng.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200 OK", rec.Code)
	}

	var body map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if body["upstream_id"] != float64(99) || body["status"] != "active" {
		t.Errorf("unexpected body payload: %+v", body)
	}

	if receivedTraceparent == "" {
		t.Error("expected W3C 'traceparent' header to be injected into outbound call")
	}
}

// TestEngine_HTTPStep_DynamicURL verifies HCL string interpolation inside step URL attributes.
func TestEngine_HTTPStep_DynamicURL(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/remote/item-55" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"matched": true}`))
	}))
	t.Cleanup(upstream.Close)

	manifestContent := fmt.Sprintf(`
server {
  host = "127.0.0.1"
  port = 8080
}

route "GET /catalog/{id}" {
  step "http" "proxy" {
    method = "GET"
    url    = "%s/remote/${ctx.request.path.id}"
  }

  respond {
    status = steps.proxy.status
    body   = steps.proxy.body
  }
}
`, upstream.URL)

	cfg, err := hclapi.Parse(manifestContent)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	eng, err := hclapi.New(cfg)
	if err != nil {
		t.Fatalf("engine init failed: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	req := httptest.NewRequest(http.MethodGet, "/catalog/item-55", http.NoBody)
	rec := httptest.NewRecorder()

	eng.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}
}
