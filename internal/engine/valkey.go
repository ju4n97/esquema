package engine

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/valkey-io/valkey-go"

	"github.com/ju4n97/hclapi/internal/config"
	"github.com/ju4n97/hclapi/internal/problem"
)

// executeValkey executes caching operations against an active Valkey connection.
func (e *Engine) executeValkey(ctx *Context, w http.ResponseWriter, name string, step *config.ValkeyStep) error {
	client, ok := e.valkeys[step.Connection]
	if !ok {
		p := problem.New(http.StatusInternalServerError, fmt.Sprintf("valkey connection %q not found", step.Connection))
		problem.Write(w, p)
		return p
	}

	keyRaw, err := ctx.EvalAny(step.KeyExpr)
	if err != nil || keyRaw == nil {
		p := problem.New(http.StatusInternalServerError, fmt.Sprintf("valkey step %q: key expression is missing or invalid", name))
		problem.Write(w, p)
		return p
	}
	key := fmt.Sprintf("%v", keyRaw)

	switch step.Op {
	case config.ValkeyOpGet:
		cmd := client.B().Get().Key(key).Build()
		res, err := client.Do(ctx.RequestContext(), cmd).ToString()
		if err != nil {
			if valkey.IsValkeyNil(err) {
				ctx.SetStepResult(name, map[string]any{"found": false, "value": nil})
				return nil
			}
			p := problem.New(http.StatusBadGateway, fmt.Sprintf("valkey step %q read failed: %v", name, err))
			problem.Write(w, p)
			return p
		}

		var val any
		if err := json.Unmarshal([]byte(res), &val); err != nil {
			val = res
		}
		ctx.SetStepResult(name, map[string]any{"found": true, "value": val})

	case config.ValkeyOpSet:
		valRaw, err := ctx.EvalAny(step.ValExpr)
		if err != nil {
			p := problem.New(http.StatusInternalServerError, fmt.Sprintf("valkey step %q: evaluate value: %v", name, err))
			problem.Write(w, p)
			return p
		}

		valBytes, err := json.Marshal(valRaw)
		if err != nil {
			p := problem.New(http.StatusInternalServerError, fmt.Sprintf("valkey step %q: marshal error: %v", name, err))
			problem.Write(w, p)
			return p
		}

		var cmd valkey.Completed
		if step.TTL > 0 {
			cmd = client.B().Set().Key(key).Value(string(valBytes)).Ex(step.TTL).Build()
		} else {
			cmd = client.B().Set().Key(key).Value(string(valBytes)).Build()
		}

		if err := client.Do(ctx.RequestContext(), cmd).Error(); err != nil {
			p := problem.New(http.StatusBadGateway, fmt.Sprintf("valkey step %q write failed: %v", name, err))
			problem.Write(w, p)
			return p
		}
		ctx.SetStepResult(name, map[string]any{"success": true})

	case config.ValkeyOpDel:
		cmd := client.B().Del().Key(key).Build()
		if err := client.Do(ctx.RequestContext(), cmd).Error(); err != nil {
			p := problem.New(http.StatusBadGateway, fmt.Sprintf("valkey step %q delete failed: %v", name, err))
			problem.Write(w, p)
			return p
		}
		ctx.SetStepResult(name, map[string]any{"success": true})
	}

	return nil
}
