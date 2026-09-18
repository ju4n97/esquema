//go:build integration

package manifest

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	valkeymodule "github.com/testcontainers/testcontainers-go/modules/valkey"
	"github.com/valkey-io/valkey-go"
)

type testValkeyContext struct {
	valkeyClient valkey.Client
	scope        Scope
	recorder     *httptest.ResponseRecorder
}

func (c *testValkeyContext) SQL(name string) (*sql.DB, error) {
	return nil, nil
}

func (c *testValkeyContext) Valkey(name string) (valkey.Client, error) {
	if c.valkeyClient != nil {
		return c.valkeyClient, nil
	}
	return nil, errors.New("unconfigured valkey connection")
}

func (c *testValkeyContext) GoHandler(name string) (GoHandler, error) {
	return nil, errors.New("go unconfigured")
}

func (c *testValkeyContext) HTTPClient() *http.Client {
	return http.DefaultClient
}

func (c *testValkeyContext) ResponseWriter() http.ResponseWriter {
	return c.recorder
}

func (c *testValkeyContext) ResponseController() *http.ResponseController {
	return http.NewResponseController(c.recorder)
}

func (c *testValkeyContext) OpenAPISpec(format string) ([]byte, string, error) {
	return nil, "", nil
}

func (c *testValkeyContext) Scope() Scope {
	return c.scope
}

func (c *testValkeyContext) Schemas() map[string]Schema {
	return nil
}

// setupValkeyContainer initializes a disposable Valkey instance via Testcontainers for integration tests.
func setupValkeyContainer(t *testing.T) (valkey.Client, context.Context) {
	t.Helper()

	ctx := context.Background()
	container, err := valkeymodule.Run(ctx, "valkey/valkey:8.0-alpine")
	if err != nil {
		t.Fatalf("failed to start valkey container: %v", err)
	}

	t.Cleanup(func() {
		_ = container.Terminate(context.Background())
	})

	uri, err := container.ConnectionString(ctx)
	if err != nil {
		t.Fatalf("failed to get valkey container connection string: %v", err)
	}

	opt, err := valkey.ParseURL(uri)
	if err != nil {
		t.Fatalf("failed to parse valkey connection url %q: %v", uri, err)
	}

	client, err := valkey.NewClient(opt)
	if err != nil {
		t.Fatalf("failed to create valkey client: %v", err)
	}

	t.Cleanup(func() {
		client.Close()
	})

	return client, ctx
}

// TestStepValkey_ValidateStep verifies compile-time checks on connections, operations, and required attributes.
func TestStepValkey_ValidateStep(t *testing.T) {
	t.Parallel()

	manifest := &Manifest{
		Connections: map[string]Connection{
			"redis_cache": {
				Name: "redis_cache",
				Type: "valkey",
			},
			"sql_db": {
				Name: "sql_db",
				Type: "sql",
			},
		},
	}

	tests := []struct {
		name      string
		step      *StepValkey
		wantError bool
		errSubstr string
	}{
		{
			name: "missing connection identifier",
			step: &StepValkey{
				Name: "cache_op",
				Op:   "get",
				Key:  NewExpr(parseExpr(t, `"user:1"`), nil),
			},
			wantError: true,
			errSubstr: "missing connection identifier",
		},
		{
			name: "references unknown connection",
			step: &StepValkey{
				Name:       "cache_op",
				Connection: "missing_pool",
				Op:         "get",
				Key:        NewExpr(parseExpr(t, `"user:1"`), nil),
			},
			wantError: true,
			errSubstr: "references unknown connection \"missing_pool\"",
		},
		{
			name: "connection is sql type instead of valkey",
			step: &StepValkey{
				Name:       "cache_op",
				Connection: "sql_db",
				Op:         "get",
				Key:        NewExpr(parseExpr(t, `"user:1"`), nil),
			},
			wantError: true,
			errSubstr: "cannot use non-valkey connection",
		},
		{
			name: "invalid operation verb",
			step: &StepValkey{
				Name:       "cache_op",
				Connection: "redis_cache",
				Op:         "flushall",
			},
			wantError: true,
			errSubstr: "invalid op \"flushall\"",
		},
		{
			name: "get operation missing key",
			step: &StepValkey{
				Name:       "cache_get",
				Connection: "redis_cache",
				Op:         "get",
			},
			wantError: true,
			errSubstr: "requires 'key' expression",
		},
		{
			name: "set operation missing value",
			step: &StepValkey{
				Name:       "cache_set",
				Connection: "redis_cache",
				Op:         "set",
				Key:        NewExpr(parseExpr(t, `"user:1"`), nil),
			},
			wantError: true,
			errSubstr: "requires 'value' expression",
		},
		{
			name: "subscribe operation missing channel",
			step: &StepValkey{
				Name:       "cache_sub",
				Connection: "redis_cache",
				Op:         "subscribe",
			},
			wantError: true,
			errSubstr: "requires 'channel' expression",
		},
		{
			name: "valid get operation",
			step: &StepValkey{
				Name:       "cache_get",
				Connection: "redis_cache",
				Op:         "get",
				Key:        NewExpr(parseExpr(t, `"user:1"`), nil),
			},
			wantError: false,
		},
		{
			name: "valid set operation with ttl",
			step: &StepValkey{
				Name:       "cache_set",
				Connection: "redis_cache",
				Op:         "set",
				Key:        NewExpr(parseExpr(t, `"user:1"`), nil),
				Value:      NewExpr(parseExpr(t, `"{}"`), nil),
				TTL:        Duration(10 * time.Minute),
			},
			wantError: false,
		},
		{
			name: "valid subscribe operation",
			step: &StepValkey{
				Name:       "cache_sub",
				Connection: "redis_cache",
				Op:         "subscribe",
				Channel:    NewExpr(parseExpr(t, `"alerts"`), nil),
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.step.ValidateStep(manifest)
			if (err != nil) != tt.wantError {
				t.Fatalf("ValidateStep() error = %v, wantError = %v", err, tt.wantError)
			}

			if tt.wantError && tt.errSubstr != "" {
				if !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("error %q does not contain expected substring %q", err.Error(), tt.errSubstr)
				}
			}
		})
	}
}

// TestStepValkey_ExecuteStep_Live verifies get, set, del, and pub/sub against a real containerized Valkey instance.
func TestStepValkey_ExecuteStep_Live(t *testing.T) {
	client, ctx := setupValkeyContainer(t)

	t.Run("get operation cache miss returns found false", func(t *testing.T) {
		step := &StepValkey{
			Name:       "check_cache",
			Connection: "cache",
			Op:         "get",
			Key:        NewExpr(parseExpr(t, `"test:user:404"`), nil),
		}

		exec := &testValkeyContext{valkeyClient: client}

		res, err := step.ExecuteStep(context.Background(), exec)
		if err != nil {
			t.Fatalf("ExecuteStep() error: %v", err)
		}

		resMap, ok := res.(map[string]any)
		if !ok {
			t.Fatalf("expected map[string]any output, got %T", res)
		}

		if resMap["found"] != false || resMap["value"] != nil {
			t.Fatalf("unexpected cache miss result: %+v", resMap)
		}
	})

	t.Run("get operation cache hit unmarshals JSON payload", func(t *testing.T) {
		key := "test:user:json"
		setCmd := client.B().Set().Key(key).Value(`{"id":42,"role":"admin"}`).Build()
		if err := client.Do(ctx, setCmd).Error(); err != nil {
			t.Fatalf("failed to seed valkey key: %v", err)
		}

		step := &StepValkey{
			Name:       "get_user",
			Connection: "cache",
			Op:         "get",
			Key:        NewExpr(parseExpr(t, `"`+key+`"`), nil),
		}

		exec := &testValkeyContext{valkeyClient: client}

		res, err := step.ExecuteStep(context.Background(), exec)
		if err != nil {
			t.Fatalf("ExecuteStep() error: %v", err)
		}

		resMap := res.(map[string]any)
		if resMap["found"] != true {
			t.Errorf("found = %v, want true", resMap["found"])
		}

		valMap, ok := resMap["value"].(map[string]any)
		if !ok {
			t.Fatalf("expected map[string]any, got %T (%v)", resMap["value"], resMap["value"])
		}
		if valMap["id"] != float64(42) || valMap["role"] != "admin" {
			t.Errorf("unexpected value: %+v", valMap)
		}
	})

	t.Run("set operation writes payload and expiration TTL", func(t *testing.T) {
		key := "test:session:ttl"
		step := &StepValkey{
			Name:       "save_session",
			Connection: "cache",
			Op:         "set",
			Key:        NewExpr(parseExpr(t, `"`+key+`"`), nil),
			Value:      NewExpr(parseExpr(t, `{ token = "session_token_xyz" }`), nil),
			TTL:        Duration(10 * time.Minute),
		}

		exec := &testValkeyContext{valkeyClient: client}

		res, err := step.ExecuteStep(context.Background(), exec)
		if err != nil {
			t.Fatalf("ExecuteStep() error: %v", err)
		}

		resMap := res.(map[string]any)
		if resMap["success"] != true {
			t.Errorf("success = %v, want true", resMap["success"])
		}

		ttlCmd := client.B().Ttl().Key(key).Build()
		ttlSeconds, ttlErr := client.Do(ctx, ttlCmd).AsInt64()
		if ttlErr != nil {
			t.Fatalf("failed to check TTL: %v", ttlErr)
		}
		if ttlSeconds <= 0 || ttlSeconds > 600 {
			t.Errorf("expected TTL in range (0, 600], got %d", ttlSeconds)
		}
	})

	t.Run("del operation removes existing key", func(t *testing.T) {
		key := "test:temp:delete"
		setCmd := client.B().Set().Key(key).Value("ephemeral_value").Build()
		if err := client.Do(ctx, setCmd).Error(); err != nil {
			t.Fatalf("failed to seed key: %v", err)
		}

		step := &StepValkey{
			Name:       "delete_cache",
			Connection: "cache",
			Op:         "del",
			Key:        NewExpr(parseExpr(t, `"`+key+`"`), nil),
		}

		exec := &testValkeyContext{valkeyClient: client}

		res, err := step.ExecuteStep(context.Background(), exec)
		if err != nil {
			t.Fatalf("ExecuteStep() error: %v", err)
		}

		resMap := res.(map[string]any)
		if resMap["success"] != true {
			t.Errorf("success = %v, want true", resMap["success"])
		}

		getCmd := client.B().Get().Key(key).Build()
		_, getErr := client.Do(ctx, getCmd).ToString()
		if !valkey.IsValkeyNil(getErr) {
			t.Errorf("expected valkey nil error after deletion, got %v", getErr)
		}
	})

	t.Run("subscribe operation yields live PubSub messages as RecordSeq", func(t *testing.T) {
		channel := "test:live:alerts"
		step := &StepValkey{
			Name:       "pubsub_step",
			Connection: "cache",
			Op:         "subscribe",
			Channel:    NewExpr(parseExpr(t, `"`+channel+`"`), nil),
		}

		exec := &testValkeyContext{valkeyClient: client}

		res, err := step.ExecuteStep(context.Background(), exec)
		if err != nil {
			t.Fatalf("ExecuteStep() error: %v", err)
		}

		resMap, ok := res.(map[string]any)
		if !ok {
			t.Fatalf("expected map[string]any output, got %T", res)
		}

		seq, ok := resMap["stream"].(RecordSeq)
		if !ok {
			t.Fatalf("expected stream output to be RecordSeq, got %T", resMap["stream"])
		}

		time.Sleep(150 * time.Millisecond)

		pub1 := client.B().Publish().Channel(channel).Message("alert-first").Build()
		if pErr := client.Do(ctx, pub1).Error(); pErr != nil {
			t.Fatalf("failed to publish message 1: %v", pErr)
		}

		pub2 := client.B().Publish().Channel(channel).Message("alert-second").Build()
		if pErr := client.Do(ctx, pub2).Error(); pErr != nil {
			t.Fatalf("failed to publish message 2: %v", pErr)
		}

		var collected []string
		for item, iErr := range seq {
			if iErr != nil {
				t.Fatalf("stream iteration error: %v", iErr)
			}
			collected = append(collected, item.(string))
			if len(collected) == 2 {
				break
			}
		}

		if len(collected) != 2 || collected[0] != "alert-first" || collected[1] != "alert-second" {
			t.Errorf("unexpected collected stream messages: %v", collected)
		}
	})
}

// TestDecodeStepValkey verifies direct HCL keyword parsing into a [*StepValkey] definition.
func TestDecodeStepValkey(t *testing.T) {
	t.Parallel()

	hclBlock := `
		connection = "cache_pool"
		op         = "set"
		key        = "session:${ctx.request.path.id}"
		value      = { user = "jane" }
		ttl        = "30m"
	`

	expr, diags := hclsyntax.ParseConfig([]byte(hclBlock), "test.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("hcl parse error: %s", diags.Error())
	}

	step, err := decodeStepValkey("cache_user", expr.Body, nil, runtimeExprFunctions())
	if err != nil {
		t.Fatalf("decodeStepValkey() error: %v", err)
	}

	valkeyStep, ok := step.(*StepValkey)
	if !ok {
		t.Fatalf("expected *StepValkey, got %T", step)
	}

	if valkeyStep.StepName() != "cache_user" {
		t.Errorf("StepName() = %q, want 'cache_user'", valkeyStep.StepName())
	}
	if valkeyStep.Connection != "cache_pool" {
		t.Errorf("Connection = %q, want 'cache_pool'", valkeyStep.Connection)
	}
	if valkeyStep.Op != "set" {
		t.Errorf("Op = %q, want 'set'", valkeyStep.Op)
	}
	if valkeyStep.TTL.Duration() != 30*time.Minute {
		t.Errorf("TTL = %v, want 30m", valkeyStep.TTL.Duration())
	}
	if valkeyStep.IsTerminal() {
		t.Error("expected IsTerminal() to be false")
	}
}
