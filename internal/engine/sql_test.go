package engine_test

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/ju4n97/hclapi"
)

// TestEngine_SQLExecution verifies dialect rewriting, multi-line comment stripping, and error code catches.
func TestEngine_SQLExecution(t *testing.T) {
	t.Parallel()

	const dbSource = "file:sql_exec_test_db?mode=memory&cache=shared"
	db, err := sql.Open("sqlite", dbSource)
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	setupSQL := `
		CREATE TABLE items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			sku TEXT UNIQUE NOT NULL,
			stock INTEGER NOT NULL
		);
		INSERT INTO items (sku, stock) VALUES ('SKU-100', 50);
	`
	if _, err := db.Exec(setupSQL); err != nil {
		t.Fatalf("failed to setup table: %v", err)
	}

	manifestContent := fmt.Sprintf(`
server {
  host = "127.0.0.1"
  port = 8080
}

connection "sql" "main" {
  engine = "sqlite"
  source = %q
}

route "POST /items" {
  request {
    body {
      field "sku" {
        type     = "string"
        required = true
      }
      field "stock" {
        type     = "integer"
        required = true
      }
    }
  }

  step "sql" "insert" {
    connection = "main"
    query      = <<-SQL
      -- Insert item with comment block
      /* Multi-line comment with @fake_param */
      INSERT INTO items (sku, stock)
      VALUES (@sku, @stock)
      RETURNING id, sku, stock
    SQL
    args = {
      sku   = ctx.request.body.sku
      stock = ctx.request.body.stock
    }
    catch "19" {
      status = 409
      body   = problem(409, "SKU already registered")
    }
  }

  respond {
    status = 201
    body   = steps.insert.row
  }
}
`, dbSource)

	cfg, err := hclapi.Parse(manifestContent)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	eng, err := hclapi.New(cfg)
	if err != nil {
		t.Fatalf("engine initialization failed: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	t.Run("successfully inserts with comments and returning clause", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader(`{"sku":"SKU-200","stock":10}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		eng.ServeHTTP(rec, req)

		if rec.Code != http.StatusCreated {
			t.Errorf("status = %d; want 201", rec.Code)
		}
	})

	t.Run("catches sqlite constraint code 19 and returns problem details", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/items", strings.NewReader(`{"sku":"SKU-100","stock":5}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		eng.ServeHTTP(rec, req)

		if rec.Code != http.StatusConflict {
			t.Errorf("status = %d; want 409", rec.Code)
		}
	})
}

// TestEngine_SQL_LexerEdgeCases verifies parameter rewriting without corrupting string literals or operators.
func TestEngine_SQL_LexerEdgeCases(t *testing.T) {
	t.Parallel()

	const dbSource = "file:sql_lexer_test_db?mode=memory&cache=shared"
	manifestContent := fmt.Sprintf(`
server {
  host = "127.0.0.1"
  port = 8080
}

connection "sql" "main" {
  engine = "sqlite"
  source = %q
}

route "POST /test-sql" {
  step "sql" "query" {
    connection = "main"
    query      = <<-SQL
      -- Contact developer at support@example.com
      /* Check @todo later */
      SELECT id, email, '@not_a_param' AS literal
      FROM contacts
      WHERE email = 'alice@example.com'
        AND status = @status
        AND age >= @min_age
    SQL
    args = {
      status  = "active"
      min_age = 21
    }
  }

  respond {
    status = 200
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

	db, ok := eng.SQL("main")
	if !ok {
		t.Fatal("expected 'main' sql database to be present")
	}

	setup := `
		CREATE TABLE contacts (
			id INTEGER PRIMARY KEY,
			email TEXT,
			status TEXT,
			age INTEGER
		);
		INSERT INTO contacts VALUES (1, 'alice@example.com', 'active', 25);
	`
	if _, err := db.Exec(setup); err != nil {
		t.Fatalf("failed to seed table: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/test-sql", http.NoBody)
	rec := httptest.NewRecorder()

	eng.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200", rec.Code)
	}
}
