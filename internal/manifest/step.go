package manifest

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"iter"
	"net/http"
	"sync"

	"github.com/hashicorp/hcl/v2"
	"github.com/valkey-io/valkey-go"
	"github.com/zclconf/go-cty/cty/function"
)

// ErrPipelineHalted signals that a terminal step or error catch completed the response.
var ErrPipelineHalted = errors.New("pipeline execution halted")

// RecordSeq defines the standard Go iterator yielding record items and potential errors.
type RecordSeq iter.Seq2[any, error]

// Step defines the minimal AST node contract for a route task.
type Step interface {
	StepName() string
	StepWhen() Expr
	IsTerminal() bool
}

// StepValidator is an optional interface implemented by AST step definitions
// that require semantic verification against the compiled manifest topology.
type StepValidator interface {
	ValidateStep(m *Manifest) error
}

// StepExecutor is implemented by steps that execute runtime request logic.
type StepExecutor interface {
	ExecuteStep(ctx context.Context, ec StepExecutionContext) (any, error)
}

// StepExecutionContext supplies runtime dependencies and request state to steps during execution.
type StepExecutionContext interface {
	SQL(name string) (*sql.DB, error)
	Valkey(name string) (valkey.Client, error)
	GoHandler(name string) (StepHandler, error)
	HTTPClient() *http.Client
	ResponseWriter() http.ResponseWriter
	ResponseController() *http.ResponseController
	OpenAPISpec(format string) ([]byte, string, error)
	Scope() Scope
	Schemas() map[string]Schema
}

// StepDecoder decodes an HCL block into a concrete [Step] implementation.
type StepDecoder func(name string, body hcl.Body, evalCtx *hcl.EvalContext, funcs map[string]function.Function) (Step, error)

type stepRegistration struct {
	kind       string
	labelNames []string
	decoder    StepDecoder
}

// StepRegistry manages step decoders and generates dynamic HCL block schemas without global state.
type StepRegistry struct {
	mu    sync.RWMutex
	steps map[string]stepRegistration
}

// NewStepRegistry returns an empty, isolated step registry.
func NewStepRegistry() *StepRegistry {
	return &StepRegistry{
		steps: make(map[string]stepRegistration),
	}
}

// Register adds a step decoder for a specific HCL block keyword and its expected labels.
func (r *StepRegistry) Register(keyword string, labelNames []string, decoder StepDecoder) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.steps[keyword] = stepRegistration{
		kind:       keyword,
		labelNames: labelNames,
		decoder:    decoder,
	}
}

// BlockHeaderSchemas exports the block schemas for all registered step keywords,
// allowing the route parser to dynamically accept direct keywords.
func (r *StepRegistry) BlockHeaderSchemas() []hcl.BlockHeaderSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()

	schemas := make([]hcl.BlockHeaderSchema, 0, len(r.steps))
	for _, reg := range r.steps {
		schemas = append(schemas, hcl.BlockHeaderSchema{
			Type:       reg.kind,
			LabelNames: reg.labelNames,
		})
	}
	return schemas
}

// Decode resolves the matching decoder for block.Type and compiles the block into a [Step].
func (r *StepRegistry) Decode(block *hcl.Block, evalCtx *hcl.EvalContext, funcs map[string]function.Function) (Step, error) {
	r.mu.RLock()
	reg, exists := r.steps[block.Type]
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("unrecognized step keyword %q", block.Type)
	}

	label := ""
	if len(block.Labels) > 0 {
		label = block.Labels[0]
	}

	return reg.decoder(label, block.Body, evalCtx, funcs)
}

// DefaultStepRegistry returns a pre-configured registry containing all standard built-in steps.
func DefaultStepRegistry() *StepRegistry {
	r := NewStepRegistry()
	r.Register("sql", []string{"name"}, decodeStepSQL)
	r.Register("http", []string{"name"}, decodeStepHTTP)
	r.Register("valkey", []string{"name"}, decodeStepValkey)
	r.Register("starlark", []string{"name"}, decodeStepStarlark)
	r.Register("go", []string{"name"}, decodeStepGo)
	r.Register("stream", []string{"format"}, decodeStepStream)
	r.Register("respond", nil, decodeStepRespond)
	r.Register("docs", nil, decodeStepDocs)
	r.Register("spec", nil, decodeStepSpec)
	return r
}
