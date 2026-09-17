package engine_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ju4n97/hclapi"
	"github.com/ju4n97/hclapi/internal/problem"
)

// TestEngine_IngressValidation verifies path, query, header, and body constraints and defaults.
func TestEngine_IngressValidation(t *testing.T) {
	t.Parallel()

	manifest := `
server {
  host = "127.0.0.1"
  port = 8080
}

route "POST /accounts/{id}" {
  request {
    path "id" {
      type     = "integer"
      required = true
    }
    header "x-api-key" {
      type     = "string"
      format   = "uuid"
      required = true
    }
    query "channel" {
      type    = "string"
      default = "web"
      enum    = ["web", "mobile"]
    }
    body {
      field "email" {
        type     = "string"
        format   = "email"
        required = true
      }
      field "username" {
        type       = "string"
        min_length = 3
        required   = true
      }
      field "age" {
        type     = "integer"
        min      = 18
        required = true
      }
      field "role" {
        type    = "string"
        default = "member"
      }
    }
  }

  respond {
    status = 201
  }
}
`

	cfg, err := hclapi.Parse(manifest)
	if err != nil {
		t.Fatalf("failed to parse manifest: %v", err)
	}

	eng, err := hclapi.New(cfg)
	if err != nil {
		t.Fatalf("engine init failed: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	tests := []struct {
		name           string
		targetURL      string
		headers        map[string]string
		bodyJSON       string
		expectedStatus int
	}{
		{
			name:      "valid payload passes validation",
			targetURL: "/accounts/101?channel=mobile",
			headers: map[string]string{
				"X-Api-Key": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
			},
			bodyJSON:       `{"email":"dev@example.com","username":"johndoe","age":24}`,
			expectedStatus: http.StatusCreated,
		},
		{
			name:      "invalid path parameter returns 422",
			targetURL: "/accounts/not-an-int",
			headers: map[string]string{
				"X-Api-Key": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
			},
			bodyJSON:       `{"email":"dev@example.com","username":"johndoe","age":24}`,
			expectedStatus: http.StatusUnprocessableEntity,
		},
		{
			name:      "missing required fields in body returns 422",
			targetURL: "/accounts/101",
			headers: map[string]string{
				"X-Api-Key": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
			},
			bodyJSON:       `{"email":"invalid-email"}`,
			expectedStatus: http.StatusUnprocessableEntity,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodPost, tt.targetURL, strings.NewReader(tt.bodyJSON))
			req.Header.Set("Content-Type", "application/json")
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}
			rec := httptest.NewRecorder()

			eng.ServeHTTP(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("status = %d; want %d", rec.Code, tt.expectedStatus)
			}
		})
	}
}

// TestEngine_Validate_StrictTypesAndMalformedJSON verifies 400 on malformed JSON and 422 on type mismatches.
func TestEngine_Validate_StrictTypesAndMalformedJSON(t *testing.T) {
	t.Parallel()

	manifest := `
server {
  host = "127.0.0.1"
  port = 8080
}

route "POST /validate-types" {
  request {
    body {
      field "name" {
        type     = "string"
        required = true
      }
    }
  }

  respond {
    status = 200
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

	t.Run("fails 422 when integer is passed to string field", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/validate-types", strings.NewReader(`{"name": 12345}`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		eng.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("status = %d; want 422 Unprocessable Entity", rec.Code)
		}
	})

	t.Run("fails 400 when body contains malformed JSON", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/validate-types", strings.NewReader(`{"name":`))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		eng.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d; want 400 Bad Request", rec.Code)
		}
	})
}

// TestEngine_Validate_PathAndQueryCoercion verifies that path and query inputs are typed in HCL.
func TestEngine_Validate_PathAndQueryCoercion(t *testing.T) {
	t.Parallel()

	manifest := `
server {
  host = "127.0.0.1"
  port = 8080
}

route "GET /items/{id}" {
  request {
    path "id" {
      type     = "integer"
      required = true
    }
    query "limit" {
      type    = "integer"
      default = 25
    }
  }

  respond {
    when   = ctx.request.path.id == 42 && ctx.request.query.limit == 25
    status = 200
    body   = {"matched": true}
  }

  respond {
    status = 400
    body   = {"matched": false}
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

	req := httptest.NewRequest(http.MethodGet, "/items/42", http.NoBody)
	rec := httptest.NewRecorder()

	eng.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d; want 200 OK (conditional failed)", rec.Code)
	}
}

// TestEngine_Validate_MaxBodySizeBoundary verifies rejection of oversized payloads with 413.
func TestEngine_Validate_MaxBodySizeBoundary(t *testing.T) {
	t.Parallel()

	manifest := `
server {
  host          = "127.0.0.1"
  port          = 8080
  max_body_size = "1KB"
}

route "POST /upload" {
  respond {
    status = 200
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

	t.Run("accepts payload within limit", func(t *testing.T) {
		payload := strings.Repeat("a", 500)
		req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader(payload))
		rec := httptest.NewRecorder()

		eng.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("status = %d; want 200", rec.Code)
		}
	})

	t.Run("rejects payload exceeding limit with 413", func(t *testing.T) {
		payload := strings.Repeat("a", 1500)
		req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader(payload))
		rec := httptest.NewRecorder()

		eng.ServeHTTP(rec, req)

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("status = %d; want 413", rec.Code)
		}

		var p problem.Problem
		_ = json.NewDecoder(rec.Body).Decode(&p)
		if p.Status != 413 {
			t.Errorf("problem status = %d; want 413", p.Status)
		}
	})
}

// TestEngine_Validate_DeepNestedSchemaValidation verifies that invalid types in deep nested schemas
// return 422 Unprocessable Entity with exact parameter paths.
func TestEngine_Validate_DeepNestedSchemaValidation(t *testing.T) {
	t.Parallel()

	manifest := `
server {
  host = "127.0.0.1"
  port = 8080
}

schema "DocumentLink" {
  field "id" {
    type     = "integer"
    required = true
  }
  field "download_url" {
    type   = "string"
    format = "uri"
  }
}

schema "Study" {
  field "id" {
    type     = "integer"
    required = true
  }
  field "carpals" {
    type = "[]DocumentLink"
  }
}

schema "WebhookPayload" {
  field "data" {
    type     = "Study"
    required = true
  }
}

route "POST /webhook" {
  request {
    body = WebhookPayload
  }

  respond {
    status = 200
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

	// Send a payload where carpals[0].download_url is a number instead of a string
	badPayload := `{
		"data": {
			"id": 100,
			"carpals": [
				{
					"id": 1,
					"download_url": 0
				}
			]
		}
	}`

	req := httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(badPayload))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	eng.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d; want 422 Unprocessable Entity. Body: %s", rec.Code, rec.Body.String())
	}

	var p problem.Problem
	if err := json.NewDecoder(rec.Body).Decode(&p); err != nil {
		t.Fatalf("failed to decode problem JSON: %v", err)
	}

	if len(p.InvalidParams) != 1 {
		t.Fatalf("expected 1 invalid param, got %d: %+v", len(p.InvalidParams), p.InvalidParams)
	}

	ip := p.InvalidParams[0]
	expectedName := "body.data.carpals[0].download_url"
	if ip.Name != expectedName {
		t.Errorf("invalid param name = %q; want %q", ip.Name, expectedName)
	}
	if ip.Reason != "must be a string" {
		t.Errorf("invalid param reason = %q; want 'must be a string'", ip.Reason)
	}
}
