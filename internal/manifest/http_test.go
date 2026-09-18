package manifest

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/valkey-io/valkey-go"
)

type testHTTPContext struct {
	client   *http.Client
	scope    Scope
	recorder *httptest.ResponseRecorder
}

func (c *testHTTPContext) SQL(name string) (*sql.DB, error) {
	return nil, nil
}

func (c *testHTTPContext) Valkey(name string) (valkey.Client, error) {
	return nil, errors.New("valkey unconfigured")
}

func (c *testHTTPContext) GoHandler(name string) (StepHandler, error) {
	return nil, errors.New("go unconfigured")
}

func (c *testHTTPContext) HTTPClient() *http.Client {
	if c.client != nil {
		return c.client
	}
	return http.DefaultClient
}

func (c *testHTTPContext) ResponseWriter() http.ResponseWriter {
	return c.recorder
}

func (c *testHTTPContext) ResponseController() *http.ResponseController {
	return http.NewResponseController(c.recorder)
}

func (c *testHTTPContext) OpenAPISpec(format string) ([]byte, string, error) {
	return nil, "", nil
}

func (c *testHTTPContext) Scope() Scope {
	return c.scope
}

func (c *testHTTPContext) Schemas() map[string]Schema {
	return nil
}

// TestStepHTTP_ValidateStep verifies method semantics and URL requirement checks.
func TestStepHTTP_ValidateStep(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		step      *StepHTTP
		wantError bool
		errSubstr string
	}{
		{
			name: "valid GET request",
			step: &StepHTTP{
				Name:   "fetch",
				Method: "GET",
				URL:    NewExpr(parseExpr(t, `"https://api.example.com"`), nil),
			},
			wantError: false,
		},
		{
			name: "missing URL expression",
			step: &StepHTTP{
				Name:   "fetch",
				Method: "GET",
			},
			wantError: true,
			errSubstr: "missing target url expression",
		},
		{
			name: "invalid HTTP verb",
			step: &StepHTTP{
				Name:   "fetch",
				Method: "INVALID_VERB",
				URL:    NewExpr(parseExpr(t, `"https://api.example.com"`), nil),
			},
			wantError: true,
			errSubstr: "invalid method \"INVALID_VERB\"",
		},
		{
			name: "default empty method normalizes to valid GET",
			step: &StepHTTP{
				Name: "fetch",
				URL:  NewExpr(parseExpr(t, `"https://api.example.com"`), nil),
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.step.ValidateStep(&Manifest{})
			if (err != nil) != tt.wantError {
				t.Fatalf("ValidateStep() error = %v, wantError = %v", err, tt.wantError)
			}

			if tt.wantError && tt.errSubstr != "" {
				if !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("error %q does not contain expected substring %q", err.Error(), tt.errSubstr)
				}
			}
		})
	}
}

// TestStepHTTP_ExecuteStep verifies round-trip execution, header transmission, and body serialization.
func TestStepHTTP_ExecuteStep(t *testing.T) {
	t.Parallel()

	t.Run("successful GET parsing JSON response", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodGet {
				w.WriteHeader(http.StatusMethodNotAllowed)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"account_id":101,"status":"active"}`))
		}))
		t.Cleanup(server.Close)

		step := &StepHTTP{
			Name:   "get_account",
			Method: "GET",
			URL:    NewExpr(parseExpr(t, `"`+server.URL+`"`), nil),
		}

		exec := &testHTTPContext{
			client:   server.Client(),
			recorder: httptest.NewRecorder(),
		}

		result, err := step.ExecuteStep(context.Background(), exec)
		if err != nil {
			t.Fatalf("ExecuteStep() unexpected error: %v", err)
		}

		resMap, ok := result.(map[string]any)
		if !ok {
			t.Fatalf("expected map[string]any result, got %T", result)
		}

		if resMap["status"] != http.StatusOK {
			t.Errorf("status = %v, want 200", resMap["status"])
		}

		body, ok := resMap["body"].(map[string]any)
		if !ok || body["status"] != "active" || body["account_id"] != float64(101) {
			t.Errorf("unexpected body payload: %+v", resMap["body"])
		}
	})

	t.Run("POST transmitting JSON payload and custom headers", func(t *testing.T) {
		t.Parallel()

		var receivedAuth string
		var receivedBody map[string]any

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedAuth = r.Header.Get("Authorization")
			_ = json.NewDecoder(r.Body).Decode(&receivedBody)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"created":true}`))
		}))
		t.Cleanup(server.Close)

		step := &StepHTTP{
			Name:   "post_item",
			Method: "POST",
			URL:    NewExpr(parseExpr(t, `"`+server.URL+`"`), nil),
			Headers: map[string]string{
				"Authorization": "Bearer secret_token",
			},
			Body: NewExpr(parseExpr(t, `{ sku = "SKU-99", qty = 10 }`), nil),
		}

		exec := &testHTTPContext{
			client:   server.Client(),
			recorder: httptest.NewRecorder(),
		}

		result, err := step.ExecuteStep(context.Background(), exec)
		if err != nil {
			t.Fatalf("ExecuteStep() error: %v", err)
		}

		if receivedAuth != "Bearer secret_token" {
			t.Errorf("received auth = %q, want 'Bearer secret_token'", receivedAuth)
		}
		if receivedBody["sku"] != "SKU-99" || receivedBody["qty"] != float64(10) {
			t.Errorf("unexpected body received by server: %+v", receivedBody)
		}

		resMap := result.(map[string]any)
		if resMap["status"] != http.StatusCreated {
			t.Errorf("status = %v, want 201", resMap["status"])
		}
	})

	t.Run("streaming mode exports live io.ReadCloser", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("chunk-a|chunk-b"))
		}))
		t.Cleanup(server.Close)

		step := &StepHTTP{
			Name:   "stream_call",
			Method: "GET",
			URL:    NewExpr(parseExpr(t, `"`+server.URL+`"`), nil),
			Stream: true,
		}

		exec := &testHTTPContext{
			client:   server.Client(),
			recorder: httptest.NewRecorder(),
		}

		result, err := step.ExecuteStep(context.Background(), exec)
		if err != nil {
			t.Fatalf("ExecuteStep() error: %v", err)
		}

		resMap := result.(map[string]any)
		reader, ok := resMap["stream"].(io.ReadCloser)
		if !ok {
			t.Fatalf("expected stream output of type io.ReadCloser, got %T", resMap["stream"])
		}
		defer reader.Close()

		data, readErr := io.ReadAll(reader)
		if readErr != nil {
			t.Fatalf("read stream error: %v", readErr)
		}

		if string(data) != "chunk-a|chunk-b" {
			t.Errorf("stream data = %q, want 'chunk-a|chunk-b'", string(data))
		}
	})

	t.Run("enforces request timeout deadline", func(t *testing.T) {
		t.Parallel()

		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(150 * time.Millisecond)
			w.WriteHeader(http.StatusOK)
		}))
		t.Cleanup(server.Close)

		step := &StepHTTP{
			Name:    "timeout_call",
			Method:  "GET",
			URL:     NewExpr(parseExpr(t, `"`+server.URL+`"`), nil),
			Timeout: Duration(30 * time.Millisecond),
		}

		exec := &testHTTPContext{
			client:   server.Client(),
			recorder: httptest.NewRecorder(),
		}

		_, err := step.ExecuteStep(context.Background(), exec)
		if err == nil {
			t.Fatal("expected deadline exceeded timeout error, got nil")
		}
	})
}

// TestDecodeStepHTTP verifies direct HCL keyword parsing into a [*StepHTTP] definition.
func TestDecodeStepHTTP(t *testing.T) {
	t.Parallel()

	hclBlock := `
		method  = "POST"
		url     = "https://api.gateway.internal/orders"
		timeout = "25s"
		headers = {
			"X-Tenant-ID" = "tenant-42"
		}
		body = {
			item = "box"
		}
		stream = true
	`

	expr, diags := hclsyntax.ParseConfig([]byte(hclBlock), "test.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("parse error: %s", diags.Error())
	}

	step, err := decodeStepHTTP("create_order", expr.Body, nil, runtimeExprFunctions())
	if err != nil {
		t.Fatalf("decodeStepHTTP error: %v", err)
	}

	httpStep, ok := step.(*StepHTTP)
	if !ok {
		t.Fatalf("expected *StepHTTP, got %T", step)
	}

	if httpStep.StepName() != "create_order" {
		t.Errorf("StepName() = %q, want 'create_order'", httpStep.StepName())
	}
	if httpStep.Method != "POST" {
		t.Errorf("Method = %q, want 'POST'", httpStep.Method)
	}
	if httpStep.Timeout.Duration() != 25*time.Second {
		t.Errorf("Timeout = %v, want 25s", httpStep.Timeout.Duration())
	}
	if httpStep.Headers["X-Tenant-ID"] != "tenant-42" {
		t.Errorf("Headers['X-Tenant-ID'] = %q, want 'tenant-42'", httpStep.Headers["X-Tenant-ID"])
	}
	if !httpStep.Stream {
		t.Error("expected Stream to be true")
	}
}
