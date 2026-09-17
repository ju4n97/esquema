package manifest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"gopkg.in/yaml.v3"

	"github.com/ju4n97/hclapi/internal/config"
	"github.com/ju4n97/hclapi/internal/problem"
)

// GenerateOpenAPI compiles a validated OpenAPI 3.1 specification document using kin-openapi.
func GenerateOpenAPI(cfg *config.Config, format string) ([]byte, error) {
	doc := &openapi3.T{
		OpenAPI:    "3.1.0",
		Paths:      openapi3.NewPaths(),
		Components: &openapi3.Components{Schemas: make(openapi3.Schemas)},
	}

	doc.Info = &openapi3.Info{
		Title:       cfg.OpenAPI.Title,
		Version:     cfg.OpenAPI.Version,
		Description: cfg.OpenAPI.Description,
	}
	if cfg.OpenAPI.Contact != nil {
		doc.Info.Contact = &openapi3.Contact{
			Name:  cfg.OpenAPI.Contact.Name,
			Email: cfg.OpenAPI.Contact.Email,
			URL:   cfg.OpenAPI.Contact.URL,
		}
	}
	if cfg.OpenAPI.License != nil {
		doc.Info.License = &openapi3.License{
			Name: cfg.OpenAPI.License.Name,
			URL:  cfg.OpenAPI.License.URL,
		}
	}

	for _, srv := range cfg.OpenAPI.Servers {
		doc.Servers = append(doc.Servers, &openapi3.Server{
			URL:         srv.URL,
			Description: srv.Description,
		})
	}
	if len(doc.Servers) == 0 {
		doc.Servers = append(doc.Servers, &openapi3.Server{
			URL:         "/",
			Description: "Current server origin",
		})
	}

	for _, t := range cfg.OpenAPI.Tags {
		doc.Tags = append(doc.Tags, &openapi3.Tag{
			Name:        t.Name,
			Description: t.Description,
		})
	}

	doc.Components.Schemas["ProblemDetails"] = &openapi3.SchemaRef{
		Value: buildProblemDetailsSchema(),
	}

	for name, s := range cfg.Schemas {
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

	for _, ep := range cfg.Endpoints {
		if shouldOmitFromSpec(ep) {
			continue
		}

		op := openapi3.NewOperation()
		op.Summary = ep.Summary
		if op.Summary == "" {
			op.Summary = fmt.Sprintf("%s %s", ep.Method, ep.Path)
		}
		if ep.Tag != "" {
			op.Tags = []string{ep.Tag}
		}

		if ep.OperationID != "" {
			op.OperationID = ep.OperationID
		} else {
			op.OperationID = deriveOperationID(ep.Method, ep.Path)
		}

		// Parameters: Path, Query, Header
		for name, field := range ep.Request.Path {
			param := openapi3.NewPathParameter(name).WithSchema(fieldToSchema(field))
			param.Required = true
			op.AddParameter(param)
		}
		for name, field := range ep.Request.Query {
			param := openapi3.NewQueryParameter(name).WithSchema(fieldToSchema(field))
			param.Required = field.Required
			op.AddParameter(param)
		}
		for name, field := range ep.Request.Headers {
			param := openapi3.NewHeaderParameter(name).WithSchema(fieldToSchema(field))
			param.Required = field.Required
			op.AddParameter(param)
		}

		// Request Body
		if ep.Request.BodyRef != "" {
			op.RequestBody = &openapi3.RequestBodyRef{
				Value: openapi3.NewRequestBody().
					WithJSONSchemaRef(&openapi3.SchemaRef{Ref: "#/components/schemas/" + ep.Request.BodyRef}).
					WithRequired(true),
			}
		} else if len(ep.Request.Body) > 0 {
			bodyObj := openapi3.NewObjectSchema()
			for fName, field := range ep.Request.Body {
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

		hasSuccess := false
		for _, s := range ep.Pipeline {
			if s.Type == config.StepTypeRespond && s.Respond != nil {
				status := s.Respond.Status
				if status == 0 {
					status = http.StatusOK
				}
				resp := openapi3.NewResponse().WithDescription(http.StatusText(status))

				if s.Respond.SchemaRef != "" {
					ref := resolveSchemaRef(s.Respond.SchemaRef)
					resp.WithJSONSchemaRef(ref)
				} else if s.Respond.BodyExpr != nil && status != http.StatusNoContent {
					// Prevent client SDKs from generating "void" return types for JSON bodies
					resp.Content = openapi3.NewContentWithJSONSchemaRef(&openapi3.SchemaRef{
						Value: openapi3.NewObjectSchema(),
					})
				}

				op.AddResponse(status, resp)
				if status < 400 {
					hasSuccess = true
				}
			}

			if s.Type == config.StepTypeSQL && s.SQL != nil {
				for _, c := range s.SQL.Catches {
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

		if !hasSuccess {
			op.AddResponse(http.StatusOK, openapi3.NewResponse().
				WithDescription("OK").
				WithContent(openapi3.NewContentWithJSONSchemaRef(&openapi3.SchemaRef{
					Value: openapi3.NewObjectSchema(),
				})))
		}

		// Document RFC 9457 application/problem+json media type for errors
		if ep.Request.HasRules() {
			op.AddResponse(http.StatusUnprocessableEntity, openapi3.NewResponse().
				WithDescription("Unprocessable Entity").
				WithContent(problemDetailsContent()))
		}

		// Default 500 Internal Server Error
		op.AddResponse(http.StatusInternalServerError, openapi3.NewResponse().
			WithDescription("Internal Server Error").
			WithContent(problemDetailsContent()))

		pathItem := doc.Paths.Find(ep.Path)
		if pathItem == nil {
			pathItem = &openapi3.PathItem{}
			doc.Paths.Set(ep.Path, pathItem)
		}
		pathItem.SetOperation(ep.Method, op)
	}

	loader := openapi3.NewLoader()
	if err := loader.ResolveRefsIn(doc, nil); err != nil {
		return nil, fmt.Errorf("resolve openapi refs: %w", err)
	}

	if err := doc.Validate(context.Background()); err != nil {
		return nil, fmt.Errorf("openapi spec validation failed: %w", err)
	}

	if strings.EqualFold(format, string(config.SpecFormatYAML)) {
		jsonBytes, err := json.Marshal(doc)
		if err != nil {
			return nil, err
		}
		var parsed any
		_ = json.Unmarshal(jsonBytes, &parsed)
		return yaml.Marshal(parsed)
	}

	return json.MarshalIndent(doc, "", "  ")
}

// deriveOperationID creates a camelCase identifier like "postUsers" or "getTodosById".
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

// problemDetailsContent returns the RFC 9457 application/problem+json media type representation.
func problemDetailsContent() openapi3.Content {
	return openapi3.Content{
		problem.ContentType: &openapi3.MediaType{
			Schema: &openapi3.SchemaRef{
				Ref: "#/components/schemas/ProblemDetails",
			},
		},
	}
}

// shouldOmitFromSpec determines whether an endpoint represents documentation or spec tooling.
func shouldOmitFromSpec(ep config.CompiledEndpoint) bool {
	if ep.Hidden {
		return true
	}
	for _, s := range ep.Pipeline {
		if s.Type == config.StepTypeDocs || s.Type == config.StepTypeSpec {
			return true
		}
	}
	return false
}

// fieldToSchema converts a Field strictly adhering to OpenAPI 3.1 types and formats.
func fieldToSchema(f config.Field) *openapi3.Schema {
	s := openapi3.NewSchema()
	s.Type = &openapi3.Types{string(f.Type)}

	// OpenAPI 3.1 requirement: Array types must define items
	if f.Type == config.DataTypeArray {
		s.Items = &openapi3.SchemaRef{
			Value: openapi3.NewSchema(),
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

// fieldToSchemaRef wraps a Field schema inside an OpenAPI SchemaRef, resolving schema $refs and array items.
func fieldToSchemaRef(f config.Field) *openapi3.SchemaRef {
	// Direct custom schema reference (e.g. type = "User")
	if f.SchemaRef != "" && f.Type == config.DataTypeObject {
		return &openapi3.SchemaRef{
			Ref: "#/components/schemas/" + f.SchemaRef,
		}
	}

	// Array of custom schemas or array of primitives (e.g. type = "[]User")
	if f.Type == config.DataTypeArray {
		s := openapi3.NewArraySchema()
		if f.Description != "" {
			s.Description = f.Description
		}

		if f.SchemaRef != "" {
			s.Items = &openapi3.SchemaRef{
				Ref: "#/components/schemas/" + f.SchemaRef,
			}
		} else if f.ItemsType != "" {
			s.Items = &openapi3.SchemaRef{
				Value: &openapi3.Schema{
					Type: &openapi3.Types{string(f.ItemsType)},
				},
			}
		} else {
			s.Items = &openapi3.SchemaRef{
				Value: openapi3.NewSchema(),
			}
		}
		return &openapi3.SchemaRef{Value: s}
	}

	// Flat primitive scalar
	return &openapi3.SchemaRef{Value: fieldToSchema(f)}
}

// resolveSchemaRef constructs schema component references supporting array notations.
func resolveSchemaRef(ref string) *openapi3.SchemaRef {
	if after, ok := strings.CutPrefix(ref, "[]"); ok {
		clean := after
		arraySchema := openapi3.NewArraySchema()
		arraySchema.Items = &openapi3.SchemaRef{
			Ref: "#/components/schemas/" + clean,
		}
		return &openapi3.SchemaRef{
			Value: arraySchema,
		}
	}
	return &openapi3.SchemaRef{Ref: "#/components/schemas/" + ref}
}

// buildProblemDetailsSchema defines the standard RFC 9457 error model for documentation.
func buildProblemDetailsSchema() *openapi3.Schema {
	invalidParamItem := openapi3.NewObjectSchema()
	invalidParamItem.WithProperty("name", openapi3.NewStringSchema())
	invalidParamItem.WithProperty("reason", openapi3.NewStringSchema())

	invalidParamsArray := openapi3.NewArraySchema()
	invalidParamsArray.Items = &openapi3.SchemaRef{Value: invalidParamItem}

	s := openapi3.NewObjectSchema()
	s.WithProperty("type", openapi3.NewStringSchema().WithFormat("uri"))
	s.WithProperty("title", openapi3.NewStringSchema())
	s.WithProperty("status", openapi3.NewIntegerSchema())
	s.WithProperty("detail", openapi3.NewStringSchema())
	s.WithProperty("instance", openapi3.NewStringSchema())
	s.WithProperty("invalid_params", invalidParamsArray)
	return s
}
