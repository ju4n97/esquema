package manifest_test

import (
	"encoding/json"
	"testing"

	"github.com/ju4n97/hclapi/internal/config"
	"github.com/ju4n97/hclapi/internal/manifest"
)

// TestGenerateOpenAPI verifies OpenAPI 3.1 specification compilation, validation, and route filtering.
func TestGenerateOpenAPI(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		OpenAPI: config.OpenAPIMetadata{
			Title:   "Inventory Service",
			Version: "1.0.0",
		},
		Schemas: map[string]config.Schema{
			"Product": {
				Name: "Product",
				Fields: map[string]config.Field{
					"id": {
						Name:     "id",
						Type:     config.DataTypeInteger,
						Required: true,
					},
					"sku": {
						Name:     "sku",
						Type:     config.DataTypeString,
						Required: true,
					},
				},
			},
		},
		Endpoints: []config.CompiledEndpoint{
			{
				Method:       "GET",
				Path:         "/products/{id}",
				RoutePattern: "GET /products/{id}",
				Summary:      "Fetch product by identifier",
				Tag:          "products",
				Request: config.RequestRules{
					Path: map[string]config.Field{
						"id": {
							Name:     "id",
							Type:     config.DataTypeInteger,
							Required: true,
						},
					},
				},
				Pipeline: []config.Step{
					{
						Type: config.StepTypeRespond,
						Respond: &config.RespondStep{
							Status:    200,
							SchemaRef: "Product",
						},
					},
				},
			},
			{
				Method:       "GET",
				Path:         "/docs",
				RoutePattern: "GET /docs",
				Pipeline: []config.Step{
					{
						Type: config.StepTypeDocs,
						Docs: &config.DocsStep{Renderer: config.DocsRendererScalar},
					},
				},
			},
			{
				Method:       "GET",
				Path:         "/internal/metrics",
				RoutePattern: "GET /internal/metrics",
				Hidden:       true,
				Pipeline: []config.Step{
					{
						Type:    config.StepTypeRespond,
						Respond: &config.RespondStep{Status: 200},
					},
				},
			},
		},
	}

	rawJSON, err := manifest.GenerateOpenAPI(cfg, string(config.SpecFormatJSON))
	if err != nil {
		t.Fatalf("GenerateOpenAPI failed: %v", err)
	}

	var doc map[string]any
	err = json.Unmarshal(rawJSON, &doc)
	if err != nil {
		t.Fatalf("failed to decode emitted OpenAPI specification: %v", err)
	}

	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi version = %v; want '3.1.0'", doc["openapi"])
	}

	paths, ok := doc["paths"].(map[string]any)
	if !ok {
		t.Fatalf("paths is not an object: %T", doc["paths"])
	}

	if _, exists := paths["/products/{id}"]; !exists {
		t.Error("expected '/products/{id}' in paths catalog")
	}
	if _, exists := paths["/docs"]; exists {
		t.Error("expected documentation route '/docs' to be excluded from paths")
	}
	if _, exists := paths["/internal/metrics"]; exists {
		t.Error("expected hidden route '/internal/metrics' to be excluded from paths")
	}

	components, ok := doc["components"].(map[string]any)
	if !ok {
		t.Fatalf("components is not an object: %T", doc["components"])
	}

	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatalf("schemas is not an object: %T", components["schemas"])
	}

	if _, exists := schemas["Product"]; !exists {
		t.Error("expected 'Product' schema under components.schemas")
	}
	if _, exists := schemas["ProblemDetails"]; !exists {
		t.Error("expected standard 'ProblemDetails' schema under components.schemas")
	}
}

// TestGenerateOpenAPI_NestedSchemas verifies nested schema $refs, array references, and descriptions.
func TestGenerateOpenAPI_NestedSchemas(t *testing.T) {
	t.Parallel()

	m := `
openapi {
  title   = "Radiology API"
  version = "1.0.0"
}

schema "Patient" {
  description = "Clinical patient profile"
  field "id" {
    type     = "integer"
    required = true
  }
  field "name" {
    type     = "string"
    required = true
  }
}

schema "Attachment" {
  description = "Document attachment link"
  field "id" {
    type     = "integer"
    required = true
  }
  field "url" {
    type   = "string"
    format = "uri"
  }
}

schema "Study" {
  description = "Examination study"
  field "id" {
    type     = "integer"
    required = true
  }
  field "patient" {
    type     = "Patient"
    required = true
  }
  field "attachments" {
    type = "[]Attachment"
  }
  field "tags" {
    type = "[]string"
  }
}

route "POST /studies" {
  request {
    body = Study
  }
  respond {
    status = 201
    schema = Study
  }
}
`

	cfg, err := manifest.Parse(m)
	if err != nil {
		t.Fatalf("parse failed: %v", err)
	}

	rawJSON, err := manifest.GenerateOpenAPI(cfg, string(config.SpecFormatJSON))
	if err != nil {
		t.Fatalf("GenerateOpenAPI failed: %v", err)
	}

	var doc map[string]any
	if err := json.Unmarshal(rawJSON, &doc); err != nil {
		t.Fatalf("failed to decode OpenAPI JSON: %v", err)
	}

	components := doc["components"].(map[string]any)
	schemas := components["schemas"].(map[string]any)

	// Verify schema descriptions
	patientSchema := schemas["Patient"].(map[string]any)
	if patientSchema["description"] != "Clinical patient profile" {
		t.Errorf("patient description = %v; want 'Clinical patient profile'", patientSchema["description"])
	}

	// Verify nested object $ref
	studySchema := schemas["Study"].(map[string]any)
	studyProps := studySchema["properties"].(map[string]any)

	patientProp := studyProps["patient"].(map[string]any)
	if patientProp["$ref"] != "#/components/schemas/Patient" {
		t.Errorf("patient $ref = %v; want '#/components/schemas/Patient'", patientProp["$ref"])
	}

	// Verify array of custom schemas $ref
	attachmentsProp := studyProps["attachments"].(map[string]any)
	if attachmentsProp["type"] != "array" {
		t.Errorf("attachments type = %v; want 'array'", attachmentsProp["type"])
	}
	attachItems := attachmentsProp["items"].(map[string]any)
	if attachItems["$ref"] != "#/components/schemas/Attachment" {
		t.Errorf("attachments items $ref = %v; want '#/components/schemas/Attachment'", attachItems["$ref"])
	}

	// Verify array of primitives
	tagsProp := studyProps["tags"].(map[string]any)
	if tagsProp["type"] != "array" {
		t.Errorf("tags type = %v; want 'array'", tagsProp["type"])
	}
	tagItems := tagsProp["items"].(map[string]any)
	if tagItems["type"] != "string" {
		t.Errorf("tags items type = %v; want 'string'", tagItems["type"])
	}
}
