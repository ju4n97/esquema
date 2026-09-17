package engine

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/ju4n97/hclapi/internal/scalar"
)

// Args represents evaluated arguments passed to a Go step callback.
type Args map[string]any

// Has reports whether key exists in the arguments map and is not nil.
func (a Args) Has(key string) bool {
	if a == nil {
		return false
	}
	val, ok := a[key]
	return ok && val != nil
}

// Get retrieves the argument at key and converts it to type T with scalar coercion.
func (a Args) Get[T any](key string) (T, bool) {
	var zero T
	if a == nil {
		return zero, false
	}

	val, ok := a[key]
	if !ok || val == nil {
		return zero, false
	}

	return coerce[T](val)
}

// GetOr retrieves the argument at key or returns fallback if absent or incompatible.
func (a Args) GetOr[T any](key string, fallback T) T {
	if val, ok := a.Get[T](key); ok {
		return val
	}
	return fallback
}

// Slice retrieves an array of type T at key, safely coercing dynamic slices.
func (a Args) Slice[T any](key string) []T {
	if a == nil {
		return nil
	}

	val, ok := a[key]
	if !ok || val == nil {
		return nil
	}
	if raw, ok := val.([]T); ok {
		return raw
	}

	rv := reflect.ValueOf(val)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil
	}

	res := make([]T, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		elem := rv.Index(i).Interface()
		if elem == nil {
			continue
		}
		if coerced, ok := coerce[T](elem); ok {
			res = append(res, coerced)
		}
	}

	return res
}

// Bind marshals and unmarshals arguments directly into a destination struct pointer.
func (a Args) Bind(dst any) error {
	if a == nil {
		return nil
	}

	data, err := json.Marshal(a)
	if err != nil {
		return fmt.Errorf("marshal args: %w", err)
	}

	if err := json.Unmarshal(data, dst); err != nil {
		return fmt.Errorf("unmarshal args: %w", err)
	}

	return nil
}

// coerce converts val into type T using scalar coercion.
func coerce[T any](val any) (T, bool) {
	var zero T
	if v, ok := val.(T); ok {
		return v, true
	}

	if num, ok := scalar.CoerceNumber[T](val); ok {
		return num, true
	}

	targetType := reflect.TypeOf(zero)
	if targetType == nil {
		return zero, false
	}

	// String and Boolean coercion
	switch targetType.Kind() {
	case reflect.String:
		if s, ok := val.(fmt.Stringer); ok {
			return reflect.ValueOf(s.String()).Convert(targetType).Interface().(T), true
		}
		return reflect.ValueOf(fmt.Sprintf("%v", val)).Convert(targetType).Interface().(T), true

	case reflect.Bool:
		switch b := val.(type) {
		case bool:
			return reflect.ValueOf(b).Convert(targetType).Interface().(T), true
		case string:
			if parsedBool, err := strconv.ParseBool(strings.TrimSpace(b)); err == nil {
				return reflect.ValueOf(parsedBool).Convert(targetType).Interface().(T), true
			}
		}
	}

	return zero, false
}
