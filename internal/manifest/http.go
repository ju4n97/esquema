package manifest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/zclconf/go-cty/cty/function"
)

const maxHTTPResponseLimit int64 = 10 * 1024 * 1024

// StepHTTP makes outbound HTTP calls to external APIs.
//
// In standard execution, response bodies up to 10MB are buffered, automatically
// parsed as JSON if applicable, and published to context as steps.<name>.status
// and steps.<name>.body. When Stream is true, buffering is skipped and the live
// socket is exported as an io.ReadCloser under steps.<name>.stream.
type StepHTTP struct {
	Name    string
	Method  string
	URL     Expr
	Body    Expr
	When    Expr
	Timeout Duration
	Headers map[string]string
	Stream  bool
}

// StepName implements [Step].
func (h *StepHTTP) StepName() string {
	return h.Name
}

// StepWhen implements [Step].
func (h *StepHTTP) StepWhen() Expr {
	return h.When
}

// IsTerminal implements [Step] and returns false.
func (h *StepHTTP) IsTerminal() bool {
	return false
}

// ValidateStep implements [StepValidator]. It verifies that the URL expression is declared
// and that the configured HTTP verb matches a standard HTTP method.
func (h *StepHTTP) ValidateStep(m *Manifest) error {
	if h.URL == nil {
		return fmt.Errorf("http step %q missing target url expression", h.Name)
	}

	method := strings.ToUpper(h.Method)
	if method == "" {
		method = http.MethodGet
	}

	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodHead, http.MethodOptions:
	default:
		return fmt.Errorf("http step %q invalid method %q", h.Name, h.Method)
	}

	return nil
}

// ExecuteStep implements [StepExecutor]. It resolves dynamic URLs and request bodies,
// injects headers, enforces request deadlines, and executes the call via the engine client.
func (h *StepHTTP) ExecuteStep(ctx context.Context, ec StepExecutionContext) (any, error) {
	urlVal, err := h.URL.Eval(ec.Scope())
	if err != nil {
		return nil, fmt.Errorf("step %q evaluate url: %w", h.Name, err)
	}

	urlStr := strings.TrimSpace(fmt.Sprintf("%v", urlVal))
	if urlStr == "" || urlStr == "<nil>" {
		return nil, fmt.Errorf("step %q target url evaluated to empty string", h.Name)
	}

	var bodyReader io.Reader
	if h.Body != nil {
		bodyData, err := h.Body.Eval(ec.Scope())
		if err != nil {
			return nil, fmt.Errorf("step %q evaluate body: %w", h.Name, err)
		}
		if bodyData != nil {
			switch v := bodyData.(type) {
			case []byte:
				bodyReader = bytes.NewReader(v)
			case string:
				bodyReader = strings.NewReader(v)
			default:
				jsonBytes, mErr := json.Marshal(bodyData)
				if mErr != nil {
					return nil, fmt.Errorf("step %q marshal json body: %w", h.Name, mErr)
				}
				bodyReader = bytes.NewReader(jsonBytes)
			}
		}
	}

	reqMethod := strings.ToUpper(h.Method)
	if reqMethod == "" {
		reqMethod = http.MethodGet
	}

	reqCtx := ctx
	var cancel context.CancelFunc

	if !h.Stream {
		timeout := h.Timeout.Duration()
		if timeout <= 0 {
			timeout = 15 * time.Second
		}
		reqCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	req, err := http.NewRequestWithContext(reqCtx, reqMethod, urlStr, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("step %q create request: %w", h.Name, err)
	}

	if bodyReader != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	for k, v := range h.Headers {
		req.Header.Set(k, v)
	}

	client := ec.HTTPClient()
	if client == nil {
		client = http.DefaultClient
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("step %q execute http call: %w", h.Name, err)
	}

	if h.Stream {
		return map[string]any{
			"status": resp.StatusCode,
			"stream": resp.Body,
		}, nil
	}
	defer resp.Body.Close()

	limitedReader := io.LimitReader(resp.Body, maxHTTPResponseLimit+1)
	respBytes, err := io.ReadAll(limitedReader)
	if err != nil {
		return nil, fmt.Errorf("step %q read response body: %w", h.Name, err)
	}

	if int64(len(respBytes)) > maxHTTPResponseLimit {
		return nil, fmt.Errorf("step %q response body exceeded %d bytes limit", h.Name, maxHTTPResponseLimit)
	}

	var parsedBody any
	if len(respBytes) > 0 {
		trimmed := bytes.TrimSpace(respBytes)
		if (bytes.HasPrefix(trimmed, []byte("{")) && bytes.HasSuffix(trimmed, []byte("}"))) ||
			(bytes.HasPrefix(trimmed, []byte("[")) && bytes.HasSuffix(trimmed, []byte("]"))) {
			if jErr := json.Unmarshal(trimmed, &parsedBody); jErr != nil {
				parsedBody = string(respBytes)
			}
		} else {
			parsedBody = string(respBytes)
		}
	}

	return map[string]any{
		"status": resp.StatusCode,
		"body":   parsedBody,
	}, nil
}

// decodeStepHTTP decodes an HCL block into a [*StepHTTP].
func decodeStepHTTP(name string, body hcl.Body, evalCtx *hcl.EvalContext, funcs map[string]function.Function) (Step, error) {
	type httpDecode struct {
		Method     string            `hcl:"method,optional"`
		URLExpr    hcl.Expression    `hcl:"url"`
		BodyExpr   hcl.Expression    `hcl:"body,optional"`
		WhenExpr   hcl.Expression    `hcl:"when,optional"`
		TimeoutRaw string            `hcl:"timeout,optional"`
		Headers    map[string]string `hcl:"headers,optional"`
		Stream     bool              `hcl:"stream,optional"`
	}

	var raw httpDecode
	raw.Method = "GET"
	if diags := gohcl.DecodeBody(body, evalCtx, &raw); diags.HasErrors() {
		return nil, diags
	}

	step := &StepHTTP{
		Name:    name,
		Method:  raw.Method,
		Headers: raw.Headers,
		Stream:  raw.Stream,
		URL:     NewExpr(raw.URLExpr, funcs),
		Body:    NewExpr(raw.BodyExpr, funcs),
		When:    NewExpr(raw.WhenExpr, funcs),
	}

	if raw.TimeoutRaw != "" {
		d, err := ParseDuration(raw.TimeoutRaw)
		if err != nil {
			return nil, fmt.Errorf("http step %q timeout: %w", name, err)
		}
		step.Timeout = d
	}

	return step, nil
}
