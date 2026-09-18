package manifest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// StepRespond terminates pipeline execution, serializes the response payload, applies
// schema-based egress field masking, and streams the HTTP response to the client.
//
// A respond step may declare a contract using an explicit named [TypeSpec] reference via Schema,
// an explicit inline schema block via InlineFields, or omit both to allow the compiler to statically
// infer the response shape directly from the Body expression.
type StepRespond struct {
	Status       int
	Headers      map[string]string
	When         Expr
	Body         Expr
	Schema       *TypeSpec
	InlineFields map[string]Field
}

// StepName implements [Step] and returns the step identifier.
func (r *StepRespond) StepName() string {
	return "respond"
}

// StepWhen implements [Step] and returns the conditional execution guard.
func (r *StepRespond) StepWhen() Expr {
	return r.When
}

// IsTerminal implements [Step] and returns true.
func (r *StepRespond) IsTerminal() bool {
	return true
}

// ValidateStep implements [StepValidator]. It verifies that the HTTP status code is valid,
// ensures that Schema and InlineFields are mutually exclusive, and checks that all referenced models exist.
func (r *StepRespond) ValidateStep(m *Manifest) error {
	if r.Status != 0 && (r.Status < 100 || r.Status > 599) {
		return fmt.Errorf("respond status %d outside valid HTTP range", r.Status)
	}

	if r.Schema != nil && len(r.InlineFields) > 0 {
		return errors.New("respond cannot declare both 'schema' attribute and inline 'schema' block")
	}

	if r.Schema != nil {
		ref := r.Schema.ElementSchemaRef()
		if ref != "" {
			if _, exists := m.Schemas[ref]; !exists {
				return fmt.Errorf("respond references unknown schema %q", ref)
			}
		}
	}

	for fName, f := range r.InlineFields {
		if f.Format != "" {
			if err := ValidateFormat(string(f.Format)); err != nil {
				return fmt.Errorf("respond inline schema field %q: %w", fName, err)
			}
		}

		ref := f.Type.ElementSchemaRef()
		if ref != "" {
			if _, exists := m.Schemas[ref]; !exists {
				return fmt.Errorf("respond inline schema field %q references unknown schema %q", fName, ref)
			}
		}
	}

	return nil
}

// ExecuteStep implements [StepExecutor]. It serializes the body, applies named or inline
// schema masking, sets headers, and streams the wire response.
func (r *StepRespond) ExecuteStep(ctx context.Context, ec StepExecutionContext) (any, error) {
	status := r.Status
	if status == 0 {
		status = http.StatusOK
	}

	var rawBody any
	if r.Body != nil {
		evaluated, err := r.Body.Eval(ec.Scope())
		if err != nil {
			return nil, fmt.Errorf("respond evaluate body: %w", err)
		}
		rawBody = evaluated
	}

	if rawBody != nil {
		if r.Schema != nil {
			masked, err := maskPayload(rawBody, *r.Schema, ec.Schemas())
			if err != nil {
				return nil, err
			}
			rawBody = masked
		} else if len(r.InlineFields) > 0 {
			masked, err := maskInlineFields(rawBody, r.InlineFields, ec.Schemas())
			if err != nil {
				return nil, err
			}
			rawBody = masked
		}
	}

	w := ec.ResponseWriter()
	for k, v := range r.Headers {
		w.Header().Set(k, v)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType == "" {
		contentType = "application/json; charset=utf-8"
		w.Header().Set("Content-Type", contentType)
	}

	w.WriteHeader(status)

	if rawBody == nil || status == http.StatusNoContent {
		return nil, ErrPipelineHalted
	}

	if strings.HasPrefix(contentType, "application/json") {
		if err := json.NewEncoder(w).Encode(rawBody); err != nil {
			return nil, fmt.Errorf("respond json encode: %w", err)
		}
		return nil, ErrPipelineHalted
	}

	switch v := rawBody.(type) {
	case []byte:
		if _, err := w.Write(v); err != nil {
			return nil, fmt.Errorf("respond write bytes: %w", err)
		}
	case string:
		if _, err := w.Write([]byte(v)); err != nil {
			return nil, fmt.Errorf("respond write string: %w", err)
		}
	default:
		if err := json.NewEncoder(w).Encode(rawBody); err != nil {
			return nil, fmt.Errorf("respond encode fallback: %w", err)
		}
	}

	return nil, ErrPipelineHalted
}

// decodeStepRespond decodes an HCL block into a [*StepRespond], supporting both
// named schema expressions and inline schema { field ... } blocks.
func decodeStepRespond(name string, body hcl.Body, evalCtx *hcl.EvalContext, funcs map[string]function.Function) (Step, error) {
	content, diags := body.Content(respondBlockSchema)
	if diags.HasErrors() {
		return nil, diags
	}

	step := &StepRespond{
		Status:       200,
		Headers:      make(map[string]string),
		InlineFields: make(map[string]Field),
	}

	if attr, ok := content.Attributes["status"]; ok {
		val, err := attr.Expr.Value(evalCtx)
		if err == nil && !val.IsNull() && val.Type() == cty.Number {
			i, _ := val.AsBigFloat().Int64()
			step.Status = int(i)
		}
	}

	if attr, ok := content.Attributes["headers"]; ok {
		step.Headers = decodeStaticHeaders(attr.Expr, evalCtx)
	}

	if attr, ok := content.Attributes["when"]; ok {
		step.When = NewExpr(attr.Expr, funcs)
	}

	if attr, ok := content.Attributes["body"]; ok {
		step.Body = NewExpr(attr.Expr, funcs)
	}

	hasSchemaAttr := false
	if attr, ok := content.Attributes["schema"]; ok {
		val, d := attr.Expr.Value(evalCtx)
		if d.HasErrors() {
			return nil, d
		}
		if val != cty.NilVal && !val.IsNull() && val.IsKnown() {
			spec, err := TypeSpecFromCty(val)
			if err != nil {
				return nil, fmt.Errorf("respond schema: %w", err)
			}
			step.Schema = &spec
			hasSchemaAttr = true
		}
	}

	for _, b := range content.Blocks {
		if b.Type == "schema" {
			if hasSchemaAttr {
				return nil, errors.New("respond cannot declare both 'schema' attribute and inline 'schema' block")
			}

			var inlineBlock struct {
				Fields []fieldDecode `hcl:"field,block"`
			}
			if d := gohcl.DecodeBody(b.Body, evalCtx, &inlineBlock); d.HasErrors() {
				return nil, d
			}

			for _, fd := range inlineBlock.Fields {
				typeVal, tDiags := fd.TypeExpr.Value(evalCtx)
				if tDiags.HasErrors() {
					return nil, tDiags
				}
				spec, err := TypeSpecFromCty(typeVal)
				if err != nil {
					return nil, err
				}

				step.InlineFields[fd.Name] = Field{
					Name:        fd.Name,
					Type:        spec,
					Required:    fd.Required,
					Format:      Format(fd.Format),
					Description: fd.Description,
					Min:         fd.Min,
					Max:         fd.Max,
					MinLength:   fd.MinLength,
					MaxLength:   fd.MaxLength,
					Enum:        fd.Enum,
				}
			}
		}
	}

	return step, nil
}

// decodeStaticHeaders extracts string key-value headers from an evaluated HCL expression.
func decodeStaticHeaders(expr hcl.Expression, ctx *hcl.EvalContext) map[string]string {
	headers := make(map[string]string)
	if expr == nil {
		return headers
	}

	val, diags := expr.Value(ctx)
	if diags.HasErrors() || val.IsNull() || !val.IsKnown() {
		return headers
	}

	if val.Type().IsObjectType() || val.Type().IsMapType() {
		for it := val.ElementIterator(); it.Next(); {
			k, v := it.Element()
			if v.Type() == cty.String {
				headers[k.AsString()] = v.AsString()
			} else {
				headers[k.AsString()] = fmt.Sprintf("%v", toNative(v))
			}
		}
	}

	return headers
}

// maskInlineFields applies egress field masking directly from a map of inline field rules.
func maskInlineFields(val any, fields map[string]Field, schemas map[string]Schema) (any, error) {
	m, ok := val.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("schema violation: expected object response for inline schema, got %T", val)
	}

	output := make(map[string]any, len(fields))
	for name, field := range fields {
		v, exists := m[name]
		if !exists {
			continue
		}

		if field.Type.IsArray() && field.Type.ElementSchemaRef() != "" {
			elemRef := field.Type.ElementSchemaRef()
			if list, okList := v.([]any); okList {
				maskedList := make([]any, len(list))
				for i, item := range list {
					if itemMap, okItem := item.(map[string]any); okItem {
						maskedList[i] = maskObject(itemMap, elemRef, schemas)
					} else {
						maskedList[i] = item
					}
				}
				output[name] = maskedList
				continue
			}
			if mapSlice, okMapSlice := v.([]map[string]any); okMapSlice {
				maskedList := make([]any, len(mapSlice))
				for i, itemMap := range mapSlice {
					maskedList[i] = maskObject(itemMap, elemRef, schemas)
				}
				output[name] = maskedList
				continue
			}
		} else if field.Type.IsObject() && field.Type.SchemaRef != "" {
			if itemMap, okItem := v.(map[string]any); okItem {
				output[name] = maskObject(itemMap, field.Type.SchemaRef, schemas)
				continue
			}
		}

		output[name] = v
	}

	return output, nil
}

// maskPayload validates collection cardinality and filters undeclared entity fields.
func maskPayload(val any, spec TypeSpec, schemas map[string]Schema) (any, error) {
	if spec.IsArray() {
		var sliceVal []any
		switch s := val.(type) {
		case []any:
			sliceVal = s
		case []map[string]any:
			sliceVal = make([]any, len(s))
			for i, m := range s {
				sliceVal[i] = m
			}
		default:
			return nil, fmt.Errorf("schema violation: expected array response, got %T", val)
		}

		elemRef := spec.ElementSchemaRef()
		if elemRef == "" {
			return sliceVal, nil
		}

		out := make([]any, len(sliceVal))
		for i, item := range sliceVal {
			if m, isMap := item.(map[string]any); isMap {
				out[i] = maskObject(m, elemRef, schemas)
			} else {
				out[i] = item
			}
		}
		return out, nil
	}

	if spec.IsObject() {
		m, ok := val.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("schema violation: expected object response, got %T", val)
		}
		if spec.SchemaRef == "" {
			return m, nil
		}
		return maskObject(m, spec.SchemaRef, schemas), nil
	}

	return val, nil
}

// maskObject prunes undeclared fields according to the target schema model.
func maskObject(input map[string]any, schemaRef string, schemas map[string]Schema) map[string]any {
	if schemaRef == "" {
		return input
	}

	target, exists := schemas[schemaRef]
	if !exists {
		return input
	}

	output := make(map[string]any, len(target.Fields))
	for name, field := range target.Fields {
		val, ok := input[name]
		if !ok {
			continue
		}

		if field.Type.IsArray() && field.Type.ElementSchemaRef() != "" {
			elemRef := field.Type.ElementSchemaRef()
			if list, okList := val.([]any); okList {
				maskedList := make([]any, len(list))
				for i, item := range list {
					if itemMap, okItem := item.(map[string]any); okItem {
						maskedList[i] = maskObject(itemMap, elemRef, schemas)
					} else {
						maskedList[i] = item
					}
				}
				output[name] = maskedList
				continue
			}
			if mapSlice, okMapSlice := val.([]map[string]any); okMapSlice {
				maskedList := make([]any, len(mapSlice))
				for i, itemMap := range mapSlice {
					maskedList[i] = maskObject(itemMap, elemRef, schemas)
				}
				output[name] = maskedList
				continue
			}
		} else if field.Type.IsObject() && field.Type.SchemaRef != "" {
			if itemMap, okItem := val.(map[string]any); okItem {
				output[name] = maskObject(itemMap, field.Type.SchemaRef, schemas)
				continue
			}
		}

		output[name] = val
	}

	return output
}

var respondBlockSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "status"},
		{Name: "headers"},
		{Name: "when"},
		{Name: "body"},
		{Name: "schema"},
	},
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "schema"},
	},
}
