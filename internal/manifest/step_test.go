package manifest

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty/function"
)

type mockStep struct {
	name       string
	when       Expr
	terminal   bool
	validateFn func(m *Manifest) error
}

func (m *mockStep) StepName() string { return m.name }

func (m *mockStep) StepWhen() Expr { return m.when }

func (m *mockStep) IsTerminal() bool { return m.terminal }

func (m *mockStep) ValidateStep(manifest *Manifest) error {
	if m.validateFn != nil {
		return m.validateFn(manifest)
	}
	return nil
}

func (m *mockStep) ExecuteStep(ctx context.Context, ec StepExecutionContext) (any, error) {
	return "mock_output", nil
}

// TestNewStepRegistry verifies isolated step registry construction and lookups.
func TestNewStepRegistry(t *testing.T) {
	t.Parallel()

	reg := NewStepRegistry()
	if reg == nil {
		t.Fatal("expected non-nil StepRegistry")
	}

	block := &hcl.Block{Type: "unregistered"}
	_, err := reg.Decode(block, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "unrecognized step keyword") {
		t.Fatalf("expected unrecognized step keyword error, got: %v", err)
	}
}

// TestStepRegistry_RegisterAndDecode verifies custom keyword registration, schema generation, and decoding.
func TestStepRegistry_RegisterAndDecode(t *testing.T) {
	t.Parallel()

	reg := NewStepRegistry()

	mockDecoder := func(label string, body hcl.Body, evalCtx *hcl.EvalContext, funcs map[string]function.Function) (Step, error) {
		return &mockStep{
			name:     label,
			terminal: label == "" || label == "terminal_sink",
		}, nil
	}

	reg.Register("kafka", []string{"topic"}, mockDecoder)
	reg.Register("terminal_sink", nil, mockDecoder)

	t.Run("exports accurate block header schemas", func(t *testing.T) {
		t.Parallel()

		schemas := reg.BlockHeaderSchemas()
		if len(schemas) != 2 {
			t.Fatalf("expected 2 block header schemas, got %d", len(schemas))
		}

		foundKafka := false
		for _, s := range schemas {
			if s.Type == "kafka" {
				foundKafka = true
				if len(s.LabelNames) != 1 || s.LabelNames[0] != "topic" {
					t.Fatalf("unexpected label names for kafka: %v", s.LabelNames)
				}
			}
		}

		if !foundKafka {
			t.Fatal("expected 'kafka' block schema to be exported")
		}
	})

	t.Run("decodes registered keyword with label", func(t *testing.T) {
		t.Parallel()

		block := &hcl.Block{
			Type:   "kafka",
			Labels: []string{"orders.created"},
		}

		step, err := reg.Decode(block, nil, nil)
		if err != nil {
			t.Fatalf("unexpected decode error: %v", err)
		}

		if step.StepName() != "orders.created" {
			t.Fatalf("StepName() = %q, want 'orders.created'", step.StepName())
		}
		if step.IsTerminal() {
			t.Fatal("expected non-terminal step")
		}
	})

	t.Run("decodes registered keyword without labels", func(t *testing.T) {
		t.Parallel()

		block := &hcl.Block{
			Type: "terminal_sink",
		}

		step, err := reg.Decode(block, nil, nil)
		if err != nil {
			t.Fatalf("unexpected decode error: %v", err)
		}

		if !step.IsTerminal() {
			t.Fatal("expected terminal step")
		}
	})
}

// TestDefaultStepRegistry verifies built-in keywords are pre-registered with correct label specs.
func TestDefaultStepRegistry(t *testing.T) {
	t.Parallel()

	reg := DefaultStepRegistry()
	if reg == nil {
		t.Fatal("expected non-nil DefaultStepRegistry")
	}

	schemas := reg.BlockHeaderSchemas()
	expectedKeywords := map[string][]string{
		"sql":     {"name"},
		"respond": nil,
		"stream":  {"format"},
	}

	for keyword, expectedLabels := range expectedKeywords {
		found := false
		for _, s := range schemas {
			if s.Type == keyword {
				found = true
				if len(s.LabelNames) != len(expectedLabels) {
					t.Fatalf("keyword %q label count = %d, want %d", keyword, len(s.LabelNames), len(expectedLabels))
				}
				for i := range expectedLabels {
					if s.LabelNames[i] != expectedLabels[i] {
						t.Errorf("keyword %q label[%d] = %q, want %q", keyword, i, s.LabelNames[i], expectedLabels[i])
					}
				}
				break
			}
		}
		if !found {
			t.Fatalf("expected keyword %q to be registered in DefaultStepRegistry", keyword)
		}
	}
}

// TestStepRegistry_Concurrency verifies thread-safe registration and resolution under parallel load.
func TestStepRegistry_Concurrency(t *testing.T) {
	t.Parallel()

	reg := NewStepRegistry()
	var wg sync.WaitGroup

	for i := 0; i < 30; i++ {
		wg.Add(3)

		go func() {
			defer wg.Done()
			reg.Register(
				"concurrent",
				[]string{"name"},
				func(label string, body hcl.Body, evalCtx *hcl.EvalContext, funcs map[string]function.Function) (Step, error) {
					return &mockStep{name: label}, nil
				},
			)
		}()

		go func() {
			defer wg.Done()
			_ = reg.BlockHeaderSchemas()
		}()

		go func() {
			defer wg.Done()
			block := &hcl.Block{Type: "concurrent", Labels: []string{"test"}}
			_, _ = reg.Decode(block, nil, nil)
		}()
	}

	wg.Wait()
}
