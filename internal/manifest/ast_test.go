package manifest

import (
	"strings"
	"testing"
)

// TestManifest_Validate verifies manifest topology, schema references, and step constraints.
func TestManifest_Validate(t *testing.T) {
	t.Parallel()

	validSchemas := map[string]Schema{
		"User": {
			Name: "User",
			Fields: map[string]Field{
				"id": {
					Name: "id",
					Type: TypeSpec{Type: TypeInteger},
				},
				"email": {
					Name:   "email",
					Type:   TypeSpec{Type: TypeString},
					Format: FormatEmail,
				},
			},
		},
	}

	validConnections := map[string]Connection{
		"main": {
			Name: "main",
			Type: "sql",
		},
		"cache": {
			Name: "cache",
			Type: "valkey",
		},
	}

	tests := []struct {
		name      string
		manifest  *Manifest
		wantError bool
		errSubstr string
	}{
		{
			name:      "nil manifest returns error",
			manifest:  nil,
			wantError: true,
			errSubstr: "manifest is nil",
		},
		{
			name: "valid manifest with terminal step passes",
			manifest: &Manifest{
				Connections: validConnections,
				Schemas:     validSchemas,
				Routes: []Route{
					{
						Method: "GET",
						Path:   "/users",
						Steps: []Step{
							&StepSQL{
								Name:       "fetch",
								Connection: "main",
								Query:      "SELECT id, email FROM users",
							},
							&StepRespond{
								Status: 200,
								Schema: &TypeSpec{Type: TypeObject, SchemaRef: "User"},
							},
						},
					},
				},
			},
			wantError: false,
		},
		{
			name: "schema field references unknown schema",
			manifest: &Manifest{
				Schemas: map[string]Schema{
					"Profile": {
						Name: "Profile",
						Fields: map[string]Field{
							"user": {
								Name: "user",
								Type: TypeSpec{Type: TypeObject, SchemaRef: "MissingSchema"},
							},
						},
					},
				},
			},
			wantError: true,
			errSubstr: "references unknown schema \"MissingSchema\"",
		},
		{
			name: "schema field has invalid format string",
			manifest: &Manifest{
				Schemas: map[string]Schema{
					"Item": {
						Name: "Item",
						Fields: map[string]Field{
							"code": {
								Name:   "code",
								Type:   TypeSpec{Type: TypeString},
								Format: Format("invalid-format"),
							},
						},
					},
				},
			},
			wantError: true,
			errSubstr: "unrecognized format",
		},
		{
			name: "route with invalid HTTP method",
			manifest: &Manifest{
				Routes: []Route{
					{
						Method: "INVALID",
						Path:   "/test",
						Steps:  []Step{&StepRespond{Status: 200}},
					},
				},
			},
			wantError: true,
			errSubstr: "unsupported HTTP method",
		},
		{
			name: "route path missing leading slash",
			manifest: &Manifest{
				Routes: []Route{
					{
						Method: "GET",
						Path:   "users",
						Steps:  []Step{&StepRespond{Status: 200}},
					},
				},
			},
			wantError: true,
			errSubstr: "path must begin with leading slash",
		},
		{
			name: "duplicate route patterns",
			manifest: &Manifest{
				Routes: []Route{
					{
						Method: "GET",
						Path:   "/users",
						Steps:  []Step{&StepRespond{Status: 200}},
					},
					{
						Method: "GET",
						Path:   "/users",
						Steps:  []Step{&StepRespond{Status: 200}},
					},
				},
			},
			wantError: true,
			errSubstr: "duplicate route pattern \"GET /users\"",
		},
		{
			name: "route lacking any terminal step",
			manifest: &Manifest{
				Connections: validConnections,
				Routes: []Route{
					{
						Method: "GET",
						Path:   "/users",
						Steps: []Step{
							&StepSQL{
								Name:       "fetch",
								Connection: "main",
								Query:      "SELECT 1",
							},
						},
					},
				},
			},
			wantError: true,
			errSubstr: "route must declare at least one terminal step",
		},
		{
			name: "route request references unknown body schema",
			manifest: &Manifest{
				Routes: []Route{
					{
						Method:  "POST",
						Path:    "/users",
						Request: &Request{BodyRef: "NonExistent"},
						Steps:   []Step{&StepRespond{Status: 201}},
					},
				},
			},
			wantError: true,
			errSubstr: "request body references unknown schema \"NonExistent\"",
		},
		{
			name: "nil step in route pipeline",
			manifest: &Manifest{
				Routes: []Route{
					{
						Method: "GET",
						Path:   "/items",
						Steps:  []Step{nil, &StepRespond{Status: 200}},
					},
				},
			},
			wantError: true,
			errSubstr: "step cannot be nil",
		},
		{
			name: "non-terminal step missing name",
			manifest: &Manifest{
				Connections: validConnections,
				Routes: []Route{
					{
						Method: "GET",
						Path:   "/users",
						Steps: []Step{
							&StepSQL{
								Name:       "",
								Connection: "main",
								Query:      "SELECT 1",
							},
							&StepRespond{Status: 200},
						},
					},
				},
			},
			wantError: true,
			errSubstr: "non-terminal step must define a non-empty name",
		},
		{
			name: "duplicate non-terminal step names in same route",
			manifest: &Manifest{
				Connections: validConnections,
				Routes: []Route{
					{
						Method: "GET",
						Path:   "/users",
						Steps: []Step{
							&StepSQL{
								Name:       "query",
								Connection: "main",
								Query:      "SELECT 1",
							},
							&StepSQL{
								Name:       "query",
								Connection: "main",
								Query:      "SELECT 2",
							},
							&StepRespond{Status: 200},
						},
					},
				},
			},
			wantError: true,
			errSubstr: "duplicate step name \"query\" in pipeline",
		},
		{
			name: "step validator failure propagates",
			manifest: &Manifest{
				Connections: validConnections,
				Routes: []Route{
					{
						Method: "GET",
						Path:   "/broken",
						Steps: []Step{
							&StepSQL{
								Name:       "broken_query",
								Connection: "missing_pool",
								Query:      "SELECT 1",
							},
							&StepRespond{Status: 200},
						},
					},
				},
			},
			wantError: true,
			errSubstr: "references unknown connection \"missing_pool\"",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.manifest.Validate()
			if (err != nil) != tt.wantError {
				t.Fatalf("Validate() error = %v, wantError = %v", err, tt.wantError)
			}

			if tt.wantError && tt.errSubstr != "" {
				if !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("error %q does not contain expected substring %q", err.Error(), tt.errSubstr)
				}
			}
		})
	}
}

// TestRoute_Helpers verifies route pattern formatting and terminal step detection.
func TestRoute_Helpers(t *testing.T) {
	t.Parallel()

	t.Run("Pattern normalizes method case", func(t *testing.T) {
		t.Parallel()

		r := Route{
			Method: "post",
			Path:   "/accounts/{id}",
		}

		if got := r.Pattern(); got != "POST /accounts/{id}" {
			t.Fatalf("Pattern() = %q, want 'POST /accounts/{id}'", got)
		}
	})

	t.Run("HasTerminalStep detects terminal steps", func(t *testing.T) {
		t.Parallel()

		nonTerminalRoute := Route{
			Steps: []Step{
				&StepSQL{Name: "fetch"},
			},
		}

		terminalRoute := Route{
			Steps: []Step{
				&StepSQL{Name: "fetch"},
				&StepRespond{Status: 200},
			},
		}

		if nonTerminalRoute.HasTerminalStep() {
			t.Error("expected nonTerminalRoute.HasTerminalStep() to be false")
		}

		if !terminalRoute.HasTerminalStep() {
			t.Error("expected terminalRoute.HasTerminalStep() to be true")
		}
	})
}

// TestRequest_HasRules verifies ingress rule detection across coordinates.
func TestRequest_HasRules(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		request *Request
		want    bool
	}{
		{
			name:    "nil request",
			request: nil,
			want:    false,
		},
		{
			name:    "empty request",
			request: &Request{},
			want:    false,
		},
		{
			name: "has path rule",
			request: &Request{
				Path: map[string]Field{"id": {Name: "id"}},
			},
			want: true,
		},
		{
			name: "has query rule",
			request: &Request{
				Query: map[string]Field{"limit": {Name: "limit"}},
			},
			want: true,
		},
		{
			name: "has header rule",
			request: &Request{
				Headers: map[string]Field{"x-trace": {Name: "x-trace"}},
			},
			want: true,
		},
		{
			name: "has body rule",
			request: &Request{
				Body: map[string]Field{"name": {Name: "name"}},
			},
			want: true,
		},
		{
			name: "has body schema reference",
			request: &Request{
				BodyRef: "User",
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.request.HasRules(); got != tt.want {
				t.Fatalf("HasRules() = %v, want %v", got, tt.want)
			}
		})
	}
}
