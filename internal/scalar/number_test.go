package scalar_test

import (
	"math"
	"testing"

	"github.com/ju4n97/hclapi/internal/scalar"
)

func TestToInt64(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    any
		expected int64
		expectOK bool
	}{
		{name: "native int", input: 42, expected: 42, expectOK: true},
		{name: "native int64", input: int64(1000), expected: 1000, expectOK: true},
		{name: "numeric string", input: "307471", expected: 307471, expectOK: true},
		{name: "numeric string with whitespace", input: "  999  ", expected: 999, expectOK: true},
		{name: "negative numeric string", input: "-50", expected: -50, expectOK: true},
		{name: "float64 truncated", input: 128.9, expected: 128, expectOK: true},
		{name: "uint64 within range", input: uint64(500), expected: 500, expectOK: true},
		{name: "uint64 overflow", input: uint64(math.MaxUint64), expected: 0, expectOK: false},
		{name: "invalid string", input: "not_a_number", expected: 0, expectOK: false},
		{name: "empty string", input: "", expected: 0, expectOK: false},
		{name: "nil value", input: nil, expected: 0, expectOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := scalar.ToInt64(tt.input)
			if ok != tt.expectOK {
				t.Fatalf("ToInt64(%v) ok = %v; want %v", tt.input, ok, tt.expectOK)
			}
			if got != tt.expected {
				t.Errorf("ToInt64(%v) = %d; want %d", tt.input, got, tt.expected)
			}
		})
	}
}

func TestToFloat64(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    any
		expected float64
		expectOK bool
	}{
		{name: "native float64", input: 3.1415, expected: 3.1415, expectOK: true},
		{name: "numeric float string", input: "99.95", expected: 99.95, expectOK: true},
		{name: "numeric integer string", input: "100", expected: 100.0, expectOK: true},
		{name: "invalid string", input: "abc", expected: 0, expectOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := scalar.ToFloat64(tt.input)
			if ok != tt.expectOK {
				t.Fatalf("ToFloat64(%v) ok = %v; want %v", tt.input, ok, tt.expectOK)
			}
			if got != tt.expected {
				t.Errorf("ToFloat64(%v) = %v; want %v", tt.input, got, tt.expected)
			}
		})
	}
}
