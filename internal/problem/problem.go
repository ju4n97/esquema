// Package problem implements RFC 9457 Problem Details for HTTP APIs.
package problem

import (
	"encoding/json"
	"maps"
	"net/http"
	"strings"
)

// ContentType is the standard RFC 9457 media type.
const ContentType = "application/problem+json"

// InvalidParam details a single schema constraint violation for 422 Unprocessable Entity responses.
type InvalidParam struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// Problem represents an RFC 9457 compliant error payload.
type Problem struct {
	Type          string         `json:"type,omitempty"`
	Title         string         `json:"title"`
	Status        int            `json:"status"`
	Detail        string         `json:"detail,omitempty"`
	Instance      string         `json:"instance,omitempty"`
	InvalidParams []InvalidParam `json:"invalid_params,omitempty"`
	Extensions    map[string]any `json:"-"`
}

// Error implements the standard Go error interface.
func (p Problem) Error() string {
	if p.Detail != "" {
		return p.Title + ": " + p.Detail
	}
	return p.Title
}

// MarshalJSON flattens extension fields and invalid_params into the root JSON object,
// automatically providing standard RFC 9457 title and type values if omitted.
func (p Problem) MarshalJSON() ([]byte, error) {
	m := make(map[string]any, 7+len(p.Extensions))
	maps.Copy(m, p.Extensions)

	title := p.Title
	if title == "" && p.Status != 0 {
		title = http.StatusText(p.Status)
		if title == "" {
			title = "Error"
		}
	}
	m["title"] = title

	problemType := p.Type
	if problemType == "" && p.Status != 0 {
		slug := strings.ToLower(strings.ReplaceAll(title, " ", "-"))
		problemType = "urn:hclapi:error:" + slug
	}
	if problemType != "" {
		m["type"] = problemType
	}

	m["status"] = p.Status
	if p.Detail != "" {
		m["detail"] = p.Detail
	}
	if p.Instance != "" {
		m["instance"] = p.Instance
	}
	if len(p.InvalidParams) > 0 {
		m["invalid_params"] = p.InvalidParams
	}
	return json.Marshal(m)
}

// New creates a Problem using the standard HTTP status code and title.
func New(status int, detail ...string) Problem {
	title := http.StatusText(status)
	if title == "" {
		title = "Error"
	}
	p := Problem{
		Status: status,
		Title:  title,
		Type:   "urn:hclapi:error:" + strings.ToLower(strings.ReplaceAll(title, " ", "-")),
	}
	if len(detail) > 0 {
		p.Detail = detail[0]
	}
	return p
}

// Write serializes the Problem as an application/problem+json HTTP response.
func Write(w http.ResponseWriter, p Problem) {
	w.Header().Set("Content-Type", ContentType)
	w.WriteHeader(p.Status)
	_ = json.NewEncoder(w).Encode(p)
}
