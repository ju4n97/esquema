package manifest

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// Manifest represents the compiled, validated AST for an entire API service.
type Manifest struct {
	Server      Server
	OpenAPI     OpenAPI
	Telemetry   Telemetry
	Connections map[string]Connection
	Schemas     map[string]Schema
	Routes      []Route
}

// Server defines listener addresses, transport limits, and network timeouts.
type Server struct {
	Host         string
	Port         int
	ReadTimeout  Duration
	WriteTimeout Duration
	MaxBodySize  ByteSize
}

// OpenAPI holds document metadata exported into OpenAPI 3.1 specifications.
type OpenAPI struct {
	Title       string          `hcl:"title,optional"`
	Version     string          `hcl:"version,optional"`
	Description string          `hcl:"description,optional"`
	Servers     []OpenAPIServer `hcl:"servers,optional"`
	Tags        []OpenAPITag    `hcl:"tags,optional"`
	Contact     *Contact        `hcl:"contact,block"`
	License     *License        `hcl:"license,block"`
}

// OpenAPIServer defines a deployment origin in the generated specification.
type OpenAPIServer struct {
	URL         string `hcl:"url"                  cty:"url"`
	Description string `hcl:"description,optional" cty:"description"`
}

// OpenAPITag groups operations inside interactive documentation portals.
type OpenAPITag struct {
	Name        string `hcl:"name"                 cty:"name"`
	Description string `hcl:"description,optional" cty:"description"`
}

// Contact contains API maintainer details exported in the specification info block.
type Contact struct {
	Name  string `hcl:"name,optional"`
	Email string `hcl:"email,optional"`
	URL   string `hcl:"url,optional"`
}

// License describes legal licensing terms for the exposed API contract.
type License struct {
	Name string `hcl:"name"`
	URL  string `hcl:"url,optional"`
}

// Telemetry configures structured logging output, log level thresholds, and key redaction.
type Telemetry struct {
	ServiceName string   `hcl:"service_name,optional"`
	LogLevel    string   `hcl:"log_level,optional"`
	LogFormat   string   `hcl:"log_format,optional"`
	Redact      []string `hcl:"redact,optional"`
}

// Connection defines external database connection pools and cache instances.
type Connection struct {
	Type    string
	Name    string
	Engine  string
	Source  string
	URL     string
	MaxOpen int
	MaxIdle int
}

// Schema represents a reusable domain model for payload validation and egress filtering.
type Schema struct {
	Name        string
	Description string
	Fields      map[string]Field
}

// Field specifies validation constraints and documentation metadata for a model attribute.
type Field struct {
	Name        string
	Type        TypeSpec
	Required    bool
	Format      Format
	Default     any
	Description string
	Min         *float64
	Max         *float64
	MinLength   *int
	MaxLength   *int
	Enum        []string
}

// Route binds an HTTP method and URL pattern to an ordered pipeline of steps.
type Route struct {
	Method  string
	Path    string
	Summary string
	Tag     string
	Hidden  bool
	Request *Request
	Steps   []Step
}

// Pattern returns the canonical route key formatted as "METHOD /path".
func (r Route) Pattern() string {
	return strings.ToUpper(r.Method) + " " + r.Path
}

// HasTerminalStep reports whether the route contains at least one response-producing step.
func (r Route) HasTerminalStep() bool {
	for _, s := range r.Steps {
		if s != nil && s.IsTerminal() {
			return true
		}
	}
	return false
}

// Request defines validation constraints across incoming HTTP request coordinates.
type Request struct {
	Path    map[string]Field
	Query   map[string]Field
	Headers map[string]Field
	Body    map[string]Field
	BodyRef string
}

// HasRules reports whether any ingress constraints are declared.
func (r *Request) HasRules() bool {
	if r == nil {
		return false
	}
	return len(r.Path) > 0 || len(r.Query) > 0 || len(r.Headers) > 0 || len(r.Body) > 0 || r.BodyRef != ""
}

// Validate checks the structural integrity, types, and referential validity of the manifest.
func (m *Manifest) Validate() error {
	if m == nil {
		return errors.New("manifest is nil")
	}

	for sName, s := range m.Schemas {
		for fName, f := range s.Fields {
			if f.Format != "" {
				if err := ValidateFormat(string(f.Format)); err != nil {
					return fmt.Errorf("schema %q field %q: %w", sName, fName, err)
				}
			}

			ref := f.Type.ElementSchemaRef()
			if ref != "" {
				if _, exists := m.Schemas[ref]; !exists {
					return fmt.Errorf("schema %q field %q references unknown schema %q", sName, fName, ref)
				}
			}
		}
	}

	seenRoutes := make(map[string]struct{}, len(m.Routes))
	for _, r := range m.Routes {
		method := strings.ToUpper(r.Method)
		switch method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch,
			http.MethodDelete, http.MethodHead, http.MethodOptions:
		default:
			return fmt.Errorf("route %q: unsupported HTTP method %q", r.Pattern(), r.Method)
		}

		if !strings.HasPrefix(r.Path, "/") {
			return fmt.Errorf("route %q: path must begin with leading slash", r.Pattern())
		}

		pattern := r.Pattern()
		if _, exists := seenRoutes[pattern]; exists {
			return fmt.Errorf("duplicate route pattern %q declared across manifests", pattern)
		}
		seenRoutes[pattern] = struct{}{}

		if !r.HasTerminalStep() {
			return fmt.Errorf("route %q: route must declare at least one terminal step (e.g. respond, stream, docs, spec)", pattern)
		}

		if r.Request != nil && r.Request.BodyRef != "" {
			if _, exists := m.Schemas[r.Request.BodyRef]; !exists {
				return fmt.Errorf("route %q: request body references unknown schema %q", pattern, r.Request.BodyRef)
			}
		}

		seenStepNames := make(map[string]struct{}, len(r.Steps))
		for _, step := range r.Steps {
			if step == nil {
				return fmt.Errorf("route %q: step cannot be nil", pattern)
			}

			if !step.IsTerminal() {
				name := step.StepName()
				if name == "" {
					return fmt.Errorf("route %q: non-terminal step must define a non-empty name", pattern)
				}
				if _, exists := seenStepNames[name]; exists {
					return fmt.Errorf("route %q: duplicate step name %q in pipeline", pattern, name)
				}
				seenStepNames[name] = struct{}{}
			}

			if validator, ok := step.(StepValidator); ok {
				if err := validator.ValidateStep(m); err != nil {
					return fmt.Errorf("route %q: %w", pattern, err)
				}
			}
		}
	}

	return nil
}
