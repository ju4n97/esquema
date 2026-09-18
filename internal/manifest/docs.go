package manifest

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/zclconf/go-cty/cty/function"

	"github.com/ju4n97/hclapi/internal/docs"
)

// StepDocs renders and streams an interactive API documentation portal.
//
// It delegates template execution to [docs.Render] using precompiled, embedded HTML
// viewer templates (Scalar, Swagger UI, Redoc, or Stoplight Elements), or an optional
// user-defined template file on disk.
//
// Upon execution, it sets the Content-Type header to "text/html; charset=utf-8",
// flushes the compiled HTML bytes, and halts pipeline progression by returning [ErrPipelineHalted].
type StepDocs struct {
	Renderer string
	SpecURL  string
	Title    string
	Template string
	When     Expr
}

// StepName implements [Step] and returns the step identifier.
func (d *StepDocs) StepName() string {
	return "docs"
}

// StepWhen implements [Step] and returns the conditional execution guard.
func (d *StepDocs) StepWhen() Expr {
	return d.When
}

// IsTerminal implements [Step] and returns true.
func (d *StepDocs) IsTerminal() bool {
	return true
}

// ValidateStep implements [StepValidator]. It verifies that the renderer is one of
// the supported portal technologies and that any custom template file exists on disk.
func (d *StepDocs) ValidateStep(m *Manifest) error {
	renderer := strings.ToLower(strings.TrimSpace(d.Renderer))
	if renderer != "" {
		switch renderer {
		case "scalar", "swagger", "redoc", "elements":
		default:
			return fmt.Errorf("docs step invalid renderer %q (allowed: scalar, swagger, redoc, elements)", d.Renderer)
		}
	}

	if d.Template != "" {
		if _, err := os.Stat(d.Template); err != nil {
			return fmt.Errorf("docs step template file not found: %w", err)
		}
	}

	return nil
}

// ExecuteStep implements [StepExecutor]. It compiles the viewer template and streams
// the resulting HTML page to the response writer.
func (d *StepDocs) ExecuteStep(ctx context.Context, ec StepExecutionContext) (any, error) {
	renderer := strings.ToLower(strings.TrimSpace(d.Renderer))
	if renderer == "" {
		renderer = "scalar"
	}

	specURL := strings.TrimSpace(d.SpecURL)
	if specURL == "" {
		specURL = "/openapi.json"
	}

	title := strings.TrimSpace(d.Title)
	if title == "" {
		title = "API Documentation"
	}

	htmlContent, err := docs.Render(renderer, d.Template, title, specURL)
	if err != nil {
		return nil, fmt.Errorf("step %q render portal: %w", d.StepName(), err)
	}

	w := ec.ResponseWriter()
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(htmlContent); err != nil {
		return nil, fmt.Errorf("step %q write response: %w", d.StepName(), err)
	}

	return nil, ErrPipelineHalted
}

// decodeStepDocs decodes an HCL block into a [*StepDocs].
func decodeStepDocs(name string, body hcl.Body, evalCtx *hcl.EvalContext, funcs map[string]function.Function) (Step, error) {
	type docsDecode struct {
		Renderer string         `hcl:"renderer,optional"`
		SpecURL  string         `hcl:"spec_url,optional"`
		Title    string         `hcl:"title,optional"`
		Template string         `hcl:"template,optional"`
		WhenExpr hcl.Expression `hcl:"when,optional"`
	}

	var raw docsDecode
	raw.Renderer = "scalar"
	raw.SpecURL = "/openapi.json"
	if diags := gohcl.DecodeBody(body, evalCtx, &raw); diags.HasErrors() {
		return nil, diags
	}

	return &StepDocs{
		Renderer: strings.ToLower(strings.TrimSpace(raw.Renderer)),
		SpecURL:  strings.TrimSpace(raw.SpecURL),
		Title:    strings.TrimSpace(raw.Title),
		Template: strings.TrimSpace(raw.Template),
		When:     NewExpr(raw.WhenExpr, funcs),
	}, nil
}
