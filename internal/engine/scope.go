package engine

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/valkey-io/valkey-go"

	"github.com/ju4n97/hclapi/internal/manifest"
	"github.com/ju4n97/hclapi/internal/openapi"
	"github.com/ju4n97/hclapi/internal/telemetry"
)

// Dependencies provides runtime connection pools, handlers, schemas, specifications,
// and telemetry instrumentation to [Context].
type Dependencies struct {
	SQL        map[string]*sql.DB
	Valkey     map[string]valkey.Client
	Handlers   map[string]manifest.StepHandler
	Schemas    map[string]manifest.Schema
	Spec       *openapi.Spec
	HTTPClient *http.Client
	Telemetry  *telemetry.Telemetry
}

// Context encapsulates the runtime execution state and dependency provider for a single HTTP request.
// It implements [manifest.StepExecutionContext].
type Context struct {
	req           *http.Request
	rw            http.ResponseWriter
	rc            *http.ResponseController
	deps          Dependencies
	pathParams    map[string]any
	queryParams   map[string]any
	headers       map[string]string
	body          any
	bodyMalformed bool
	now           time.Time
	steps         map[string]any
}

// NewContext constructs an execution context, enforcing body boundaries, normalizing headers,
// and capturing a frozen reference timestamp that remains immutable throughout the request lifecycle.
func NewContext(
	r *http.Request,
	w http.ResponseWriter,
	routePattern string,
	maxBodySize int64,
	deps Dependencies,
) (*Context, error) {
	if r == nil {
		return nil, errors.New("http request is nil")
	}

	if maxBodySize <= 0 {
		maxBodySize = 10 * 1024 * 1024
	}

	now := time.Now().UTC()
	ctx := &Context{
		req:         r,
		rw:          w,
		deps:        deps,
		pathParams:  make(map[string]any),
		queryParams: make(map[string]any),
		headers:     make(map[string]string),
		now:         now,
		steps:       make(map[string]any),
	}

	if w != nil {
		ctx.rc = http.NewResponseController(w)
	}

	if r.Body != nil && r.Body != http.NoBody {
		lr := io.LimitReader(r.Body, maxBodySize+1)
		raw, err := io.ReadAll(lr)
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}

		if int64(len(raw)) > maxBodySize {
			return nil, fmt.Errorf("request body exceeds limit of %d bytes", maxBodySize)
		}

		r.Body = io.NopCloser(bytes.NewReader(raw))

		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &ctx.body); err != nil {
				ctx.bodyMalformed = true
				ctx.body = string(raw)
			}
		}
	}

	fallbackVars := matchPathVariables(routePattern, r.URL.Path)
	for _, p := range extractPathVariables(routePattern) {
		val := r.PathValue(p)
		if val == "" && fallbackVars != nil {
			val = fallbackVars[p]
		}
		ctx.pathParams[p] = val
	}

	for k, v := range r.URL.Query() {
		if len(v) == 1 {
			ctx.queryParams[k] = v[0]
		} else if len(v) > 1 {
			ctx.queryParams[k] = v
		}
	}

	for k, v := range r.Header {
		if len(v) > 0 {
			ctx.headers[strings.ToLower(k)] = v[0]
		}
	}

	return ctx, nil
}

// SQL implements [manifest.StepExecutionContext] and returns a managed database pool by name.
func (c *Context) SQL(name string) (*sql.DB, error) {
	if c.deps.SQL == nil {
		return nil, fmt.Errorf("sql pool %q not configured", name)
	}
	db, exists := c.deps.SQL[name]
	if !exists || db == nil {
		return nil, fmt.Errorf("sql pool %q not configured", name)
	}
	return db, nil
}

// Valkey implements [manifest.StepExecutionContext] and returns an active Valkey client by name.
func (c *Context) Valkey(name string) (valkey.Client, error) {
	if c.deps.Valkey == nil {
		return nil, fmt.Errorf("valkey connection %q not configured", name)
	}
	client, exists := c.deps.Valkey[name]
	if !exists || client == nil {
		return nil, fmt.Errorf("valkey connection %q not configured", name)
	}
	return client, nil
}

// GoHandler implements [manifest.StepExecutionContext] and returns a registered Go callback.
func (c *Context) GoHandler(name string) (manifest.StepHandler, error) {
	if c.deps.Handlers == nil {
		return nil, fmt.Errorf("unregistered go handler %q", name)
	}
	h, exists := c.deps.Handlers[name]
	if !exists || h == nil {
		return nil, fmt.Errorf("unregistered go handler %q", name)
	}
	return h, nil
}

// HTTPClient implements [manifest.StepExecutionContext].
func (c *Context) HTTPClient() *http.Client {
	if c.deps.HTTPClient != nil {
		return c.deps.HTTPClient
	}
	return http.DefaultClient
}

// ResponseWriter implements [manifest.StepExecutionContext].
func (c *Context) ResponseWriter() http.ResponseWriter {
	return c.rw
}

// ResponseController implements [manifest.StepExecutionContext].
func (c *Context) ResponseController() *http.ResponseController {
	return c.rc
}

// OpenAPISpec implements [manifest.StepExecutionContext] and returns the precompiled specification and ETag.
func (c *Context) OpenAPISpec(format string) ([]byte, string, error) {
	if c.deps.Spec == nil {
		return nil, "", errors.New("openapi specification unconfigured")
	}

	if strings.EqualFold(format, "yaml") {
		return c.deps.Spec.YAML, c.deps.Spec.YAMLETag, nil
	}

	return c.deps.Spec.JSON, c.deps.Spec.JSONETag, nil
}

// Scope implements [manifest.StepExecutionContext] and constructs an immutable expression evaluation scope.
func (c *Context) Scope() manifest.Scope {
	return manifest.Scope{
		Request: map[string]any{
			"method":  c.req.Method,
			"path":    c.pathParams,
			"query":   c.queryParams,
			"headers": c.headers,
			"body":    c.body,
		},
		Steps: c.steps,
		Now:   c.now,
	}
}

// Schemas implements [manifest.StepExecutionContext].
func (c *Context) Schemas() map[string]manifest.Schema {
	return c.deps.Schemas
}

// Telemetry returns the active telemetry instance configured for the request context.
func (c *Context) Telemetry() *telemetry.Telemetry {
	return c.deps.Telemetry
}

// SetStepResult records a completed step's output under its published identifier.
func (c *Context) SetStepResult(name string, result any) {
	if name != "" {
		c.steps[name] = result
	}
}

// SetPathParam assigns a coerced scalar value to a path parameter coordinate.
func (c *Context) SetPathParam(name string, val any) {
	c.pathParams[name] = val
}

// SetQueryParam assigns a coerced scalar or slice value to a query parameter coordinate.
func (c *Context) SetQueryParam(name string, val any) {
	c.queryParams[name] = val
}

// BodyMalformed reports whether the incoming JSON payload contained syntax errors.
func (c *Context) BodyMalformed() bool {
	return c.bodyMalformed
}

// Body returns the parsed JSON structure or raw text body.
func (c *Context) Body() any {
	return c.body
}

// SetBody replaces the body payload, typically after defaults injection during ingress validation.
func (c *Context) SetBody(body any) {
	c.body = body
}

// extractPathVariables parses standard Go 1.22+ wildcard parameter names ({param}) from a pattern.
func extractPathVariables(pattern string) []string {
	var vars []string
	for _, segment := range strings.Split(pattern, "/") {
		if strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}") {
			trimmed := segment[1 : len(segment)-1]
			if trimmed != "" {
				vars = append(vars, trimmed)
			}
		}
	}
	return vars
}

// matchPathVariables extracts wildcard parameter values from a URL path using the route pattern
// as a fallback when http.Request.PathValue is unpopulated (e.g. in direct unit test invocations).
func matchPathVariables(pattern, path string) map[string]string {
	if idx := strings.Index(pattern, " "); idx != -1 {
		pattern = pattern[idx+1:]
	}

	cleanPattern := strings.Trim(pattern, "/")
	cleanPath := strings.Trim(path, "/")

	if cleanPattern == "" || cleanPath == "" {
		return nil
	}

	patternParts := strings.Split(cleanPattern, "/")
	pathParts := strings.Split(cleanPath, "/")

	if len(patternParts) != len(pathParts) {
		return nil
	}

	vars := make(map[string]string)
	for i := range patternParts {
		if strings.HasPrefix(patternParts[i], "{") && strings.HasSuffix(patternParts[i], "}") {
			paramName := patternParts[i][1 : len(patternParts[i])-1]
			vars[paramName] = pathParts[i]
		}
	}
	return vars
}
