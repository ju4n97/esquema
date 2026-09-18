package manifest

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	byteUnit int64 = 1
	kiloByte int64 = 1000 * byteUnit
	kibiByte int64 = 1024 * byteUnit
	megaByte int64 = 1000 * kiloByte
	mebiByte int64 = 1024 * kibiByte
	gigaByte int64 = 1000 * megaByte
	gibiByte int64 = 1024 * mebiByte
	teraByte int64 = 1000 * gigaByte
	tebiByte int64 = 1024 * gibiByte
)

var byteTiers = [...]struct {
	binVal int64
	binStr string
	decVal int64
	decStr string
}{
	{tebiByte, "TiB", teraByte, "TB"},
	{gibiByte, "GiB", gigaByte, "GB"},
	{mebiByte, "MiB", megaByte, "MB"},
	{kibiByte, "KiB", kiloByte, "KB"},
}

// Duration wraps time.Duration to represent human-configured intervals.
type Duration time.Duration

// Duration returns the underlying time.Duration.
func (d Duration) Duration() time.Duration {
	return time.Duration(d)
}

// String returns the formatted duration string.
func (d Duration) String() string {
	return time.Duration(d).String()
}

// ByteSize represents a quantity of bytes.
type ByteSize int64

// Bytes returns the size in raw bytes as an int64.
func (b ByteSize) Bytes() int64 {
	return int64(b)
}

// String returns a human-readable representation using FormatByteSize.
func (b ByteSize) String() string {
	return FormatByteSize(int64(b))
}

// ParseDuration parses a human-readable duration string into a Duration.
// An empty string, whitespace-only string, or "0" yields a zero duration.
func ParseDuration(raw string) (Duration, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "0" {
		return 0, nil
	}

	d, err := time.ParseDuration(trimmed)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q: %w", raw, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("invalid duration %q: cannot be negative", raw)
	}

	return Duration(d), nil
}

// ParseByteSize converts a size string (e.g. "10MB", "1.5GiB", "1048576") into ByteSize.
func ParseByteSize(raw string) (ByteSize, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "0" {
		return 0, nil
	}

	numPart, unitPart := splitNumericAndUnit(trimmed)
	if numPart == "" {
		return 0, fmt.Errorf("invalid byte size %q: missing numeric magnitude", raw)
	}

	val, err := strconv.ParseFloat(numPart, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid byte size %q: %w", raw, err)
	}
	if val < 0 {
		return 0, fmt.Errorf("invalid byte size %q: cannot be negative", raw)
	}

	multiplier, err := resolveByteMultiplier(unitPart)
	if err != nil {
		return 0, fmt.Errorf("invalid byte size %q: %w", raw, err)
	}

	return ByteSize(val * float64(multiplier)), nil
}

// FormatByteSize formats a byte count into a compact human-readable representation.
// Unaligned values within a magnitude tier fall back to exact byte counts.
func FormatByteSize(bytes int64) string {
	for _, t := range byteTiers {
		if bytes >= t.decVal {
			if bytes%t.binVal == 0 {
				return fmt.Sprintf("%d%s", bytes/t.binVal, t.binStr)
			}
			if bytes%t.decVal == 0 {
				return fmt.Sprintf("%d%s", bytes/t.decVal, t.decStr)
			}
			break
		}
	}
	return fmt.Sprintf("%dB", bytes)
}

// splitNumericAndUnit isolates the numeric prefix from trailing unit characters.
func splitNumericAndUnit(s string) (string, string) {
	i := 0
	if i < len(s) && (s[i] == '+' || s[i] == '-') {
		i++
	}
	for i < len(s) && ((s[i] >= '0' && s[i] <= '9') || s[i] == '.') {
		i++
	}

	if i == 0 || (i == 1 && (s[0] == '+' || s[0] == '-')) {
		return "", s
	}

	return s[:i], strings.TrimSpace(s[i:])
}

// resolveByteMultiplier maps standard binary and decimal suffixes to their byte scalar.
func resolveByteMultiplier(unit string) (int64, error) {
	switch strings.ToUpper(unit) {
	case "", "B":
		return byteUnit, nil
	case "KB", "K":
		return kiloByte, nil
	case "KIB":
		return kibiByte, nil
	case "MB", "M":
		return megaByte, nil
	case "MIB":
		return mebiByte, nil
	case "GB", "G":
		return gigaByte, nil
	case "GIB":
		return gibiByte, nil
	case "TB", "T":
		return teraByte, nil
	case "TIB":
		return tebiByte, nil
	default:
		return 0, fmt.Errorf("unrecognized byte unit %q", unit)
	}
}
