package engine

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/ju4n97/hclapi/internal/manifest"
	"github.com/ju4n97/hclapi/internal/problem"
)

// buildRouteHandler compiles a [manifest.Route] into an [http.HandlerFunc] coordinating
// ingress validation, OpenTelemetry step span tracking, sequential step execution,
// and terminal response streaming.
func buildRouteHandler(r manifest.Route, deps Dependencies, maxBodySize int64) http.HandlerFunc {
	pattern := r.Pattern()

	return func(w http.ResponseWriter, req *http.Request) {
		ctx, err := NewContext(req, w, pattern, maxBodySize, deps)
		if err != nil {
			p := problem.New(http.StatusBadRequest, err.Error())
			problem.Write(w, p)
			return
		}

		if prob := ValidateIngress(ctx, r.Request, deps.Schemas); prob != nil {
			problem.Write(w, *prob)
			return
		}

		for _, step := range r.Steps {
			if reqErr := req.Context().Err(); reqErr != nil {
				p := problem.New(http.StatusGatewayTimeout, "Client request canceled")
				problem.Write(w, p)
				return
			}

			if when := step.StepWhen(); when != nil {
				matched, evalErr := when.EvalBool(ctx.Scope())
				if evalErr != nil {
					p := problem.New(http.StatusInternalServerError, evalErr.Error())
					problem.Write(w, p)
					return
				}
				if !matched {
					continue
				}
			}

			executor, ok := step.(manifest.StepExecutor)
			if !ok {
				continue
			}

			stepCtx := req.Context()
			var endSpan func(error)
			if deps.Telemetry != nil {
				stepType := deriveStepType(step)
				stepCtx, endSpan = deps.Telemetry.StartStepSpan(stepCtx, stepType, step.StepName())
			}

			res, execErr := executor.ExecuteStep(stepCtx, ctx)
			if endSpan != nil {
				if errors.Is(execErr, manifest.ErrPipelineHalted) {
					endSpan(nil)
				} else {
					endSpan(execErr)
				}
			}

			if execErr != nil {
				if errors.Is(execErr, manifest.ErrPipelineHalted) {
					return
				}

				p := problem.New(http.StatusInternalServerError, execErr.Error())
				problem.Write(w, p)
				return
			}

			if step.IsTerminal() {
				return
			}

			ctx.SetStepResult(step.StepName(), res)
		}

		p := problem.New(http.StatusInternalServerError, "Pipeline completed without producing a response")
		problem.Write(w, p)
	}
}

// deriveStepType extracts a normalized, lowercase step kind identifier from a [manifest.Step] implementation.
// It strips pointer indicators, package qualifiers, and the canonical "Step" prefix (e.g. "*manifest.StepSQL"
// becomes "sql", and "*engine.testStepMock" becomes "teststepmock").
func deriveStepType(step manifest.Step) string {
	typeName := fmt.Sprintf("%T", step)
	typeName = strings.TrimPrefix(typeName, "*")
	if idx := strings.LastIndex(typeName, "."); idx != -1 {
		typeName = typeName[idx+1:]
	}
	if stripped := strings.TrimPrefix(typeName, "Step"); stripped != "" {
		typeName = stripped
	}
	return strings.ToLower(typeName)
}
