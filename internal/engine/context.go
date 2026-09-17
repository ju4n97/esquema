package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/ju4n97/hclapi/internal/ctyconv"
)

var pathParamRegex = regexp.MustCompile(`\{([a-zA-Z0-9_]+)\}`)

// Context encapsulates request metadata, body data, and pipeline step execution results.
type Context struct {
	req           *http.Request
	pathParams    map[string]any
	queryParams   map[string]any
	headers       map[string]string
	bodyData      any
	bodyMalformed bool
	steps         map[string]any
}

// NewContext builds an execution context enforcing bounded body reads.
func NewContext(r *http.Request, routePattern string, maxBytes int64) (*Context, error) {
	var bodyData any
	var malformed bool

	if r.Body != nil && r.Body != http.NoBody {
		lr := io.LimitReader(r.Body, maxBytes+1)
		raw, err := io.ReadAll(lr)
		if err != nil {
			return nil, fmt.Errorf("read request body: %w", err)
		}
		if int64(len(raw)) > maxBytes {
			return nil, fmt.Errorf("request body exceeds limit of %d bytes", maxBytes)
		}

		r.Body = io.NopCloser(bytes.NewReader(raw))

		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &bodyData); err != nil {
				malformed = true
				bodyData = string(raw)
			}
		}
	}

	pathParams := make(map[string]any)
	matches := pathParamRegex.FindAllStringSubmatch(routePattern, -1)
	for _, m := range matches {
		if len(m) > 1 {
			p := m[1]
			pathParams[p] = r.PathValue(p)
		}
	}

	queryParams := make(map[string]any)
	for k, v := range r.URL.Query() {
		if len(v) > 0 {
			queryParams[k] = v[0]
		}
	}

	headers := make(map[string]string)
	for k, v := range r.Header {
		if len(v) > 0 {
			headers[strings.ToLower(k)] = v[0]
		}
	}

	return &Context{
		req:           r,
		pathParams:    pathParams,
		queryParams:   queryParams,
		headers:       headers,
		bodyData:      bodyData,
		bodyMalformed: malformed,
		steps:         make(map[string]any),
	}, nil
}

// requestData builds the canonical request representation.
func (c *Context) requestData() map[string]any {
	return map[string]any{
		"method":  c.req.Method,
		"path":    c.pathParams,
		"query":   c.queryParams,
		"headers": c.headers,
		"body":    c.bodyData,
	}
}

// EvalContext constructs the live HCL evaluation context containing request and step data.
func (c *Context) EvalContext() *hcl.EvalContext {
	ctxObj := ctyconv.ToCty(map[string]any{
		"request":   c.requestData(),
		"timestamp": time.Now().Unix(),
		"now":       time.Now().UTC().Format(time.RFC3339),
	})

	return &hcl.EvalContext{
		Variables: map[string]cty.Value{
			"ctx":   ctxObj,
			"steps": ctyconv.ToCty(c.steps),
		},
		Functions: ctyconv.BuiltinFunctions(),
	}
}

// EvalBool evaluates an HCL expression expecting a boolean outcome.
func (c *Context) EvalBool(expr hcl.Expression) (bool, error) {
	return ctyconv.EvalBool(expr, c.EvalContext())
}

// EvalAny evaluates an HCL expression into a native Go value.
func (c *Context) EvalAny(expr hcl.Expression) (any, error) {
	return ctyconv.EvalAny(expr, c.EvalContext())
}

// SetStepResult registers outputs from an executed step into the context.
func (c *Context) SetStepResult(name string, res any) {
	c.steps[name] = res
}

// RequestContext returns the standard request context.
func (c *Context) RequestContext() context.Context {
	return c.req.Context()
}
