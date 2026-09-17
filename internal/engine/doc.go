package engine

import (
	"net/http"

	"github.com/ju4n97/hclapi/internal/config"
	"github.com/ju4n97/hclapi/internal/docs"
	"github.com/ju4n97/hclapi/internal/problem"
)

// executeDocs renders interactive API documentation portals using the configured template.
func (e *Engine) executeDocs(ctx *Context, w http.ResponseWriter, step *config.DocsStep) {
	title := step.Title
	if title == "" {
		title = e.cfg.OpenAPI.Title
		if title == "" {
			title = e.cfg.Telemetry.ServiceName
		}
		if title == "" {
			title = "API Documentation"
		}
	}

	htmlBytes, err := docs.Render(string(step.Renderer), step.Template, docs.TemplateData{
		Title:   title,
		SpecURL: step.SpecURL,
	})
	if err != nil {
		problem.Write(w, problem.New(http.StatusInternalServerError, err.Error()))
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(htmlBytes)
}
