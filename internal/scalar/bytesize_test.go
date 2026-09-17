package scalar_test

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/ju4n97/hclapi/internal/scalar"
)

func TestByteSize_Parse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input    string
		expected scalar.ByteSize
	}{
		{"10MB", 10 * 1000 * 1000},
		{"10MiB", 10 * 1024 * 1024},
		{"512B", 512},
		{"1GB", 1000 * 1000 * 1000},
		{"1GiB", 1024 * 1024 * 1024},
		{"1048576", 1048576},
		{"2.5MB", 2500000},
		{"0", 0},
		{"", 0},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			b, err := scalar.ParseByteSize(tt.input)
			if err != nil {
				t.Fatalf("unexpected error for %q: %v", tt.input, err)
			}
			if b != tt.expected {
				t.Errorf("ParseByteSize(%q) = %d; want %d", tt.input, b, tt.expected)
			}
			if b.Bytes() != int64(tt.expected) {
				t.Errorf("Bytes() = %d; want %d", b.Bytes(), tt.expected)
			}
		})
	}
}

func TestByteSize_Invalid(t *testing.T) {
	t.Parallel()

	invalid := []string{"invalid", "10XB", "MB", "fooB"}
	for _, input := range invalid {
		t.Run(input, func(t *testing.T) {
			t.Parallel()
			var b scalar.ByteSize
			if err := b.UnmarshalText([]byte(input)); err == nil {
				t.Errorf("expected error for %q, got nil", input)
			}
		})
	}
}

func TestByteSize_YAML(t *testing.T) {
	t.Parallel()

	var target struct {
		Limit scalar.ByteSize `yaml:"limit"`
	}

	src := "limit: 25MB"
	if err := yaml.Unmarshal([]byte(src), &target); err != nil {
		t.Fatalf("unexpected YAML unmarshal error: %v", err)
	}

	if target.Limit.Bytes() != 25*1000*1000 {
		t.Errorf("Limit = %d; want 25000000", target.Limit.Bytes())
	}
}
