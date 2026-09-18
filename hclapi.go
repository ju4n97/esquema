// Package hclapi provides a type-safe, declarative HTTP API engine powered by HashiCorp HCL.
// It compiles HCL manifests into instant HTTP services backed by relational databases,
// Valkey/Redis caching, sandboxed Starlark transformations, and zero-drift OpenAPI 3.1 specifications.
package hclapi

import (
	"net/http"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty/function"

	"github.com/ju4n97/hclapi/internal/engine"
	"github.com/ju4n97/hclapi/internal/manifest"
	"github.com/ju4n97/hclapi/internal/openapi"
	"github.com/ju4n97/hclapi/internal/problem"
	"github.com/ju4n97/hclapi/internal/telemetry"
)

type (
	// Manifest represents the compiled, validated AST for an entire API service.
	Manifest = manifest.Manifest

	// Server defines listener addresses, transport limits, and network timeouts.
	Server = manifest.Server

	// OpenAPI holds document metadata exported into OpenAPI 3.1 specifications.
	OpenAPI = manifest.OpenAPI

	// Telemetry configures structured logging output, log level thresholds, and key redaction.
	Telemetry = manifest.Telemetry

	// TelemetryService coordinates runtime logging, OpenTelemetry tracing, and panic recovery middleware.
	TelemetryService = telemetry.Telemetry

	// Connection defines external database connection pools and cache instances.
	Connection = manifest.Connection

	// Schema represents a reusable domain model for payload validation and egress filtering.
	Schema = manifest.Schema

	// Field specifies validation constraints and documentation metadata for a model attribute.
	Field = manifest.Field

	// Route binds an HTTP method and URL pattern to an ordered pipeline of steps.
	Route = manifest.Route

	// Request defines validation constraints across incoming HTTP request coordinates.
	Request = manifest.Request

	// Step defines the minimal AST node contract for a route task.
	Step = manifest.Step

	// StepValidator is an optional interface implemented by AST step definitions
	// that require semantic verification against the compiled manifest topology.
	StepValidator = manifest.StepValidator

	// StepExecutor is implemented by steps that execute runtime request logic.
	StepExecutor = manifest.StepExecutor

	// StepExecutionContext supplies runtime dependencies and request state to steps during execution.
	StepExecutionContext = manifest.StepExecutionContext

	// StepRegistry manages step decoders and generates dynamic HCL block schemas without global state.
	StepRegistry = manifest.StepRegistry

	// StepDecoder decodes an HCL block into a concrete [Step] implementation.
	StepDecoder = manifest.StepDecoder

	// Parser coordinates file discovery, HCL parsing, and AST compilation using a configurable [StepRegistry].
	Parser = manifest.Parser

	// Expr represents a compiled HCL expression ready for evaluation against an execution scope.
	Expr = manifest.Expr

	// RecordSeq defines the standard Go iterator yielding record items and potential errors.
	RecordSeq = manifest.RecordSeq

	// StepInput encapsulates the input arguments and active HTTP request passed to a [GoHandler].
	StepInput = manifest.StepInput

	// StepHandler defines the function signature for custom Go step callbacks registered on the runtime.
	StepHandler = manifest.StepHandler

	// Args represents evaluated key-value arguments supplied to a native Go step handler.
	Args = manifest.Args

	// Duration wraps time.Duration to represent human-configured intervals.
	Duration = manifest.Duration

	// ByteSize represents a quantity of bytes.
	ByteSize = manifest.ByteSize

	// Problem represents an RFC 9457 compliant HTTP error payload.
	Problem = problem.Problem

	// InvalidParam describes a single schema constraint violation for 422 Unprocessable Entity responses.
	InvalidParam = problem.InvalidParam

	// Spec represents a compiled, validated OpenAPI 3.1 specification ready for serving.
	Spec = openapi.Spec

	// Engine coordinates HTTP routing, connection pool lifecycles, and request execution.
	// It implements the standard [http.Handler] interface.
	Engine = engine.Engine

	// Option configures an [Engine] during initialization.
	Option = engine.Option
)

// ErrPipelineHalted signals that a terminal step or error catch completed the response.
var ErrPipelineHalted = manifest.ErrPipelineHalted

// Load discovers, reads, merges, and compiles HCL files matching the supplied patterns
// into a validated [Manifest] using default built-in steps.
func Load(patterns ...string) (*Manifest, error) {
	return manifest.Load(patterns...)
}

// Parse compiles an in-memory HCL manifest string into a validated [Manifest] using default built-in steps.
func Parse(source string) (*Manifest, error) {
	return manifest.Parse(source)
}

// New initializes an [Engine] from a validated [Manifest], opening connection pools,
// configuring telemetry, precompiling OpenAPI specifications, and binding routes.
func New(m *Manifest, opts ...Option) (*Engine, error) {
	return engine.New(m, opts...)
}

// CompileSpec builds a validated OpenAPI 3.1 specification from a compiled [Manifest],
// serializing both JSON and YAML documents with calculated SHA-256 ETags.
func CompileSpec(m *Manifest) (*Spec, error) {
	return openapi.Compile(m)
}

// WithGoHandler registers a custom native Go callback by its identifier.
func WithGoHandler(name string, h StepHandler) Option {
	return engine.WithGoHandler(name, h)
}

// WithStep is an alias for [WithGoHandler].
func WithStep(name string, h StepHandler) Option {
	return engine.WithGoHandler(name, h)
}

// WithHTTPClient overrides the default HTTP client used for outbound requests.
func WithHTTPClient(client *http.Client) Option {
	return engine.WithHTTPClient(client)
}

// WithTelemetry overrides the default telemetry instance configured by the manifest.
func WithTelemetry(t *TelemetryService) Option {
	return engine.WithTelemetry(t)
}

// NewStepRegistry returns an empty, isolated step registry for custom language extensions.
func NewStepRegistry() *StepRegistry {
	return manifest.NewStepRegistry()
}

// DefaultStepRegistry returns a pre-configured registry containing all standard built-in steps
// (sql, http, valkey, starlark, go, stream, respond, docs, spec).
func DefaultStepRegistry() *StepRegistry {
	return manifest.DefaultStepRegistry()
}

// NewParser constructs a [Parser] configured with the supplied [StepRegistry].
func NewParser(registry *StepRegistry) *Parser {
	return manifest.NewParser(registry)
}

// NewExpr compiles an HCL expression and optional function registry into an [Expr].
func NewExpr(raw hcl.Expression, funcs map[string]function.Function) Expr {
	return manifest.NewExpr(raw, funcs)
}
