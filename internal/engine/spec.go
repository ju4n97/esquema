package engine

import (
	"net/http"
	"strings"

	"github.com/ju4n97/hclapi/internal/config"
)

// executeSpec serves pre-cached OpenAPI specifications with ETag 304 caching.
func (e *Engine) executeSpec(ctx *Context, w http.ResponseWriter, step *config.SpecStep) {
	isYAML := strings.EqualFold(string(step.Format), "yaml") || strings.EqualFold(string(step.Format), "yml")
	body := e.specJSON
	etag := e.specJSONETag
	contentType := "application/json"

	if isYAML {
		body = e.specYAML
		etag = e.specYAMLETag
		contentType = "application/yaml"
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("ETag", etag)

	if match := ctx.req.Header.Get("If-None-Match"); match != "" && (match == etag || match == "*") {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}
