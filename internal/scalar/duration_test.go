package scalar_test

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ju4n97/hclapi/internal/scalar"
)

func TestDuration_UnmarshalText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected time.Duration
	}{
		{"10s", 10 * time.Second},
		{"500ms", 500 * time.Millisecond},
		{"15m", 15 * time.Minute},
		{"1h", 1 * time.Hour},
		{"1h30m", 90 * time.Minute},
		{"0s", 0},
		{"0", 0},
		{"", 0},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			d, err := scalar.ParseDuration(tt.input)
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.input, err)
			}
			if d.Duration() != tt.expected {
				t.Errorf("Duration() = %v; want %v", d.Duration(), tt.expected)
			}
		})
	}
}

func TestDuration_Invalid(t *testing.T) {
	t.Parallel()

	invalid := []string{"invalid", "10x", "100years", "abc"}
	for _, input := range invalid {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			var d scalar.Duration
			if err := d.UnmarshalText([]byte(input)); err == nil {
				t.Errorf("expected error for %q, got nil", input)
			}
		})
	}
}

func TestDuration_YAML(t *testing.T) {
	t.Parallel()

	var target struct {
		Timeout scalar.Duration `yaml:"timeout"`
	}

	src := "timeout: 30s"
	if err := yaml.Unmarshal([]byte(src), &target); err != nil {
		t.Fatalf("unexpected YAML unmarshal error: %v", err)
	}

	if target.Timeout.Duration() != 30*time.Second {
		t.Errorf("Timeout = %v; want 30s", target.Timeout.Duration())
	}
}
