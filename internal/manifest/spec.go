package manifest

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/zclconf/go-cty/cty/function"
)

// StepSpec serves the compiled OpenAPI specification in JSON or YAML format with ETag caching.
//
// When incoming requests include a matching If-None-Match header, StepSpec returns HTTP 304 Not Modified
// without re-transmitting the document payload.
type StepSpec struct {
	Format string
	When   Expr
}

// StepName implements [Step].
func (s *StepSpec) StepName() string {
	return "spec"
}

// StepWhen implements [Step].
func (s *StepSpec) StepWhen() Expr {
	return s.When
}

// IsTerminal implements [Step] and returns true.
func (s *StepSpec) IsTerminal() bool {
	return true
}

// ValidateStep implements [StepValidator]. It verifies that the format is either "json" or "yaml".
func (s *StepSpec) ValidateStep(m *Manifest) error {
	format := strings.ToLower(strings.TrimSpace(s.Format))
	if format != "" && format != "json" && format != "yaml" {
		return fmt.Errorf("spec step invalid format %q (allowed: json, yaml)", s.Format)
	}
	return nil
}

// ExecuteStep implements [StepExecutor]. It retrieves pre-cached specification bytes and ETag headers,
// verifies conditional cache headers, and streams the document to the client.
func (s *StepSpec) ExecuteStep(ctx context.Context, ec StepExecutionContext) (any, error) {
	format := strings.ToLower(strings.TrimSpace(s.Format))
	if format == "" {
		format = "json"
	}

	payload, etag, err := ec.OpenAPISpec(format)
	if err != nil {
		return nil, fmt.Errorf("spec step retrieve specification: %w", err)
	}

	w := ec.ResponseWriter()
	w.Header().Set("ETag", etag)

	clientETag := ec.Scope().Headers["if-none-match"]
	if clientETag != "" && (clientETag == etag || clientETag == "*") {
		w.WriteHeader(http.StatusNotModified)
		return nil, ErrPipelineHalted
	}

	if format == "yaml" {
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	} else {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(payload)

	return nil, ErrPipelineHalted
}

// decodeStepSpec decodes an HCL block into a [*StepSpec].
func decodeStepSpec(name string, body hcl.Body, evalCtx *hcl.EvalContext, funcs map[string]function.Function) (Step, error) {
	type specDecode struct {
		Format   string         `hcl:"format,optional"`
		WhenExpr hcl.Expression `hcl:"when,optional"`
	}

	var raw specDecode
	raw.Format = "json"
	if diags := gohcl.DecodeBody(body, evalCtx, &raw); diags.HasErrors() {
		return nil, diags
	}

	return &StepSpec{
		Format: strings.ToLower(strings.TrimSpace(raw.Format)),
		When:   NewExpr(raw.WhenExpr, funcs),
	}, nil
}
