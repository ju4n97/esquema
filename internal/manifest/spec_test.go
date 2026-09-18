package manifest

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/valkey-io/valkey-go"
)

type testSpecContext struct {
	scope    Scope
	recorder *httptest.ResponseRecorder
	specJSON []byte
	jsonETag string
	specYAML []byte
	yamlETag string
}

func (c *testSpecContext) SQL(name string) (*sql.DB, error) {
	return nil, nil
}

func (c *testSpecContext) Valkey(name string) (valkey.Client, error) {
	return nil, nil
}

func (c *testSpecContext) GoHandler(name string) (GoHandler, error) {
	return nil, nil
}

func (c *testSpecContext) HTTPClient() *http.Client {
	return http.DefaultClient
}

func (c *testSpecContext) ResponseWriter() http.ResponseWriter {
	return c.recorder
}

func (c *testSpecContext) ResponseController() *http.ResponseController {
	return nil
}

func (c *testSpecContext) OpenAPISpec(format string) ([]byte, string, error) {
	if format == "yaml" {
		return c.specYAML, c.yamlETag, nil
	}
	return c.specJSON, c.jsonETag, nil
}

func (c *testSpecContext) Scope() Scope {
	return c.scope
}

func (c *testSpecContext) Schemas() map[string]Schema {
	return nil
}

// TestStepSpec_ValidateStep verifies allowed output serialization formats.
func TestStepSpec_ValidateStep(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		step      *StepSpec
		wantError bool
		errSubstr string
	}{
		{
			name: "valid json format",
			step: &StepSpec{
				Format: "json",
			},
			wantError: false,
		},
		{
			name: "valid yaml format",
			step: &StepSpec{
				Format: "yaml",
			},
			wantError: false,
		},
		{
			name: "empty format defaults cleanly",
			step: &StepSpec{
				Format: "",
			},
			wantError: false,
		},
		{
			name: "unsupported xml format",
			step: &StepSpec{
				Format: "xml",
			},
			wantError: true,
			errSubstr: "invalid format \"xml\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.step.ValidateStep(&Manifest{})
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

// TestStepSpec_ExecuteStep verifies ETag generation, 304 conditional handling, and serialization headers.
func TestStepSpec_ExecuteStep(t *testing.T) {
	t.Parallel()

	jsonPayload := []byte(`{"openapi":"3.1.0","info":{"title":"Test API"}}`)
	jsonETag := `"etag-json-123"`

	t.Run("serves json spec with etag header", func(t *testing.T) {
		t.Parallel()

		step := &StepSpec{Format: "json"}
		rec := httptest.NewRecorder()
		exec := &testSpecContext{
			recorder: rec,
			specJSON: jsonPayload,
			jsonETag: jsonETag,
		}

		_, err := step.ExecuteStep(context.Background(), exec)
		if !errors.Is(err, ErrPipelineHalted) {
			t.Fatalf("expected ErrPipelineHalted, got: %v", err)
		}

		if rec.Code != http.StatusOK {
			t.Errorf("Status = %d, want 200", rec.Code)
		}

		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}

		if etag := rec.Header().Get("ETag"); etag != jsonETag {
			t.Errorf("ETag = %q, want %q", etag, jsonETag)
		}

		if rec.Body.String() != string(jsonPayload) {
			t.Errorf("body = %s, want %s", rec.Body.String(), string(jsonPayload))
		}
	})

	t.Run("returns 304 Not Modified when matching If-None-Match header sent", func(t *testing.T) {
		t.Parallel()

		step := &StepSpec{Format: "json"}
		rec := httptest.NewRecorder()
		exec := &testSpecContext{
			recorder: rec,
			specJSON: jsonPayload,
			jsonETag: jsonETag,
			scope: Scope{
				Headers: map[string]string{
					"if-none-match": jsonETag,
				},
			},
		}

		_, err := step.ExecuteStep(context.Background(), exec)
		if !errors.Is(err, ErrPipelineHalted) {
			t.Fatalf("expected ErrPipelineHalted, got: %v", err)
		}

		if rec.Code != http.StatusNotModified {
			t.Fatalf("Status = %d, want 304", rec.Code)
		}

		if rec.Body.Len() != 0 {
			t.Errorf("expected empty body on 304 Not Modified, got: %s", rec.Body.String())
		}
	})
}

// TestDecodeStepSpec verifies direct HCL block parsing into [*StepSpec].
func TestDecodeStepSpec(t *testing.T) {
	t.Parallel()

	hclBlock := `
		format = "yaml"
	`

	expr, diags := hclsyntax.ParseConfig([]byte(hclBlock), "test.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("parse error: %s", diags.Error())
	}

	step, err := decodeStepSpec("", expr.Body, nil, runtimeExprFunctions())
	if err != nil {
		t.Fatalf("decodeStepSpec() error: %v", err)
	}

	specStep, ok := step.(*StepSpec)
	if !ok {
		t.Fatalf("expected *StepSpec, got %T", step)
	}

	if specStep.Format != "yaml" {
		t.Errorf("Format = %q, want 'yaml'", specStep.Format)
	}
	if !specStep.IsTerminal() {
		t.Error("expected IsTerminal() to be true")
	}
}
