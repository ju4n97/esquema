package engine

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/ju4n97/hclapi/internal/config"
)

// executeRespond serializes the final HTTP response payload and performs egress masking.
func (e *Engine) executeRespond(ctx *Context, w http.ResponseWriter, step *config.RespondStep) {
	status := step.Status
	if status == 0 {
		status = http.StatusOK
	}

	body, _ := ctx.EvalAny(step.BodyExpr)

	if step.SchemaRef != "" && body != nil {
		body = e.maskResponse(body, step.SchemaRef)
	}

	for k, v := range step.Headers {
		w.Header().Set(k, v)
	}

	if status == http.StatusNoContent {
		w.WriteHeader(status)
		return
	}

	// Respect user-specified Content-Type; fallback to application/json
	contentType := w.Header().Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
		w.Header().Set("Content-Type", contentType)
	}

	w.WriteHeader(status)
	if body == nil {
		return
	}

	// Stream directly if Content-Type is JSON
	if strings.HasPrefix(contentType, "application/json") {
		_ = json.NewEncoder(w).Encode(body)
		return
	}

	// Write raw bytes or string directly for non-JSON payloads
	switch v := body.(type) {
	case []byte:
		_, _ = w.Write(v)
	case string:
		_, _ = w.Write([]byte(v))
	default:
		_ = json.NewEncoder(w).Encode(body)
	}
}

// maskResponse filters out fields not declared in the target response schema,
// recursively handling nested schemas and array items.
func (e *Engine) maskResponse(data any, schemaRef string) any {
	cleanRef := strings.TrimPrefix(schemaRef, "[]")
	targetSchema, exists := e.cfg.Schemas[cleanRef]
	if !exists {
		return data
	}

	var maskValue func(val any, field config.Field) any

	maskMap := func(m map[string]any, schema config.Schema) map[string]any {
		out := make(map[string]any, len(schema.Fields))
		for fName, field := range schema.Fields {
			if val, ok := m[fName]; ok {
				out[fName] = maskValue(val, field)
			}
		}
		return out
	}

	maskValue = func(val any, field config.Field) any {
		if field.SchemaRef == "" {
			return val
		}
		nestedSchema, ok := e.cfg.Schemas[field.SchemaRef]
		if !ok {
			return val
		}
		switch v := val.(type) {
		case []map[string]any:
			out := make([]any, len(v))
			for i, item := range v {
				out[i] = maskMap(item, nestedSchema)
			}
			return out
		case []any:
			out := make([]any, len(v))
			for i, item := range v {
				if m, isMap := item.(map[string]any); isMap {
					out[i] = maskMap(m, nestedSchema)
				} else {
					out[i] = item
				}
			}
			return out
		case map[string]any:
			return maskMap(v, nestedSchema)
		default:
			return val
		}
	}

	switch v := data.(type) {
	case []map[string]any:
		outList := make([]any, len(v))
		for i, m := range v {
			outList[i] = maskMap(m, targetSchema)
		}
		return outList
	case []any:
		outList := make([]any, len(v))
		for i, item := range v {
			if m, ok := item.(map[string]any); ok {
				outList[i] = maskMap(m, targetSchema)
			} else {
				outList[i] = item
			}
		}
		return outList
	case map[string]any:
		return maskMap(v, targetSchema)
	default:
		return data
	}
}
