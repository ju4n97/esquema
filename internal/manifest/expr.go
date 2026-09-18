package manifest

import (
	"fmt"
	"math/big"
	"reflect"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

var anyCapsuleType = cty.Capsule("any", reflect.TypeOf((*any)(nil)).Elem())

// Scope encapsulates the runtime execution state supplied to an [Expr] during evaluation.
type Scope struct {
	Request map[string]any
	Steps   map[string]any
	Headers map[string]string
	Now     time.Time
}

// Expr represents a compiled HCL expression ready for evaluation against a [Scope].
type Expr interface {
	Eval(scope Scope) (any, error)
	EvalBool(scope Scope) (bool, error)
	EvalMap(scope Scope) (map[string]any, error)
	String() string
	Raw() hcl.Expression
}

type hclExpr struct {
	raw   hcl.Expression
	funcs map[string]function.Function
}

// NewExpr compiles an HCL expression and optional function registry into an [Expr].
func NewExpr(raw hcl.Expression, funcs map[string]function.Function) Expr {
	if raw == nil {
		return nil
	}
	return &hclExpr{raw: raw, funcs: funcs}
}

// Eval implements [Expr].
func (e *hclExpr) Eval(scope Scope) (any, error) {
	if e == nil || e.raw == nil {
		return nil, nil
	}
	val, diags := e.raw.Value(e.buildEvalContext(scope))
	if diags.HasErrors() {
		return nil, diags
	}
	return toNative(val), nil
}

// EvalBool implements [Expr].
func (e *hclExpr) EvalBool(scope Scope) (bool, error) {
	if e == nil || e.raw == nil {
		return true, nil
	}
	val, diags := e.raw.Value(e.buildEvalContext(scope))
	if diags.HasErrors() {
		return false, diags
	}
	if val == cty.NilVal || val.IsNull() || !val.IsKnown() || val == cty.DynamicVal {
		return true, nil
	}
	if val.Type() != cty.Bool {
		return false, fmt.Errorf("expected boolean expression, got %s", val.Type().FriendlyName())
	}
	return val.True(), nil
}

// EvalMap implements [Expr].
func (e *hclExpr) EvalMap(scope Scope) (map[string]any, error) {
	res, err := e.Eval(scope)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}
	m, ok := res.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected map, got %T", res)
	}
	return m, nil
}

// String implements [Expr] and [fmt.Stringer].
func (e *hclExpr) String() string {
	if e == nil || e.raw == nil {
		return ""
	}
	return fmt.Sprintf("%v", e.raw)
}

// Raw returns the underlying raw HCL expression for static analysis and schema inference.
func (e *hclExpr) Raw() hcl.Expression {
	if e == nil {
		return nil
	}
	return e.raw
}

// buildEvalContext builds an [hcl.EvalContext] for the given [Scope].
func (e *hclExpr) buildEvalContext(scope Scope) *hcl.EvalContext {
	ctxVal := map[string]cty.Value{
		"request":   toCty(scope.Request),
		"timestamp": cty.NumberIntVal(scope.Now.Unix()),
		"now":       cty.StringVal(scope.Now.Format(time.RFC3339)),
	}

	return &hcl.EvalContext{
		Variables: map[string]cty.Value{
			"ctx":   cty.ObjectVal(ctxVal),
			"steps": toCty(scope.Steps),
		},
		Functions: e.funcs,
	}
}

// toCty converts Go values into [cty.Value]. Non-primitive types like iterators,
// channels, and readers are preserved inside a [cty.Capsule] value.
func toCty(val any) cty.Value {
	if val == nil {
		return cty.NullVal(cty.DynamicPseudoType)
	}
	switch v := val.(type) {
	case string:
		return cty.StringVal(v)
	case bool:
		return cty.BoolVal(v)
	case int:
		return cty.NumberIntVal(int64(v))
	case int64:
		return cty.NumberIntVal(v)
	case float64:
		return cty.NumberFloatVal(v)
	case map[string]any:
		if len(v) == 0 {
			return cty.EmptyObjectVal
		}
		attrs := make(map[string]cty.Value, len(v))
		for k, item := range v {
			attrs[k] = toCty(item)
		}
		return cty.ObjectVal(attrs)
	case map[string]string:
		if len(v) == 0 {
			return cty.EmptyObjectVal
		}
		attrs := make(map[string]cty.Value, len(v))
		for k, item := range v {
			attrs[k] = cty.StringVal(item)
		}
		return cty.ObjectVal(attrs)
	case []any:
		if len(v) == 0 {
			return cty.EmptyTupleVal
		}
		elems := make([]cty.Value, len(v))
		for i, item := range v {
			elems[i] = toCty(item)
		}
		return cty.TupleVal(elems)
	case []map[string]any:
		if len(v) == 0 {
			return cty.EmptyTupleVal
		}
		elems := make([]cty.Value, len(v))
		for i, item := range v {
			elems[i] = toCty(item)
		}
		return cty.TupleVal(elems)
	default:
		// Preserve arbitrary Go objects (iterators, io.Reader, channels) via capsule
		return cty.CapsuleVal(anyCapsuleType, &val)
	}
}

// toNative converts a [cty.Value] back into standard Go types, unwrapping capsules.
func toNative(val cty.Value) any {
	if val == cty.NilVal || val.IsNull() || !val.IsKnown() {
		return nil
	}

	if val.Type().IsCapsuleType() {
		return *val.EncapsulatedValue().(*any)
	}

	ty := val.Type()
	switch {
	case ty == cty.String:
		return val.AsString()
	case ty == cty.Bool:
		return val.True()
	case ty == cty.Number:
		bf := val.AsBigFloat()
		if i, acc := bf.Int64(); acc == big.Exact {
			return i
		}
		f, _ := bf.Float64()
		return f
	case ty.IsTupleType() || ty.IsListType() || ty.IsSetType():
		out := make([]any, 0, val.LengthInt())
		for it := val.ElementIterator(); it.Next(); {
			_, el := it.Element()
			out = append(out, toNative(el))
		}
		return out
	case ty.IsObjectType() || ty.IsMapType():
		out := make(map[string]any)
		for it := val.ElementIterator(); it.Next(); {
			k, el := it.Element()
			out[k.AsString()] = toNative(el)
		}
		return out
	default:
		return nil
	}
}
