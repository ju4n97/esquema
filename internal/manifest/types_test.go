package manifest

import (
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// TestDataType_IsValid verifies primitive OpenAPI type identification.
func TestDataType_IsValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input DataType
		want  bool
	}{
		{
			name:  "string type",
			input: TypeString,
			want:  true,
		},
		{
			name:  "integer type",
			input: TypeInteger,
			want:  true,
		},
		{
			name:  "number type",
			input: TypeNumber,
			want:  true,
		},
		{
			name:  "boolean type",
			input: TypeBoolean,
			want:  true,
		},
		{
			name:  "object type",
			input: TypeObject,
			want:  true,
		},
		{
			name:  "array type",
			input: TypeArray,
			want:  true,
		},
		{
			name:  "unrecognized data type",
			input: DataType("custom"),
			want:  false,
		},
		{
			name:  "empty data type",
			input: DataType(""),
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.input.IsValid(); got != tt.want {
				t.Fatalf("DataType.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestValidateFormat verifies string semantic constraint rules.
func TestValidateFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		format    string
		wantError bool
	}{
		{
			name:      "empty format",
			format:    "",
			wantError: false,
		},
		{
			name:      "whitespace format",
			format:    "   ",
			wantError: false,
		},
		{
			name:      "email format",
			format:    "email",
			wantError: false,
		},
		{
			name:      "uuid format",
			format:    "uuid",
			wantError: false,
		},
		{
			name:      "uri format",
			format:    "uri",
			wantError: false,
		},
		{
			name:      "date-time format",
			format:    "date-time",
			wantError: false,
		},
		{
			name:      "date format",
			format:    "date",
			wantError: false,
		},
		{
			name:      "ipv4 format",
			format:    "ipv4",
			wantError: false,
		},
		{
			name:      "ipv6 format",
			format:    "ipv6",
			wantError: false,
		},
		{
			name:      "hostname format",
			format:    "hostname",
			wantError: false,
		},
		{
			name:      "case-insensitive format matching",
			format:    "UUID",
			wantError: false,
		},
		{
			name:      "unsupported format",
			format:    "custom-format",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ValidateFormat(tt.format)
			if (err != nil) != tt.wantError {
				t.Fatalf("ValidateFormat(%q) error = %v, wantError = %v", tt.format, err, tt.wantError)
			}
		})
	}
}

// TestConstructors verifies functional constructors for scalar, object, and collection types.
func TestConstructors(t *testing.T) {
	t.Parallel()

	t.Run("ScalarType success", func(t *testing.T) {
		t.Parallel()

		spec, err := ScalarType(TypeString)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if spec.Type != TypeString || spec.ElemType != nil || spec.SchemaRef != "" {
			t.Fatalf("unexpected scalar spec: %+v", spec)
		}
	})

	t.Run("ScalarType rejects collection data type", func(t *testing.T) {
		t.Parallel()

		_, err := ScalarType(TypeArray)
		if err == nil {
			t.Fatal("expected error rejecting array as scalar, got nil")
		}
	})

	t.Run("ObjectType sets schema reference", func(t *testing.T) {
		t.Parallel()

		spec := ObjectType("User")
		if spec.Type != TypeObject || spec.SchemaRef != "User" || spec.ElemType != nil {
			t.Fatalf("unexpected object spec: %+v", spec)
		}
	})

	t.Run("ListType sets array and nested element", func(t *testing.T) {
		t.Parallel()

		elem := ObjectType("User")
		spec := ListType(elem)
		if spec.Type != TypeArray || spec.ElemType == nil || !spec.ElemType.Equal(elem) {
			t.Fatalf("unexpected list spec: %+v", spec)
		}
	})
}

// TestParseTypeSpec verifies type parsing across scalars, models, list(T).
func TestParseTypeSpec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		want      TypeSpec
		wantError bool
		errSubstr string
	}{
		{
			name:  "primitive string",
			input: "string",
			want:  TypeSpec{Type: TypeString},
		},
		{
			name:  "primitive integer",
			input: "integer",
			want:  TypeSpec{Type: TypeInteger},
		},
		{
			name:  "primitive number",
			input: "number",
			want:  TypeSpec{Type: TypeNumber},
		},
		{
			name:  "primitive boolean",
			input: "boolean",
			want:  TypeSpec{Type: TypeBoolean},
		},
		{
			name:  "generic object",
			input: "object",
			want:  TypeSpec{Type: TypeObject},
		},
		{
			name:  "custom schema identifier",
			input: "UserProfile",
			want:  TypeSpec{Type: TypeObject, SchemaRef: "UserProfile"},
		},
		{
			name:  "list syntax with primitive",
			input: "list(string)",
			want: TypeSpec{
				Type:     TypeArray,
				ElemType: &TypeSpec{Type: TypeString},
			},
		},
		{
			name:  "list syntax with custom schema",
			input: "list(Account)",
			want: TypeSpec{
				Type:     TypeArray,
				ElemType: &TypeSpec{Type: TypeObject, SchemaRef: "Account"},
			},
		},
		{
			name:  "nested list hierarchy",
			input: "list(list(integer))",
			want: TypeSpec{
				Type: TypeArray,
				ElemType: &TypeSpec{
					Type: TypeArray,
					ElemType: &TypeSpec{
						Type: TypeInteger,
					},
				},
			},
		},
		{
			name:  "whitespace normalization",
			input: "  list(   User  ) ",
			want: TypeSpec{
				Type:     TypeArray,
				ElemType: &TypeSpec{Type: TypeObject, SchemaRef: "User"},
			},
		},
		{
			name:      "empty input",
			input:     "",
			wantError: true,
			errSubstr: "cannot be empty",
		},
		{
			name:      "untyped list keyword rejected",
			input:     "list",
			wantError: true,
			errSubstr: "untyped \"list\" is not allowed",
		},
		{
			name:      "missing closing parenthesis",
			input:     "list(string",
			wantError: true,
			errSubstr: "missing closing parenthesis",
		},
		{
			name:      "empty list element",
			input:     "list()",
			wantError: true,
			errSubstr: "element type cannot be empty",
		},
		{
			name:      "trailing characters after list",
			input:     "list(string)trailing",
			wantError: true,
			errSubstr: "unexpected trailing characters",
		},
		{
			name:      "unbalanced parentheses",
			input:     "list(string))",
			wantError: true,
			errSubstr: "unexpected trailing characters",
		},
		{
			name:      "invalid identifier starting with number",
			input:     "9Account",
			wantError: true,
			errSubstr: "invalid type identifier",
		},
		{
			name:      "invalid identifier with punctuation",
			input:     "item-profile",
			wantError: true,
			errSubstr: "invalid type identifier",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := ParseTypeSpec(tt.input)
			if (err != nil) != tt.wantError {
				t.Fatalf("ParseTypeSpec(%q) error = %v, wantError = %v", tt.input, err, tt.wantError)
			}

			if tt.wantError {
				if tt.errSubstr != "" && !strings.Contains(err.Error(), tt.errSubstr) {
					t.Fatalf("error %q does not contain substring %q", err.Error(), tt.errSubstr)
				}
				return
			}

			if !got.Equal(tt.want) {
				t.Fatalf("ParseTypeSpec(%q) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}

// TestTypeSpec_Methods verifies inspection, string representations, and deep equality.
func TestTypeSpec_Methods(t *testing.T) {
	t.Parallel()

	scalarSpec := TypeSpec{Type: TypeString}
	objectSpec := TypeSpec{Type: TypeObject, SchemaRef: "Item"}
	listSpec := TypeSpec{
		Type:     TypeArray,
		ElemType: &TypeSpec{Type: TypeObject, SchemaRef: "Item"},
	}

	t.Run("classification predicates", func(t *testing.T) {
		t.Parallel()

		if !scalarSpec.IsScalar() || scalarSpec.IsArray() || scalarSpec.IsObject() {
			t.Errorf("scalarSpec misclassified: %+v", scalarSpec)
		}

		if !objectSpec.IsObject() || objectSpec.IsScalar() || objectSpec.IsArray() {
			t.Errorf("objectSpec misclassified: %+v", objectSpec)
		}

		if !listSpec.IsArray() || listSpec.IsScalar() || listSpec.IsObject() {
			t.Errorf("listSpec misclassified: %+v", listSpec)
		}
	})

	t.Run("ElementSchemaRef resolution", func(t *testing.T) {
		t.Parallel()

		if got := scalarSpec.ElementSchemaRef(); got != "" {
			t.Errorf("scalar ElementSchemaRef() = %q, want empty", got)
		}

		if got := objectSpec.ElementSchemaRef(); got != "Item" {
			t.Errorf("object ElementSchemaRef() = %q, want 'Item'", got)
		}

		if got := listSpec.ElementSchemaRef(); got != "Item" {
			t.Errorf("list ElementSchemaRef() = %q, want 'Item'", got)
		}

		nestedList := TypeSpec{Type: TypeArray, ElemType: &listSpec}
		if got := nestedList.ElementSchemaRef(); got != "Item" {
			t.Errorf("nested list ElementSchemaRef() = %q, want 'Item'", got)
		}
	})

	t.Run("String formatting", func(t *testing.T) {
		t.Parallel()

		if got := scalarSpec.String(); got != "string" {
			t.Errorf("scalar String() = %q, want 'string'", got)
		}

		if got := objectSpec.String(); got != "Item" {
			t.Errorf("object String() = %q, want 'Item'", got)
		}

		if got := listSpec.String(); got != "list(Item)" {
			t.Errorf("list String() = %q, want 'list(Item)'", got)
		}

		emptyList := TypeSpec{Type: TypeArray}
		if got := emptyList.String(); got != "list()" {
			t.Errorf("empty list String() = %q, want 'list()'", got)
		}
	})

	t.Run("Equal comparison", func(t *testing.T) {
		t.Parallel()

		clone := TypeSpec{
			Type:     TypeArray,
			ElemType: &TypeSpec{Type: TypeObject, SchemaRef: "Item"},
		}

		differentElem := TypeSpec{
			Type:     TypeArray,
			ElemType: &TypeSpec{Type: TypeString},
		}

		if !listSpec.Equal(clone) {
			t.Error("identical specs did not evaluate as equal")
		}

		if listSpec.Equal(differentElem) {
			t.Error("different element types evaluated as equal")
		}

		if listSpec.Equal(scalarSpec) {
			t.Error("collection and scalar evaluated as equal")
		}
	})
}

// TestTypeSpecFromCty verifies unpacking evaluated cty values into TypeSpec structures.
func TestTypeSpecFromCty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     cty.Value
		want      TypeSpec
		wantError bool
	}{
		{
			name:  "cty string scalar",
			input: cty.StringVal("integer"),
			want:  TypeSpec{Type: TypeInteger},
		},
		{
			name:  "cty string list specification",
			input: cty.StringVal("list(User)"),
			want: TypeSpec{
				Type:     TypeArray,
				ElemType: &TypeSpec{Type: TypeObject, SchemaRef: "User"},
			},
		},
		{
			name:      "cty nil value",
			input:     cty.NilVal,
			wantError: true,
		},
		{
			name:      "cty typed null",
			input:     cty.NullVal(cty.String),
			wantError: true,
		},
		{
			name:      "cty unknown value",
			input:     cty.UnknownVal(cty.String),
			wantError: true,
		},
		{
			name:      "cty non-string numeric type",
			input:     cty.NumberIntVal(42),
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := TypeSpecFromCty(tt.input)
			if (err != nil) != tt.wantError {
				t.Fatalf("TypeSpecFromCty() error = %v, wantError = %v", err, tt.wantError)
			}

			if !tt.wantError && !got.Equal(tt.want) {
				t.Fatalf("TypeSpecFromCty() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// TestBuiltinTypeHelpers verifies HCL evaluation scope helpers for list, array, and types.
func TestBuiltinTypeHelpers(t *testing.T) {
	t.Parallel()

	t.Run("BuiltinTypeFunctions executes list constructor", func(t *testing.T) {
		t.Parallel()

		funcs := BuiltinTypeFunctions()

		fn, ok := funcs["list"]
		if !ok {
			t.Fatalf("missing list function in registry")
		}

		res, err := fn.Call([]cty.Value{cty.StringVal("Account")})
		if err != nil {
			t.Fatalf("list unexpected error: %v", err)
		}

		if res.Type() != cty.String || res.AsString() != "list(Account)" {
			t.Fatalf("list returned %q, want 'list(Account)'", res.AsString())
		}
	})

	t.Run("BuiltinTypeFunctions rejects empty element identifier", func(t *testing.T) {
		t.Parallel()

		funcs := BuiltinTypeFunctions()
		listFn := funcs["list"]

		_, err := listFn.Call([]cty.Value{cty.StringVal("   ")})
		if err == nil {
			t.Fatal("expected error on empty element type, got nil")
		}
	})

	t.Run("BuiltinTypeVariables populates primitives and filters invalid identifiers", func(t *testing.T) {
		t.Parallel()

		vars := BuiltinTypeVariables("User", "Session", "123BadIdentifier")

		for _, p := range []string{"string", "integer", "number", "boolean", "object"} {
			val, exists := vars[p]
			if !exists || val.AsString() != p {
				t.Errorf("missing or invalid primitive variable %q in scope", p)
			}
		}

		if val, exists := vars["User"]; !exists || val.AsString() != "User" {
			t.Errorf("expected 'User' variable in scope, got %#v", val)
		}

		if val, exists := vars["Session"]; !exists || val.AsString() != "Session" {
			t.Errorf("expected 'Session' variable in scope, got %#v", val)
		}

		if _, exists := vars["123BadIdentifier"]; exists {
			t.Error("expected invalid identifier to be filtered out")
		}
	})
}
