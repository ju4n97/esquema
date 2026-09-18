package manifest

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// DataType represents an OpenAPI 3.1 primitive or collection type.
type DataType string

// Supported OpenAPI data types.
const (
	TypeString  DataType = "string"
	TypeInteger DataType = "integer"
	TypeNumber  DataType = "number"
	TypeBoolean DataType = "boolean"
	TypeObject  DataType = "object"
	TypeArray   DataType = "array"
)

// IsValid reports whether dt matches a supported OpenAPI data type.
func (dt DataType) IsValid() bool {
	switch dt {
	case TypeString, TypeInteger, TypeNumber, TypeBoolean, TypeObject, TypeArray:
		return true
	default:
		return false
	}
}

// Format defines semantic string constraints specified by OpenAPI.
type Format string

// Supported OpenAPI semantic formats.
const (
	FormatEmail    Format = "email"
	FormatUUID     Format = "uuid"
	FormatURI      Format = "uri"
	FormatDateTime Format = "date-time"
	FormatDate     Format = "date"
	FormatIPv4     Format = "ipv4"
	FormatIPv6     Format = "ipv6"
	FormatHostname Format = "hostname"
)

// ValidateFormat reports whether format is an allowed OpenAPI semantic constraint.
// An empty or whitespace-only string is considered valid (no constraint).
func ValidateFormat(format string) error {
	trimmed := strings.TrimSpace(format)
	if trimmed == "" {
		return nil
	}

	switch Format(strings.ToLower(trimmed)) {
	case FormatEmail, FormatUUID, FormatURI, FormatDateTime, FormatDate, FormatIPv4, FormatIPv6, FormatHostname:
		return nil
	default:
		return fmt.Errorf("unrecognized format %q (allowed: email, uuid, uri, date-time, date, ipv4, ipv6, hostname)", format)
	}
}

// TypeSpec encapsulates a scalar primitive, custom schema reference, or nested list.
//
// Because TypeSpec contains a pointer field (ElemType), instances must never be
// compared using Go == or != operators. Use [TypeSpec.Equal] instead.
type TypeSpec struct {
	Type      DataType
	SchemaRef string
	ElemType  *TypeSpec
}

// ScalarType constructs a TypeSpec for primitive scalar types.
func ScalarType(dt DataType) (TypeSpec, error) {
	if !dt.IsValid() || dt == TypeArray {
		return TypeSpec{}, fmt.Errorf("invalid scalar data type %q", dt)
	}
	return TypeSpec{Type: dt}, nil
}

// ObjectType constructs a TypeSpec referencing a declared schema model.
func ObjectType(schemaRef string) TypeSpec {
	return TypeSpec{
		Type:      TypeObject,
		SchemaRef: strings.TrimSpace(schemaRef),
	}
}

// ListType constructs a TypeSpec representing an array of the specified element type.
func ListType(elem TypeSpec) TypeSpec {
	return TypeSpec{
		Type:     TypeArray,
		ElemType: &elem,
	}
}

// IsScalar reports whether the specification represents a primitive scalar value.
func (t TypeSpec) IsScalar() bool {
	switch t.Type {
	case TypeString, TypeInteger, TypeNumber, TypeBoolean:
		return true
	default:
		return false
	}
}

// IsObject reports whether the specification represents a single object or schema reference.
func (t TypeSpec) IsObject() bool {
	return t.Type == TypeObject
}

// IsArray reports whether the specification represents a list or array.
func (t TypeSpec) IsArray() bool {
	return t.Type == TypeArray
}

// ElementSchemaRef recursively traverses nested lists to locate the underlying
// schema identifier, returning an empty string if the element is a scalar primitive.
func (t TypeSpec) ElementSchemaRef() string {
	if t.Type == TypeArray && t.ElemType != nil {
		return t.ElemType.ElementSchemaRef()
	}
	return t.SchemaRef
}

// Equal performs a deep equality comparison against another TypeSpec.
func (t TypeSpec) Equal(other TypeSpec) bool {
	if t.Type != other.Type || t.SchemaRef != other.SchemaRef {
		return false
	}

	if (t.ElemType == nil) != (other.ElemType == nil) {
		return false
	}

	if t.ElemType != nil {
		return t.ElemType.Equal(*other.ElemType)
	}

	return true
}

// String returns the canonical HCL representation of the type specification.
func (t TypeSpec) String() string {
	if t.Type == TypeArray {
		if t.ElemType == nil {
			return "list()"
		}
		return fmt.Sprintf("list(%s)", t.ElemType.String())
	}

	if t.SchemaRef != "" {
		return t.SchemaRef
	}

	return string(t.Type)
}

// ParseTypeSpec parses a type string, accepting primitives, schema identifiers,
// and collection wrappers using list(T).
func ParseTypeSpec(raw string) (TypeSpec, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return TypeSpec{}, errors.New("type specification cannot be empty")
	}

	switch s {
	case "string":
		return TypeSpec{Type: TypeString}, nil
	case "integer":
		return TypeSpec{Type: TypeInteger}, nil
	case "number":
		return TypeSpec{Type: TypeNumber}, nil
	case "boolean":
		return TypeSpec{Type: TypeBoolean}, nil
	case "object":
		return TypeSpec{Type: TypeObject}, nil
	case "list":
		return TypeSpec{}, fmt.Errorf("untyped %q is not allowed: specify element type using list(T)", s)
	}

	// Handle list(T)
	const prefix = "list("
	if strings.HasPrefix(s, prefix) {
		depth := 0
		for i := 0; i < len(s); i++ {
			switch s[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth < 0 {
					return TypeSpec{}, fmt.Errorf("malformed collection specification %q: unbalanced parentheses", s)
				}
				if depth == 0 && i < len(s)-1 {
					return TypeSpec{}, fmt.Errorf("malformed collection specification %q: unexpected trailing characters", s)
				}
			}
		}

		if depth > 0 {
			return TypeSpec{}, fmt.Errorf("malformed collection specification %q: missing closing parenthesis", s)
		}

		inner := strings.TrimSpace(s[len(prefix) : len(s)-1])
		if inner == "" {
			return TypeSpec{}, fmt.Errorf("malformed collection specification %q: element type cannot be empty", s)
		}

		elem, err := ParseTypeSpec(inner)
		if err != nil {
			return TypeSpec{}, err
		}

		return TypeSpec{
			Type:     TypeArray,
			ElemType: &elem,
		}, nil
	}

	if !isValidIdentifier(s) {
		return TypeSpec{}, fmt.Errorf("invalid type identifier %q: must be a valid schema or primitive name", s)
	}

	return TypeSpec{
		Type:      TypeObject,
		SchemaRef: s,
	}, nil
}

// TypeSpecFromCty converts an evaluated cty.Value into a verified TypeSpec.
func TypeSpecFromCty(val cty.Value) (TypeSpec, error) {
	if val == cty.NilVal || val.IsNull() || !val.IsKnown() {
		return TypeSpec{}, errors.New("type specification expression resolved to null or uninitialized value")
	}

	if val.Type() != cty.String {
		return TypeSpec{}, fmt.Errorf("expected string or type identifier, got %s", val.Type().FriendlyName())
	}

	return ParseTypeSpec(val.AsString())
}

// BuiltinTypeVariables returns identifiers allowing unquoted syntax like type = string or type = User.
func BuiltinTypeVariables(schemas ...string) map[string]cty.Value {
	vars := map[string]cty.Value{
		"string":  cty.StringVal("string"),
		"integer": cty.StringVal("integer"),
		"number":  cty.StringVal("number"),
		"boolean": cty.StringVal("boolean"),
		"object":  cty.StringVal("object"),
	}

	for _, s := range schemas {
		trimmed := strings.TrimSpace(s)
		if trimmed != "" && isValidIdentifier(trimmed) {
			vars[trimmed] = cty.StringVal(trimmed)
		}
	}

	return vars
}

// BuiltinTypeFunctions returns constructors exposed during manifest evaluation.
func BuiltinTypeFunctions() map[string]function.Function {
	return map[string]function.Function{
		"list": function.New(&function.Spec{
			Params: []function.Parameter{
				{
					Name: "elem",
					Type: cty.String,
				},
			},
			Type: function.StaticReturnType(cty.String),
			Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
				elem := strings.TrimSpace(args[0].AsString())
				if elem == "" {
					return cty.NilVal, errors.New("collection element type cannot be empty")
				}
				return cty.StringVal(fmt.Sprintf("list(%s)", elem)), nil
			},
		}),
	}
}

func isValidIdentifier(s string) bool {
	if s == "" {
		return false
	}

	for i, r := range s {
		if i == 0 {
			if !unicode.IsLetter(r) && r != '_' {
				return false
			}
			continue
		}

		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' {
			return false
		}
	}

	return true
}
