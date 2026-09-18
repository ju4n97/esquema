package engine

import (
	"bytes"
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/ju4n97/hclapi/internal/manifest"
	"github.com/ju4n97/hclapi/internal/openapi"
)

// TestNewContext_CreationAndPayloadBoundaries verifies body bounding, coordinate extraction, and frozen time.
func TestNewContext_CreationAndPayloadBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("nil request returns error", func(t *testing.T) {
		t.Parallel()

		_, err := NewContext(nil, nil, "/users", 1024, Dependencies{})
		if err == nil {
			t.Fatal("expected error on nil request, got nil")
		}
	})

	t.Run("parses coordinates, headers, and JSON body", func(t *testing.T) {
		t.Parallel()

		payload := `{"name":"Alice","role":"admin"}`
		req := httptest.NewRequest(http.MethodPost, "/orgs/42/users?role=staff&tag=web&tag=prod", strings.NewReader(payload))
		req.SetPathValue("org_id", "42")
		req.Header.Set("X-Custom-Token", "secret-token-123")

		rec := httptest.NewRecorder()
		deps := Dependencies{
			Schemas: map[string]manifest.Schema{"User": {Name: "User"}},
		}

		ctx, err := NewContext(req, rec, "POST /orgs/{org_id}/users", 1024, deps)
		if err != nil {
			t.Fatalf("NewContext() unexpected error: %v", err)
		}

		if ctx.BodyMalformed() {
			t.Error("expected bodyMalformed to be false")
		}

		bodyMap, ok := ctx.Body().(map[string]any)
		if !ok || bodyMap["name"] != "Alice" {
			t.Fatalf("unexpected body: %+v", ctx.Body())
		}

		scope := ctx.Scope()
		reqData, ok := scope.Request["body"].(map[string]any)
		if !ok || reqData["role"] != "admin" {
			t.Errorf("scope.Request body = %+v, want role=admin", scope.Request["body"])
		}

		queryData := scope.Request["query"].(map[string]any)
		if queryData["role"] != "staff" {
			t.Errorf("query single value = %v, want 'staff'", queryData["role"])
		}
		expectedTags := []string{"web", "prod"}
		if !reflect.DeepEqual(queryData["tag"], expectedTags) {
			t.Errorf("query slice = %v, want %v", queryData["tag"], expectedTags)
		}

		headersData := scope.Request["headers"].(map[string]string)
		if headersData["x-custom-token"] != "secret-token-123" {
			t.Errorf("headers = %+v, want lowercase key 'x-custom-token'", headersData)
		}

		pathData := scope.Request["path"].(map[string]any)
		if pathData["org_id"] != "42" {
			t.Errorf("path org_id = %v, want '42'", pathData["org_id"])
		}

		t1 := ctx.Scope().Now
		time.Sleep(10 * time.Millisecond)
		t2 := ctx.Scope().Now
		if !t1.Equal(t2) {
			t.Errorf("frozen time drifted: t1 = %v, t2 = %v", t1, t2)
		}
	})

	t.Run("rejects payload exceeding maxBodySize", func(t *testing.T) {
		t.Parallel()

		oversized := bytes.Repeat([]byte("a"), 2048)
		req := httptest.NewRequest(http.MethodPost, "/upload", bytes.NewReader(oversized))

		_, err := NewContext(req, httptest.NewRecorder(), "POST /upload", 1024, Dependencies{})
		if err == nil || !strings.Contains(err.Error(), "exceeds limit") {
			t.Fatalf("expected payload limit error, got: %v", err)
		}
	})

	t.Run("handles malformed JSON body gracefully", func(t *testing.T) {
		t.Parallel()

		broken := `{"unclosed": "json`
		req := httptest.NewRequest(http.MethodPost, "/data", strings.NewReader(broken))

		ctx, err := NewContext(req, httptest.NewRecorder(), "POST /data", 1024, Dependencies{})
		if err != nil {
			t.Fatalf("unexpected error on malformed body: %v", err)
		}

		if !ctx.BodyMalformed() {
			t.Error("expected BodyMalformed() to be true")
		}

		if str, ok := ctx.Body().(string); !ok || str != broken {
			t.Errorf("Body() = %v, want raw broken string %q", ctx.Body(), broken)
		}
	})
}

// TestContext_StepExecutionContextMethods verifies provider contracts for databases, handlers, and steps.
func TestContext_StepExecutionContextMethods(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("failed to open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	handler := func(ctx context.Context, req *manifest.StepInput) (any, error) {
		return "handler_ok", nil
	}

	spec := &openapi.Spec{
		JSON:     []byte(`{"openapi":"3.1.0"}`),
		JSONETag: `"json-etag-1"`,
		YAML:     []byte(`openapi: 3.1.0`),
		YAMLETag: `"yaml-etag-1"`,
	}

	deps := Dependencies{
		SQL:      map[string]*sql.DB{"primary": db},
		Handlers: map[string]manifest.StepHandler{"test.handler": handler},
		Schemas:  map[string]manifest.Schema{"Item": {Name: "Item"}},
		Spec:     spec,
	}

	req := httptest.NewRequest(http.MethodGet, "/items", http.NoBody)
	rec := httptest.NewRecorder()

	ctx, err := NewContext(req, rec, "GET /items", 1024, deps)
	if err != nil {
		t.Fatalf("NewContext() unexpected error: %v", err)
	}

	t.Run("SQL pool lookup", func(t *testing.T) {
		t.Parallel()

		gotDB, err := ctx.SQL("primary")
		if err != nil || gotDB != db {
			t.Fatalf("SQL('primary') = (%v, %v), want (%v, nil)", gotDB, err, db)
		}

		_, missingErr := ctx.SQL("missing")
		if missingErr == nil {
			t.Fatal("expected error on missing SQL pool, got nil")
		}
	})

	t.Run("Valkey lookup unconfigured", func(t *testing.T) {
		t.Parallel()

		_, err := ctx.Valkey("cache")
		if err == nil {
			t.Fatal("expected error for unconfigured Valkey, got nil")
		}
	})

	t.Run("GoHandler lookup", func(t *testing.T) {
		t.Parallel()

		h, err := ctx.GoHandler("test.handler")
		if err != nil {
			t.Fatalf("GoHandler('test.handler') error: %v", err)
		}

		out, err := h(context.Background(), nil)
		if err != nil || out != "handler_ok" {
			t.Fatalf("handler out = (%v, %v), want ('handler_ok', nil)", out, err)
		}

		_, missing := ctx.GoHandler("unregistered")
		if missing == nil {
			t.Fatal("expected error for unregistered handler")
		}
	})

	t.Run("OpenAPISpec retrieval", func(t *testing.T) {
		t.Parallel()

		jsonBytes, jsonETag, err := ctx.OpenAPISpec("json")
		if err != nil || string(jsonBytes) != `{"openapi":"3.1.0"}` || jsonETag != `"json-etag-1"` {
			t.Fatalf("unexpected JSON spec output: (%s, %s, %v)", string(jsonBytes), jsonETag, err)
		}

		yamlBytes, yamlETag, err := ctx.OpenAPISpec("yaml")
		if err != nil || string(yamlBytes) != `openapi: 3.1.0` || yamlETag != `"yaml-etag-1"` {
			t.Fatalf("unexpected YAML spec output: (%s, %s, %v)", string(yamlBytes), yamlETag, err)
		}
	})

	t.Run("Step output publishing and coordinate coercion", func(t *testing.T) {
		t.Parallel()

		ctx.SetStepResult("query_step", map[string]any{"count": 42})
		ctx.SetPathParam("id", int64(100))
		ctx.SetQueryParam("limit", int64(50))
		ctx.SetBody(map[string]any{"injected": true})

		scope := ctx.Scope()
		stepData := scope.Steps["query_step"].(map[string]any)
		if stepData["count"] != 42 {
			t.Errorf("step output = %v, want 42", stepData["count"])
		}

		pathData := scope.Request["path"].(map[string]any)
		if pathData["id"] != int64(100) {
			t.Errorf("coerced path = %v, want int64(100)", pathData["id"])
		}

		queryData := scope.Request["query"].(map[string]any)
		if queryData["limit"] != int64(50) {
			t.Errorf("coerced query = %v, want int64(50)", queryData["limit"])
		}

		bodyData := scope.Request["body"].(map[string]any)
		if bodyData["injected"] != true {
			t.Errorf("injected body = %v, want true", bodyData["injected"])
		}
	})
}
