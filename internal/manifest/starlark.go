package manifest

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/zclconf/go-cty/cty/function"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
)

const maxStarlarkSteps uint64 = 100_000

var starlarkFileOptions = &syntax.FileOptions{
	Set:       true,
	While:     true,
	Recursion: true,
}

// StepStarlark executes a sandboxed, deterministic Starlark (Python dialect) script
// for in-memory payload transformation, field enrichment, and filtering.
//
// Scripts run in a hermetic runtime without access to the host filesystem, network,
// or environment variables. Execution is bounded by an instruction budget of 100,000 steps
// to guarantee termination and prevent runaway loops or unbounded recursion.
//
// The script must declare an entrypoint function with the signature:
//
//	def execute(ctx):
//	    ...
//	    return result
//
// The return value is unmarshaled into native Go types and published to the request scope
// as steps.<name>.result.
type StepStarlark struct {
	Name   string
	Source string
	When   Expr

	compiledFn starlark.Callable
}

// StepName implements [Step].
func (s *StepStarlark) StepName() string {
	return s.Name
}

// StepWhen implements [Step].
func (s *StepStarlark) StepWhen() Expr {
	return s.When
}

// IsTerminal implements [Step] and returns false.
func (s *StepStarlark) IsTerminal() bool {
	return false
}

// ValidateStep implements [StepValidator]. It compiles the script at boot time, verifies
// syntax validity, and ensures that an 'execute(ctx)' callable entrypoint is exported.
func (s *StepStarlark) ValidateStep(m *Manifest) error {
	trimmed := strings.TrimSpace(s.Source)
	if trimmed == "" {
		return fmt.Errorf("starlark step %q source cannot be empty", s.Name)
	}

	thread := &starlark.Thread{Name: "compile"}
	globals, err := starlark.ExecFileOptions(starlarkFileOptions, thread, s.Name+".star", trimmed, nil)
	if err != nil {
		return fmt.Errorf("starlark step %q syntax error: %w", s.Name, err)
	}

	execVal, exists := globals["execute"]
	if !exists {
		return fmt.Errorf("starlark step %q: script must define an 'execute(ctx)' function", s.Name)
	}

	callable, ok := execVal.(starlark.Callable)
	if !ok {
		return fmt.Errorf("starlark step %q: 'execute' must be a callable function, got %s", s.Name, execVal.Type())
	}

	s.compiledFn = callable
	return nil
}

// ExecuteStep implements [StepExecutor]. It populates the execution context dictionary,
// bounds execution with a 100,000 instruction limit, and returns the unpacked result.
func (s *StepStarlark) ExecuteStep(ctx context.Context, ec StepExecutionContext) (any, error) {
	if s.compiledFn == nil {
		if err := s.ValidateStep(nil); err != nil {
			return nil, err
		}
	}

	thread := &starlark.Thread{Name: "starlark-vm"}
	thread.SetMaxExecutionSteps(maxStarlarkSteps)

	scope := ec.Scope()
	ctxDict := toStarlarkValue(map[string]any{
		"request":   scope.Request,
		"steps":     scope.Steps,
		"timestamp": scope.Now.Unix(),
		"now":       scope.Now.Format(time.RFC3339),
	})

	resultVal, err := starlark.Call(thread, s.compiledFn, starlark.Tuple{ctxDict}, nil)
	if err != nil {
		return nil, fmt.Errorf("step %q starlark execution: %w", s.Name, err)
	}

	return map[string]any{
		"result": fromStarlarkValue(resultVal),
	}, nil
}

// toStarlarkValue converts Go primitives, maps, and slices into their corresponding Starlark values.
func toStarlarkValue(v any) starlark.Value {
	if v == nil {
		return starlark.None
	}

	switch val := v.(type) {
	case string:
		return starlark.String(val)
	case bool:
		return starlark.Bool(val)
	case int:
		return starlark.MakeInt(val)
	case int64:
		return starlark.MakeInt64(val)
	case float64:
		return starlark.Float(val)
	case map[string]any:
		dict := starlark.NewDict(len(val))
		for k, item := range val {
			_ = dict.SetKey(starlark.String(k), toStarlarkValue(item))
		}
		return dict
	case map[string]string:
		dict := starlark.NewDict(len(val))
		for k, item := range val {
			_ = dict.SetKey(starlark.String(k), starlark.String(item))
		}
		return dict
	case []any:
		elems := make([]starlark.Value, len(val))
		for i, item := range val {
			elems[i] = toStarlarkValue(item)
		}
		return starlark.NewList(elems)
	case []map[string]any:
		elems := make([]starlark.Value, len(val))
		for i, item := range val {
			elems[i] = toStarlarkValue(item)
		}
		return starlark.NewList(elems)
	default:
		return starlark.String(fmt.Sprintf("%v", val))
	}
}

// fromStarlarkValue converts Starlark structures back into standard Go primitives, slices, and maps.
func fromStarlarkValue(v starlark.Value) any {
	if v == nil || v == starlark.None {
		return nil
	}

	switch val := v.(type) {
	case starlark.String:
		return val.GoString()
	case starlark.Bool:
		return bool(val)
	case starlark.Int:
		if i, ok := val.Int64(); ok {
			return i
		}
		return val.String()
	case starlark.Float:
		return float64(val)
	case *starlark.List:
		res := make([]any, val.Len())
		for i := 0; i < val.Len(); i++ {
			res[i] = fromStarlarkValue(val.Index(i))
		}
		return res
	case *starlark.Dict:
		res := make(map[string]any, val.Len())
		for _, item := range val.Items() {
			k := item.Index(0).String()
			if s, ok := item.Index(0).(starlark.String); ok {
				k = s.GoString()
			}
			res[k] = fromStarlarkValue(item.Index(1))
		}
		return res
	default:
		return val.String()
	}
}

// decodeStepStarlark decodes the HCL block representation into a [*StepStarlark].
func decodeStepStarlark(name string, body hcl.Body, evalCtx *hcl.EvalContext, funcs map[string]function.Function) (Step, error) {
	type starlarkDecode struct {
		Source   string         `hcl:"source"`
		WhenExpr hcl.Expression `hcl:"when,optional"`
	}

	var raw starlarkDecode
	if diags := gohcl.DecodeBody(body, evalCtx, &raw); diags.HasErrors() {
		return nil, diags
	}

	return &StepStarlark{
		Name:   name,
		Source: raw.Source,
		When:   NewExpr(raw.WhenExpr, funcs),
	}, nil
}
