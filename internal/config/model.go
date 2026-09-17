package config

import (
	"time"

	"github.com/hashicorp/hcl/v2"

	"github.com/ju4n97/hclapi/internal/scalar"
)

// Config represents the evaluated, type-checked hclapi service configuration.
type Config struct {
	Server      Server                `json:"server"`
	OpenAPI     OpenAPIMetadata       `json:"openapi"`
	Telemetry   Telemetry             `json:"telemetry"`
	Connections map[string]Connection `json:"connections"`
	Schemas     map[string]Schema     `json:"schemas"`
	Endpoints   []CompiledEndpoint    `json:"-"`
}

// Server holds listener host/port boundaries and timeout limits.
type Server struct {
	Host         string          `hcl:"host,optional"          json:"host"`
	Port         int             `hcl:"port,optional"          json:"port"`
	ReadTimeout  scalar.Duration `hcl:"read_timeout,optional"  json:"read_timeout"`
	WriteTimeout scalar.Duration `hcl:"write_timeout,optional" json:"write_timeout"`
	MaxBodySize  scalar.ByteSize `hcl:"max_body_size,optional" json:"max_body_size"`
}

// OpenAPIMetadata defines global documentation metadata according to OpenAPI 3.1.
type OpenAPIMetadata struct {
	Title       string          `hcl:"title,optional"       json:"title"`
	Version     string          `hcl:"version,optional"     json:"version"`
	Description string          `hcl:"description,optional" json:"description"`
	Servers     []OpenAPIServer `                           json:"servers"`
	Tags        []OpenAPITag    `                           json:"tags"`
	Contact     *Contact        `                           json:"contact,omitempty"`
	License     *License        `                           json:"license,omitempty"`
}

// OpenAPIServer defines a target deployment server.
type OpenAPIServer struct {
	URL         string `hcl:"url"                  json:"url"`
	Description string `hcl:"description,optional" json:"description"`
}

// OpenAPITag categorizes endpoints in interactive documentation portals.
type OpenAPITag struct {
	Name        string `hcl:"name"                 json:"name"`
	Description string `hcl:"description,optional" json:"description"`
}

// Contact contains maintainer contact details for documentation specs.
type Contact struct {
	Name  string `hcl:"name,optional"  json:"name"`
	Email string `hcl:"email,optional" json:"email"`
	URL   string `hcl:"url,optional"   json:"url"`
}

// License contains licensing information for the exposed API.
type License struct {
	Name string `hcl:"name"         json:"name"`
	URL  string `hcl:"url,optional" json:"url"`
}

// Telemetry configures distributed tracing and operational logging.
type Telemetry struct {
	ServiceName string  `hcl:"service_name,optional" json:"service_name"`
	Logging     Logging `hcl:"logging,block"         json:"logging"`
}

// Logging sets log level, output formatting, and sensitive field redaction paths.
type Logging struct {
	Level  string   `hcl:"level,optional"  json:"level"`
	Format string   `hcl:"format,optional" json:"format"`
	Redact []string `hcl:"redact,optional" json:"redact"`
}

// Connection represents an external database or cache pool.
type Connection struct {
	Type   ConnectionType `json:"type"`
	Name   string         `json:"name"`
	Engine string         `json:"engine"`
	Source string         `json:"source,omitempty"`
	URL    string         `json:"url,omitempty"`
	Pool   *Pool          `json:"pool,omitempty"`
}

// Pool sets database connection pool bounds.
type Pool struct {
	MaxOpen int `hcl:"max_open,optional" json:"max_open"`
	MaxIdle int `hcl:"max_idle,optional" json:"max_idle"`
}

// Schema defines a reusable domain data model.
type Schema struct {
	Name        string           `json:"name"`
	Description string           `json:"description,omitempty"`
	Fields      map[string]Field `json:"fields"`
}

// Field defines validation rules and OpenAPI schema attributes strictly adhering to OpenAPI 3.1.
type Field struct {
	Name        string   `json:"name"`
	Type        DataType `json:"type"`                 // Primary OpenAPI type (string, integer, array, object)
	SchemaRef   string   `json:"schema_ref,omitempty"` // Target schema name if type is a custom schema or []schema
	ItemsType   DataType `json:"items_type,omitempty"` // Primitive items type if type is []string, []int, etc.
	Format      Format   `json:"format,omitempty"`     // Strict OpenAPI format
	Required    bool     `json:"required,omitempty"`
	Default     any      `json:"default,omitempty"`
	Description string   `json:"description,omitempty"`
	Min         *float64 `json:"min,omitempty"`
	Max         *float64 `json:"max,omitempty"`
	MinLength   *int     `json:"min_length,omitempty"`
	MaxLength   *int     `json:"max_length,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// RequestRules defines ingress validation constraints across the 4 request coordinates.
type RequestRules struct {
	Path    map[string]Field `json:"path"`
	Query   map[string]Field `json:"query"`
	Headers map[string]Field `json:"headers"`
	Body    map[string]Field `json:"body"`
	BodyRef string           `json:"body_ref,omitempty"`
}

// HasRules reports whether any coordinate defines validation constraints.
func (r RequestRules) HasRules() bool {
	return len(r.Path) > 0 || len(r.Query) > 0 || len(r.Headers) > 0 || len(r.Body) > 0 || r.BodyRef != ""
}

// CompiledEndpoint is a flattened route binding method, path, and pipeline steps.
type CompiledEndpoint struct {
	Method       string
	Path         string
	RoutePattern string
	OperationID  string
	Summary      string
	Tag          string
	Hidden       bool
	Request      RequestRules
	Pipeline     []Step
}

// Step represents a polymorphic pipeline execution step.
type Step struct {
	Type     StepType `json:"type"`
	Name     string
	WhenExpr hcl.Expression

	SQL      *SQLStep
	Valkey   *ValkeyStep
	Respond  *RespondStep
	Docs     *DocsStep
	Spec     *SpecStep
	Go       *GoStep
	Starlark *StarlarkStep
	HTTP     *HTTPStep
}

// SQLStep models relational database queries.
type SQLStep struct {
	Connection string
	Query      string
	ArgsExpr   hcl.Expression
	Catches    []SQLCatch
}

// SQLCatch configures HTTP problem responses for specific database error codes.
type SQLCatch struct {
	Code     string
	Status   int
	BodyExpr hcl.Expression
}

// ValkeyStep executes key-value caching operations against Valkey.
type ValkeyStep struct {
	Connection string
	Op         ValkeyOp
	KeyExpr    hcl.Expression
	ValExpr    hcl.Expression
	TTL        time.Duration
}

// HTTPStep makes outbound HTTP requests to external services.
type HTTPStep struct {
	Method   string
	URLExpr  hcl.Expression
	Headers  map[string]string
	BodyExpr hcl.Expression
	Timeout  scalar.Duration
}

// StarlarkStep executes an embedded Starlark script.
type StarlarkStep struct {
	Source string
}

// GoStep invokes a registered native Go callback.
type GoStep struct {
	Use      string
	ArgsExpr hcl.Expression
}

// RespondStep serializes an HTTP response and terminates pipeline execution.
type RespondStep struct {
	Status    int
	SchemaRef string
	Headers   map[string]string
	BodyExpr  hcl.Expression
}

// DocsStep configures rendering of an interactive API documentation portal.
type DocsStep struct {
	Renderer DocsRenderer
	SpecURL  string
	Title    string
	Template string
}

// SpecStep serves the compiled OpenAPI 3.1 specification document.
type SpecStep struct {
	Format SpecFormat
}
