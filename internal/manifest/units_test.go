package manifest

import (
	"strings"
	"testing"
	"time"
)

// TestParseDuration verifies parsing of human-readable duration strings.
func TestParseDuration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		want      Duration
		wantError bool
		errSubstr string
	}{
		{
			name:  "milliseconds",
			input: "500ms",
			want:  Duration(500 * time.Millisecond),
		},
		{
			name:  "seconds",
			input: "15s",
			want:  Duration(15 * time.Second),
		},
		{
			name:  "minutes",
			input: "10m",
			want:  Duration(10 * time.Minute),
		},
		{
			name:  "hours",
			input: "2h",
			want:  Duration(2 * time.Hour),
		},
		{
			name:  "compound duration",
			input: "1h30m",
			want:  Duration(90 * time.Minute),
		},
		{
			name:  "whitespace padded",
			input: "  30s  ",
			want:  Duration(30 * time.Second),
		},
		{
			name:  "empty string yields zero duration",
			input: "",
			want:  0,
		},
		{
			name:  "zero string yields zero duration",
			input: "0",
			want:  0,
		},
		{
			name:      "negative duration rejected",
			input:     "-15s",
			wantError: true,
			errSubstr: "cannot be negative",
		},
		{
			name:      "unsupported unit",
			input:     "10x",
			wantError: true,
			errSubstr: "invalid duration",
		},
		{
			name:      "non-numeric input",
			input:     "invalid",
			wantError: true,
			errSubstr: "invalid duration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseDuration(tt.input)
			if (err != nil) != tt.wantError {
				t.Fatalf("ParseDuration(%q) error = %v, wantError = %v", tt.input, err, tt.wantError)
			}

			if tt.wantError {
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("error %q does not contain substring %q", err.Error(), tt.errSubstr)
				}
				return
			}

			if got != tt.want {
				t.Fatalf("ParseDuration(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestParseByteSize verifies parsing of decimal and binary byte sizes.
func TestParseByteSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		want      ByteSize
		wantError bool
		errSubstr string
	}{
		{
			name:  "explicit bytes",
			input: "512B",
			want:  512,
		},
		{
			name:  "raw byte integer",
			input: "1048576",
			want:  1048576,
		},
		{
			name:  "decimal kilobytes",
			input: "10KB",
			want:  10000,
		},
		{
			name:  "binary kibibytes",
			input: "10KiB",
			want:  10240,
		},
		{
			name:  "decimal megabytes",
			input: "25MB",
			want:  25000000,
		},
		{
			name:  "binary mebibytes",
			input: "25MiB",
			want:  26214400,
		},
		{
			name:  "decimal gigabytes",
			input: "2GB",
			want:  2000000000,
		},
		{
			name:  "binary gibibytes",
			input: "2GiB",
			want:  2147483648,
		},
		{
			name:  "floating point decimal",
			input: "1.5MB",
			want:  1500000,
		},
		{
			name:  "whitespace padded",
			input: "  50MB  ",
			want:  50000000,
		},
		{
			name:  "empty string yields zero bytes",
			input: "",
			want:  0,
		},
		{
			name:  "zero string yields zero bytes",
			input: "0",
			want:  0,
		},
		{
			name:      "negative magnitude rejected",
			input:     "-10MB",
			wantError: true,
			errSubstr: "cannot be negative",
		},
		{
			name:      "missing numeric magnitude",
			input:     "MB",
			wantError: true,
			errSubstr: "missing numeric magnitude",
		},
		{
			name:      "unrecognized unit",
			input:     "100XB",
			wantError: true,
			errSubstr: "unrecognized byte unit",
		},
		{
			name:      "non-numeric characters in magnitude",
			input:     "tenMB",
			wantError: true,
			errSubstr: "invalid byte size",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseByteSize(tt.input)
			if (err != nil) != tt.wantError {
				t.Fatalf("ParseByteSize(%q) error = %v, wantError = %v", tt.input, err, tt.wantError)
			}

			if tt.wantError {
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("error %q does not contain substring %q", err.Error(), tt.errSubstr)
				}
				return
			}

			if got != tt.want {
				t.Fatalf("ParseByteSize(%q) = %d, want %d", tt.input, got, tt.want)
			}
		})
	}
}

// TestFormatByteSize verifies human-readable formatting of byte quantities.
func TestFormatByteSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		bytes int64
		want  string
	}{
		{
			name:  "raw bytes",
			bytes: 512,
			want:  "512B",
		},
		{
			name:  "exact decimal kilobytes",
			bytes: 10000,
			want:  "10KB",
		},
		{
			name:  "exact binary kibibytes",
			bytes: 10240,
			want:  "10KiB",
		},
		{
			name:  "exact decimal megabytes",
			bytes: 25000000,
			want:  "25MB",
		},
		{
			name:  "exact binary mebibytes",
			bytes: 26214400,
			want:  "25MiB",
		},
		{
			name:  "exact decimal gigabytes",
			bytes: 2000000000,
			want:  "2GB",
		},
		{
			name:  "exact binary gibibytes",
			bytes: 2147483648,
			want:  "2GiB",
		},
		{
			name:  "unaligned magnitude falls back to bytes",
			bytes: 1550000,
			want:  "1550000B",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := FormatByteSize(tt.bytes); got != tt.want {
				t.Fatalf("FormatByteSize(%d) = %q, want %q", tt.bytes, got, tt.want)
			}
		})
	}
}
