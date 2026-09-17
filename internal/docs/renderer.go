package docs

import (
	"bytes"
	"embed"
	"fmt"
	"html/template"
	"os"
	"strings"
)

//go:embed templates/*.html
var templatesFS embed.FS

// TemplateData holds template interpolation variables for documentation renderers.
type TemplateData struct {
	Title   string
	SpecURL string
}

// Render compiles and renders the requested documentation portal into HTML bytes.
func Render(renderer, customTemplate string, data TemplateData) ([]byte, error) {
	var tmplContent string

	if customTemplate != "" {
		if content, err := os.ReadFile(customTemplate); err == nil {
			tmplContent = string(content)
		} else {
			tmplContent = customTemplate
		}
	} else {
		r := strings.ToLower(strings.TrimSpace(renderer))
		if r == "" {
			r = "scalar"
		}

		filePath := fmt.Sprintf("templates/%s.html", r)
		content, err := templatesFS.ReadFile(filePath)
		if err != nil {
			return nil, fmt.Errorf("unsupported documentation renderer %q (supported: scalar, swagger, elements, redoc)", renderer)
		}
		tmplContent = string(content)
	}

	tmpl, err := template.New("hclapi_docs").Parse(tmplContent)
	if err != nil {
		return nil, fmt.Errorf("parse docs template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("render docs template: %w", err)
	}

	return buf.Bytes(), nil
}
