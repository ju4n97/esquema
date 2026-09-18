package manifest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/valkey-io/valkey-go"
)

type testRespondExecutionContext struct {
	scope    Scope
	recorder *httptest.ResponseRecorder
	schemas  map[string]Schema
}

func (c *testRespondExecutionContext) SQL(name string) (*sql.DB, error) {
	return nil, nil
}

func (c *testRespondExecutionContext) Valkey(name string) (valkey.Client, error) {
	return nil, nil
}

func (c *testRespondExecutionContext) GoHandler(name string) (StepHandler, error) {
	return nil, nil
}

func (c *testRespondExecutionContext) HTTPClient() *http.Client {
	return http.DefaultClient
}

func (c *testRespondExecutionContext) ResponseWriter() http.ResponseWriter {
	return c.recorder
}

func (c *testRespondExecutionContext) ResponseController() *http.ResponseController {
	return http.NewResponseController(c.recorder)
}

func (c *testRespondExecutionContext) OpenAPISpec(format string) ([]byte, string, error) {
	return nil, "", nil
}

func (c *testRespondExecutionContext) Scope() Scope {
	return c.scope
}

func (c *testRespondExecutionContext) Schemas() map[string]Schema {
	return c.schemas
}

// TestStepRespond_ValidateStep verifies compile-time checks for named and inline schemas.
func TestStepRespond_ValidateStep(t *testing.T) {
	t.Parallel()

	manifest := &Manifest{
		Schemas: map[string]Schema{
			"User": {Name: "User"},
		},
	}

	tests := []struct {
		name      string
		step      *StepRespond
		wantError bool
		errSubstr string
	}{
		{
			name: "valid status with known schema reference",
			step: &StepRespond{
				Status: 201,
				Schema: &TypeSpec{Type: TypeObject, SchemaRef: "User"},
			},
			wantError: false,
		},
		{
			name: "valid inline schema block",
			step: &StepRespond{
				Status: 200,
				InlineFields: map[string]Field{
					"id":   {Name: "id", Type: TypeSpec{Type: TypeInteger}},
					"user": {Name: "user", Type: TypeSpec{Type: TypeObject, SchemaRef: "User"}},
				},
			},
			wantError: false,
		},
		{
			name: "error when both schema attribute and inline schema block declared",
			step: &StepRespond{
				Status: 200,
				Schema: &TypeSpec{Type: TypeObject, SchemaRef: "User"},
				InlineFields: map[string]Field{
					"name": {Name: "name", Type: TypeSpec{Type: TypeString}},
				},
			},
			wantError: true,
			errSubstr: "cannot declare both 'schema' attribute and inline 'schema' block",
		},
		{
			name: "references unknown named schema",
			step: &StepRespond{
				Status: 200,
				Schema: &TypeSpec{Type: TypeObject, SchemaRef: "MissingSchema"},
			},
			wantError: true,
			errSubstr: "references unknown schema \"MissingSchema\"",
		},
		{
			name: "inline field references unknown schema",
			step: &StepRespond{
				Status: 200,
				InlineFields: map[string]Field{
					"profile": {Name: "profile", Type: TypeSpec{Type: TypeObject, SchemaRef: "MissingSchema"}},
				},
			},
			wantError: true,
			errSubstr: "references unknown schema \"MissingSchema\"",
		},
		{
			name: "inline field has invalid format constraint",
			step: &StepRespond{
				Status: 200,
				InlineFields: map[string]Field{
					"email": {Name: "email", Type: TypeSpec{Type: TypeString}, Format: Format("unsupported-format")},
				},
			},
			wantError: true,
			errSubstr: "unrecognized format",
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
					t.Fatalf("error %q does not contain expected substring %q", err.Error(), tt.errSubstr)
				}
			}
		})
	}
}

// TestStepRespond_ExecuteStep_InlineMasking verifies field pruning and nested masking on inline schemas.
func TestStepRespond_ExecuteStep_InlineMasking(t *testing.T) {
	t.Parallel()

	schemas := map[string]Schema{
		"User": {
			Name: "User",
			Fields: map[string]Field{
				"id":    {Name: "id", Type: TypeSpec{Type: TypeInteger}},
				"email": {Name: "email", Type: TypeSpec{Type: TypeString}},
			},
		},
	}

	t.Run("prunes undeclared fields using inline schema block", func(t *testing.T) {
		t.Parallel()

		step := &StepRespond{
			Status: 200,
			InlineFields: map[string]Field{
				"message": {Name: "message", Type: TypeSpec{Type: TypeString}},
				"user":    {Name: "user", Type: TypeSpec{Type: TypeObject, SchemaRef: "User"}},
			},
			Body: NewExpr(parseExpr(t, `{
				message       = "registered",
				secret_token  = "sensitive_data",
				user = {
					id            = 42,
					email         = "user@example.com",
					password_hash = "hidden_hash"
				}
			}`), nil),
		}

		rec := httptest.NewRecorder()
		exec := &testRespondExecutionContext{recorder: rec, schemas: schemas}

		_, err := step.ExecuteStep(context.Background(), exec)
		if !errors.Is(err, ErrPipelineHalted) {
			t.Fatalf("expected ErrPipelineHalted, got: %v", err)
		}

		var body map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("json decode error: %v", err)
		}

		if _, exists := body["secret_token"]; exists {
			t.Errorf("egress violation: secret_token was not pruned: %+v", body)
		}
		if body["message"] != "registered" {
			t.Errorf("message = %v, want 'registered'", body["message"])
		}

		userMap, ok := body["user"].(map[string]any)
		if !ok {
			t.Fatalf("expected nested user map, got %T", body["user"])
		}
		if _, exists := userMap["password_hash"]; exists {
			t.Errorf("egress violation: password_hash was not pruned: %+v", userMap)
		}
		if userMap["email"] != "user@example.com" || userMap["id"] != float64(42) {
			t.Errorf("unexpected user contents: %+v", userMap)
		}
	})
}

// TestDecodeStepRespond_InlineSchema verifies decoding HCL inline schema blocks.
func TestDecodeStepRespond_InlineSchema(t *testing.T) {
	t.Parallel()

	hclBlock := `
		status = 201

		schema {
			field "message" {
				type        = string
				description = "Acknowledgment"
			}
			field "user_id" {
				type     = integer
				required = true
			}
		}

		body = {
			message = "done"
			user_id = 99
		}
	`

	expr, diags := hclsyntax.ParseConfig([]byte(hclBlock), "test.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("hcl parse error: %s", diags.Error())
	}

	evalCtx := buildStaticEvalContext(nil)
	funcs := runtimeExprFunctions()

	step, err := decodeStepRespond("respond", expr.Body, evalCtx, funcs)
	if err != nil {
		t.Fatalf("decodeStepRespond() error: %v", err)
	}

	respond, ok := step.(*StepRespond)
	if !ok {
		t.Fatalf("expected *StepRespond, got %T", step)
	}

	if respond.Status != 201 {
		t.Errorf("Status = %d, want 201", respond.Status)
	}
	if len(respond.InlineFields) != 2 {
		t.Fatalf("expected 2 inline fields, got %d", len(respond.InlineFields))
	}

	msgField, ok := respond.InlineFields["message"]
	if !ok || msgField.Type.Type != TypeString || msgField.Description != "Acknowledgment" {
		t.Errorf("unexpected message field: %+v", msgField)
	}

	idField, ok := respond.InlineFields["user_id"]
	if !ok || idField.Type.Type != TypeInteger || !idField.Required {
		t.Errorf("unexpected user_id field: %+v", idField)
	}
}
