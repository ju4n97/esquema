package problem_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ju4n97/hclapi/internal/problem"
)

// TestProblem_Serialization verifies RFC 9457 JSON marshaling and extension field inlining.
func TestProblem_Serialization(t *testing.T) {
	t.Parallel()

	p := problem.New(http.StatusConflict, "Entity already exists")
	p.Instance = "/accounts/42"
	p.Extensions = map[string]any{
		"trace_id": "trace-999",
	}
	p.InvalidParams = []problem.InvalidParam{
		{Name: "email", Reason: "unique constraint violation"},
	}

	rawJSON, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("failed to marshal problem: %v", err)
	}

	var parsed map[string]any
	err = json.Unmarshal(rawJSON, &parsed)
	if err != nil {
		t.Fatalf("failed to unmarshal problem json: %v", err)
	}

	if parsed["status"] != float64(409) {
		t.Errorf("status = %v; want 409", parsed["status"])
	}
	if parsed["title"] != "Conflict" {
		t.Errorf("title = %v; want 'Conflict'", parsed["title"])
	}
	if parsed["trace_id"] != "trace-999" {
		t.Errorf("trace_id = %v; want 'trace-999'", parsed["trace_id"])
	}

	rec := httptest.NewRecorder()
	problem.Write(rec, p)

	if rec.Code != http.StatusConflict {
		t.Errorf("status code = %d; want 409", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "application/problem+json" {
		t.Errorf("Content-Type = %q; want 'application/problem+json'", rec.Header().Get("Content-Type"))
	}
}
