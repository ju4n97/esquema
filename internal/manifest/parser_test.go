package manifest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ju4n97/hclapi/internal/config"
	"github.com/ju4n97/hclapi/internal/manifest"
)

// TestParser_ValidManifest verifies parsing of server, openapi, schemas, connections, and routes.
func TestParser_ValidManifest(t *testing.T) {
	t.Parallel()

	manifestContent := `
server {
  host = "0.0.0.0"
  port = 9000
}

openapi {
  title   = "Storefront API"
  version = "2.1.0"
}

connection "sql" "primary" {
  engine = "sqlite"
  source = "file::memory:?cache=shared"
}

schema "Item" {
  field "id" {
    type     = "integer"
    required = true
  }
  field "name" {
    type       = "string"
    required   = true
    min_length = 2
  }
}

route "GET /items" {
  summary = "List items"
  tag     = "inventory"

  step "sql" "fetch" {
    connection = "primary"
    query      = "SELECT id, name FROM items"
  }

  respond {
    status = 200
    schema = "[]Item"
    body   = steps.fetch.rows
  }
}
`

	tempDir := t.TempDir()
	filePath := filepath.Join(tempDir, "main.hcl")
	err := os.WriteFile(filePath, []byte(manifestContent), 0o600)
	if err != nil {
		t.Fatalf("failed to write manifest: %v", err)
	}

	cfg, err := manifest.Load(filePath)
	if err != nil {
		t.Fatalf("manifest.Load failed: %v", err)
	}

	if cfg.Server.Port != 9000 || cfg.Server.Host != "0.0.0.0" {
		t.Errorf("unexpected server settings: %+v", cfg.Server)
	}
	if cfg.OpenAPI.Title != "Storefront API" {
		t.Errorf("OpenAPI.Title = %q; want 'Storefront API'", cfg.OpenAPI.Title)
	}
	if len(cfg.Connections) != 1 || cfg.Connections["primary"].Type != config.ConnectionTypeSQL {
		t.Errorf("unexpected connections: %+v", cfg.Connections)
	}
	if len(cfg.Endpoints) != 1 {
		t.Fatalf("expected 1 endpoint, got %d", len(cfg.Endpoints))
	}

	ep := cfg.Endpoints[0]
	if ep.Method != "GET" || ep.Path != "/items" || ep.Summary != "List items" {
		t.Errorf("unexpected endpoint metadata: %+v", ep)
	}
	if len(ep.Pipeline) != 2 {
		t.Fatalf("expected 2 pipeline steps, got %d", len(ep.Pipeline))
	}
}

// TestParser_IntegrityFailures verifies compile-time validation for missing connections and schemas.
func TestParser_IntegrityFailures(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		hcl           string
		expectedError string
	}{
		{
			name: "unknown connection reference",
			hcl: `
route "GET /test" {
  step "sql" "query" {
    connection = "missing"
    query      = "SELECT 1"
  }
  respond {
    status = 200
  }
}`,
			expectedError: `references undeclared connection "missing"`,
		},
		{
			name: "unknown schema in response",
			hcl: `
route "GET /test" {
  respond {
    status = 200
    schema = "UnknownModel"
  }
}`,
			expectedError: `references unknown schema "UnknownModel"`,
		},
		{
			name: "invalid route label format",
			hcl: `
route "INVALID_LABEL_WITHOUT_PATH" {
  respond {
    status = 200
  }
}`,
			expectedError: `expected 'METHOD /path'`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tempDir := t.TempDir()
			filePath := filepath.Join(tempDir, "test.hcl")
			writeErr := os.WriteFile(filePath, []byte(tt.hcl), 0o600)
			if writeErr != nil {
				t.Fatalf("failed to write test file: %v", writeErr)
			}

			_, err := manifest.Load(filePath)
			if err == nil {
				t.Fatalf("expected compilation error containing %q, got nil", tt.expectedError)
			}
			if !strings.Contains(err.Error(), tt.expectedError) {
				t.Errorf("error = %q; want substring %q", err.Error(), tt.expectedError)
			}
		})
	}
}

// TestParser_UnknownNestedSchemaFailure verifies compile-time failure when fields reference unknown schemas.
func TestParser_UnknownNestedSchemaFailure(t *testing.T) {
	t.Parallel()

	m := `
schema "User" {
  field "profile" {
    type = "NonExistentProfile"
  }
}

route "GET /test" {
  respond {
    status = 200
  }
}
`
	_, err := manifest.Parse(m)
	if err == nil {
		t.Fatal("expected compile error for undeclared schema reference, got nil")
	}

	expectedSub := `references unknown schema "NonExistentProfile"`
	if !strings.Contains(err.Error(), expectedSub) {
		t.Errorf("error %q does not contain %q", err.Error(), expectedSub)
	}
}
