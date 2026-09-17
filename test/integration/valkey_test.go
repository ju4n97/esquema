//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/valkey"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/ju4n97/hclapi"
)

// TestValkey_Integration verifies live Valkey operations (set with TTL, get, miss, and del).
func TestValkey_Integration(t *testing.T) {
	ctx := context.Background()

	valkeyContainer, err := valkey.Run(ctx,
		"valkey/valkey:8.0-alpine",
		testcontainers.WithWaitStrategy(
			wait.ForLog("Ready to accept connections").
				WithStartupTimeout(15*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("failed to start valkey container: %v", err)
	}
	t.Cleanup(func() { _ = valkeyContainer.Terminate(ctx) })

	uri, err := valkeyContainer.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to obtain valkey connection URI: %v", err)
	}

	manifestContent := fmt.Sprintf(`
server {
  host = "127.0.0.1"
  port = 8080
}

connection "valkey" "cache" {
  engine = "valkey"
  url    = %q
}

route "POST /cache/{key}" {
  request {
    path "key" {
      type     = "string"
      required = true
    }
    body {
      field "payload" {
        type     = "string"
        required = true
      }
    }
  }

  step "valkey" "set_cache" {
    connection = "cache"
    op         = "set"
    key        = ctx.request.path.key
    value      = ctx.request.body.payload
    ttl        = "30s"
  }

  respond {
    status = 201
    body = {
      stored = true
    }
  }
}

route "GET /cache/{key}" {
  request {
    path "key" {
      type     = "string"
      required = true
    }
  }

  step "valkey" "get_cache" {
    connection = "cache"
    op         = "get"
    key        = ctx.request.path.key
  }

  respond {
    when   = !steps.get_cache.found
    status = 404
    body   = problem(404, "Cache key expired or missing")
  }

  respond {
    status = 200
    body = {
      data = steps.get_cache.value
    }
  }
}

route "DELETE /cache/{key}" {
  request {
    path "key" {
      type     = "string"
      required = true
    }
  }

  step "valkey" "del_cache" {
    connection = "cache"
    op         = "del"
    key        = ctx.request.path.key
  }

  respond {
    status = 204
  }
}
`, uri)

	cfg, err := hclapi.Parse(manifestContent)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	eng, err := hclapi.New(cfg)
	if err != nil {
		t.Fatalf("failed to initialize engine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	t.Run("stores and retrieves cached payload", func(t *testing.T) {
		setReq := httptest.NewRequest(http.MethodPost, "/cache/token-abc", strings.NewReader(`{"payload":"secret-data"}`))
		setReq.Header.Set("Content-Type", "application/json")
		setRec := httptest.NewRecorder()
		eng.ServeHTTP(setRec, setReq)

		if setRec.Code != http.StatusCreated {
			t.Fatalf("expected 201 Created, got: %d", setRec.Code)
		}

		getReq := httptest.NewRequest(http.MethodGet, "/cache/token-abc", http.NoBody)
		getRec := httptest.NewRecorder()
		eng.ServeHTTP(getRec, getReq)

		if getRec.Code != http.StatusOK {
			t.Fatalf("expected 200 OK, got: %d", getRec.Code)
		}

		var resp map[string]any
		if err := json.NewDecoder(getRec.Body).Decode(&resp); err != nil {
			t.Fatalf("failed to decode response: %v", err)
		}
		if resp["data"] != "secret-data" {
			t.Errorf("data = %v; want 'secret-data'", resp["data"])
		}
	})

	t.Run("returns 404 problem details on cache miss", func(t *testing.T) {
		getReq := httptest.NewRequest(http.MethodGet, "/cache/nonexistent-key", http.NoBody)
		getRec := httptest.NewRecorder()
		eng.ServeHTTP(getRec, getReq)

		if getRec.Code != http.StatusNotFound {
			t.Fatalf("expected 404 Not Found, got: %d", getRec.Code)
		}
	})

	t.Run("deletes key and confirms subsequent miss", func(t *testing.T) {
		delReq := httptest.NewRequest(http.MethodDelete, "/cache/token-abc", http.NoBody)
		delRec := httptest.NewRecorder()
		eng.ServeHTTP(delRec, delReq)

		if delRec.Code != http.StatusNoContent {
			t.Fatalf("expected 204 No Content, got: %d", delRec.Code)
		}

		getReq := httptest.NewRequest(http.MethodGet, "/cache/token-abc", http.NoBody)
		getRec := httptest.NewRecorder()
		eng.ServeHTTP(getRec, getReq)

		if getRec.Code != http.StatusNotFound {
			t.Errorf("expected 404 after deletion, got: %d", getRec.Code)
		}
	})
}
