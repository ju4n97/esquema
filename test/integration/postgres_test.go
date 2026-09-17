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
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/ju4n97/hclapi"
)

// TestPostgres_Integration verifies live PostgreSQL connections, $1 parameter rewriting, and catch "23505".
func TestPostgres_Integration(t *testing.T) {
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx,
		"postgres:17-alpine",
		postgres.WithDatabase("hclapitest"),
		postgres.WithUsername("hclapi"),
		postgres.WithPassword("secret"),
		postgres.WithInitScripts(),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(15*time.Second),
		),
	)
	if err != nil {
		t.Fatalf("failed to start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = pgContainer.Terminate(ctx) })

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("failed to obtain postgres connection string: %v", err)
	}

	manifestContent := fmt.Sprintf(`
server {
  host = "127.0.0.1"
  port = 8080
}

connection "sql" "primary" {
  engine = "postgres"
  source = %q
}

route "POST /init" {
  step "sql" "schema" {
    connection = "primary"
    query      = <<-SQL
      CREATE TABLE IF NOT EXISTS members (
        id SERIAL PRIMARY KEY,
        email VARCHAR(255) UNIQUE NOT NULL
      );
    SQL
  }

  respond {
    status = 200
  }
}

route "POST /members" {
  request {
    body {
      field "email" {
        type     = "string"
        format   = "email"
        required = true
      }
    }
  }

  step "sql" "insert" {
    connection = "primary"
    query      = "INSERT INTO members (email) VALUES (@email) RETURNING id, email"
    args = {
      email = ctx.request.body.email
    }
    catch "23505" {
      status = 409
      body   = problem(409, "Email address already registered")
    }
  }

  respond {
    status = 201
    body   = steps.insert.row
  }
}
`, connStr)

	cfg, err := hclapi.Parse(manifestContent)
	if err != nil {
		t.Fatalf("failed to parse manifest: %v", err)
	}

	eng, err := hclapi.New(cfg)
	if err != nil {
		t.Fatalf("failed to initialize engine: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	initReq := httptest.NewRequest(http.MethodPost, "/init", http.NoBody)
	initRec := httptest.NewRecorder()
	eng.ServeHTTP(initRec, initReq)
	if initRec.Code != http.StatusOK {
		t.Fatalf("table creation failed with status: %d", initRec.Code)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/members", strings.NewReader(`{"email":"admin@example.com"}`))
	createReq.Header.Set("Content-Type", "application/json")
	createRec := httptest.NewRecorder()
	eng.ServeHTTP(createRec, createReq)

	if createRec.Code != http.StatusCreated {
		t.Fatalf("expected 201 Created, got: %d", createRec.Code)
	}

	duplicateReq := httptest.NewRequest(http.MethodPost, "/members", strings.NewReader(`{"email":"admin@example.com"}`))
	duplicateReq.Header.Set("Content-Type", "application/json")
	duplicateRec := httptest.NewRecorder()
	eng.ServeHTTP(duplicateRec, duplicateReq)

	if duplicateRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 Conflict, got: %d", duplicateRec.Code)
	}

	var p map[string]any
	err = json.NewDecoder(duplicateRec.Body).Decode(&p)
	if err != nil {
		t.Fatalf("failed to decode conflict problem response: %v", err)
	}
	if p["detail"] != "Email address already registered" {
		t.Errorf("detail = %v; want 'Email address already registered'", p["detail"])
	}
}
