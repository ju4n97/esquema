package problem

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ContentType is the standard RFC 9457 media type for Problem Details JSON payloads.
const ContentType = "application/problem+json"

// InvalidParam describes a single schema constraint violation for 422 Unprocessable Entity responses.
type InvalidParam struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Problem represents an RFC 9457 compliant HTTP error payload.
//
// It implements the standard Go [error] interface. Custom metadata added to Extensions
// is flattened directly into the root JSON object during marshaling. Standard RFC 9457
// attributes take precedence over extension keys sharing identical names.
type Problem struct {
	Type          string         `json:"type,omitempty"`
	Title         string         `json:"title"`
	Status        int            `json:"status"`
	Detail        string         `json:"detail,omitempty"`
	Instance      string         `json:"instance,omitempty"`
	InvalidParams []InvalidParam `json:"invalid_params,omitempty"`
	Extensions    map[string]any `json:"-"`
}

// New constructs a [Problem] with the supplied HTTP status code and optional detail explanation.
//
// If status is not a recognized HTTP status code, Title defaults to "Error".
// Type is automatically generated as an RFC 9457 URN based on Title (e.g. "urn:hclapi:error:not-found").
func New(status int, detail ...string) Problem {
	if status <= 0 {
		status = http.StatusInternalServerError
	}

	title := http.StatusText(status)
	if title == "" {
		title = "Error"
	}

	p := Problem{
		Status: status,
		Title:  title,
		Type:   "urn:hclapi:error:" + slugify(title),
	}

	if len(detail) > 0 && detail[0] != "" {
		p.Detail = detail[0]
	}

	return p
}

// Validation constructs an RFC 9457 HTTP 422 Unprocessable Entity problem pre-configured
// with schema constraint violation parameters.
func Validation(detail string, params ...InvalidParam) Problem {
	p := New(http.StatusUnprocessableEntity, detail)
	if len(params) > 0 {
		p.InvalidParams = params
	}
	return p
}

// WithInstance returns a copy of p with the Instance attribute set.
func (p Problem) WithInstance(instance string) Problem {
	p.Instance = instance
	return p
}

// WithExtension sets a key-value pair in Extensions, allocating the underlying map if nil.
func (p Problem) WithExtension(key string, value any) Problem {
	if p.Extensions == nil {
		p.Extensions = make(map[string]any)
	}
	p.Extensions[key] = value
	return p
}

// Error implements [error].
func (p Problem) Error() string {
	if p.Detail != "" {
		if p.Title != "" {
			return p.Title + ": " + p.Detail
		}
		return p.Detail
	}
	if p.Title != "" {
		return p.Title
	}
	return fmt.Sprintf("HTTP %d", p.Status)
}

// MarshalJSON flattens Extensions directly into the root JSON object while enforcing RFC 9457
// attribute types and automatic defaults for Title and Type if omitted.
func (p Problem) MarshalJSON() ([]byte, error) {
	size := 6 + len(p.Extensions)
	root := make(map[string]any, size)

	for k, v := range p.Extensions {
		root[k] = v
	}

	title := p.Title
	if title == "" && p.Status != 0 {
		title = http.StatusText(p.Status)
		if title == "" {
			title = "Error"
		}
	}
	root["title"] = title

	problemType := p.Type
	if problemType == "" && p.Status != 0 {
		problemType = "urn:hclapi:error:" + slugify(title)
	}
	if problemType != "" {
		root["type"] = problemType
	}

	root["status"] = p.Status

	if p.Detail != "" {
		root["detail"] = p.Detail
	}
	if p.Instance != "" {
		root["instance"] = p.Instance
	}
	if len(p.InvalidParams) > 0 {
		root["invalid_params"] = p.InvalidParams
	}

	return json.Marshal(root)
}

// UnmarshalJSON unmarshals RFC 9457 JSON, extracting known members into typed struct fields
// and collecting all unrecognized keys into the Extensions map.
func (p *Problem) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}

	if v, ok := raw["type"].(string); ok {
		p.Type = v
		delete(raw, "type")
	}
	if v, ok := raw["title"].(string); ok {
		p.Title = v
		delete(raw, "title")
	}
	if v, ok := raw["status"].(float64); ok {
		p.Status = int(v)
		delete(raw, "status")
	}
	if v, ok := raw["detail"].(string); ok {
		p.Detail = v
		delete(raw, "detail")
	}
	if v, ok := raw["instance"].(string); ok {
		p.Instance = v
		delete(raw, "instance")
	}

	if rawParams, ok := raw["invalid_params"]; ok {
		paramBytes, err := json.Marshal(rawParams)
		if err == nil {
			var params []InvalidParam
			if err := json.Unmarshal(paramBytes, &params); err == nil {
				p.InvalidParams = params
			}
		}
		delete(raw, "invalid_params")
	}

	if len(raw) > 0 {
		p.Extensions = raw
	} else {
		p.Extensions = nil
	}

	return nil
}

// Write sets the Content-Type header to "application/problem+json", writes the status code,
// and streams the serialized Problem payload to w.
func Write(w http.ResponseWriter, p Problem) {
	if w == nil {
		return
	}

	status := p.Status
	if status < 100 || status > 599 {
		status = http.StatusInternalServerError
	}

	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(p)
}

// slugify formats a title string into a lowercase hyphenated URN slug.
func slugify(s string) string {
	lower := strings.ToLower(strings.TrimSpace(s))
	return strings.ReplaceAll(lower, " ", "-")
}
