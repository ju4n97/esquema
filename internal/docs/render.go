package docs

import (
	"bytes"
	"embed"
	"fmt"
	"os"
	"strings"
	"text/template"
)

//go:embed templates/*.html
var templatesFS embed.FS

// ViewParameters contains dynamic variables interpolated into documentation portal templates.
type ViewParameters struct {
	Title   string
	SpecURL string
}

// Render compiles and executes the HTML portal template matching the requested renderer.
func Render(renderer, customTemplatePath, title, specURL string) ([]byte, error) {
	r := strings.ToLower(strings.TrimSpace(renderer))
	if r == "" {
		r = "scalar"
	}

	params := ViewParameters{
		Title:   strings.TrimSpace(title),
		SpecURL: strings.TrimSpace(specURL),
	}
	if params.Title == "" {
		params.Title = "API Documentation"
	}
	if params.SpecURL == "" {
		params.SpecURL = "/openapi.json"
	}

	var tplSource string
	if customTemplatePath != "" {
		data, err := os.ReadFile(customTemplatePath)
		if err != nil {
			return nil, fmt.Errorf("read custom docs template: %w", err)
		}
		tplSource = string(data)
	} else {
		path := fmt.Sprintf("templates/%s.html", r)
		data, err := templatesFS.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("unsupported docs renderer %q (allowed: scalar, swagger, redoc, elements)", renderer)
		}
		tplSource = string(data)
	}

	tpl, err := template.New("docs_portal").Parse(tplSource)
	if err != nil {
		return nil, fmt.Errorf("parse docs template: %w", err)
	}

	var buf bytes.Buffer
	if err := tpl.Execute(&buf, params); err != nil {
		return nil, fmt.Errorf("execute docs template: %w", err)
	}

	return buf.Bytes(), nil
}
