package manifest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/valkey-io/valkey-go"
	"github.com/zclconf/go-cty/cty/function"
)

// StepValkey executes caching commands or Pub/Sub subscriptions against Valkey or Redis.
//
// Key-value commands ("get", "set", "del") publish execution metadata to context:
//   - "get": sets steps.<name>.found (bool) and steps.<name>.value (unmarshaled JSON or string).
//     A cache miss doesn't return an error; it sets found = false and value = nil.
//   - "set": serializes the payload, sets expiration if configured, and sets steps.<name>.success = true.
//   - "del": removes the key and sets steps.<name>.success = true.
//
// The "subscribe" operation produces a continuous [RecordSeq] iterator under steps.<name>.stream,
// allowing live messages to be piped directly into a terminal [StepStream] (e.g. SSE).
type StepValkey struct {
	Name       string
	Connection string
	Op         string
	Key        Expr
	Value      Expr
	Channel    Expr
	TTL        Duration
	When       Expr
}

// StepName implements [Step].
func (v *StepValkey) StepName() string {
	return v.Name
}

// StepWhen implements [Step].
func (v *StepValkey) StepWhen() Expr {
	return v.When
}

// IsTerminal implements [Step] and returns false.
func (v *StepValkey) IsTerminal() bool {
	return false
}

// ValidateStep implements [StepValidator]. It verifies connection presence and type,
// operation keywords, and required parameter expressions for each operation mode.
func (v *StepValkey) ValidateStep(m *Manifest) error {
	if v.Connection == "" {
		return fmt.Errorf("valkey step %q missing connection identifier", v.Name)
	}

	conn, exists := m.Connections[v.Connection]
	if !exists {
		return fmt.Errorf("valkey step %q references unknown connection %q", v.Name, v.Connection)
	}

	if conn.Type != "valkey" {
		return fmt.Errorf("valkey step %q cannot use non-valkey connection %q (type: %s)", v.Name, v.Connection, conn.Type)
	}

	op := strings.ToLower(v.Op)
	switch op {
	case "get", "del":
		if v.Key == nil {
			return fmt.Errorf("valkey step %q op %q requires 'key' expression", v.Name, op)
		}

	case "set":
		if v.Key == nil {
			return fmt.Errorf("valkey step %q op 'set' requires 'key' expression", v.Name)
		}
		if v.Value == nil {
			return fmt.Errorf("valkey step %q op 'set' requires 'value' expression", v.Name)
		}

	case "subscribe":
		if v.Channel == nil {
			return fmt.Errorf("valkey step %q op 'subscribe' requires 'channel' expression", v.Name)
		}

	default:
		return fmt.Errorf("valkey step %q invalid op %q (allowed: get, set, del, subscribe)", v.Name, v.Op)
	}

	return nil
}

// ExecuteStep implements [StepExecutor]. It performs caching operations or builds
// a live subscription iterator based on the declared operation.
func (v *StepValkey) ExecuteStep(ctx context.Context, ec StepExecutionContext) (any, error) {
	client, err := ec.Valkey(v.Connection)
	if err != nil {
		return nil, fmt.Errorf("step %q: %w", v.Name, err)
	}

	op := strings.ToLower(v.Op)
	switch op {
	case "get":
		return v.executeGet(ctx, ec, client)
	case "set":
		return v.executeSet(ctx, ec, client)
	case "del":
		return v.executeDel(ctx, ec, client)
	case "subscribe":
		return v.executeSubscribe(ctx, ec, client)
	default:
		return nil, fmt.Errorf("step %q invalid op %q", v.Name, v.Op)
	}
}

// executeGet reads a key and attempts to decode it from JSON into structured data.
func (v *StepValkey) executeGet(ctx context.Context, ec StepExecutionContext, client valkey.Client) (any, error) {
	keyVal, err := v.Key.Eval(ec.Scope())
	if err != nil {
		return nil, fmt.Errorf("step %q evaluate key: %w", v.Name, err)
	}
	key := fmt.Sprintf("%v", keyVal)

	cmd := client.B().Get().Key(key).Build()
	res, err := client.Do(ctx, cmd).ToString()
	if err != nil {
		if valkey.IsValkeyNil(err) {
			return map[string]any{
				"found": false,
				"value": nil,
			}, nil
		}
		return nil, fmt.Errorf("step %q valkey get: %w", v.Name, err)
	}

	var parsed any
	if jErr := json.Unmarshal([]byte(res), &parsed); jErr == nil {
		return map[string]any{
			"found": true,
			"value": parsed,
		}, nil
	}

	return map[string]any{
		"found": true,
		"value": res,
	}, nil
}

// executeSet serializes the payload, sets expiration if configured, and writes the key.
func (v *StepValkey) executeSet(ctx context.Context, ec StepExecutionContext, client valkey.Client) (any, error) {
	keyVal, err := v.Key.Eval(ec.Scope())
	if err != nil {
		return nil, fmt.Errorf("step %q evaluate key: %w", v.Name, err)
	}
	key := fmt.Sprintf("%v", keyVal)

	valData, err := v.Value.Eval(ec.Scope())
	if err != nil {
		return nil, fmt.Errorf("step %q evaluate value: %w", v.Name, err)
	}

	var payload string
	switch raw := valData.(type) {
	case string:
		payload = raw
	case []byte:
		payload = string(raw)
	default:
		b, mErr := json.Marshal(valData)
		if mErr != nil {
			return nil, fmt.Errorf("step %q marshal json value: %w", v.Name, mErr)
		}
		payload = string(b)
	}

	var cmd valkey.Completed
	ttl := v.TTL.Duration()
	if ttl > 0 {
		cmd = client.B().Set().Key(key).Value(payload).Ex(ttl).Build()
	} else {
		cmd = client.B().Set().Key(key).Value(payload).Build()
	}

	if err := client.Do(ctx, cmd).Error(); err != nil {
		return nil, fmt.Errorf("step %q valkey set: %w", v.Name, err)
	}

	return map[string]any{"success": true}, nil
}

// executeDel removes the specified key from the store.
func (v *StepValkey) executeDel(ctx context.Context, ec StepExecutionContext, client valkey.Client) (any, error) {
	keyVal, err := v.Key.Eval(ec.Scope())
	if err != nil {
		return nil, fmt.Errorf("step %q evaluate key: %w", v.Name, err)
	}
	key := fmt.Sprintf("%v", keyVal)

	cmd := client.B().Del().Key(key).Build()
	if err := client.Do(ctx, cmd).Error(); err != nil {
		return nil, fmt.Errorf("step %q valkey del: %w", v.Name, err)
	}

	return map[string]any{"success": true}, nil
}

// executeSubscribe establishes a Pub/Sub subscription and yields messages as a [RecordSeq].
func (v *StepValkey) executeSubscribe(ctx context.Context, ec StepExecutionContext, client valkey.Client) (any, error) {
	channelVal, err := v.Channel.Eval(ec.Scope())
	if err != nil {
		return nil, fmt.Errorf("step %q evaluate channel: %w", v.Name, err)
	}
	channelName := fmt.Sprintf("%v", channelVal)

	seq := func(yield func(any, error) bool) {
		subCtx, cancel := context.WithCancel(ctx)
		defer cancel()

		msgChan := make(chan string, 64)
		errChan := make(chan error, 1)

		go func() {
			defer close(msgChan)
			cmd := client.B().Subscribe().Channel(channelName).Build()
			subErr := client.Receive(subCtx, cmd, func(msg valkey.PubSubMessage) {
				select {
				case msgChan <- msg.Message:
				case <-subCtx.Done():
				}
			})
			if subErr != nil && !errors.Is(subErr, context.Canceled) {
				select {
				case errChan <- subErr:
				default:
				}
			}
		}()

		for {
			select {
			case <-ctx.Done():
				return
			case subErr := <-errChan:
				yield(nil, subErr)
				return
			case msg, ok := <-msgChan:
				if !ok {
					return
				}
				if !yield(msg, nil) {
					return
				}
			}
		}
	}

	return map[string]any{
		"stream": RecordSeq(seq),
	}, nil
}

// decodeStepValkey decodes an HCL block into a [*StepValkey].
func decodeStepValkey(name string, body hcl.Body, evalCtx *hcl.EvalContext, funcs map[string]function.Function) (Step, error) {
	type valkeyDecode struct {
		Connection  string         `hcl:"connection"`
		Op          string         `hcl:"op"`
		KeyExpr     hcl.Expression `hcl:"key,optional"`
		ValueExpr   hcl.Expression `hcl:"value,optional"`
		ChannelExpr hcl.Expression `hcl:"channel,optional"`
		TTLRaw      string         `hcl:"ttl,optional"`
		WhenExpr    hcl.Expression `hcl:"when,optional"`
	}

	var raw valkeyDecode
	if diags := gohcl.DecodeBody(body, evalCtx, &raw); diags.HasErrors() {
		return nil, diags
	}

	step := &StepValkey{
		Name:       name,
		Connection: raw.Connection,
		Op:         strings.ToLower(strings.TrimSpace(raw.Op)),
		Key:        NewExpr(raw.KeyExpr, funcs),
		Value:      NewExpr(raw.ValueExpr, funcs),
		Channel:    NewExpr(raw.ChannelExpr, funcs),
		When:       NewExpr(raw.WhenExpr, funcs),
	}

	if raw.TTLRaw != "" {
		d, err := ParseDuration(raw.TTLRaw)
		if err != nil {
			return nil, fmt.Errorf("valkey step %q ttl: %w", name, err)
		}
		step.TTL = d
	}

	return step, nil
}
