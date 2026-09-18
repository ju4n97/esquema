package manifest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/valkey-io/valkey-go"
	_ "modernc.org/sqlite"

	"github.com/ju4n97/hclapi/internal/problem"
)

type testExecutionContext struct {
	db       *sql.DB
	scope    Scope
	recorder *httptest.ResponseRecorder
	schemas  map[string]Schema
}

func (c *testExecutionContext) SQL(name string) (*sql.DB, error) {
	if c.db == nil {
		return nil, sql.ErrConnDone
	}
	return c.db, nil
}

func (c *testExecutionContext) Valkey(name string) (valkey.Client, error) {
	return nil, nil
}

func (c *testExecutionContext) GoHandler(name string) (StepHandler, error) {
	return nil, nil
}

func (c *testExecutionContext) HTTPClient() *http.Client {
	return http.DefaultClient
}

func (c *testExecutionContext) ResponseWriter() http.ResponseWriter {
	return c.recorder
}

func (c *testExecutionContext) ResponseController() *http.ResponseController {
	return http.NewResponseController(c.recorder)
}

func (c *testExecutionContext) OpenAPISpec(format string) ([]byte, string, error) {
	return nil, "", nil
}

func (c *testExecutionContext) Scope() Scope {
	return c.scope
}

func (c *testExecutionContext) Schemas() map[string]Schema {
	return c.schemas
}

// TestStepSQL_ValidateStep verifies compile-time configuration checks and dialect assignment.
func TestStepSQL_ValidateStep(t *testing.T) {
	t.Parallel()

	manifest := &Manifest{
		Connections: map[string]Connection{
			"valid_pg": {
				Name:   "valid_pg",
				Type:   "sql",
				Engine: "postgres",
			},
			"valid_mysql": {
				Name:   "valid_mysql",
				Type:   "sql",
				Engine: "mysql",
			},
			"cache_conn": {
				Name: "cache_conn",
				Type: "valkey",
			},
		},
	}

	tests := []struct {
		name      string
		step      *StepSQL
		wantError bool
		errSubstr string
	}{
		{
			name: "missing connection identifier",
			step: &StepSQL{
				Name:       "query",
				Connection: "",
				Query:      "SELECT 1",
			},
			wantError: true,
			errSubstr: "missing connection identifier",
		},
		{
			name: "unknown connection identifier",
			step: &StepSQL{
				Name:       "query",
				Connection: "missing_pool",
				Query:      "SELECT 1",
			},
			wantError: true,
			errSubstr: "references unknown connection",
		},
		{
			name: "connection is not sql type",
			step: &StepSQL{
				Name:       "query",
				Connection: "cache_conn",
				Query:      "SELECT 1",
			},
			wantError: true,
			errSubstr: "cannot use non-sql connection",
		},
		{
			name: "query is empty",
			step: &StepSQL{
				Name:       "query",
				Connection: "valid_pg",
				Query:      "   ",
			},
			wantError: true,
			errSubstr: "query cannot be empty",
		},
		{
			name: "catch block missing all criteria",
			step: &StepSQL{
				Name:       "query",
				Connection: "valid_pg",
				Query:      "SELECT 1",
				Catches: []SQLCatch{
					{Status: 400},
				},
			},
			wantError: true,
			errSubstr: "must define at least one of 'code', 'constraint', or 'match'",
		},
		{
			name: "catch block status out of range",
			step: &StepSQL{
				Name:       "query",
				Connection: "valid_pg",
				Query:      "SELECT 1",
				Catches: []SQLCatch{
					{Code: "23505", Status: 999},
				},
			},
			wantError: true,
			errSubstr: "catch status 999 outside valid HTTP range",
		},
		{
			name: "valid postgres step compiles successfully",
			step: &StepSQL{
				Name:       "query",
				Connection: "valid_pg",
				Query:      "SELECT id, email FROM users WHERE id = @id",
			},
			wantError: false,
		},
		{
			name: "valid mysql step compiles placeholders at boot time",
			step: &StepSQL{
				Name:       "query",
				Connection: "valid_mysql",
				Query:      "SELECT * FROM items WHERE status = @status AND category = @cat",
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.step.ValidateStep(manifest)
			if (err != nil) != tt.wantError {
				t.Fatalf("ValidateStep() error = %v, wantError = %v", err, tt.wantError)
			}

			if tt.wantError && tt.errSubstr != "" {
				if !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("error %q does not contain substring %q", err.Error(), tt.errSubstr)
				}
				return
			}

			if !tt.wantError && tt.step.dialect == "mysql" {
				if tt.step.compiledQuery != "SELECT * FROM items WHERE status = ? AND category = ?" {
					t.Fatalf("unexpected compiled query: %q", tt.step.compiledQuery)
				}
				expectedOrder := []string{"status", "cat"}
				if !reflect.DeepEqual(tt.step.paramOrder, expectedOrder) {
					t.Fatalf("paramOrder = %v, want %v", tt.step.paramOrder, expectedOrder)
				}
			}
		})
	}
}

// TestStepSQL_ResolveQueryAndArgs verifies native positional pass-through and dialect-specific binding.
func TestStepSQL_ResolveQueryAndArgs(t *testing.T) {
	t.Parallel()

	t.Run("positional slice arguments pass query untouched", func(t *testing.T) {
		t.Parallel()

		step := &StepSQL{
			Query:   "SELECT * FROM users WHERE id = $1 AND email = $2",
			dialect: "postgres",
		}

		argsList := []any{int64(42), "user@example.com"}
		query, bound, err := step.resolveQueryAndArgs(argsList)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if query != step.Query {
			t.Errorf("query modified unexpectedly: got %q, want %q", query, step.Query)
		}
		if !reflect.DeepEqual(bound, argsList) {
			t.Errorf("bound arguments = %v, want %v", bound, argsList)
		}
	})

	t.Run("postgres named args uses pgx NamedArgs directly", func(t *testing.T) {
		t.Parallel()

		step := &StepSQL{
			Query:   "SELECT id::text, @email::text FROM users WHERE id = @id",
			dialect: "postgres",
		}

		argsMap := map[string]any{"id": int64(10), "email": "test@domain.com"}
		query, bound, err := step.resolveQueryAndArgs(argsMap)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if query != step.Query {
			t.Errorf("postgres query was altered: %q", query)
		}

		if len(bound) != 1 {
			t.Fatalf("expected 1 bound argument, got %d", len(bound))
		}

		namedArgs, ok := bound[0].(pgx.NamedArgs)
		if !ok {
			t.Fatalf("expected argument of type pgx.NamedArgs, got %T", bound[0])
		}

		if namedArgs["id"] != int64(10) || namedArgs["email"] != "test@domain.com" {
			t.Errorf("unexpected NamedArgs contents: %+v", namedArgs)
		}
	})

	t.Run("sqlite named args delegates to sql.Named", func(t *testing.T) {
		t.Parallel()

		step := &StepSQL{
			Query:   "SELECT * FROM contacts WHERE id = @id",
			dialect: "sqlite",
		}

		argsMap := map[string]any{"id": int64(55)}
		query, bound, err := step.resolveQueryAndArgs(argsMap)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if query != step.Query {
			t.Errorf("sqlite query modified: %q", query)
		}

		if len(bound) != 1 {
			t.Fatalf("expected 1 named argument, got %d", len(bound))
		}

		namedArg, ok := bound[0].(sql.NamedArg)
		if !ok || namedArg.Name != "id" || namedArg.Value != int64(55) {
			t.Errorf("unexpected sql.NamedArg: %+v", bound[0])
		}
	})

	t.Run("mysql compiles query and slices map positionally", func(t *testing.T) {
		t.Parallel()

		step := &StepSQL{
			Query:         "SELECT * FROM logs WHERE level = @lvl AND service = @svc",
			compiledQuery: "SELECT * FROM logs WHERE level = ? AND service = ?",
			paramOrder:    []string{"lvl", "svc"},
			dialect:       "mysql",
		}

		argsMap := map[string]any{
			"lvl": "error",
			"svc": "auth-service",
		}

		query, bound, err := step.resolveQueryAndArgs(argsMap)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if query != step.compiledQuery {
			t.Errorf("query = %q, want %q", query, step.compiledQuery)
		}

		expectedBound := []any{"error", "auth-service"}
		if !reflect.DeepEqual(bound, expectedBound) {
			t.Errorf("bound = %v, want %v", bound, expectedBound)
		}
	})
}

// TestCompileMySQLPlaceholders verifies regex edge cases on comments and string literals.
func TestCompileMySQLPlaceholders(t *testing.T) {
	t.Parallel()

	query := `
		-- Contact admin@example.com for support
		/* Multi-line comment with @ignored_param */
		SELECT id, email, '@not_a_param' AS literal
		FROM users
		WHERE email = @email AND age >= @min_age
	`

	rewritten, order := compileMySQLPlaceholders(query)

	if !strings.Contains(rewritten, "admin@example.com") {
		t.Errorf("literal in comment was corrupted: %s", rewritten)
	}

	if !strings.Contains(rewritten, "'@not_a_param'") {
		t.Errorf("single-quoted literal string was corrupted: %s", rewritten)
	}

	if !strings.Contains(rewritten, "WHERE email = ? AND age >= ?") {
		t.Errorf("parameters were not rewritten to ? correctly: %s", rewritten)
	}

	expectedOrder := []string{"email", "min_age"}
	if !reflect.DeepEqual(order, expectedOrder) {
		t.Fatalf("param order = %v, want %v", order, expectedOrder)
	}
}

// TestStepSQL_ExecuteStep_SQLite verifies real statement execution, scanning, and catch blocks.
func TestStepSQL_ExecuteStep_SQLite(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open sqlite database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	schema := `
		CREATE TABLE items (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			sku TEXT UNIQUE NOT NULL,
			price REAL NOT NULL
		);
		INSERT INTO items (sku, price) VALUES ('SKU-100', 19.99);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("failed to seed test schema: %v", err)
	}

	t.Run("query producing rows scans dataset correctly", func(t *testing.T) {
		t.Parallel()

		step := &StepSQL{
			Name:       "fetch",
			Connection: "main",
			Query:      "SELECT id, sku, price FROM items WHERE sku = @sku",
			dialect:    "sqlite",
			Args:       NewExpr(parseExpr(t, `{ sku = "SKU-100" }`), nil),
		}

		exec := &testExecutionContext{
			db:       db,
			recorder: httptest.NewRecorder(),
		}

		result, err := step.ExecuteStep(context.Background(), exec)
		if err != nil {
			t.Fatalf("ExecuteStep() error: %v", err)
		}

		resMap, ok := result.(map[string]any)
		if !ok {
			t.Fatalf("expected map[string]any output, got %T", result)
		}

		if resMap["rows_affected"] != 1 {
			t.Errorf("rows_affected = %v, want 1", resMap["rows_affected"])
		}

		row, ok := resMap["row"].(map[string]any)
		if !ok || row["sku"] != "SKU-100" {
			t.Errorf("unexpected first row: %+v", resMap["row"])
		}
	})

	t.Run("non-row producing statement records rows_affected", func(t *testing.T) {
		t.Parallel()

		step := &StepSQL{
			Name:       "update_item",
			Connection: "main",
			Query:      "UPDATE items SET price = 24.99 WHERE sku = @sku",
			dialect:    "sqlite",
			Args:       NewExpr(parseExpr(t, `{ sku = "SKU-100" }`), nil),
		}

		exec := &testExecutionContext{
			db:       db,
			recorder: httptest.NewRecorder(),
		}

		result, err := step.ExecuteStep(context.Background(), exec)
		if err != nil {
			t.Fatalf("ExecuteStep() error: %v", err)
		}

		resMap := result.(map[string]any)
		rows, ok := resMap["rows"].([]map[string]any)
		if !ok || len(rows) != 0 {
			t.Errorf("expected empty rows slice, got: %v", resMap["rows"])
		}
		if resMap["row"] != nil {
			t.Errorf("expected row = nil, got %v", resMap["row"])
		}
	})

	t.Run("catches sqlite constraint code and streams problem response", func(t *testing.T) {
		t.Parallel()

		step := &StepSQL{
			Name:       "insert_duplicate",
			Connection: "main",
			Query:      "INSERT INTO items (sku, price) VALUES (@sku, @price)",
			dialect:    "sqlite",
			Args:       NewExpr(parseExpr(t, `{ sku = "SKU-100", price = 9.99 }`), nil),
			Catches: []SQLCatch{
				{
					Code:   "19",
					Status: http.StatusConflict,
					Body:   NewExpr(parseExpr(t, `problem(409, "SKU already registered")`), runtimeExprFunctions()),
				},
			},
		}

		rec := httptest.NewRecorder()
		exec := &testExecutionContext{
			db:       db,
			recorder: rec,
		}

		_, err := step.ExecuteStep(context.Background(), exec)
		if !errors.Is(err, ErrPipelineHalted) {
			t.Fatalf("expected ErrPipelineHalted, got: %v", err)
		}

		if rec.Code != http.StatusConflict {
			t.Fatalf("HTTP Status = %d, want 409", rec.Code)
		}

		if ct := rec.Header().Get("Content-Type"); ct != problem.ContentType {
			t.Errorf("Content-Type = %q, want %q", ct, problem.ContentType)
		}

		var p problem.Problem
		if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
			t.Fatalf("failed to decode response problem details: %v", err)
		}

		if p.Status != 409 || p.Detail != "SKU already registered" {
			t.Errorf("unexpected problem details payload: %+v", p)
		}
	})

	t.Run("catches error by message substring match", func(t *testing.T) {
		t.Parallel()

		step := &StepSQL{
			Name:       "insert_duplicate_match",
			Connection: "main",
			Query:      "INSERT INTO items (sku, price) VALUES (@sku, @price)",
			dialect:    "sqlite",
			Args:       NewExpr(parseExpr(t, `{ sku = "SKU-100", price = 9.99 }`), nil),
			Catches: []SQLCatch{
				{
					Match:  "UNIQUE constraint failed",
					Status: http.StatusConflict,
				},
			},
		}

		rec := httptest.NewRecorder()
		exec := &testExecutionContext{
			db:       db,
			recorder: rec,
		}

		_, err := step.ExecuteStep(context.Background(), exec)
		if !errors.Is(err, ErrPipelineHalted) {
			t.Fatalf("expected ErrPipelineHalted, got: %v", err)
		}

		if rec.Code != http.StatusConflict {
			t.Fatalf("HTTP status = %d, want 409", rec.Code)
		}
	})

	t.Run("unhandled database error returns standard error", func(t *testing.T) {
		t.Parallel()

		step := &StepSQL{
			Name:       "broken_statement",
			Connection: "main",
			Query:      "SELECT * FROM non_existent_table",
			dialect:    "sqlite",
		}

		exec := &testExecutionContext{
			db:       db,
			recorder: httptest.NewRecorder(),
		}

		_, err := step.ExecuteStep(context.Background(), exec)
		if err == nil || errors.Is(err, ErrPipelineHalted) {
			t.Fatalf("expected standard unhandled database error, got: %v", err)
		}
	})
}
