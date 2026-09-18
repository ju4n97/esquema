package engine

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ju4n97/hclapi/internal/manifest"
)

// TestValidateIngress_Coordinates verifies path, query, header, and body schema constraints.
func TestValidateIngress_Coordinates(t *testing.T) {
	t.Parallel()

	minLen := 3
	rules := &manifest.Request{
		Path: map[string]manifest.Field{
			"id": {
				Name:     "id",
				Type:     manifest.TypeSpec{Type: manifest.TypeInteger},
				Required: true,
			},
		},
		Query: map[string]manifest.Field{
			"channel": {
				Name:    "channel",
				Type:    manifest.TypeSpec{Type: manifest.TypeString},
				Default: "web",
				Enum:    []string{"web", "mobile"},
			},
			"tags": {
				Name: "tags",
				Type: manifest.TypeSpec{
					Type:     manifest.TypeArray,
					ElemType: &manifest.TypeSpec{Type: manifest.TypeString},
				},
			},
		},
		Headers: map[string]manifest.Field{
			"x-api-key": {
				Name:     "x-api-key",
				Type:     manifest.TypeSpec{Type: manifest.TypeString},
				Format:   manifest.FormatUUID,
				Required: true,
			},
		},
		Body: map[string]manifest.Field{
			"email": {
				Name:     "email",
				Type:     manifest.TypeSpec{Type: manifest.TypeString},
				Format:   manifest.FormatEmail,
				Required: true,
			},
			"username": {
				Name:      "username",
				Type:      manifest.TypeSpec{Type: manifest.TypeString},
				MinLength: &minLen,
				Required:  true,
			},
		},
	}

	tests := []struct {
		name           string
		targetURL      string
		headers        map[string]string
		bodyJSON       string
		wantStatusCode int
		wantCoercedID  int64
		wantChannel    string
	}{
		{
			name:      "valid payload passes and coerces types",
			targetURL: "/accounts/101?channel=mobile&tags=dev&tags=prod",
			headers: map[string]string{
				"X-Api-Key": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
			},
			bodyJSON:       `{"email":"alice@example.com","username":"alice"}`,
			wantStatusCode: 0,
			wantCoercedID:  101,
			wantChannel:    "mobile",
		},
		{
			name:      "invalid path parameter integer returns 422",
			targetURL: "/accounts/not-an-int",
			headers: map[string]string{
				"X-Api-Key": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
			},
			bodyJSON:       `{"email":"alice@example.com","username":"alice"}`,
			wantStatusCode: http.StatusUnprocessableEntity,
		},
		{
			name:           "missing required header returns 422",
			targetURL:      "/accounts/101",
			headers:        map[string]string{},
			bodyJSON:       `{"email":"alice@example.com","username":"alice"}`,
			wantStatusCode: http.StatusUnprocessableEntity,
		},
		{
			name:      "invalid header uuid format returns 422",
			targetURL: "/accounts/101",
			headers: map[string]string{
				"X-Api-Key": "invalid-uuid",
			},
			bodyJSON:       `{"email":"alice@example.com","username":"alice"}`,
			wantStatusCode: http.StatusUnprocessableEntity,
		},
		{
			name:      "invalid query enum returns 422",
			targetURL: "/accounts/101?channel=desktop",
			headers: map[string]string{
				"X-Api-Key": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
			},
			bodyJSON:       `{"email":"alice@example.com","username":"alice"}`,
			wantStatusCode: http.StatusUnprocessableEntity,
		},
		{
			name:      "invalid body email format returns 422",
			targetURL: "/accounts/101",
			headers: map[string]string{
				"X-Api-Key": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
			},
			bodyJSON:       `{"email":"not-an-email","username":"alice"}`,
			wantStatusCode: http.StatusUnprocessableEntity,
		},
		{
			name:      "body string min_length violation returns 422",
			targetURL: "/accounts/101",
			headers: map[string]string{
				"X-Api-Key": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
			},
			bodyJSON:       `{"email":"alice@example.com","username":"a"}`,
			wantStatusCode: http.StatusUnprocessableEntity,
		},
		{
			name:      "malformed body JSON returns 400 Bad Request",
			targetURL: "/accounts/101",
			headers: map[string]string{
				"X-Api-Key": "f47ac10b-58cc-4372-a567-0e02b2c3d479",
			},
			bodyJSON:       `{"email":`,
			wantStatusCode: http.StatusBadRequest,
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
			ctx, err := NewContext(req, rec, "POST /accounts/{id}", 1024, Dependencies{})
			if err != nil {
				t.Fatalf("NewContext() unexpected error: %v", err)
			}

			prob := ValidateIngress(ctx, rules, nil)

			if tt.wantStatusCode == 0 {
				if prob != nil {
					t.Fatalf("unexpected validation problem: %+v", prob)
				}
				if gotID := ctx.pathParams["id"]; gotID != tt.wantCoercedID {
					t.Errorf("coerced path id = %v, want %d", gotID, tt.wantCoercedID)
				}
				if gotChan := ctx.queryParams["channel"]; gotChan != tt.wantChannel {
					t.Errorf("query channel = %v, want %q", gotChan, tt.wantChannel)
				}
				return
			}

			if prob == nil {
				t.Fatalf("expected validation problem with status %d, got nil", tt.wantStatusCode)
			}
			if prob.Status != tt.wantStatusCode {
				t.Errorf("problem status = %d, want %d", prob.Status, tt.wantStatusCode)
			}
		})
	}
}

// TestValidateIngress_DeepNestedSchemas verifies recursive validation across schema references.
func TestValidateIngress_DeepNestedSchemas(t *testing.T) {
	t.Parallel()

	schemas := map[string]manifest.Schema{
		"Address": {
			Name: "Address",
			Fields: map[string]manifest.Field{
				"street": {
					Name:     "street",
					Type:     manifest.TypeSpec{Type: manifest.TypeString},
					Required: true,
				},
				"zip": {
					Name:     "zip",
					Type:     manifest.TypeSpec{Type: manifest.TypeInteger},
					Required: true,
				},
			},
		},
		"Profile": {
			Name: "Profile",
			Fields: map[string]manifest.Field{
				"address": {
					Name:     "address",
					Type:     manifest.TypeSpec{Type: manifest.TypeObject, SchemaRef: "Address"},
					Required: true,
				},
			},
		},
	}

	rules := &manifest.Request{
		BodyRef: "Profile",
	}

	t.Run("valid deep nested payload passes", func(t *testing.T) {
		t.Parallel()

		body := `{"address":{"street":"Main St","zip":12345}}`
		req := httptest.NewRequest(http.MethodPost, "/profiles", strings.NewReader(body))
		ctx, err := NewContext(req, httptest.NewRecorder(), "POST /profiles", 1024, Dependencies{})
		if err != nil {
			t.Fatalf("NewContext() error: %v", err)
		}

		prob := ValidateIngress(ctx, rules, schemas)
		if prob != nil {
			t.Fatalf("unexpected validation problem: %+v", prob)
		}
	})

	t.Run("nested required field missing returns 422 with exact path", func(t *testing.T) {
		t.Parallel()

		body := `{"address":{"street":"Main St"}}`
		req := httptest.NewRequest(http.MethodPost, "/profiles", strings.NewReader(body))
		ctx, err := NewContext(req, httptest.NewRecorder(), "POST /profiles", 1024, Dependencies{})
		if err != nil {
			t.Fatalf("NewContext() error: %v", err)
		}

		prob := ValidateIngress(ctx, rules, schemas)
		if prob == nil || prob.Status != http.StatusUnprocessableEntity {
			t.Fatalf("expected 422 problem, got %+v", prob)
		}

		expectedPath := "body.address.zip"
		if len(prob.InvalidParams) != 1 || prob.InvalidParams[0].Name != expectedPath {
			t.Fatalf("invalid params = %+v, want name %q", prob.InvalidParams, expectedPath)
		}
	})
}
