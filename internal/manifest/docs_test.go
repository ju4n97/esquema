package manifest

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/valkey-io/valkey-go"
)

type testDocsContext struct {
	recorder *httptest.ResponseRecorder
}

func (c *testDocsContext) SQL(name string) (*sql.DB, error)                  { return nil, nil }
func (c *testDocsContext) Valkey(name string) (valkey.Client, error)         { return nil, nil }
func (c *testDocsContext) GoHandler(name string) (GoHandler, error)          { return nil, nil }
func (c *testDocsContext) HTTPClient() *http.Client                          { return http.DefaultClient }
func (c *testDocsContext) ResponseWriter() http.ResponseWriter               { return c.recorder }
func (c *testDocsContext) ResponseController() *http.ResponseController      { return nil }
func (c *testDocsContext) OpenAPISpec(format string) ([]byte, string, error) { return nil, "", nil }
func (c *testDocsContext) Scope() Scope                                      { return Scope{} }
func (c *testDocsContext) Schemas() map[string]Schema                        { return nil }

// TestStepDocs_ValidateStep verifies supported renderer validation and custom template checks.
func TestStepDocs_ValidateStep(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	customTpl := filepath.Join(dir, "portal.html")
	if err := os.WriteFile(customTpl, []byte(`<html>{{ .Title }}</html>`), 0o600); err != nil {
		t.Fatalf("failed to write custom template fixture: %v", err)
	}

	tests := []struct {
		name      string
		step      *StepDocs
		wantError bool
		errSubstr string
	}{
		{
			name: "default scalar renderer",
			step: &StepDocs{
				Renderer: "scalar",
			},
			wantError: false,
		},
		{
			name: "case-insensitive swagger renderer",
			step: &StepDocs{
				Renderer: "SWAGGER",
			},
			wantError: false,
		},
		{
			name: "redoc renderer",
			step: &StepDocs{
				Renderer: "redoc",
			},
			wantError: false,
		},
		{
			name: "elements renderer",
			step: &StepDocs{
				Renderer: "elements",
			},
			wantError: false,
		},
		{
			name: "unrecognized renderer returns error",
			step: &StepDocs{
				Renderer: "unsupported-ui",
			},
			wantError: true,
			errSubstr: "invalid renderer \"unsupported-ui\"",
		},
		{
			name: "existing custom template",
			step: &StepDocs{
				Template: customTpl,
			},
			wantError: false,
		},
		{
			name: "missing custom template file returns error",
			step: &StepDocs{
				Template: filepath.Join(dir, "non_existent.html"),
			},
			wantError: true,
			errSubstr: "template file not found",
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

// TestStepDocs_ExecuteStep verifies HTML rendering, status codes, and template variable interpolation.
func TestStepDocs_ExecuteStep(t *testing.T) {
	t.Parallel()

	renderers := []string{"scalar", "swagger", "redoc", "elements"}

	for _, r := range renderers {
		t.Run("renders "+r+" documentation portal", func(t *testing.T) {
			t.Parallel()

			step := &StepDocs{
				Renderer: r,
				Title:    "Storefront API",
				SpecURL:  "/api/v1/spec.json",
			}

			rec := httptest.NewRecorder()
			exec := &testDocsContext{recorder: rec}

			_, err := step.ExecuteStep(context.Background(), exec)
			if !errors.Is(err, ErrPipelineHalted) {
				t.Fatalf("expected ErrPipelineHalted, got: %v", err)
			}

			if rec.Code != http.StatusOK {
				t.Errorf("Status = %d, want 200", rec.Code)
			}

			ct := rec.Header().Get("Content-Type")
			if !strings.HasPrefix(ct, "text/html") {
				t.Errorf("Content-Type = %q, want text/html", ct)
			}

			body := rec.Body.String()
			if !strings.Contains(body, "Storefront API") {
				t.Errorf("expected HTML to contain title 'Storefront API', body:\n%s", body)
			}
			if !strings.Contains(body, "/api/v1/spec.json") {
				t.Errorf("expected HTML to contain spec url '/api/v1/spec.json', body:\n%s", body)
			}
		})
	}
}

// TestDecodeStepDocs verifies direct HCL block parsing into [*StepDocs].
func TestDecodeStepDocs(t *testing.T) {
	t.Parallel()

	hclBlock := `
		renderer = "redoc"
		spec_url = "/openapi.yaml"
		title    = "Documentation Portal"
	`

	expr, diags := hclsyntax.ParseConfig([]byte(hclBlock), "test.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("hcl parse error: %s", diags.Error())
	}

	step, err := decodeStepDocs("", expr.Body, nil, runtimeExprFunctions())
	if err != nil {
		t.Fatalf("decodeStepDocs() error: %v", err)
	}

	docsStep, ok := step.(*StepDocs)
	if !ok {
		t.Fatalf("expected *StepDocs, got %T", step)
	}

	if docsStep.Renderer != "redoc" {
		t.Errorf("Renderer = %q, want 'redoc'", docsStep.Renderer)
	}
	if docsStep.SpecURL != "/openapi.yaml" {
		t.Errorf("SpecURL = %q, want '/openapi.yaml'", docsStep.SpecURL)
	}
	if docsStep.Title != "Documentation Portal" {
		t.Errorf("Title = %q, want 'Documentation Portal'", docsStep.Title)
	}
	if !docsStep.IsTerminal() {
		t.Error("expected IsTerminal() to be true")
	}
}
