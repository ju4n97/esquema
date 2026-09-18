package manifest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"runtime/debug"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/zclconf/go-cty/cty/function"

	"github.com/ju4n97/hclapi/internal/problem"
)

// Args represents evaluated key-value arguments supplied to a native Go step handler.
type Args map[string]any

// Has reports whether key exists in the arguments map and is not nil.
func (a Args) Has(key string) bool {
	if a == nil {
		return false
	}
	val, exists := a[key]
	return exists && val != nil
}

// Get retrieves the argument associated with key and coerces it to type T.
// It returns (zero, false) if the key is missing, nil, or cannot be coerced to T.
func (a Args) Get[T any](key string) (T, bool) {
	var zero T
	if a == nil {
		return zero, false
	}

	val, exists := a[key]
	if !exists || val == nil {
		return zero, false
	}

	return coerce[T](val)
}

// GetOr retrieves the argument at key coerced to type T, returning fallback if absent or incompatible.
func (a Args) GetOr[T any](key string, fallback T) T {
	if val, ok := a.Get[T](key); ok {
		return val
	}
	return fallback
}

// Slice retrieves an array at key and coerces its elements to type T.
// If the value is not a slice or array, it returns nil.
func (a Args) Slice[T any](key string) []T {
	if a == nil {
		return nil
	}

	val, exists := a[key]
	if !exists || val == nil {
		return nil
	}

	if direct, ok := val.([]T); ok {
		return direct
	}

	rv := reflect.ValueOf(val)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
		return nil
	}

	result := make([]T, 0, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		elem := rv.Index(i).Interface()
		if elem == nil {
			continue
		}
		if coerced, ok := coerce[T](elem); ok {
			result = append(result, coerced)
		}
	}

	return result
}

// Bind serializes arguments to JSON and deserializes them into the destination struct pointer.
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

// coerce safely converts arbitrary scalar values into type T without panic.
func coerce[T any](val any) (T, bool) {
	var zero T
	if direct, ok := val.(T); ok {
		return direct, true
	}

	switch any(zero).(type) {
	case int:
		if n, ok := toInt64(val); ok {
			return any(int(n)).(T), true
		}
	case int8:
		if n, ok := toInt64(val); ok {
			return any(int8(n)).(T), true
		}
	case int16:
		if n, ok := toInt64(val); ok {
			return any(int16(n)).(T), true
		}
	case int32:
		if n, ok := toInt64(val); ok {
			return any(int32(n)).(T), true
		}
	case int64:
		if n, ok := toInt64(val); ok {
			return any(n).(T), true
		}
	case uint:
		if n, ok := toInt64(val); ok && n >= 0 {
			return any(uint(n)).(T), true
		}
	case uint8:
		if n, ok := toInt64(val); ok && n >= 0 {
			return any(uint8(n)).(T), true
		}
	case uint16:
		if n, ok := toInt64(val); ok && n >= 0 {
			return any(uint16(n)).(T), true
		}
	case uint32:
		if n, ok := toInt64(val); ok && n >= 0 {
			return any(uint32(n)).(T), true
		}
	case uint64:
		if n, ok := toInt64(val); ok && n >= 0 {
			return any(uint64(n)).(T), true
		}
	case float32:
		if f, ok := toFloat64(val); ok {
			return any(float32(f)).(T), true
		}
	case float64:
		if f, ok := toFloat64(val); ok {
			return any(f).(T), true
		}
	case bool:
		switch b := val.(type) {
		case bool:
			return any(b).(T), true
		case string:
			if parsed, err := strconv.ParseBool(strings.TrimSpace(b)); err == nil {
				return any(parsed).(T), true
			}
		case int, int64:
			return any(val != 0).(T), true
		}
	case string:
		if s, ok := val.(fmt.Stringer); ok {
			return any(s.String()).(T), true
		}
		return any(fmt.Sprintf("%v", val)).(T), true
	}

	return zero, false
}

// toInt64 converts various numeric primitives and string numbers to int64.
func toInt64(val any) (int64, bool) {
	switch v := val.(type) {
	case int:
		return int64(v), true
	case int64:
		return v, true
	case int32:
		return int64(v), true
	case int16:
		return int64(v), true
	case int8:
		return int64(v), true
	case uint:
		return int64(v), true
	case uint64:
		return int64(v), true
	case uint32:
		return int64(v), true
	case uint16:
		return int64(v), true
	case uint8:
		return int64(v), true
	case float64:
		return int64(v), true
	case float32:
		return int64(v), true
	case string:
		if i, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return i, true
		}
	}
	return 0, false
}

// toFloat64 converts various numeric primitives and string numbers to float64.
func toFloat64(val any) (float64, bool) {
	switch v := val.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case int32:
		return float64(v), true
	case uint:
		return float64(v), true
	case uint64:
		return float64(v), true
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return f, true
		}
	}
	return 0, false
}

// GoRequest encapsulates the input arguments and active HTTP request passed to a [GoHandler].
type GoRequest struct {
	Name    string
	Args    Args
	Request *http.Request
}

// GoHandler defines the function signature for custom Go step callbacks registered on the runtime.
type GoHandler func(ctx context.Context, req *GoRequest) (any, error)

// StepGo invokes a custom Go callback function registered on the runtime engine.
type StepGo struct {
	Name string
	Use  string
	Args Expr
	When Expr
}

// StepName implements [Step].
func (g *StepGo) StepName() string {
	return g.Name
}

// StepWhen implements [Step].
func (g *StepGo) StepWhen() Expr {
	return g.When
}

// IsTerminal implements [Step] and returns false.
func (g *StepGo) IsTerminal() bool {
	return false
}

// ValidateStep implements [StepValidator]. It verifies that the callback identifier is non-empty.
func (g *StepGo) ValidateStep(m *Manifest) error {
	if strings.TrimSpace(g.Use) == "" {
		return fmt.Errorf("go step %q missing handler identifier in 'use'", g.Name)
	}
	return nil
}

// ExecuteStep implements [StepExecutor]. It resolves the registered handler, evaluates arguments,
// executes the callback behind a panic boundary, and captures returned data or error problems.
func (g *StepGo) ExecuteStep(ctx context.Context, ec StepExecutionContext) (res any, err error) {
	handler, hErr := ec.GoHandler(g.Use)
	if hErr != nil {
		return nil, fmt.Errorf("step %q: %w", g.Name, hErr)
	}

	var evalArgs map[string]any
	if g.Args != nil {
		m, aErr := g.Args.EvalMap(ec.Scope())
		if aErr != nil {
			return nil, fmt.Errorf("step %q evaluate args: %w", g.Name, aErr)
		}
		evalArgs = m
	}

	req := &GoRequest{
		Name: g.Name,
		Args: Args(evalArgs),
	}

	defer func() {
		if rec := recover(); rec != nil {
			stack := string(debug.Stack())
			p := problem.New(http.StatusInternalServerError, fmt.Sprintf("step %q panic: %v", g.Name, rec)).
				WithExtension("stack", stack)

			problem.Write(ec.ResponseWriter(), p)
			err = ErrPipelineHalted
		}
	}()

	callRes, callErr := handler(ctx, req)
	if callErr != nil {
		var prob problem.Problem
		if errors.As(callErr, &prob) {
			problem.Write(ec.ResponseWriter(), prob)
			return nil, ErrPipelineHalted
		}
		return nil, fmt.Errorf("step %q execution: %w", g.Name, callErr)
	}

	return map[string]any{
		"result": callRes,
	}, nil
}

// decodeStepGo decodes an HCL block into a [*StepGo].
func decodeStepGo(name string, body hcl.Body, evalCtx *hcl.EvalContext, funcs map[string]function.Function) (Step, error) {
	type goDecode struct {
		Use      string         `hcl:"use"`
		ArgsExpr hcl.Expression `hcl:"args,optional"`
		WhenExpr hcl.Expression `hcl:"when,optional"`
	}

	var raw goDecode
	if diags := gohcl.DecodeBody(body, evalCtx, &raw); diags.HasErrors() {
		return nil, diags
	}

	return &StepGo{
		Name: name,
		Use:  strings.TrimSpace(raw.Use),
		Args: NewExpr(raw.ArgsExpr, funcs),
		When: NewExpr(raw.WhenExpr, funcs),
	}, nil
}
