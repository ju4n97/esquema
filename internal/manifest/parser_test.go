package manifest

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/zclconf/go-cty/cty/function"
)

// TestParse_DirectKeywords verifies full compilation of manifests using direct action keywords.
func TestParse_DirectKeywords(t *testing.T) {
	t.Parallel()

	hclContent := `
server {
  host          = "0.0.0.0"
  port          = 9000
  read_timeout  = "20s"
  write_timeout = "30s"
  max_body_size = "25MB"
}

openapi {
  title       = "Storefront API"
  version     = "2.1.0"
  description = "Customer checkout services"
  servers = [
    {
      url         = "https://api.example.com/v1"
      description = "Production gateway"
    }
  ]
  tags = [
    {
      name        = "users"
      description = "User accounts"
    }
  ]
}

connection "sql" "main" {
  engine = "postgres"
  source = "postgres://user:pass@localhost:5432/store"
  pool {
    max_open = 50
    max_idle = 10
  }
}

connection "valkey" "cache" {
  url = "redis://127.0.0.1:6379"
}

schema "User" {
  description = "Registered user account"
  field "id" {
    type     = integer
    required = true
  }
  field "email" {
    type     = string
    format   = "email"
    required = true
  }
}

route "POST /users" {
  summary = "Create user"
  tag     = "users"

  request {
    body = User
  }

  sql "insert" {
    connection = "main"
    query      = "INSERT INTO users (email) VALUES (@email) RETURNING id, email"
    args = {
      email = ctx.request.body.email
    }
    catch {
      code   = "23505"
      status = 409
      body   = problem(409, "Email is already registered")
    }
  }

  respond {
    status = 201
    schema = User
    body   = steps.insert.row
  }
}

route "GET /analytics/export" {
  sql "events" {
    connection = "main"
    query      = "SELECT id, name FROM audit_events"
  }

  stream "ndjson" {
    source = steps.events.stream
  }
}
`

	m, err := Parse(hclContent)
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}

	t.Run("server attributes", func(t *testing.T) {
		t.Parallel()

		if m.Server.Host != "0.0.0.0" {
			t.Errorf("Host = %q, want '0.0.0.0'", m.Server.Host)
		}
		if m.Server.Port != 9000 {
			t.Errorf("Port = %d, want 9000", m.Server.Port)
		}
		if m.Server.ReadTimeout.Duration() != 20*time.Second {
			t.Errorf("ReadTimeout = %v, want 20s", m.Server.ReadTimeout)
		}
		if m.Server.WriteTimeout.Duration() != 30*time.Second {
			t.Errorf("WriteTimeout = %v, want 30s", m.Server.WriteTimeout)
		}
		if m.Server.MaxBodySize.Bytes() != 25*1000*1000 {
			t.Errorf("MaxBodySize = %d, want 25000000", m.Server.MaxBodySize.Bytes())
		}
	})

	t.Run("openapi attributes", func(t *testing.T) {
		t.Parallel()

		if m.OpenAPI.Title != "Storefront API" || m.OpenAPI.Version != "2.1.0" {
			t.Errorf("unexpected OpenAPI config: %+v", m.OpenAPI)
		}
		if len(m.OpenAPI.Servers) != 1 || m.OpenAPI.Servers[0].URL != "https://api.example.com/v1" {
			t.Errorf("unexpected OpenAPI servers: %+v", m.OpenAPI.Servers)
		}
		if len(m.OpenAPI.Tags) != 1 || m.OpenAPI.Tags[0].Name != "users" {
			t.Errorf("unexpected tags: %+v", m.OpenAPI.Tags)
		}
	})

	t.Run("connection attributes", func(t *testing.T) {
		t.Parallel()

		sqlConn, ok := m.Connections["main"]
		if !ok || sqlConn.Type != "sql" || sqlConn.Engine != "postgres" || sqlConn.MaxOpen != 50 {
			t.Errorf("unexpected sql connection: %+v", sqlConn)
		}

		valkeyConn, ok := m.Connections["cache"]
		if !ok || valkeyConn.Type != "valkey" || valkeyConn.Source != "redis://127.0.0.1:6379" {
			t.Errorf("unexpected valkey connection: %+v", valkeyConn)
		}
	})

	t.Run("schema compilation", func(t *testing.T) {
		t.Parallel()

		user, ok := m.Schemas["User"]
		if !ok || len(user.Fields) != 2 {
			t.Fatalf("expected User schema with 2 fields, got %+v", user)
		}

		idField := user.Fields["id"]
		if idField.Type.Type != TypeInteger || !idField.Required {
			t.Errorf("unexpected id field: %+v", idField)
		}

		emailField := user.Fields["email"]
		if emailField.Type.Type != TypeString || emailField.Format != FormatEmail {
			t.Errorf("unexpected email field: %+v", emailField)
		}
	})

	t.Run("route step sequences with direct keywords", func(t *testing.T) {
		t.Parallel()

		if len(m.Routes) != 2 {
			t.Fatalf("expected 2 routes, got %d", len(m.Routes))
		}

		postRoute := m.Routes[0]
		if postRoute.Pattern() != "POST /users" {
			t.Errorf("unexpected route pattern: %s", postRoute.Pattern())
		}
		if postRoute.Request == nil || postRoute.Request.BodyRef != "User" {
			t.Errorf("unexpected route request rules: %+v", postRoute.Request)
		}
		if len(postRoute.Steps) != 2 {
			t.Fatalf("expected 2 steps in POST /users, got %d", len(postRoute.Steps))
		}

		sqlStep, ok := postRoute.Steps[0].(*StepSQL)
		if !ok || sqlStep.Name != "insert" || sqlStep.Connection != "main" {
			t.Fatalf("step[0] = %+v, want *StepSQL", postRoute.Steps[0])
		}
		if len(sqlStep.Catches) != 1 || sqlStep.Catches[0].Code != "23505" || sqlStep.Catches[0].Status != 409 {
			t.Errorf("unexpected sql catch block: %+v", sqlStep.Catches)
		}

		respondStep, ok := postRoute.Steps[1].(*StepRespond)
		if !ok || respondStep.Status != 201 || respondStep.Schema == nil || respondStep.Schema.SchemaRef != "User" {
			t.Fatalf("step[1] = %+v, want *StepRespond with User schema", postRoute.Steps[1])
		}

		getRoute := m.Routes[1]
		if getRoute.Pattern() != "GET /analytics/export" {
			t.Errorf("unexpected route pattern: %s", getRoute.Pattern())
		}
		if len(getRoute.Steps) != 2 {
			t.Fatalf("expected 2 steps in GET /analytics/export, got %d", len(getRoute.Steps))
		}

		streamStep, ok := getRoute.Steps[1].(*StepStream)
		if !ok || streamStep.Format != "ndjson" {
			t.Fatalf("step[1] = %+v, want *StepStream with format 'ndjson'", getRoute.Steps[1])
		}
	})
}

// TestParse_LexicalOrdering verifies that mixed steps, responds, and streams retain exact file order.
func TestParse_LexicalOrdering(t *testing.T) {
	t.Parallel()

	hclContent := `
connection "sql" "main" {
  engine = "sqlite"
  source = ":memory:"
}

route "GET /order-check/{id}" {
  sql "lookup" {
    connection = "main"
    query      = "SELECT id FROM orders WHERE id = :id"
  }

  respond {
    when   = steps.lookup.rows_affected == 0
    status = 404
  }

  sql "audit_log" {
    connection = "main"
    query      = "INSERT INTO audit_logs (id) VALUES (:id)"
  }

  respond {
    status = 200
  }
}
`

	m, err := Parse(hclContent)
	if err != nil {
		t.Fatalf("Parse() unexpected error: %v", err)
	}

	if len(m.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(m.Routes))
	}

	steps := m.Routes[0].Steps
	if len(steps) != 4 {
		t.Fatalf("expected 4 steps, got %d", len(steps))
	}

	if s, ok := steps[0].(*StepSQL); !ok || s.Name != "lookup" {
		t.Errorf("step[0] = %T (%+v), want *StepSQL named 'lookup'", steps[0], steps[0])
	}

	if s, ok := steps[1].(*StepRespond); !ok || s.Status != 404 {
		t.Errorf("step[1] = %T (%+v), want *StepRespond with status 404", steps[1], steps[1])
	}

	if s, ok := steps[2].(*StepSQL); !ok || s.Name != "audit_log" {
		t.Errorf("step[2] = %T (%+v), want *StepSQL named 'audit_log'", steps[2], steps[2])
	}

	if s, ok := steps[3].(*StepRespond); !ok || s.Status != 200 {
		t.Errorf("step[3] = %T (%+v), want *StepRespond with status 200", steps[3], steps[3])
	}
}

// TestParse_CustomStepRegistration verifies that embedders can register custom direct keywords.
func TestParse_CustomStepRegistration(t *testing.T) {
	t.Parallel()

	reg := DefaultStepRegistry()

	customKafkaDecoder := func(label string, body hcl.Body, evalCtx *hcl.EvalContext, funcs map[string]function.Function) (Step, error) {
		var raw struct {
			Topic       string         `hcl:"topic"`
			PayloadExpr hcl.Expression `hcl:"payload"`
		}
		if diags := gohcl.DecodeBody(body, evalCtx, &raw); diags.HasErrors() {
			return nil, diags
		}

		return &mockStep{
			name: label,
		}, nil
	}

	reg.Register("kafka", []string{"name"}, customKafkaDecoder)

	customParser := NewParser(reg)

	hclContent := `
route "POST /events" {
  kafka "publish" {
    topic   = "orders.created"
    payload = "payload_data"
  }

  respond {
    status = 202
  }
}
`

	m, err := customParser.Parse(hclContent)
	if err != nil {
		t.Fatalf("customParser.Parse() error: %v", err)
	}

	if len(m.Routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(m.Routes))
	}

	route := m.Routes[0]
	if len(route.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(route.Steps))
	}

	kafkaStep, ok := route.Steps[0].(*mockStep)
	if !ok || kafkaStep.name != "publish" {
		t.Fatalf("step[0] = %+v, want *mockStep named 'publish'", route.Steps[0])
	}
}

// TestParse_InvalidManifests verifies syntax diagnostics, invalid configurations, and referential failures.
func TestParse_InvalidManifests(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		source    string
		errSubstr string
	}{
		{
			name:      "empty manifest source",
			source:    "    ",
			errSubstr: "cannot be empty",
		},
		{
			name:      "syntax error unclosed brace",
			source:    "server {",
			errSubstr: "parse manifest",
		},
		{
			name: "malformed route label missing path",
			source: `
route "INVALID_NO_PATH" {
  respond { status = 200 }
}`,
			errSubstr: "expected 'METHOD /path'",
		},
		{
			name: "non-canonical mysql engine alias",
			source: `
connection "sql" "db" {
  engine = "mariadb"
  source = "root@tcp(127.0.0.1:3306)/test"
}`,
			errSubstr: "use canonical engine name \"mysql\"",
		},
		{
			name: "unsupported connection type",
			source: `
connection "grpc" "svc" {
  url = "localhost:50051"
}`,
			errSubstr: "invalid type \"grpc\"",
		},
		{
			name: "invalid read_timeout unit format",
			source: `
server {
  read_timeout = "20weeks"
}`,
			errSubstr: "server.read_timeout: invalid duration",
		},
		{
			name: "invalid max_body_size byte unit",
			source: `
server {
  max_body_size = "100ZB"
}`,
			errSubstr: "unrecognized byte unit \"ZB\"",
		},
		{
			name: "unregistered direct keyword inside route",
			source: `
route "GET /test" {
  unregistered_action "call" {
    param = "value"
  }
  respond { status = 200 }
}`,
			errSubstr: "unregistered_action",
		},
		{
			name: "sql step references missing connection pool",
			source: `
route "GET /data" {
  sql "fetch" {
    connection = "missing_db"
    query      = "SELECT 1"
  }
  respond { status = 200 }
}`,
			errSubstr: "references unknown connection \"missing_db\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := Parse(tt.source)
			if err == nil {
				t.Fatalf("Parse() expected error containing %q, got nil", tt.errSubstr)
			}
			if !strings.Contains(err.Error(), tt.errSubstr) {
				t.Fatalf("error %q does not contain expected substring %q", err.Error(), tt.errSubstr)
			}
		})
	}
}

// TestLoad verifies multi-file directory traversal, .hclapiignore pruning, and project merging.
func TestLoad(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	files := map[string]string{
		"server.hcl": `
server {
  port = 8085
}
`,
		"connections.hcl": `
connection "sql" "primary" {
  engine = "sqlite"
  source = ":memory:"
}
`,
		"schemas/user.hcl": `
schema "User" {
  field "name" {
    type = string
  }
}
`,
		"routes/users.hcl": `
route "GET /users" {
  respond {
    status = 200
    schema = list(User)
  }
}
`,
	}

	for rel, content := range files {
		abs := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o750); err != nil {
			t.Fatalf("failed to create directory: %v", err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o600); err != nil {
			t.Fatalf("failed to write file %q: %v", rel, err)
		}
	}

	m, err := Load(dir)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if m.Server.Port != 8085 {
		t.Errorf("Server.Port = %d, want 8085", m.Server.Port)
	}
	if _, ok := m.Connections["primary"]; !ok {
		t.Error("expected 'primary' connection to be loaded")
	}
	if _, ok := m.Schemas["User"]; !ok {
		t.Error("expected 'User' schema to be loaded")
	}
	if len(m.Routes) != 1 || m.Routes[0].Pattern() != "GET /users" {
		t.Errorf("unexpected routes: %+v", m.Routes)
	}
}
