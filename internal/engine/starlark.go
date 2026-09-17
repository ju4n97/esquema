package engine

import (
	"fmt"
	"net/http"
	"time"

	"go.starlark.net/starlark"

	"github.com/ju4n97/hclapi/internal/problem"
)

const maxStarlarkSteps = 100_000

// executeStarlark executes a precompiled Starlark callable against the current request state.
func (e *Engine) executeStarlark(ctx *Context, w http.ResponseWriter, routePattern, name string) error {
	key := routePattern + ":" + name
	callable, ok := e.starlarkFuncs[key]
	if !ok {
		p := problem.New(http.StatusInternalServerError, fmt.Sprintf("starlark step %q not initialized", name))
		problem.Write(w, p)
		return p
	}

	thread := &starlark.Thread{Name: "hclapi-starlark-vm"}
	thread.SetMaxExecutionSteps(maxStarlarkSteps)

	starCtx := toStarlarkValue(map[string]any{
		"request":   ctx.requestData(),
		"steps":     ctx.steps,
		"timestamp": time.Now().Unix(),
	})

	resultVal, err := starlark.Call(thread, callable, starlark.Tuple{starCtx}, nil)
	if err != nil {
		p := problem.New(http.StatusInternalServerError, fmt.Sprintf("step %q runtime error: %v", name, err))
		problem.Write(w, p)
		return p
	}

	ctx.SetStepResult(name, map[string]any{
		"result": fromStarlarkValue(resultVal),
	})
	return nil
}

// toStarlarkValue converts Go primitives, maps, and lists into Starlark values.
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
		d := starlark.NewDict(len(val))
		for k, item := range val {
			_ = d.SetKey(starlark.String(k), toStarlarkValue(item))
		}
		return d
	case map[string]string:
		d := starlark.NewDict(len(val))
		for k, item := range val {
			_ = d.SetKey(starlark.String(k), starlark.String(item))
		}
		return d
	case []any:
		l := make([]starlark.Value, len(val))
		for i, item := range val {
			l[i] = toStarlarkValue(item)
		}
		return starlark.NewList(l)
	default:
		return starlark.String(fmt.Sprintf("%v", val))
	}
}

// fromStarlarkValue converts Starlark values into standard Go primitives, maps, and lists.
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
		i, _ := val.Int64()
		return i
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
