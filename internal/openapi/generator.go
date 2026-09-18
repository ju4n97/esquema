package openapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"gopkg.in/yaml.v3"

	"github.com/ju4n97/hclapi/internal/manifest"
	"github.com/ju4n97/hclapi/internal/problem"
)

// Spec represents a compiled, validated OpenAPI 3.1 specification ready for serving.
type Spec struct {
	JSON     []byte
	JSONETag string
	YAML     []byte
	YAMLETag string
}

// Compile builds a validated OpenAPI 3.1 specification from a compiled [manifest.Manifest],
// serializing both JSON and YAML documents with calculated SHA-256 ETags.
func Compile(m *manifest.Manifest) (*Spec, error) {
	if m == nil {
		return nil, errors.New("manifest is nil")
	}

	doc := &openapi3.T{
		OpenAPI:    "3.1.0",
		Paths:      openapi3.NewPaths(),
		Components: &openapi3.Components{Schemas: make(openapi3.Schemas)},
	}

	title := strings.TrimSpace(m.OpenAPI.Title)
	if title == "" {
		title = "API Documentation"
	}

	version := strings.TrimSpace(m.OpenAPI.Version)
	if version == "" {
		version = "1.0.0"
	}

	doc.Info = &openapi3.Info{
		Title:       title,
		Version:     version,
		Description: m.OpenAPI.Description,
	}

	if m.OpenAPI.Contact != nil {
		doc.Info.Contact = &openapi3.Contact{
			Name:  m.OpenAPI.Contact.Name,
			Email: m.OpenAPI.Contact.Email,
			URL:   m.OpenAPI.Contact.URL,
		}
	}

	if m.OpenAPI.License != nil {
		doc.Info.License = &openapi3.License{
			Name: m.OpenAPI.License.Name,
			URL:  m.OpenAPI.License.URL,
		}
	}

	for _, srv := range m.OpenAPI.Servers {
		url := strings.TrimSpace(srv.URL)
		if url == "" {
			continue
		}
		doc.Servers = append(doc.Servers, &openapi3.Server{
			URL:         url,
			Description: strings.TrimSpace(srv.Description),
		})
	}
	if len(doc.Servers) == 0 {
		doc.Servers = append(doc.Servers, &openapi3.Server{
			URL:         "/",
			Description: "Default origin",
		})
	}

	for _, t := range m.OpenAPI.Tags {
		name := strings.TrimSpace(t.Name)
		if name == "" {
			continue
		}
		doc.Tags = append(doc.Tags, &openapi3.Tag{
			Name:        name,
			Description: strings.TrimSpace(t.Description),
		})
	}

	doc.Components.Schemas["ProblemDetails"] = &openapi3.SchemaRef{
		Value: buildProblemDetailsSchema(),
	}

	for name, s := range m.Schemas {
		schemaObj := openapi3.NewObjectSchema()
		if s.Description != "" {
			schemaObj.Description = s.Description
		}
		for fName, field := range s.Fields {
			schemaObj.Properties[fName] = fieldToSchemaRef(field)
			if field.Required {
				schemaObj.Required = append(schemaObj.Required, fName)
			}
		}
		doc.Components.Schemas[name] = &openapi3.SchemaRef{Value: schemaObj}
	}

	for _, r := range m.Routes {
		if shouldOmitFromSpec(r) {
			continue
		}

		op := openapi3.NewOperation()
		op.Summary = r.Summary
		if op.Summary == "" {
			op.Summary = r.Pattern()
		}
		if r.Tag != "" {
			op.Tags = []string{r.Tag}
		}
		op.OperationID = deriveOperationID(r.Method, r.Path)

		if r.Request != nil {
			for name, field := range r.Request.Path {
				p := openapi3.NewPathParameter(name).WithSchema(fieldToSchema(field))
				p.Required = true
				op.AddParameter(p)
			}
			for name, field := range r.Request.Query {
				p := openapi3.NewQueryParameter(name).WithSchema(fieldToSchema(field))
				p.Required = field.Required
				op.AddParameter(p)
			}
			for name, field := range r.Request.Headers {
				p := openapi3.NewHeaderParameter(name).WithSchema(fieldToSchema(field))
				p.Required = field.Required
				op.AddParameter(p)
			}

			if r.Request.BodyRef != "" {
				op.RequestBody = &openapi3.RequestBodyRef{
					Value: openapi3.NewRequestBody().
						WithJSONSchemaRef(&openapi3.SchemaRef{Ref: "#/components/schemas/" + r.Request.BodyRef}).
						WithRequired(true),
				}
			} else if len(r.Request.Body) > 0 {
				bodyObj := openapi3.NewObjectSchema()
				for fName, field := range r.Request.Body {
					bodyObj.Properties[fName] = fieldToSchemaRef(field)
					if field.Required {
						bodyObj.Required = append(bodyObj.Required, fName)
					}
				}
				op.RequestBody = &openapi3.RequestBodyRef{
					Value: openapi3.NewRequestBody().
						WithJSONSchemaRef(&openapi3.SchemaRef{Value: bodyObj}).
						WithRequired(true),
				}
			}
		}

		hasSuccessResponse := false

		for _, step := range r.Steps {
			switch s := step.(type) {
			case *manifest.StepRespond:
				status := s.Status
				if status == 0 {
					status = http.StatusOK
				}
				resp := openapi3.NewResponse().WithDescription(http.StatusText(status))

				contentType := s.Headers["Content-Type"]
				if contentType == "" {
					contentType = "application/json"
				}

				if s.Schema != nil {
					ref := resolveSchemaRef(*s.Schema)
					resp.Content = openapi3.Content{
						contentType: &openapi3.MediaType{Schema: ref},
					}
				} else if len(s.InlineFields) > 0 {
					schemaObj := openapi3.NewObjectSchema()
					for fName, field := range s.InlineFields {
						schemaObj.Properties[fName] = fieldToSchemaRef(field)
						if field.Required {
							schemaObj.Required = append(schemaObj.Required, fName)
						}
					}
					resp.Content = openapi3.Content{
						contentType: &openapi3.MediaType{Schema: &openapi3.SchemaRef{Value: schemaObj}},
					}
				} else if s.Body != nil && status != http.StatusNoContent {
					// Static AST inference from body expression
					inferred := inferSchemaFromExpr(s.Body.Raw(), r, m)
					if inferred == nil {
						inferred = &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()}
					}
					resp.Content = openapi3.Content{
						contentType: &openapi3.MediaType{Schema: inferred},
					}
				}

				op.AddResponse(status, resp)
				if status < 400 {
					hasSuccessResponse = true
				}

			case *manifest.StepStream:
				status := http.StatusOK
				resp := openapi3.NewResponse().WithDescription(http.StatusText(status))

				switch strings.ToLower(s.Format) {
				case "sse":
					mediaType := "text/event-stream"
					var schemaRef *openapi3.SchemaRef
					if s.Schema != nil {
						schemaRef = resolveSchemaRef(*s.Schema)
					} else {
						schemaRef = &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}
					}
					resp.Content = openapi3.Content{
						mediaType: &openapi3.MediaType{Schema: schemaRef},
					}

				case "ndjson":
					mediaType := "application/x-ndjson"
					arrSchema := openapi3.NewArraySchema()
					if s.Schema != nil {
						arrSchema.Items = resolveSchemaRef(*s.Schema)
					} else {
						arrSchema.Items = &openapi3.SchemaRef{Value: openapi3.NewObjectSchema()}
					}
					resp.Content = openapi3.Content{
						mediaType: &openapi3.MediaType{Schema: &openapi3.SchemaRef{Value: arrSchema}},
					}

				case "csv":
					resp.Content = openapi3.Content{
						"text/csv": &openapi3.MediaType{Schema: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}},
					}

				case "raw":
					ct := s.ContentType
					if ct == "" {
						ct = "application/octet-stream"
					}
					resp.Content = openapi3.Content{
						ct: &openapi3.MediaType{Schema: &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}},
					}
				}

				op.AddResponse(status, resp)
				hasSuccessResponse = true

			case *manifest.StepSQL:
				for _, c := range s.Catches {
					status := c.Status
					if status == 0 {
						status = http.StatusBadRequest
					}
					resp := openapi3.NewResponse().
						WithDescription(http.StatusText(status)).
						WithContent(problemDetailsContent())
					op.AddResponse(status, resp)
				}
			}
		}

		if !hasSuccessResponse {
			op.AddResponse(http.StatusOK, openapi3.NewResponse().
				WithDescription("OK").
				WithContent(openapi3.NewContentWithJSONSchemaRef(&openapi3.SchemaRef{
					Value: openapi3.NewObjectSchema(),
				})))
		}

		if r.Request != nil && r.Request.HasRules() {
			op.AddResponse(http.StatusUnprocessableEntity, openapi3.NewResponse().
				WithDescription("Unprocessable Entity").
				WithContent(problemDetailsContent()))
		}

		op.AddResponse(http.StatusInternalServerError, openapi3.NewResponse().
			WithDescription("Internal Server Error").
			WithContent(problemDetailsContent()))

		pathItem := doc.Paths.Find(r.Path)
		if pathItem == nil {
			pathItem = &openapi3.PathItem{}
			doc.Paths.Set(r.Path, pathItem)
		}
		pathItem.SetOperation(r.Method, op)
	}

	loader := openapi3.NewLoader()
	if err := loader.ResolveRefsIn(doc, nil); err != nil {
		return nil, fmt.Errorf("resolve openapi schema references: %w", err)
	}

	if err := doc.Validate(context.Background()); err != nil {
		return nil, fmt.Errorf("validate openapi 3.1 specification: %w", err)
	}

	jsonBytes, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal openapi json: %w", err)
	}

	var intermediate any
	if err := json.Unmarshal(jsonBytes, &intermediate); err != nil {
		return nil, err
	}
	yamlBytes, err := yaml.Marshal(intermediate)
	if err != nil {
		return nil, fmt.Errorf("marshal openapi yaml: %w", err)
	}

	jsonHash := sha256.Sum256(jsonBytes)
	yamlHash := sha256.Sum256(yamlBytes)

	return &Spec{
		JSON:     jsonBytes,
		JSONETag: `"` + hex.EncodeToString(jsonHash[:]) + `"`,
		YAML:     yamlBytes,
		YAMLETag: `"` + hex.EncodeToString(yamlHash[:]) + `"`,
	}, nil
}

// inferSchemaFromExpr statically evaluates an HCL expression AST to construct an OpenAPI schema.
func inferSchemaFromExpr(rawExpr hcl.Expression, r manifest.Route, m *manifest.Manifest) *openapi3.SchemaRef {
	if rawExpr == nil {
		return nil
	}

	switch e := rawExpr.(type) {
	case *hclsyntax.ObjectConsExpr:
		schema := openapi3.NewObjectSchema()
		for _, item := range e.Items {
			keyName := extractObjectKey(item.KeyExpr)
			if keyName == "" {
				continue
			}

			propSchema := inferSchemaFromExpr(item.ValueExpr, r, m)
			if propSchema == nil {
				propSchema = &openapi3.SchemaRef{Value: openapi3.NewSchema()}
			}
			schema.Properties[keyName] = propSchema
		}
		return &openapi3.SchemaRef{Value: schema}

	case *hclsyntax.TupleConsExpr:
		schema := openapi3.NewArraySchema()
		if len(e.Exprs) > 0 {
			schema.Items = inferSchemaFromExpr(e.Exprs[0], r, m)
		} else {
			schema.Items = &openapi3.SchemaRef{Value: openapi3.NewSchema()}
		}
		return &openapi3.SchemaRef{Value: schema}

	case *hclsyntax.LiteralValueExpr:
		ty := e.Val.Type()
		switch ty {
		case cty.String:
			return &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}
		case cty.Bool:
			return &openapi3.SchemaRef{Value: openapi3.NewBoolSchema()}
		case cty.Number:
			s := openapi3.NewIntegerSchema()
			bf := e.Val.AsBigFloat()
			if _, acc := bf.Int64(); acc != big.Exact {
				s = openapi3.NewFloat64Schema()
			}
			return &openapi3.SchemaRef{Value: s}
		}

	case *hclsyntax.TemplateWrapExpr:
		if inner := inferSchemaFromExpr(e.Wrapped, r, m); inner != nil {
			return inner
		}
		return &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}

	case *hclsyntax.TemplateExpr:
		return &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}

	case *hclsyntax.ParenthesesExpr:
		return inferSchemaFromExpr(e.Expression, r, m)

	case *hclsyntax.FunctionCallExpr:
		switch strings.ToLower(e.Name) {
		case "now":
			s := openapi3.NewStringSchema().WithFormat("date-time")
			return &openapi3.SchemaRef{Value: s}
		case "uuid":
			s := openapi3.NewStringSchema().WithFormat("uuid")
			return &openapi3.SchemaRef{Value: s}
		case "upper", "lower", "trim", "trimprefix", "trimsuffix", "env":
			return &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}
		case "problem":
			return &openapi3.SchemaRef{Ref: "#/components/schemas/ProblemDetails"}
		}

	case *hclsyntax.ScopeTraversalExpr:
		return inferFromTraversal(e.Traversal, r, m)
	}

	return nil
}

// inferFromTraversal maps context lookups (ctx.request.body, ctx.request.headers.*) to declared schemas.
func inferFromTraversal(traversal hcl.Traversal, r manifest.Route, m *manifest.Manifest) *openapi3.SchemaRef {
	parts := traversalToStrings(traversal)
	if len(parts) == 0 {
		return nil
	}

	if parts[0] == "ctx" && len(parts) >= 3 && parts[1] == "request" {
		coord := parts[2]
		if r.Request == nil {
			return nil
		}

		switch coord {
		case "body":
			if len(parts) == 3 {
				if r.Request.BodyRef != "" {
					return &openapi3.SchemaRef{Ref: "#/components/schemas/" + r.Request.BodyRef}
				}
				if len(r.Request.Body) > 0 {
					obj := openapi3.NewObjectSchema()
					for fName, f := range r.Request.Body {
						obj.Properties[fName] = fieldToSchemaRef(f)
					}
					return &openapi3.SchemaRef{Value: obj}
				}
			} else if len(parts) == 4 {
				fieldName := parts[3]
				if r.Request.BodyRef != "" && m != nil {
					if s, ok := m.Schemas[r.Request.BodyRef]; ok {
						if f, exists := s.Fields[fieldName]; exists {
							return fieldToSchemaRef(f)
						}
					}
				}
				if f, exists := r.Request.Body[fieldName]; exists {
					return fieldToSchemaRef(f)
				}
			}

		case "headers":
			if len(parts) >= 4 {
				hName := strings.ToLower(parts[3])
				if f, exists := r.Request.Headers[hName]; exists {
					return fieldToSchemaRef(f)
				}
				return &openapi3.SchemaRef{Value: openapi3.NewStringSchema()}
			}

		case "query":
			if len(parts) >= 4 {
				qName := parts[3]
				if f, exists := r.Request.Query[qName]; exists {
					return fieldToSchemaRef(f)
				}
			}

		case "path":
			if len(parts) >= 4 {
				pName := parts[3]
				if f, exists := r.Request.Path[pName]; exists {
					return fieldToSchemaRef(f)
				}
			}
		}
	}

	return nil
}

// extractObjectKey extracts string field names from object constructor keys.
func extractObjectKey(keyExpr hclsyntax.Expression) string {
	switch k := keyExpr.(type) {
	case *hclsyntax.ObjectConsKeyExpr:
		return extractObjectKey(k.Wrapped)
	case *hclsyntax.ScopeTraversalExpr:
		return k.Traversal.RootName()
	case *hclsyntax.LiteralValueExpr:
		if k.Val.Type() == cty.String {
			return k.Val.AsString()
		}
	case *hclsyntax.TemplateWrapExpr:
		return extractObjectKey(k.Wrapped)
	case *hclsyntax.TemplateExpr:
		if k.IsStringLiteral() {
			val, diags := k.Value(nil)
			if !diags.HasErrors() && val.Type() == cty.String {
				return val.AsString()
			}
		}
	}
	return ""
}

// traversalToStrings flattens an HCL scope traversal path into string segments.
func traversalToStrings(t hcl.Traversal) []string {
	var res []string
	for _, step := range t {
		switch s := step.(type) {
		case hcl.TraverseRoot:
			res = append(res, s.Name)
		case hcl.TraverseAttr:
			res = append(res, s.Name)
		case hcl.TraverseIndex:
			if s.Key.Type() == cty.String {
				res = append(res, s.Key.AsString())
			}
		}
	}
	return res
}

func shouldOmitFromSpec(r manifest.Route) bool {
	if r.Hidden {
		return true
	}
	for _, s := range r.Steps {
		name := s.StepName()
		if name == "docs" || name == "spec" {
			return true
		}
	}
	return false
}

func deriveOperationID(method, path string) string {
	parts := strings.FieldsFunc(path, func(r rune) bool {
		return r == '/' || r == '-' || r == '_' || r == '{' || r == '}'
	})

	var b strings.Builder
	b.WriteString(strings.ToLower(method))
	for _, p := range parts {
		if p == "" || strings.EqualFold(p, "api") || strings.HasPrefix(strings.ToLower(p), "v") {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(strings.ToLower(p[1:]))
	}

	res := b.String()
	if res == strings.ToLower(method) {
		return strings.ToLower(method) + "Root"
	}
	return res
}

func problemDetailsContent() openapi3.Content {
	return openapi3.Content{
		problem.ContentType: &openapi3.MediaType{
			Schema: &openapi3.SchemaRef{
				Ref: "#/components/schemas/ProblemDetails",
			},
		},
	}
}

func fieldToSchema(f manifest.Field) *openapi3.Schema {
	s := openapi3.NewSchema()
	s.Type = &openapi3.Types{string(f.Type.Type)}

	if f.Type.IsArray() {
		s.Items = &openapi3.SchemaRef{
			Value: openapi3.NewSchema(),
		}
		if f.Type.ElemType != nil {
			s.Items.Value.Type = &openapi3.Types{string(f.Type.ElemType.Type)}
		}
	}

	if f.Format != "" {
		s.Format = string(f.Format)
	}
	if f.Min != nil {
		s.Min = f.Min
	}
	if f.Max != nil {
		s.Max = f.Max
	}
	if f.MinLength != nil {
		v := uint64(*f.MinLength)
		s.MinLength = v
	}
	if f.MaxLength != nil {
		v := uint64(*f.MaxLength)
		s.MaxLength = &v
	}
	if len(f.Enum) > 0 {
		for _, e := range f.Enum {
			s.Enum = append(s.Enum, e)
		}
	}
	if f.Default != nil {
		s.Default = f.Default
	}
	if f.Description != "" {
		s.Description = f.Description
	}

	return s
}

func fieldToSchemaRef(f manifest.Field) *openapi3.SchemaRef {
	if f.Type.SchemaRef != "" && !f.Type.IsArray() {
		return &openapi3.SchemaRef{
			Ref: "#/components/schemas/" + f.Type.SchemaRef,
		}
	}

	if f.Type.IsArray() {
		s := openapi3.NewArraySchema()
		if f.Description != "" {
			s.Description = f.Description
		}

		if f.Type.ElementSchemaRef() != "" {
			s.Items = &openapi3.SchemaRef{
				Ref: "#/components/schemas/" + f.Type.ElementSchemaRef(),
			}
		} else if f.Type.ElemType != nil {
			s.Items = &openapi3.SchemaRef{
				Value: &openapi3.Schema{
					Type: &openapi3.Types{string(f.Type.ElemType.Type)},
				},
			}
		} else {
			s.Items = &openapi3.SchemaRef{Value: openapi3.NewSchema()}
		}
		return &openapi3.SchemaRef{Value: s}
	}

	return &openapi3.SchemaRef{Value: fieldToSchema(f)}
}

func resolveSchemaRef(spec manifest.TypeSpec) *openapi3.SchemaRef {
	if spec.IsArray() {
		arr := openapi3.NewArraySchema()
		if spec.ElementSchemaRef() != "" {
			arr.Items = &openapi3.SchemaRef{
				Ref: "#/components/schemas/" + spec.ElementSchemaRef(),
			}
		} else if spec.ElemType != nil {
			arr.Items = &openapi3.SchemaRef{
				Value: &openapi3.Schema{Type: &openapi3.Types{string(spec.ElemType.Type)}},
			}
		} else {
			arr.Items = &openapi3.SchemaRef{Value: openapi3.NewSchema()}
		}
		return &openapi3.SchemaRef{Value: arr}
	}

	if spec.SchemaRef != "" {
		return &openapi3.SchemaRef{Ref: "#/components/schemas/" + spec.SchemaRef}
	}

	return &openapi3.SchemaRef{
		Value: &openapi3.Schema{Type: &openapi3.Types{string(spec.Type)}},
	}
}

func buildProblemDetailsSchema() *openapi3.Schema {
	paramItem := openapi3.NewObjectSchema()
	paramItem.WithProperty("name", openapi3.NewStringSchema())
	paramItem.WithProperty("reason", openapi3.NewStringSchema())

	paramsArray := openapi3.NewArraySchema()
	paramsArray.Items = &openapi3.SchemaRef{Value: paramItem}

	s := openapi3.NewObjectSchema()
	s.WithProperty("type", openapi3.NewStringSchema().WithFormat("uri"))
	s.WithProperty("title", openapi3.NewStringSchema())
	s.WithProperty("status", openapi3.NewIntegerSchema())
	s.WithProperty("detail", openapi3.NewStringSchema())
	s.WithProperty("instance", openapi3.NewStringSchema())
	s.WithProperty("invalid_params", paramsArray)
	return s
}
