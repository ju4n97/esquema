package engine_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/ju4n97/hclapi"
)

// setupSQLiteDB initializes an isolated in-memory SQLite table for end-to-end testing.
func setupSQLiteDB(t *testing.T, dbName string) string {
	t.Helper()
	source := fmt.Sprintf("file:%s?mode=memory&cache=shared", dbName)

	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatalf("failed to open sqlite database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	schema := `
		CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			email TEXT UNIQUE NOT NULL,
			role TEXT NOT NULL DEFAULT 'member'
		);
		INSERT INTO users (email, role) VALUES ('alice@example.com', 'admin');
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("failed to seed database: %v", err)
	}

	return source
}

// TestEngine_FullPipelineExecution verifies SQL query execution, Starlark, and conditional responses.
func TestEngine_FullPipelineExecution(t *testing.T) {
	t.Parallel()

	dbSource := setupSQLiteDB(t, "pipeline_exec_db")

	manifestContent := fmt.Sprintf(`
server {
  host = "127.0.0.1"
  port = 8080
}

connection "sql" "main" {
  engine = "sqlite"
  source = %q
}

route "POST /users" {
  request {
    body {
      field "email" {
        type     = "string"
        format   = "email"
        required = true
      }
    }
  }

  step "starlark" "normalize" {
    source = <<-PYTHON
      def execute(ctx):
          email = ctx["request"]["body"].get("email", "")
          return {"clean_email": email.strip().lower()}
    PYTHON
  }

  step "sql" "insert" {
    connection = "main"
    query      = "INSERT INTO users (email) VALUES (@email) RETURNING id, email, role"
    args = {
      email = steps.normalize.result.clean_email
    }
    catch "19" {
      status = 409
      body   = problem(409, "User already registered")
    }
  }

  respond {
    status = 201
    body   = steps.insert.row
  }
}

route "GET /users/{id}" {
  request {
    path "id" {
      type     = "integer"
      required = true
    }
  }

  step "sql" "fetch" {
    connection = "main"
    query      = "SELECT id, email, role FROM users WHERE id = @id"
    args = {
      id = ctx.request.path.id
    }
  }

  respond {
    when   = steps.fetch.rows_affected == 0
    status = 404
    body   = problem(404, "User not found")
  }

  respond {
    status = 200
    body   = steps.fetch.row
  }
}

route "GET /compute" {
  step "go" "math" {
    use = "math.double"
    args = {
      value = 21
    }
  }

  respond {
    status = 200
    body   = steps.math.result
  }
}
`, dbSource)

	cfg, err := hclapi.Parse(manifestContent)
	if err != nil {
		t.Fatalf("failed to parse manifest: %v", err)
	}

	doubleHandler := func(ctx context.Context, step *hclapi.Step) (any, error) {
		val := step.Args.GetOr("value", int64(0))
		return map[string]any{"doubled": val * 2}, nil
	}

	eng, err := hclapi.New(cfg, hclapi.WithStep("math.double", doubleHandler))
	if err != nil {
		t.Fatalf("failed to initialize engine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	t.Run("inserts record through starlark and sql steps", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"email":"  BOB@EXAMPLE.COM  "}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		eng.ServeHTTP(rec, req)

		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d; want 201 Created", rec.Code)
		}

		var body map[string]any
		_ = json.NewDecoder(rec.Body).Decode(&body)
		if body["email"] != "bob@example.com" {
			t.Errorf("normalized email = %v; want 'bob@example.com'", body["email"])
		}
	})

	t.Run("catches sqlite constraint violation and emits problem response", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/users", strings.NewReader(`{"email":"alice@example.com"}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		eng.ServeHTTP(rec, req)

		if rec.Code != http.StatusConflict {
			t.Fatalf("status = %d; want 409 Conflict", rec.Code)
		}
	})

	t.Run("evaluates conditional when expression returning 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/users/9999", http.NoBody)
		rec := httptest.NewRecorder()

		eng.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("status = %d; want 404 Not Found", rec.Code)
		}
	})

	t.Run("executes registered Go step handler", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/compute", http.NoBody)
		rec := httptest.NewRecorder()

		eng.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d; want 200 OK", rec.Code)
		}

		var body map[string]any
		_ = json.NewDecoder(rec.Body).Decode(&body)
		if body["doubled"] != float64(42) && body["doubled"] != int64(42) {
			t.Errorf("doubled = %v; want 42", body["doubled"])
		}
	})
}
