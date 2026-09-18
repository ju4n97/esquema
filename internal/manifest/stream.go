package manifest

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// StepStream consumes a data stream and flushes items according to the declared protocol.
type StepStream struct {
	Format      string
	Source      Expr
	Event       string
	Heartbeat   Duration
	Retry       Duration
	FlushEvery  int
	Headers     map[string]string
	ContentType string
	Schema      *TypeSpec
	When        Expr
}

// StepName implements [Step].
func (s *StepStream) StepName() string {
	return "stream"
}

// StepWhen implements [Step].
func (s *StepStream) StepWhen() Expr {
	return s.When
}

// IsTerminal implements [Step].
func (s *StepStream) IsTerminal() bool {
	return true
}

// ValidateStep implements [StepValidator].
func (s *StepStream) ValidateStep(m *Manifest) error {
	format := strings.ToLower(s.Format)
	switch format {
	case "sse", "ndjson", "csv", "raw":
	default:
		return fmt.Errorf("unsupported stream format %q (allowed: sse, ndjson, csv, raw)", s.Format)
	}

	if s.Source == nil {
		return fmt.Errorf("stream %q requires a 'source' expression", s.Format)
	}

	if s.Schema != nil {
		ref := s.Schema.ElementSchemaRef()
		if ref != "" {
			if _, exists := m.Schemas[ref]; !exists {
				return fmt.Errorf("stream schema references unknown schema %q", ref)
			}
		}
	}

	return nil
}

// ExecuteStep implements [StepExecutor].
func (s *StepStream) ExecuteStep(ctx context.Context, ec StepExecutionContext) (any, error) {
	rc := ec.ResponseController()
	if rc == nil {
		return nil, errors.New("streaming unsupported by transport")
	}

	if err := rc.SetWriteDeadline(time.Time{}); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return nil, fmt.Errorf("clear write deadline: %w", err)
	}

	rawSource, err := s.Source.Eval(ec.Scope())
	if err != nil {
		return nil, fmt.Errorf("stream evaluate source: %w", err)
	}

	w := ec.ResponseWriter()
	for k, v := range s.Headers {
		w.Header().Set(k, v)
	}

	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("X-Accel-Buffering", "no")

	format := strings.ToLower(s.Format)
	switch format {
	case "sse":
		return nil, s.streamSSE(ctx, ec, rc, rawSource)
	case "ndjson":
		return nil, s.streamNDJSON(ctx, ec, rc, rawSource)
	case "csv":
		return nil, s.streamCSV(ctx, ec, rc, rawSource)
	case "raw":
		return nil, s.streamRaw(ctx, ec, rc, rawSource)
	default:
		return nil, fmt.Errorf("unsupported stream format %q", s.Format)
	}
}

// streamSSE streams SSE events from a RecordSeq.
func (s *StepStream) streamSSE(ctx context.Context, ec StepExecutionContext, rc *http.ResponseController, source any) error {
	w := ec.ResponseWriter()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		return err
	}

	heartbeatDur := s.Heartbeat.Duration()
	if heartbeatDur <= 0 {
		heartbeatDur = 15 * time.Second
	}

	retryDur := s.Retry.Duration()
	if retryDur > 0 {
		_, _ = fmt.Fprintf(w, "retry: %d\n\n", retryDur.Milliseconds())
		_ = rc.Flush()
	}

	seq, ok := source.(RecordSeq)
	if !ok {
		return fmt.Errorf("stream 'sse' source must yield RecordSeq, got %T", source)
	}

	ticker := time.NewTicker(heartbeatDur)
	defer ticker.Stop()

	for item, itemErr := range seq {
		select {
		case <-ctx.Done():
			return ErrPipelineHalted
		case <-ticker.C:
			if _, err := fmt.Fprintf(w, ": heartbeat\n\n"); err != nil {
				return err
			}
			if err := rc.Flush(); err != nil {
				return err
			}
		default:
		}

		if itemErr != nil {
			return itemErr
		}

		var payload string
		if s.Schema != nil {
			masked, mErr := maskPayload(item, *s.Schema, ec.Schemas())
			if mErr == nil {
				item = masked
			}
		}

		if b, err := json.Marshal(item); err == nil {
			payload = string(b)
		} else {
			payload = fmt.Sprintf("%v", item)
		}

		if s.Event != "" {
			if _, err := fmt.Fprintf(w, "event: %s\n", s.Event); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
			return err
		}
		if err := rc.Flush(); err != nil {
			return err
		}
	}

	return ErrPipelineHalted
}

// streamNDJSON streams NDJSON rows from a RecordSeq.
func (s *StepStream) streamNDJSON(ctx context.Context, ec StepExecutionContext, rc *http.ResponseController, source any) error {
	seq, ok := source.(RecordSeq)
	if !ok {
		return fmt.Errorf("stream 'ndjson' source must yield RecordSeq, got %T", source)
	}

	w := ec.ResponseWriter()
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		return err
	}

	enc := json.NewEncoder(w)
	flushInterval := s.FlushEvery
	if flushInterval <= 0 {
		flushInterval = 1
	}

	count := 0
	for item, itemErr := range seq {
		select {
		case <-ctx.Done():
			return ErrPipelineHalted
		default:
		}

		if itemErr != nil {
			return itemErr
		}

		if s.Schema != nil {
			masked, mErr := maskPayload(item, *s.Schema, ec.Schemas())
			if mErr == nil {
				item = masked
			}
		}

		if err := enc.Encode(item); err != nil {
			return err
		}

		count++
		if count%flushInterval == 0 {
			if err := rc.Flush(); err != nil {
				return err
			}
		}
	}

	_ = rc.Flush()
	return ErrPipelineHalted
}

// streamCSV streams CSV rows from a RecordSeq.
func (s *StepStream) streamCSV(ctx context.Context, ec StepExecutionContext, rc *http.ResponseController, source any) error {
	seq, ok := source.(RecordSeq)
	if !ok {
		return fmt.Errorf("stream 'csv' source must yield RecordSeq, got %T", source)
	}

	w := ec.ResponseWriter()
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	}
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		return err
	}

	csvWriter := csv.NewWriter(w)
	var headers []string

	for item, itemErr := range seq {
		select {
		case <-ctx.Done():
			return ErrPipelineHalted
		default:
		}

		if itemErr != nil {
			return itemErr
		}

		rowMap, isMap := item.(map[string]any)
		if !isMap {
			continue
		}

		if headers == nil {
			for k := range rowMap {
				headers = append(headers, k)
			}
			if err := csvWriter.Write(headers); err != nil {
				return err
			}
		}

		rowValues := make([]string, len(headers))
		for i, h := range headers {
			rowValues[i] = fmt.Sprintf("%v", rowMap[h])
		}

		if err := csvWriter.Write(rowValues); err != nil {
			return err
		}

		csvWriter.Flush()
		if err := rc.Flush(); err != nil {
			return err
		}
	}

	return ErrPipelineHalted
}

// streamRaw streams raw bytes from an io.Reader.
func (s *StepStream) streamRaw(ctx context.Context, ec StepExecutionContext, rc *http.ResponseController, source any) error {
	reader, ok := source.(io.Reader)
	if !ok {
		return fmt.Errorf("stream 'raw' source must implement io.Reader, got %T", source)
	}

	if closer, isCloser := reader.(io.Closer); isCloser {
		defer closer.Close()
	}

	w := ec.ResponseWriter()
	if s.ContentType != "" {
		w.Header().Set("Content-Type", s.ContentType)
	}
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		return err
	}

	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			return ErrPipelineHalted
		default:
		}

		n, rErr := reader.Read(buf)
		if n > 0 {
			if _, wErr := w.Write(buf[:n]); wErr != nil {
				return wErr
			}
			if err := rc.Flush(); err != nil {
				return err
			}
		}

		if rErr != nil {
			if errors.Is(rErr, io.EOF) {
				return ErrPipelineHalted
			}
			return rErr
		}
	}
}

// decodeStepStream decodes the HCL block representation into a [*StepStream].
func decodeStepStream(name string, body hcl.Body, evalCtx *hcl.EvalContext, funcs map[string]function.Function) (Step, error) {
	type streamDecode struct {
		SourceExpr  hcl.Expression    `hcl:"source"`
		Event       string            `hcl:"event,optional"`
		Heartbeat   string            `hcl:"heartbeat,optional"`
		Retry       string            `hcl:"retry,optional"`
		FlushEvery  int               `hcl:"flush_every,optional"`
		Headers     map[string]string `hcl:"headers,optional"`
		ContentType string            `hcl:"content_type,optional"`
		SchemaExpr  hcl.Expression    `hcl:"schema,optional"`
		WhenExpr    hcl.Expression    `hcl:"when,optional"`
	}

	var raw streamDecode
	if diags := gohcl.DecodeBody(body, evalCtx, &raw); diags.HasErrors() {
		return nil, diags
	}

	step := &StepStream{
		Format:      name,
		Event:       raw.Event,
		FlushEvery:  raw.FlushEvery,
		Headers:     raw.Headers,
		ContentType: raw.ContentType,
		Source:      NewExpr(raw.SourceExpr, funcs),
		When:        NewExpr(raw.WhenExpr, funcs),
	}

	if raw.Heartbeat != "" {
		d, err := ParseDuration(raw.Heartbeat)
		if err != nil {
			return nil, fmt.Errorf("stream heartbeat: %w", err)
		}
		step.Heartbeat = d
	}

	if raw.Retry != "" {
		d, err := ParseDuration(raw.Retry)
		if err != nil {
			return nil, fmt.Errorf("stream retry: %w", err)
		}
		step.Retry = d
	}

	if raw.SchemaExpr != nil {
		val, diags := raw.SchemaExpr.Value(evalCtx)
		if diags.HasErrors() {
			return nil, diags
		}
		if val != cty.NilVal && !val.IsNull() && val.IsKnown() {
			spec, err := TypeSpecFromCty(val)
			if err != nil {
				return nil, fmt.Errorf("stream schema: %w", err)
			}
			step.Schema = &spec
		}
	}

	return step, nil
}
