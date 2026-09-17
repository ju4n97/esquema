package engine_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/ju4n97/hclapi"
)

// TestEngine_Respond_CustomContentType verifies that custom non-JSON media types are streamed raw.
func TestEngine_Respond_CustomContentType(t *testing.T) {
	t.Parallel()

	manifest := `
server {
  host = "127.0.0.1"
  port = 8080
}

route "GET /export" {
  respond {
    status = 200
    headers = {
      "Content-Type"        = "text/csv"
      "Content-Disposition" = "attachment; filename=\"report.csv\""
    }
    body = "id,name\n1,alice"
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

	req := httptest.NewRequest(http.MethodGet, "/export", http.NoBody)
	rec := httptest.NewRecorder()

	eng.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}

	if ct := rec.Header().Get("Content-Type"); ct != "text/csv" {
		t.Errorf("Content-Type = %q; want 'text/csv'", ct)
	}

	expectedBody := "id,name\n1,alice"
	if rec.Body.String() != expectedBody {
		t.Errorf("body = %q; want %q", rec.Body.String(), expectedBody)
	}
}

// TestEngine_Respond_RowMasking verifies response schema field masking on slices of SQL rows.
func TestEngine_Respond_RowMasking(t *testing.T) {
	t.Parallel()

	const dbSource = "file:mask_test_db?mode=memory&cache=shared"
	db, err := sql.Open("sqlite", dbSource)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	setup := `
		CREATE TABLE accounts (
			id INTEGER PRIMARY KEY,
			username TEXT NOT NULL,
			password_hash TEXT NOT NULL
		);
		INSERT INTO accounts VALUES (1, 'alice', 'argon2$supersecret');
	`
	if _, err := db.Exec(setup); err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	manifestContent := fmt.Sprintf(`
server {
  host = "127.0.0.1"
  port = 8080
}

connection "sql" "db" {
  engine = "sqlite"
  source = %q
}

schema "PublicAccount" {
  field "id" {
    type = "integer"
  }
  field "username" {
    type = "string"
  }
}

route "GET /accounts" {
  step "sql" "list" {
    connection = "db"
    query      = "SELECT id, username, password_hash FROM accounts"
  }

  respond {
    status = 200
    schema = "[]PublicAccount"
    body   = steps.list.rows
  }
}
`, dbSource)

	cfg, err := hclapi.Parse(manifestContent)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	eng, err := hclapi.New(cfg)
	if err != nil {
		t.Fatalf("engine init failed: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	req := httptest.NewRequest(http.MethodGet, "/accounts", http.NoBody)
	rec := httptest.NewRecorder()

	eng.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; want 200", rec.Code)
	}

	var results []map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&results); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if len(results) != 1 {
		t.Fatalf("expected 1 record, got %d", len(results))
	}

	item := results[0]
	if _, exists := item["password_hash"]; exists {
		t.Fatalf("SECURITY VIOLATION: password_hash was not masked! Item: %+v", item)
	}
	if item["username"] != "alice" {
		t.Errorf("username = %v; want 'alice'", item["username"])
	}
}
