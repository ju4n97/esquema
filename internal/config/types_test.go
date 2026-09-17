package config_test

import (
	"strings"
	"testing"

	"github.com/ju4n97/hclapi/internal/config"
)

// TestParseStepType verifies that only canonical step types are accepted.
func TestParseStepType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input       string
		expected    config.StepType
		expectError bool
	}{
		{input: "sql", expected: config.StepTypeSQL},
		{input: "valkey", expected: config.StepTypeValkey},
		{input: "http", expected: config.StepTypeHTTP},
		{input: "starlark", expected: config.StepTypeStarlark},
		{input: "go", expected: config.StepTypeGo},
		{input: "respond", expected: config.StepTypeRespond},
		{input: "docs", expected: config.StepTypeDocs},
		{input: "spec", expected: config.StepTypeSpec},
		{input: "redis", expectError: true},
		{input: "kv", expectError: true},
		{input: "script", expectError: true},
		{input: "call", expectError: true},
		{input: "unknown", expectError: true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()

			got, err := config.ParseStepType(tt.input)
			if tt.expectError && err == nil {
				t.Fatalf("expected error for %q, got nil", tt.input)
			}
			if !tt.expectError && err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.input, err)
			}
			if got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

// TestParseDataType verifies strict adherence to OpenAPI 3.1 primitive types.
func TestParseDataType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input       string
		expected    config.DataType
		expectError bool
	}{
		{input: "string", expected: config.DataTypeString},
		{input: "integer", expected: config.DataTypeInteger},
		{input: "number", expected: config.DataTypeNumber},
		{input: "boolean", expected: config.DataTypeBoolean},
		{input: "array", expected: config.DataTypeArray},
		{input: "object", expected: config.DataTypeObject},
		{input: "int", expectError: true},
		{input: "bool", expectError: true},
		{input: "float", expectError: true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()

			got, err := config.ParseDataType(tt.input)
			if tt.expectError && err == nil {
				t.Fatalf("expected error for %q, got nil", tt.input)
			}
			if !tt.expectError && err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.input, err)
			}
			if got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}

// TestValidateFormat verifies standard OpenAPI semantic format strings.
func TestValidateFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		format      string
		expectError bool
	}{
		{format: "email", expectError: false},
		{format: "uuid", expectError: false},
		{format: "date-time", expectError: false},
		{format: "date", expectError: false},
		{format: "uri", expectError: false},
		{format: "ipv4", expectError: false},
		{format: "ipv6", expectError: false},
		{format: "hostname", expectError: false},
		{format: "", expectError: false},
		{format: "custom-unsupported", expectError: true},
	}

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			t.Parallel()

			err := config.ValidateFormat(tt.format)
			if tt.expectError && err == nil {
				t.Fatalf("expected error for format %q, got nil", tt.format)
			}
			if !tt.expectError && err != nil {
				t.Fatalf("unexpected error for format %q: %v", tt.format, err)
			}
		})
	}
}

// TestParseSQLEngine verifies strict enforcement of canonical database engine names.
func TestParseSQLEngine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input       string
		expected    config.SQLEngine
		expectError bool
		errContains string
	}{
		{input: "postgres", expected: config.SQLEnginePostgres},
		{input: "mysql", expected: config.SQLEngineMySQL},
		{input: "sqlite", expected: config.SQLEngineSQLite},
		{input: "sqlserver", expected: config.SQLEngineSQLServer},
		// Rejection of non-canonical aliases with hints
		{input: "postgresql", expectError: true, errContains: `use canonical name "postgres"`},
		{input: "pgx", expectError: true, errContains: `use canonical name "postgres"`},
		{input: "sqlite3", expectError: true, errContains: `use canonical name "sqlite"`},
		{input: "mariadb", expectError: true, errContains: `use canonical name "mysql"`},
		{input: "mssql", expectError: true, errContains: `use canonical name "sqlserver"`},
		{input: "unknown_db", expectError: true, errContains: `unsupported sql engine`},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()

			got, err := config.ParseSQLEngine(tt.input)
			if tt.expectError {
				if err == nil {
					t.Fatalf("expected error for %q, got nil", tt.input)
				}
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error %q does not contain %q", err.Error(), tt.errContains)
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.input, err)
			}
			if got != tt.expected {
				t.Errorf("got %q, want %q", got, tt.expected)
			}
		})
	}
}
