package engine_test

import (
	"reflect"
	"testing"

	"github.com/ju4n97/hclapi/internal/engine"
)

// TestArgs_GetAndCoercion verifies typed argument extraction and numeric coercion.
func TestArgs_GetAndCoercion(t *testing.T) {
	t.Parallel()

	args := engine.Args{
		"limit":     int64(50),
		"threshold": float64(98.5),
		"enabled":   "true",
		"endpoint":  "https://api.internal",
	}

	t.Run("retrieves string directly", func(t *testing.T) {
		t.Parallel()
		val, ok := args.Get[string]("endpoint")
		if !ok || val != "https://api.internal" {
			t.Errorf("Get[string]() = (%v, %v); want ('https://api.internal', true)", val, ok)
		}
	})

	t.Run("coerces int64 to int", func(t *testing.T) {
		t.Parallel()
		val, ok := args.Get[int]("limit")
		if !ok || val != 50 {
			t.Errorf("Get[int]() = (%v, %v); want (50, true)", val, ok)
		}
	})

	t.Run("coerces string boolean to bool", func(t *testing.T) {
		t.Parallel()
		val, ok := args.Get[bool]("enabled")
		if !ok || !val {
			t.Errorf("Get[bool]() = (%v, %v); want (true, true)", val, ok)
		}
	})

	t.Run("returns fallback using GetOr", func(t *testing.T) {
		t.Parallel()
		if val := args.GetOr("missing_key", 8080); val != 8080 {
			t.Errorf("GetOr() fallback = %v; want 8080", val)
		}
	})

	t.Run("reports key existence via Has", func(t *testing.T) {
		t.Parallel()
		if !args.Has("limit") {
			t.Error("expected Has('limit') to be true")
		}
		if args.Has("nonexistent") {
			t.Error("expected Has('nonexistent') to be false")
		}
	})
}

// TestArgs_SliceAndBind verifies typed slice extraction and struct deserialization.
func TestArgs_SliceAndBind(t *testing.T) {
	t.Parallel()

	args := engine.Args{
		"tags":  []any{"web", "prod"},
		"ports": []any{int64(80), int64(443)},
	}

	t.Run("coerces slice elements to strings", func(t *testing.T) {
		t.Parallel()
		tags := args.Slice[string]("tags")
		expected := []string{"web", "prod"}
		if !reflect.DeepEqual(tags, expected) {
			t.Errorf("Slice[string] = %v; want %v", tags, expected)
		}
	})

	t.Run("coerces slice elements to integers", func(t *testing.T) {
		t.Parallel()
		ports := args.Slice[int]("ports")
		expected := []int{80, 443}
		if !reflect.DeepEqual(ports, expected) {
			t.Errorf("Slice[int] = %v; want %v", ports, expected)
		}
	})

	t.Run("binds arguments directly to struct", func(t *testing.T) {
		t.Parallel()
		type Payload struct {
			Tags  []string `json:"tags"`
			Ports []int    `json:"ports"`
		}

		var p Payload
		if err := args.Bind(&p); err != nil {
			t.Fatalf("Bind failed: %v", err)
		}
		if len(p.Tags) != 2 || len(p.Ports) != 2 {
			t.Errorf("unexpected bound payload: %+v", p)
		}
	})
}
