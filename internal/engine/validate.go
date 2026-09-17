package engine

import (
	"fmt"
	"math"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/ju4n97/hclapi/internal/config"
	"github.com/ju4n97/hclapi/internal/problem"
)

var uuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

// validateIngress recursively validates path, query, header, and body inputs against OpenAPI schemas.
func (e *Engine) validateIngress(ctx *Context, ep config.CompiledEndpoint) *problem.Problem {
	if !ep.Request.HasRules() {
		return nil
	}

	var invalidParams []problem.InvalidParam

	for name, field := range ep.Request.Path {
		raw, exists := ctx.pathParams[name]
		val, _ := raw.(string)
		if !exists || val == "" {
			invalidParams = append(invalidParams, problem.InvalidParam{
				Name:   "path." + name,
				Reason: "path parameter is required",
			})
			continue
		}
		if reason := validateScalarString(val, field); reason != "" {
			invalidParams = append(invalidParams, problem.InvalidParam{
				Name:   "path." + name,
				Reason: reason,
			})
			continue
		}
		if coerced, ok := coerceScalar(val, field.Type); ok {
			ctx.pathParams[name] = coerced
		}
	}

	for name, field := range ep.Request.Query {
		raw, exists := ctx.queryParams[name]
		val, _ := raw.(string)
		if !exists || val == "" {
			if field.Default != nil {
				ctx.queryParams[name] = field.Default
				continue
			}
			if field.Required {
				invalidParams = append(invalidParams, problem.InvalidParam{
					Name:   "query." + name,
					Reason: "query parameter is required",
				})
			}
			continue
		}
		if reason := validateScalarString(val, field); reason != "" {
			invalidParams = append(invalidParams, problem.InvalidParam{
				Name:   "query." + name,
				Reason: reason,
			})
			continue
		}
		if coerced, ok := coerceScalar(val, field.Type); ok {
			ctx.queryParams[name] = coerced
		}
	}

	for name, field := range ep.Request.Headers {
		lookup := strings.ToLower(name)
		val, exists := ctx.headers[lookup]
		if !exists || val == "" {
			if field.Required {
				invalidParams = append(invalidParams, problem.InvalidParam{
					Name:   "header." + name,
					Reason: "header is required",
				})
			}
			continue
		}
		if reason := validateScalarString(val, field); reason != "" {
			invalidParams = append(invalidParams, problem.InvalidParam{
				Name:   "header." + name,
				Reason: reason,
			})
		}
	}

	if len(ep.Request.Body) > 0 {
		if ctx.bodyMalformed {
			p := problem.New(http.StatusBadRequest, "Malformed JSON request body")
			return &p
		}

		bodyMap, ok := ctx.bodyData.(map[string]any)
		if !ok {
			bodyMap = make(map[string]any)
		}

		for name, field := range ep.Request.Body {
			val, exists := bodyMap[name]
			path := "body." + name

			if !exists || val == nil {
				if field.Default != nil {
					bodyMap[name] = field.Default
					continue
				}
				if field.Required {
					invalidParams = append(invalidParams, problem.InvalidParam{
						Name:   path,
						Reason: "field is required",
					})
				}
				continue
			}

			e.validateDeepField(path, val, field, &invalidParams)
		}
		ctx.bodyData = bodyMap
	}

	if len(invalidParams) > 0 {
		p := problem.New(http.StatusUnprocessableEntity, "Request failed schema validation constraints")
		p.InvalidParams = invalidParams
		return &p
	}

	return nil
}

// validateDeepField recursively evaluates fields, nested schemas, and array elements.
func (e *Engine) validateDeepField(path string, val any, field config.Field, invalidParams *[]problem.InvalidParam) {
	// Direct nested schema reference
	if field.SchemaRef != "" && field.Type == config.DataTypeObject {
		nestedSchema, exists := e.cfg.Schemas[field.SchemaRef]
		if !exists {
			return
		}

		m, ok := val.(map[string]any)
		if !ok {
			*invalidParams = append(*invalidParams, problem.InvalidParam{
				Name:   path,
				Reason: "must be an object",
			})
			return
		}

		for fName, f := range nestedSchema.Fields {
			subPath := path + "." + fName
			subVal, subExists := m[fName]

			if !subExists || subVal == nil {
				if f.Default != nil {
					m[fName] = f.Default
					continue
				}
				if f.Required {
					*invalidParams = append(*invalidParams, problem.InvalidParam{
						Name:   subPath,
						Reason: "field is required",
					})
				}
				continue
			}

			e.validateDeepField(subPath, subVal, f, invalidParams)
		}
		return
	}

	// Array of custom schemas or primitives
	if field.Type == config.DataTypeArray {
		list, ok := val.([]any)
		if !ok {
			*invalidParams = append(*invalidParams, problem.InvalidParam{
				Name:   path,
				Reason: "must be an array",
			})
			return
		}

		if field.MinLength != nil && len(list) < *field.MinLength {
			*invalidParams = append(*invalidParams, problem.InvalidParam{
				Name:   path,
				Reason: fmt.Sprintf("array must contain at least %d items", *field.MinLength),
			})
		}
		if field.MaxLength != nil && len(list) > *field.MaxLength {
			*invalidParams = append(*invalidParams, problem.InvalidParam{
				Name:   path,
				Reason: fmt.Sprintf("array must contain at most %d items", *field.MaxLength),
			})
		}

		for i, item := range list {
			itemPath := fmt.Sprintf("%s[%d]", path, i)

			if field.SchemaRef != "" {
				nestedSchema, exists := e.cfg.Schemas[field.SchemaRef]
				if !exists {
					continue
				}

				elemMap, isMap := item.(map[string]any)
				if !isMap {
					*invalidParams = append(*invalidParams, problem.InvalidParam{
						Name:   itemPath,
						Reason: "must be an object",
					})
					continue
				}

				for fName, f := range nestedSchema.Fields {
					subPath := itemPath + "." + fName
					subVal, subExists := elemMap[fName]

					if !subExists || subVal == nil {
						if f.Default != nil {
							elemMap[fName] = f.Default
							continue
						}
						if f.Required {
							*invalidParams = append(*invalidParams, problem.InvalidParam{
								Name:   subPath,
								Reason: "field is required",
							})
						}
						continue
					}

					e.validateDeepField(subPath, subVal, f, invalidParams)
				}
			} else if field.ItemsType != "" {
				elemField := config.Field{Type: field.ItemsType, Format: field.Format}
				if reason := validateTypedValue(item, elemField); reason != "" {
					*invalidParams = append(*invalidParams, problem.InvalidParam{
						Name:   itemPath,
						Reason: reason,
					})
				}
			}
		}
		return
	}

	// Scalar primitives (string, integer, number, boolean)
	if reason := validateTypedValue(val, field); reason != "" {
		*invalidParams = append(*invalidParams, problem.InvalidParam{
			Name:   path,
			Reason: reason,
		})
	}
}

// coerceScalar converts validated scalar strings into typed Go primitives.
func coerceScalar(val string, dt config.DataType) (any, bool) {
	switch dt {
	case config.DataTypeInteger:
		i, err := strconv.ParseInt(val, 10, 64)
		return i, err == nil
	case config.DataTypeNumber:
		f, err := strconv.ParseFloat(val, 64)
		return f, err == nil
	case config.DataTypeBoolean:
		b, err := strconv.ParseBool(val)
		return b, err == nil
	default:
		return val, true
	}
}

// validateTypedValue validates dynamic JSON-decoded data structures against schema rules.
func validateTypedValue(val any, field config.Field) string {
	switch field.Type {
	case config.DataTypeString:
		str, ok := val.(string)
		if !ok {
			return "must be a string"
		}
		return checkStringConstraints(str, field)

	case config.DataTypeInteger:
		var intVal int64
		switch n := val.(type) {
		case float64:
			if n != math.Trunc(n) {
				return "must be an integer"
			}
			intVal = int64(n)
		case int:
			intVal = int64(n)
		case int64:
			intVal = n
		default:
			return "must be an integer"
		}
		if field.Min != nil && float64(intVal) < *field.Min {
			return fmt.Sprintf("must be greater than or equal to %v", *field.Min)
		}
		if field.Max != nil && float64(intVal) > *field.Max {
			return fmt.Sprintf("must be less than or equal to %v", *field.Max)
		}

	case config.DataTypeNumber:
		var num float64
		switch n := val.(type) {
		case float64:
			num = n
		case int:
			num = float64(n)
		case int64:
			num = float64(n)
		default:
			return "must be a number"
		}
		if field.Min != nil && num < *field.Min {
			return fmt.Sprintf("must be greater than or equal to %v", *field.Min)
		}
		if field.Max != nil && num > *field.Max {
			return fmt.Sprintf("must be less than or equal to %v", *field.Max)
		}

	case config.DataTypeBoolean:
		if _, ok := val.(bool); !ok {
			return "must be a boolean"
		}

	case config.DataTypeArray:
		list, ok := val.([]any)
		if !ok {
			return "must be an array"
		}
		if field.MinLength != nil && len(list) < *field.MinLength {
			return fmt.Sprintf("array must contain at least %d items", *field.MinLength)
		}
		if field.MaxLength != nil && len(list) > *field.MaxLength {
			return fmt.Sprintf("array must contain at most %d items", *field.MaxLength)
		}

	case config.DataTypeObject:
		if _, ok := val.(map[string]any); !ok {
			return "must be an object"
		}
	}

	return ""
}

// validateScalarString validates string inputs from path, query, and headers.
func validateScalarString(val string, field config.Field) string {
	switch field.Type {
	case config.DataTypeInteger:
		i, err := strconv.ParseInt(val, 10, 64)
		if err != nil {
			return "must be an integer"
		}
		if field.Min != nil && float64(i) < *field.Min {
			return fmt.Sprintf("must be greater than or equal to %v", *field.Min)
		}
		if field.Max != nil && float64(i) > *field.Max {
			return fmt.Sprintf("must be less than or equal to %v", *field.Max)
		}

	case config.DataTypeNumber:
		f, err := strconv.ParseFloat(val, 64)
		if err != nil {
			return "must be a number"
		}
		if field.Min != nil && f < *field.Min {
			return fmt.Sprintf("must be greater than or equal to %v", *field.Min)
		}
		if field.Max != nil && f > *field.Max {
			return fmt.Sprintf("must be less than or equal to %v", *field.Max)
		}

	case config.DataTypeBoolean:
		if val != "true" && val != "false" {
			return "must be a boolean"
		}

	case config.DataTypeString:
		return checkStringConstraints(val, field)
	}

	return ""
}

// checkStringConstraints verifies length, format, and enum rules for strings.
func checkStringConstraints(val string, field config.Field) string {
	if field.MinLength != nil && len(val) < *field.MinLength {
		return fmt.Sprintf("length must be at least %d characters", *field.MinLength)
	}
	if field.MaxLength != nil && len(val) > *field.MaxLength {
		return fmt.Sprintf("length must be at most %d characters", *field.MaxLength)
	}

	switch field.Format {
	case config.FormatEmail:
		if _, err := mail.ParseAddress(val); err != nil || !strings.Contains(val, "@") {
			return "must be a valid email address"
		}
	case config.FormatUUID:
		if !uuidRegex.MatchString(val) {
			return "must be a valid UUID"
		}
	case config.FormatURI:
		if u, err := url.ParseRequestURI(val); err != nil || u.Scheme == "" {
			return "must be a valid URI"
		}
	case config.FormatDateTime:
		if _, err := time.Parse(time.RFC3339, val); err != nil {
			return "must be an RFC 3339 date-time string"
		}
	case config.FormatDate:
		if _, err := time.Parse("2006-01-02", val); err != nil {
			return "must be a YYYY-MM-DD date string"
		}
	}

	if len(field.Enum) > 0 {
		matched := slices.Contains(field.Enum, val)
		if !matched {
			return fmt.Sprintf("must be one of: [%s]", strings.Join(field.Enum, ", "))
		}
	}

	return ""
}
