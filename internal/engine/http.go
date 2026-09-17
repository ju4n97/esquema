package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"

	"github.com/ju4n97/hclapi/internal/config"
	"github.com/ju4n97/hclapi/internal/problem"
)

// maxHTTPResponseBodyLimit restricts remote payloads to 10MB to prevent memory exhaustion.
const maxHTTPResponseBodyLimit = 10 * 1024 * 1024

// defaultTransport provides an optimized connection pool for outbound HTTP steps.
var defaultTransport = &http.Transport{
	MaxIdleConns:        100,
	MaxIdleConnsPerHost: 20,
	IdleConnTimeout:     90 * time.Second,
}

var sharedHTTPClient = &http.Client{
	Transport: defaultTransport,
}

// executeHTTP performs an outbound HTTP request, evaluating dynamic URLs and enforcing deadlines.
func (e *Engine) executeHTTP(ctx *Context, w http.ResponseWriter, name string, step *config.HTTPStep) error {
	urlRaw, err := ctx.EvalAny(step.URLExpr)
	if err != nil || urlRaw == nil {
		p := problem.New(http.StatusInternalServerError, fmt.Sprintf("step %q resolve url: %v", name, err))
		problem.Write(w, p)
		return p
	}
	resolvedURL := fmt.Sprintf("%v", urlRaw)

	var bodyReader io.Reader
	if step.BodyExpr != nil {
		bodyData, err := ctx.EvalAny(step.BodyExpr)
		if err == nil && bodyData != nil {
			b, marshalErr := json.Marshal(bodyData)
			if marshalErr != nil {
				p := problem.New(http.StatusInternalServerError, fmt.Sprintf("step %q marshal request body: %v", name, marshalErr))
				problem.Write(w, p)
				return p
			}
			bodyReader = bytes.NewReader(b)
		}
	}

	timeout := step.Timeout.Duration()
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	reqCtx, cancel := context.WithTimeout(ctx.RequestContext(), timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, step.Method, resolvedURL, bodyReader)
	if err != nil {
		p := problem.New(http.StatusInternalServerError, fmt.Sprintf("step %q create request: %v", name, err))
		problem.Write(w, p)
		return p
	}

	if bodyReader != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range step.Headers {
		req.Header.Set(k, v)
	}

	// Inject W3C traceparent headers so downstream services continue the same trace waterfall
	otel.GetTextMapPropagator().Inject(reqCtx, propagation.HeaderCarrier(req.Header))

	resp, err := sharedHTTPClient.Do(req)
	if err != nil {
		p := problem.New(http.StatusBadGateway, fmt.Sprintf("step %q http call failed: %v", name, err))
		problem.Write(w, p)
		return p
	}
	defer resp.Body.Close()

	limitedReader := io.LimitReader(resp.Body, maxHTTPResponseBodyLimit)
	respBytes, err := io.ReadAll(limitedReader)
	if err != nil {
		p := problem.New(http.StatusBadGateway, fmt.Sprintf("step %q read response: %v", name, err))
		problem.Write(w, p)
		return p
	}

	var respData any
	if err := json.Unmarshal(respBytes, &respData); err != nil {
		respData = string(respBytes)
	}

	ctx.SetStepResult(name, map[string]any{
		"status": resp.StatusCode,
		"body":   respData,
	})

	return nil
}
