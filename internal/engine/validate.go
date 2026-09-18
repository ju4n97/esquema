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

	"github.com/ju4n97/hclapi/internal/manifest"
	"github.com/ju4n97/hclapi/internal/problem"
)

var uuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

// ValidateIngress validates path, query, header, and body parameters against configured request rules.
// Valid scalar path and query parameters are coerced to native Go types (int64, float64, bool) in place.
func ValidateIngress(ctx *Context, rules *manifest.Request, schemas map[string]manifest.Schema) *problem.Problem {
	if rules == nil || !rules.HasRules() {
		return nil
	}

	var invalidParams []problem.InvalidParam

	for name, field := range rules.Path {
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

		if coerced, ok := coerceScalarString(val, field.Type.Type); ok {
			ctx.SetPathParam(name, coerced)
		}
	}

	for name, field := range rules.Query {
		raw, exists := ctx.queryParams[name]
		if !exists || raw == nil {
			if field.Default != nil {
				ctx.SetQueryParam(name, field.Default)
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

		if field.Type.IsArray() {
			var rawSlice []string
			switch v := raw.(type) {
			case string:
				rawSlice = []string{v}
			case []string:
				rawSlice = v
			}

			if field.MinLength != nil && len(rawSlice) < *field.MinLength {
				invalidParams = append(invalidParams, problem.InvalidParam{
					Name:   "query." + name,
					Reason: fmt.Sprintf("array must contain at least %d items", *field.MinLength),
				})
				continue
			}

			coercedSlice := make([]any, len(rawSlice))
			elemType := manifest.TypeString
			if field.Type.ElemType != nil {
				elemType = field.Type.ElemType.Type
			}

			elemField := manifest.Field{
				Type:   manifest.TypeSpec{Type: elemType},
				Format: field.Format,
			}

			hasError := false
			for i, item := range rawSlice {
				if reason := validateScalarString(item, elemField); reason != "" {
					invalidParams = append(invalidParams, problem.InvalidParam{
						Name:   fmt.Sprintf("query.%s[%d]", name, i),
						Reason: reason,
					})
					hasError = true
					break
				}
				if coerced, ok := coerceScalarString(item, elemType); ok {
					coercedSlice[i] = coerced
				} else {
					coercedSlice[i] = item
				}
			}

			if !hasError {
				ctx.SetQueryParam(name, coercedSlice)
			}
			continue
		}

		val, _ := raw.(string)
		if reason := validateScalarString(val, field); reason != "" {
			invalidParams = append(invalidParams, problem.InvalidParam{
				Name:   "query." + name,
				Reason: reason,
			})
			continue
		}

		if coerced, ok := coerceScalarString(val, field.Type.Type); ok {
			ctx.SetQueryParam(name, coerced)
		}
	}

	for name, field := range rules.Headers {
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

	hasBodyRules := len(rules.Body) > 0 || rules.BodyRef != ""
	if hasBodyRules {
		if ctx.BodyMalformed() {
			p := problem.New(http.StatusBadRequest, "Malformed JSON request body")
			return &p
		}

		bodyMap, ok := ctx.Body().(map[string]any)
		if !ok {
			bodyMap = make(map[string]any)
		}

		fields := rules.Body
		if rules.BodyRef != "" {
			if schema, exists := schemas[rules.BodyRef]; exists {
				fields = schema.Fields
			}
		}

		for name, field := range fields {
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

			validateDeepField(path, val, field, schemas, &invalidParams)
		}

		ctx.SetBody(bodyMap)
	}

	if len(invalidParams) > 0 {
		p := problem.New(http.StatusUnprocessableEntity, "Request failed schema validation constraints")
		p.InvalidParams = invalidParams
		return &p
	}

	return nil
}

// validateDeepField recursively validates nested objects, custom schemas, and array items.
func validateDeepField(
	path string,
	val any,
	field manifest.Field,
	schemas map[string]manifest.Schema,
	invalidParams *[]problem.InvalidParam,
) {
	if field.Type.SchemaRef != "" && field.Type.IsObject() {
		nestedSchema, exists := schemas[field.Type.SchemaRef]
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

			validateDeepField(subPath, subVal, f, schemas, invalidParams)
		}
		return
	}

	if field.Type.IsArray() {
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

		elemRef := field.Type.ElementSchemaRef()
		for i, item := range list {
			itemPath := fmt.Sprintf("%s[%d]", path, i)

			if elemRef != "" {
				nestedSchema, exists := schemas[elemRef]
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

					validateDeepField(subPath, subVal, f, schemas, invalidParams)
				}
			} else if field.Type.ElemType != nil {
				elemField := manifest.Field{
					Type:   *field.Type.ElemType,
					Format: field.Format,
				}
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

	if reason := validateTypedValue(val, field); reason != "" {
		*invalidParams = append(*invalidParams, problem.InvalidParam{
			Name:   path,
			Reason: reason,
		})
	}
}

// coerceScalarString converts textual path and query parameters into native int64, float64, or bool primitives.
func coerceScalarString(val string, dt manifest.DataType) (any, bool) {
	switch dt {
	case manifest.TypeInteger:
		i, err := strconv.ParseInt(val, 10, 64)
		return i, err == nil
	case manifest.TypeNumber:
		f, err := strconv.ParseFloat(val, 64)
		return f, err == nil
	case manifest.TypeBoolean:
		b, err := strconv.ParseBool(val)
		return b, err == nil
	default:
		return val, true
	}
}

// validateTypedValue verifies parsed JSON data against bounds, types, and constraints.
func validateTypedValue(val any, field manifest.Field) string {
	switch field.Type.Type {
	case manifest.TypeString:
		str, ok := val.(string)
		if !ok {
			return "must be a string"
		}
		return checkStringConstraints(str, field)

	case manifest.TypeInteger:
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

	case manifest.TypeNumber:
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

	case manifest.TypeBoolean:
		if _, ok := val.(bool); !ok {
			return "must be a boolean"
		}

	case manifest.TypeArray:
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

	case manifest.TypeObject:
		if _, ok := val.(map[string]any); !ok {
			return "must be an object"
		}
	}

	return ""
}

// validateScalarString validates raw string parameters against scalar rules.
func validateScalarString(val string, field manifest.Field) string {
	switch field.Type.Type {
	case manifest.TypeInteger:
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

	case manifest.TypeNumber:
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

	case manifest.TypeBoolean:
		if val != "true" && val != "false" {
			return "must be a boolean"
		}

	case manifest.TypeString:
		return checkStringConstraints(val, field)
	}

	return ""
}

// checkStringConstraints checks length bounds, regex formats, and enum matches.
func checkStringConstraints(val string, field manifest.Field) string {
	if field.MinLength != nil && len(val) < *field.MinLength {
		return fmt.Sprintf("length must be at least %d characters", *field.MinLength)
	}
	if field.MaxLength != nil && len(val) > *field.MaxLength {
		return fmt.Sprintf("length must be at most %d characters", *field.MaxLength)
	}

	switch field.Format {
	case manifest.FormatEmail:
		if _, err := mail.ParseAddress(val); err != nil || !strings.Contains(val, "@") {
			return "must be a valid email address"
		}
	case manifest.FormatUUID:
		if !uuidRegex.MatchString(val) {
			return "must be a valid UUID"
		}
	case manifest.FormatURI:
		if u, err := url.ParseRequestURI(val); err != nil || u.Scheme == "" {
			return "must be a valid URI"
		}
	case manifest.FormatDateTime:
		if _, err := time.Parse(time.RFC3339, val); err != nil {
			return "must be an RFC 3339 date-time string"
		}
	case manifest.FormatDate:
		if _, err := time.Parse("2006-01-02", val); err != nil {
			return "must be a YYYY-MM-DD date string"
		}
	}

	if len(field.Enum) > 0 {
		if !slices.Contains(field.Enum, val) {
			return fmt.Sprintf("must be one of: [%s]", strings.Join(field.Enum, ", "))
		}
	}

	return ""
}
