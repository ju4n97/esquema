package problem

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

// TestNew verifies constructor defaults, status mapping, and URN generation.
func TestNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		status     int
		detail     string
		wantTitle  string
		wantType   string
		wantDetail string
	}{
		{
			name:       "standard not found",
			status:     http.StatusNotFound,
			detail:     "User record 42 does not exist",
			wantTitle:  "Not Found",
			wantType:   "urn:hclapi:error:not-found",
			wantDetail: "User record 42 does not exist",
		},
		{
			name:       "standard internal server error without detail",
			status:     http.StatusInternalServerError,
			wantTitle:  "Internal Server Error",
			wantType:   "urn:hclapi:error:internal-server-error",
			wantDetail: "",
		},
		{
			name:       "conflict error",
			status:     http.StatusConflict,
			detail:     "Email is already in use",
			wantTitle:  "Conflict",
			wantType:   "urn:hclapi:error:conflict",
			wantDetail: "Email is already in use",
		},
		{
			name:       "unrecognized status code defaults title to error",
			status:     999,
			detail:     "Custom failure",
			wantTitle:  "Error",
			wantType:   "urn:hclapi:error:error",
			wantDetail: "Custom failure",
		},
		{
			name:       "non-positive status code defaults to 500",
			status:     0,
			detail:     "Zero status",
			wantTitle:  "Internal Server Error",
			wantType:   "urn:hclapi:error:internal-server-error",
			wantDetail: "Zero status",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := New(tt.status, tt.detail)

			expectedStatus := tt.status
			if expectedStatus <= 0 {
				expectedStatus = http.StatusInternalServerError
			}

			if p.Status != expectedStatus {
				t.Errorf("Status = %d, want %d", p.Status, expectedStatus)
			}
			if p.Title != tt.wantTitle {
				t.Errorf("Title = %q, want %q", p.Title, tt.wantTitle)
			}
			if p.Type != tt.wantType {
				t.Errorf("Type = %q, want %q", p.Type, tt.wantType)
			}
			if p.Detail != tt.wantDetail {
				t.Errorf("Detail = %q, want %q", p.Detail, tt.wantDetail)
			}
		})
	}
}

// TestValidation verifies 422 Unprocessable Entity construction and invalid parameter binding.
func TestValidation(t *testing.T) {
	t.Parallel()

	params := []InvalidParam{
		{Name: "email", Reason: "must be a valid email address"},
		{Name: "age", Reason: "must be greater than or equal to 18"},
	}

	p := Validation("Payload failed validation", params...)

	if p.Status != http.StatusUnprocessableEntity {
		t.Fatalf("Status = %d, want 422", p.Status)
	}
	if p.Title != "Unprocessable Entity" {
		t.Errorf("Title = %q, want 'Unprocessable Entity'", p.Title)
	}
	if p.Detail != "Payload failed validation" {
		t.Errorf("Detail = %q, want 'Payload failed validation'", p.Detail)
	}
	if !reflect.DeepEqual(p.InvalidParams, params) {
		t.Fatalf("InvalidParams = %+v, want %+v", p.InvalidParams, params)
	}
}

// TestProblem_FluentMethods verifies immutability and builder chaining.
func TestProblem_FluentMethods(t *testing.T) {
	t.Parallel()

	base := New(http.StatusBadRequest, "Invalid payload")
	p := base.
		WithInstance("/users/42").
		WithExtension("trace_id", "trace-xyz-123").
		WithExtension("attempt", 3)

	if p.Instance != "/users/42" {
		t.Errorf("Instance = %q, want '/users/42'", p.Instance)
	}
	if base.Instance != "" {
		t.Error("expected base instance to remain empty (immutable builder pattern)")
	}

	if got := p.Extensions["trace_id"]; got != "trace-xyz-123" {
		t.Errorf("Extensions['trace_id'] = %v, want 'trace-xyz-123'", got)
	}
	if got := p.Extensions["attempt"]; got != 3 {
		t.Errorf("Extensions['attempt'] = %v, want 3", got)
	}
}

// TestProblem_Error verifies formatting of the error message string.
func TestProblem_Error(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		problem   Problem
		wantError string
	}{
		{
			name: "title and detail present",
			problem: Problem{
				Title:  "Bad Request",
				Detail: "Missing required query parameter",
			},
			wantError: "Bad Request: Missing required query parameter",
		},
		{
			name: "title only",
			problem: Problem{
				Title: "Unauthorized",
			},
			wantError: "Unauthorized",
		},
		{
			name: "detail only without title",
			problem: Problem{
				Detail: "Database connection failed",
			},
			wantError: "Database connection failed",
		},
		{
			name: "status code fallback when title and detail are empty",
			problem: Problem{
				Status: 502,
			},
			wantError: "HTTP 502",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.problem.Error(); got != tt.wantError {
				t.Fatalf("Error() = %q, want %q", got, tt.wantError)
			}
		})
	}
}

// TestProblem_MarshalJSON verifies extension flattening and attribute precedence.
func TestProblem_MarshalJSON(t *testing.T) {
	t.Parallel()

	t.Run("flattens extension fields into root object", func(t *testing.T) {
		t.Parallel()

		p := New(http.StatusNotFound, "Item not found").
			WithInstance("/items/10").
			WithExtension("account_id", "acc-99").
			WithExtension("retryable", false)

		data, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("Marshal() error: %v", err)
		}

		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("Unmarshal() error: %v", err)
		}

		if raw["status"] != float64(404) {
			t.Errorf("raw['status'] = %v, want 404", raw["status"])
		}
		if raw["title"] != "Not Found" {
			t.Errorf("raw['title'] = %v, want 'Not Found'", raw["title"])
		}
		if raw["instance"] != "/items/10" {
			t.Errorf("raw['instance'] = %v, want '/items/10'", raw["instance"])
		}
		if raw["account_id"] != "acc-99" {
			t.Errorf("raw['account_id'] = %v, want 'acc-99'", raw["account_id"])
		}
		if raw["retryable"] != false {
			t.Errorf("raw['retryable'] = %v, want false", raw["retryable"])
		}
	})

	t.Run("standard fields overwrite identical extension keys", func(t *testing.T) {
		t.Parallel()

		p := New(http.StatusBadRequest, "Original detail").
			WithExtension("status", 200). // Attempt to spoof status
			WithExtension("title", "OK")  // Attempt to spoof title

		data, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("Marshal() error: %v", err)
		}

		var raw map[string]any
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("Unmarshal() error: %v", err)
		}

		if raw["status"] != float64(400) {
			t.Errorf("spoofed status was not overwritten: got %v, want 400", raw["status"])
		}
		if raw["title"] != "Bad Request" {
			t.Errorf("spoofed title was not overwritten: got %v, want 'Bad Request'", raw["title"])
		}
	})

	t.Run("serializes invalid_params slice correctly", func(t *testing.T) {
		t.Parallel()

		p := Validation("Invalid parameters", InvalidParam{Name: "token", Reason: "expired"})
		data, err := json.Marshal(p)
		if err != nil {
			t.Fatalf("Marshal() error: %v", err)
		}

		var raw struct {
			InvalidParams []InvalidParam `json:"invalid_params"`
		}
		if err := json.Unmarshal(data, &raw); err != nil {
			t.Fatalf("Unmarshal() error: %v", err)
		}

		if len(raw.InvalidParams) != 1 || raw.InvalidParams[0].Name != "token" {
			t.Fatalf("unexpected invalid_params: %+v", raw.InvalidParams)
		}
	})
}

// TestProblem_UnmarshalJSON verifies extraction of standard attributes and capture of extensions.
func TestProblem_UnmarshalJSON(t *testing.T) {
	t.Parallel()

	jsonInput := `{
		"type": "urn:hclapi:error:bad-request",
		"title": "Bad Request",
		"status": 400,
		"detail": "Missing identifier",
		"instance": "/items",
		"invalid_params": [
			{"name": "id", "reason": "required"}
		],
		"trace_id": "trace-101",
		"custom_flag": true
	}`

	var p Problem
	if err := json.Unmarshal([]byte(jsonInput), &p); err != nil {
		t.Fatalf("Unmarshal() error: %v", err)
	}

	if p.Status != 400 {
		t.Errorf("Status = %d, want 400", p.Status)
	}
	if p.Title != "Bad Request" {
		t.Errorf("Title = %q, want 'Bad Request'", p.Title)
	}
	if p.Detail != "Missing identifier" {
		t.Errorf("Detail = %q, want 'Missing identifier'", p.Detail)
	}
	if p.Instance != "/items" {
		t.Errorf("Instance = %q, want '/items'", p.Instance)
	}
	if len(p.InvalidParams) != 1 || p.InvalidParams[0].Name != "id" {
		t.Errorf("unexpected InvalidParams: %+v", p.InvalidParams)
	}

	if len(p.Extensions) != 2 {
		t.Fatalf("expected 2 extension fields, got %d: %+v", len(p.Extensions), p.Extensions)
	}
	if p.Extensions["trace_id"] != "trace-101" {
		t.Errorf("Extensions['trace_id'] = %v, want 'trace-101'", p.Extensions["trace_id"])
	}
	if p.Extensions["custom_flag"] != true {
		t.Errorf("Extensions['custom_flag'] = %v, want true", p.Extensions["custom_flag"])
	}
}

// TestWrite verifies streaming serialization, status codes, and media type headers.
func TestWrite(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		problem    Problem
		wantStatus int
	}{
		{
			name:       "standard 404 response",
			problem:    New(http.StatusNotFound, "Resource missing"),
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "standard 500 response",
			problem:    New(http.StatusInternalServerError, "Internal failure"),
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "invalid status code defaults to 500",
			problem:    Problem{Status: 999, Title: "Out of range"},
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := httptest.NewRecorder()
			Write(rec, tt.problem)

			if rec.Code != tt.wantStatus {
				t.Errorf("HTTP status = %d, want %d", rec.Code, tt.wantStatus)
			}

			contentType := rec.Header().Get("Content-Type")
			if contentType != ContentType {
				t.Errorf("Content-Type = %q, want %q", contentType, ContentType)
			}

			var body map[string]any
			if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
				t.Fatalf("failed to decode response body JSON: %v", err)
			}

			if int(body["status"].(float64)) != tt.problem.Status {
				t.Errorf("body['status'] = %v, want %d", body["status"], tt.problem.Status)
			}
		})
	}

	t.Run("nil response writer does not panic", func(t *testing.T) {
		t.Parallel()

		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Write() panicked on nil writer: %v", r)
			}
		}()

		Write(nil, New(http.StatusOK))
	})
}
