package engine

import (
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"

	"github.com/ju4n97/hclapi/internal/config"
	"github.com/ju4n97/hclapi/internal/problem"
)

// executeGo invokes a registered native Go callback function with boundary panic recovery.
func (e *Engine) executeGo(ctx *Context, w http.ResponseWriter, name string, step *config.GoStep) (err error) {
	e.regMu.RLock()
	handler, ok := e.registry[step.Use]
	e.regMu.RUnlock()

	if !ok {
		p := problem.New(http.StatusInternalServerError, fmt.Sprintf("unregistered step handler %q", step.Use))
		problem.Write(w, p)
		return p
	}

	var rawArgs map[string]any
	if step.ArgsExpr != nil {
		raw, evalErr := ctx.EvalAny(step.ArgsExpr)
		if evalErr == nil {
			rawArgs, _ = raw.(map[string]any)
		}
	}

	defer func() {
		if rec := recover(); rec != nil {
			stack := string(debug.Stack())
			e.logger.ErrorContext(ctx.RequestContext(), "panic in go step callback",
				"step", name,
				"handler", step.Use,
				"panic", rec,
				"stack", stack,
			)

			p := problem.New(http.StatusInternalServerError, fmt.Sprintf("step %q panic: %v", name, rec))
			problem.Write(w, p)
			err = p
		}
	}()

	res, callErr := handler(ctx.RequestContext(), &Step{
		Name:    name,
		Args:    Args(rawArgs),
		Request: ctx.req,
	})
	if callErr != nil {
		var p problem.Problem
		if errors.As(callErr, &p) {
			problem.Write(w, p)
			return callErr
		}

		p = problem.New(http.StatusInternalServerError, callErr.Error())
		problem.Write(w, p)
		return callErr
	}

	ctx.SetStepResult(name, map[string]any{"result": res})
	return nil
}
