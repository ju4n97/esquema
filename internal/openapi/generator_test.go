package openapi

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/ju4n97/hclapi/internal/manifest"
)

func parseHCL(t *testing.T, src string) manifest.Expr {
	t.Helper()

	expr, diags := hclsyntax.ParseExpression([]byte(src), "test.hcl", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("parse expr error: %s", diags.Error())
	}

	return manifest.NewExpr(expr, nil)
}

// TestCompile_FullSpecification verifies OpenAPI 3.1 compilation across routes, schemas, and streams.
func TestCompile_FullSpecification(t *testing.T) {
	t.Parallel()

	m := &manifest.Manifest{
		OpenAPI: manifest.OpenAPI{
			Title:       "Storefront API",
			Version:     "1.0.0",
			Description: "API for customer orders and items",
			Servers: []manifest.OpenAPIServer{
				{URL: "https://api.example.com/v1", Description: "Production"},
			},
			Tags: []manifest.OpenAPITag{
				{Name: "inventory", Description: "Items catalog"},
			},
		},
		Schemas: map[string]manifest.Schema{
			"Item": {
				Name:        "Item",
				Description: "Purchasable item",
				Fields: map[string]manifest.Field{
					"id": {
						Name:     "id",
						Type:     manifest.TypeSpec{Type: manifest.TypeInteger},
						Required: true,
					},
					"sku": {
						Name:     "sku",
						Type:     manifest.TypeSpec{Type: manifest.TypeString},
						Required: true,
					},
				},
			},
		},
		Routes: []manifest.Route{
			{
				Method:  "GET",
				Path:    "/items",
				Summary: "List items",
				Tag:     "inventory",
				Request: &manifest.Request{
					Query: map[string]manifest.Field{
						"limit": {
							Name: "limit",
							Type: manifest.TypeSpec{Type: manifest.TypeInteger},
						},
						"tags": {
							Name: "tags",
							Type: manifest.TypeSpec{
								Type:     manifest.TypeArray,
								ElemType: &manifest.TypeSpec{Type: manifest.TypeString},
							},
						},
					},
				},
				Steps: []manifest.Step{
					&manifest.StepRespond{
						Status: 200,
						Schema: &manifest.TypeSpec{
							Type:     manifest.TypeArray,
							ElemType: &manifest.TypeSpec{Type: manifest.TypeObject, SchemaRef: "Item"},
						},
					},
				},
			},
			{
				Method: "GET",
				Path:   "/stream/logs",
				Steps: []manifest.Step{
					&manifest.StepStream{
						Format: "sse",
					},
				},
			},
			{
				Method: "GET",
				Path:   "/docs",
				Steps: []manifest.Step{
					&manifest.StepDocs{Renderer: "scalar"},
				},
			},
			{
				Method: "GET",
				Path:   "/openapi.json",
				Steps: []manifest.Step{
					&manifest.StepSpec{Format: "json"},
				},
			},
			{
				Method: "GET",
				Path:   "/internal/metrics",
				Hidden: true,
				Steps: []manifest.Step{
					&manifest.StepRespond{Status: 200},
				},
			},
		},
	}

	spec, err := Compile(m)
	if err != nil {
		t.Fatalf("Compile() unexpected error: %v", err)
	}

	if spec.JSONETag == "" || spec.YAMLETag == "" {
		t.Fatal("expected precomputed ETags on Spec")
	}

	var rawDoc map[string]any
	if err := json.Unmarshal(spec.JSON, &rawDoc); err != nil {
		t.Fatalf("failed to decode emitted OpenAPI JSON: %v", err)
	}

	t.Run("openapi 3.1 version", func(t *testing.T) {
		t.Parallel()

		if rawDoc["openapi"] != "3.1.0" {
			t.Errorf("openapi = %v, want '3.1.0'", rawDoc["openapi"])
		}
	})

	t.Run("catalog exclusions for docs and spec steps", func(t *testing.T) {
		t.Parallel()

		paths := rawDoc["paths"].(map[string]any)

		if _, exists := paths["/items"]; !exists {
			t.Error("expected '/items' route to be present in paths")
		}
		if _, exists := paths["/docs"]; exists {
			t.Error("expected '/docs' step to be excluded from paths catalog")
		}
		if _, exists := paths["/openapi.json"]; exists {
			t.Error("expected '/openapi.json' step to be excluded from paths catalog")
		}
		if _, exists := paths["/internal/metrics"]; exists {
			t.Error("expected hidden route to be excluded from paths catalog")
		}
	})

	t.Run("array query parameter items typing", func(t *testing.T) {
		t.Parallel()

		paths := rawDoc["paths"].(map[string]any)
		itemsPath := paths["/items"].(map[string]any)
		getOp := itemsPath["get"].(map[string]any)
		params := getOp["parameters"].([]any)

		var tagsParam map[string]any
		for _, p := range params {
			pm := p.(map[string]any)
			if pm["name"] == "tags" {
				tagsParam = pm
				break
			}
		}

		if tagsParam == nil {
			t.Fatal("expected 'tags' parameter in operation")
		}

		schema := tagsParam["schema"].(map[string]any)
		items := schema["items"].(map[string]any)

		switch itemType := items["type"].(type) {
		case string:
			if itemType != "string" {
				t.Fatalf("expected items type = 'string', got %q", itemType)
			}
		case []any:
			if len(itemType) != 1 || itemType[0] != "string" {
				t.Fatalf("expected items type = ['string'], got %v", itemType)
			}
		default:
			t.Fatalf("unexpected items type field representation: %T (%v)", items["type"], items["type"])
		}
	})

	t.Run("yaml output matches json tree", func(t *testing.T) {
		t.Parallel()

		if len(spec.YAML) == 0 {
			t.Fatal("expected non-empty YAML spec bytes")
		}
		if !strings.Contains(string(spec.YAML), "openapi: 3.1.0") {
			t.Errorf("expected YAML to contain openapi version: %s", string(spec.YAML))
		}
	})
}

// TestCompile_ResponseSchemas verifies named schemas, inline schema blocks, and static AST inference from body.
func TestCompile_ResponseSchemas(t *testing.T) {
	t.Parallel()

	userCreateSchema := manifest.Schema{
		Name: "UserCreate",
		Fields: map[string]manifest.Field{
			"email": {Name: "email", Type: manifest.TypeSpec{Type: manifest.TypeString}},
		},
	}

	schemas := map[string]manifest.Schema{
		"UserCreate": userCreateSchema,
	}

	t.Run("compiles explicit inline schema block into typed properties", func(t *testing.T) {
		t.Parallel()

		m := &manifest.Manifest{
			Schemas: schemas,
			Routes: []manifest.Route{
				{
					Method: "POST",
					Path:   "/register",
					Steps: []manifest.Step{
						&manifest.StepRespond{
							Status: 201,
							InlineFields: map[string]manifest.Field{
								"message": {
									Name: "message",
									Type: manifest.TypeSpec{Type: manifest.TypeString},
								},
								"user": {
									Name: "user",
									Type: manifest.TypeSpec{Type: manifest.TypeObject, SchemaRef: "UserCreate"},
								},
							},
						},
					},
				},
			},
		}

		spec, err := Compile(m)
		if err != nil {
			t.Fatalf("Compile() error: %v", err)
		}

		var doc map[string]any
		_ = json.Unmarshal(spec.JSON, &doc)

		paths := doc["paths"].(map[string]any)
		op := paths["/register"].(map[string]any)["post"].(map[string]any)
		resp201 := op["responses"].(map[string]any)["201"].(map[string]any)
		content := resp201["content"].(map[string]any)["application/json"].(map[string]any)
		schema := content["schema"].(map[string]any)

		if schema["type"] != "object" {
			t.Errorf("schema type = %v, want 'object'", schema["type"])
		}

		props := schema["properties"].(map[string]any)
		if props["message"] == nil {
			t.Error("expected 'message' property in inline schema")
		}

		userProp := props["user"].(map[string]any)
		if userProp["$ref"] != "#/components/schemas/UserCreate" {
			t.Errorf("user $ref = %v, want '#/components/schemas/UserCreate'", userProp["$ref"])
		}
	})

	t.Run("statically infers schema from body expression without explicit schema", func(t *testing.T) {
		t.Parallel()

		m := &manifest.Manifest{
			Schemas: schemas,
			Routes: []manifest.Route{
				{
					Method: "POST",
					Path:   "/inferred",
					Request: &manifest.Request{
						Headers: map[string]manifest.Field{
							"x-api-key": {
								Name:   "x-api-key",
								Type:   manifest.TypeSpec{Type: manifest.TypeString},
								Format: manifest.FormatUUID,
							},
						},
						Query: map[string]manifest.Field{
							"source": {
								Name: "source",
								Type: manifest.TypeSpec{Type: manifest.TypeString},
								Enum: []string{"web", "app"},
							},
						},
						BodyRef: "UserCreate",
					},
					Steps: []manifest.Step{
						&manifest.StepRespond{
							Status: 201,
							Body: parseHCL(t, `{
								message    = "Created account",
								user       = ctx.request.body,
								api_key    = ctx.request.headers["x-api-key"],
								source     = ctx.request.query.source,
								created_at = now()
							}`),
						},
					},
				},
			},
		}

		spec, err := Compile(m)
		if err != nil {
			t.Fatalf("Compile() error: %v", err)
		}

		var doc map[string]any
		_ = json.Unmarshal(spec.JSON, &doc)

		paths := doc["paths"].(map[string]any)
		op := paths["/inferred"].(map[string]any)["post"].(map[string]any)
		resp201 := op["responses"].(map[string]any)["201"].(map[string]any)
		content := resp201["content"].(map[string]any)["application/json"].(map[string]any)
		schema := content["schema"].(map[string]any)

		props := schema["properties"].(map[string]any)

		// Literal string inference
		msgProp := props["message"].(map[string]any)
		if msgProp["type"] != "string" {
			t.Errorf("message type = %v, want 'string'", msgProp["type"])
		}

		// Traversal to request.body with schema ref
		userProp := props["user"].(map[string]any)
		if userProp["$ref"] != "#/components/schemas/UserCreate" {
			t.Errorf("user $ref = %v, want '#/components/schemas/UserCreate'", userProp["$ref"])
		}

		// Traversal to headers with format preservation
		apiKeyProp := props["api_key"].(map[string]any)
		if apiKeyProp["type"] != "string" || apiKeyProp["format"] != "uuid" {
			t.Errorf("api_key = %+v, want string with uuid format", apiKeyProp)
		}

		// Traversal to query with enum preservation
		sourceProp := props["source"].(map[string]any)
		if sourceProp["type"] != "string" || len(sourceProp["enum"].([]any)) != 2 {
			t.Errorf("source = %+v, want string with 2 enums", sourceProp)
		}

		// Built-in function call now() inference to date-time
		createdProp := props["created_at"].(map[string]any)
		if createdProp["type"] != "string" || createdProp["format"] != "date-time" {
			t.Errorf("created_at = %+v, want string with date-time format", createdProp)
		}
	})
}
