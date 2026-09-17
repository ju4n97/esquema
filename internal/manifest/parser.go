package manifest

import (
	"fmt"
	"maps"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"

	"github.com/ju4n97/hclapi/internal/config"
	"github.com/ju4n97/hclapi/internal/ctyconv"
	"github.com/ju4n97/hclapi/internal/scalar"
)

// Load discovers, parses, validates, and compiles HCL manifests into a verified Config.
func Load(patterns ...string) (*config.Config, error) {
	files, err := DiscoverFiles(patterns...)
	if err != nil {
		return nil, fmt.Errorf("discover files: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no manifest files matched patterns: %v", patterns)
	}

	parser := hclparse.NewParser()
	var hclFiles []*hcl.File

	for _, file := range files {
		f, diags := parser.ParseHCLFile(file)
		if diags.HasErrors() {
			return nil, fmt.Errorf("parse error in %s:\n%s", file, diags.Error())
		}
		hclFiles = append(hclFiles, f)
	}

	return compile(hclFiles)
}

// Parse compiles an in-memory HCL manifest string into a verified Config.
func Parse(source string) (*config.Config, error) {
	parser := hclparse.NewParser()
	f, diags := parser.ParseHCL([]byte(source), "manifest.hcl")
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse error:\n%s", diags.Error())
	}
	return compile([]*hcl.File{f})
}

// compile processes parsed HCL files through structural validation and assembly passes.
func compile(files []*hcl.File) (*config.Config, error) {
	cfg := &config.Config{
		Server: config.Server{
			Host:         "127.0.0.1",
			Port:         8080,
			ReadTimeout:  scalar.Duration(15 * time.Second),
			WriteTimeout: scalar.Duration(15 * time.Second),
			MaxBodySize:  10 * scalar.MB,
		},
		OpenAPI: config.OpenAPIMetadata{
			Title:   "API Documentation",
			Version: "1.0.0",
		},
		Connections: make(map[string]config.Connection),
		Schemas:     make(map[string]config.Schema),
	}

	envMap := make(map[string]string)
	for _, e := range os.Environ() {
		if k, v, ok := strings.Cut(e, "="); ok {
			envMap[k] = v
		}
	}
	evalCtx := &hcl.EvalContext{
		Variables: map[string]cty.Value{
			"env": ctyconv.ToCty(envMap),
		},
		Functions: ctyconv.BuiltinFunctions(),
	}

	// Parse top-level infrastructure declarations
	for _, file := range files {
		content, _, diags := file.Body.PartialContent(rootSchema)
		if diags.HasErrors() {
			return nil, diags
		}

		for _, block := range content.Blocks {
			switch block.Type {
			case "server":
				var raw struct {
					Host         string `hcl:"host,optional"`
					Port         int    `hcl:"port,optional"`
					ReadTimeout  string `hcl:"read_timeout,optional"`
					WriteTimeout string `hcl:"write_timeout,optional"`
					MaxBodySize  string `hcl:"max_body_size,optional"`
				}
				if diags := gohcl.DecodeBody(block.Body, evalCtx, &raw); diags.HasErrors() {
					return nil, diags
				}
				if raw.Host != "" {
					cfg.Server.Host = raw.Host
				}
				if raw.Port != 0 {
					cfg.Server.Port = raw.Port
				}
				if raw.ReadTimeout != "" {
					d, err := scalar.ParseDuration(raw.ReadTimeout)
					if err != nil {
						return nil, fmt.Errorf("server.read_timeout: %w", err)
					}
					cfg.Server.ReadTimeout = d
				}
				if raw.WriteTimeout != "" {
					d, err := scalar.ParseDuration(raw.WriteTimeout)
					if err != nil {
						return nil, fmt.Errorf("server.write_timeout: %w", err)
					}
					cfg.Server.WriteTimeout = d
				}
				if raw.MaxBodySize != "" {
					b, err := scalar.ParseByteSize(raw.MaxBodySize)
					if err != nil {
						return nil, fmt.Errorf("server.max_body_size: %w", err)
					}
					cfg.Server.MaxBodySize = b
				}

			case "openapi":
				if err := decodeOpenAPIMetadata(block.Body, evalCtx, &cfg.OpenAPI); err != nil {
					return nil, err
				}

			case "telemetry":
				if diags := gohcl.DecodeBody(block.Body, evalCtx, &cfg.Telemetry); diags.HasErrors() {
					return nil, diags
				}

			case "connection":
				connType, err := config.ParseConnectionType(block.Labels[0])
				if err != nil {
					return nil, err
				}
				connName := block.Labels[1]
				conn, err := decodeConnection(connType, connName, block.Body, evalCtx)
				if err != nil {
					return nil, err
				}
				cfg.Connections[connName] = conn

			case "schema":
				schemaName := block.Labels[0]
				schema, err := decodeSchema(schemaName, block.Body, evalCtx)
				if err != nil {
					return nil, err
				}
				cfg.Schemas[schemaName] = schema
			}
		}
	}

	// Parse route blocks and sequential pipeline steps
	for _, file := range files {
		content, _, diags := file.Body.PartialContent(rootSchema)
		if diags.HasErrors() {
			return nil, diags
		}

		for _, block := range content.Blocks {
			if block.Type != "route" {
				continue
			}

			endpointLabel := block.Labels[0]
			method, path, ok := strings.Cut(strings.TrimSpace(endpointLabel), " ")
			if !ok {
				return nil, fmt.Errorf("invalid route label %q: expected 'METHOD /path' (e.g. 'GET /users')", endpointLabel)
			}
			method = strings.ToUpper(strings.TrimSpace(method))
			path = strings.TrimSpace(path)

			ep, err := decodeRoute(method, path, block.Body, evalCtx, cfg.Schemas)
			if err != nil {
				return nil, fmt.Errorf("route %s %s: %w", method, path, err)
			}
			cfg.Endpoints = append(cfg.Endpoints, ep)
		}
	}

	if err := validateIntegrity(cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

// decodeConnection builds a typed Connection struct directly using labels and body attributes.
func decodeConnection(connType config.ConnectionType, name string, body hcl.Body, ctx *hcl.EvalContext) (config.Connection, error) {
	conn := config.Connection{Type: connType, Name: name}

	switch connType {
	case config.ConnectionTypeSQL:
		var raw struct {
			Engine string       `hcl:"engine"`
			Source string       `hcl:"source"`
			Pool   *config.Pool `hcl:"pool,block"`
		}
		if diags := gohcl.DecodeBody(body, ctx, &raw); diags.HasErrors() {
			return conn, diags
		}

		engine, err := config.ParseSQLEngine(raw.Engine)
		if err != nil {
			return conn, fmt.Errorf("connection %q: %w", name, err)
		}

		conn.Engine = string(engine)
		conn.Source = raw.Source
		conn.Pool = raw.Pool

	case config.ConnectionTypeValkey:
		var raw struct {
			Engine string `hcl:"engine,optional"`
			URL    string `hcl:"url"`
		}
		if diags := gohcl.DecodeBody(body, ctx, &raw); diags.HasErrors() {
			return conn, diags
		}
		conn.Engine = "valkey"
		conn.URL = raw.URL
	}

	return conn, nil
}

// decodeSchema extracts reusable model definitions directly into config.Schema.
func decodeSchema(name string, body hcl.Body, ctx *hcl.EvalContext) (config.Schema, error) {
	schema := config.Schema{Name: name, Fields: make(map[string]config.Field)}

	content, diags := body.Content(schemaBlockSchema)
	if diags.HasErrors() {
		return schema, diags
	}

	attrs, _ := body.JustAttributes()
	if attr, ok := attrs["description"]; ok {
		val, _ := attr.Expr.Value(ctx)
		schema.Description = val.AsString()
	}

	for _, block := range content.Blocks {
		if block.Type == "field" {
			f, err := decodeField(block.Labels[0], block.Body, ctx)
			if err != nil {
				return schema, err
			}
			schema.Fields[f.Name] = f
		}
	}

	return schema, nil
}

// decodeRoute compiles an endpoint and preserves exact step sequence order.
func decodeRoute(
	method, path string,
	body hcl.Body,
	ctx *hcl.EvalContext,
	schemas map[string]config.Schema,
) (config.CompiledEndpoint, error) {
	pattern := fmt.Sprintf("%s %s", method, path)
	ep := config.CompiledEndpoint{
		Method:       method,
		Path:         path,
		RoutePattern: pattern,
		Request: config.RequestRules{
			Path:    make(map[string]config.Field),
			Query:   make(map[string]config.Field),
			Headers: make(map[string]config.Field),
			Body:    make(map[string]config.Field),
		},
	}

	content, diags := body.Content(routeBodySchema)
	if diags.HasErrors() {
		return ep, diags
	}

	attrs, _ := body.JustAttributes()
	if attr, ok := attrs["operation_id"]; ok {
		val, _ := attr.Expr.Value(ctx)
		ep.OperationID = val.AsString()
	}
	if attr, ok := attrs["summary"]; ok {
		val, _ := attr.Expr.Value(ctx)
		ep.Summary = val.AsString()
	}
	if attr, ok := attrs["tag"]; ok {
		val, _ := attr.Expr.Value(ctx)
		ep.Tag = val.AsString()
	} else {
		ep.Tag = deriveTag(path)
	}
	if attr, ok := attrs["hidden"]; ok {
		val, _ := attr.Expr.Value(ctx)
		ep.Hidden = val.True()
	}

	for _, block := range content.Blocks {
		switch block.Type {
		case "request":
			if err := decodeRequestBlock(block.Body, ctx, &ep.Request, schemas); err != nil {
				return ep, err
			}

		case "step":
			stepType, err := config.ParseStepType(block.Labels[0])
			if err != nil {
				return ep, err
			}
			stepName := ""
			if len(block.Labels) > 1 {
				stepName = block.Labels[1]
			}
			step, err := decodeStep(stepType, stepName, block.Body, ctx)
			if err != nil {
				return ep, err
			}
			ep.Pipeline = append(ep.Pipeline, step)

		case "respond":
			step, err := decodeStep(config.StepTypeRespond, "", block.Body, ctx)
			if err != nil {
				return ep, err
			}
			ep.Pipeline = append(ep.Pipeline, step)

		case "docs":
			step, err := decodeStep(config.StepTypeDocs, "", block.Body, ctx)
			if err != nil {
				return ep, err
			}
			ep.Pipeline = append(ep.Pipeline, step)

		case "spec":
			step, err := decodeStep(config.StepTypeSpec, "", block.Body, ctx)
			if err != nil {
				return ep, err
			}
			ep.Pipeline = append(ep.Pipeline, step)
		}
	}

	return ep, nil
}

// decodeStep initializes a Step directly into config types, ensuring WhenExpr is nil unless explicitly declared.
func decodeStep(stepType config.StepType, name string, body hcl.Body, ctx *hcl.EvalContext) (config.Step, error) {
	step := config.Step{Type: stepType, Name: name}

	attrs, _ := body.JustAttributes()
	if whenAttr, ok := attrs["when"]; ok {
		step.WhenExpr = whenAttr.Expr
	}

	switch stepType {
	case config.StepTypeSQL:
		s := &config.SQLStep{}
		if attr, ok := attrs["connection"]; ok {
			s.Connection = resolveIdentifier(attr.Expr)
		}
		if attr, ok := attrs["query"]; ok {
			s.Query = exprToString(attr.Expr)
		}
		if attr, ok := attrs["args"]; ok {
			s.ArgsExpr = attr.Expr
		}

		content, _ := body.Content(sqlStepSchema)
		for _, b := range content.Blocks {
			if b.Type == "catch" {
				cAttrs, _ := b.Body.JustAttributes()
				catch := config.SQLCatch{Code: b.Labels[0], Status: 400}
				if statusAttr, ok := cAttrs["status"]; ok {
					catch.Status = exprToInt(statusAttr.Expr, 400)
				}
				if bodyAttr, ok := cAttrs["body"]; ok {
					catch.BodyExpr = bodyAttr.Expr
				}
				s.Catches = append(s.Catches, catch)
			}
		}
		step.SQL = s

	case config.StepTypeValkey:
		s := &config.ValkeyStep{}
		if attr, ok := attrs["connection"]; ok {
			s.Connection = resolveIdentifier(attr.Expr)
		}
		if attr, ok := attrs["op"]; ok {
			op, err := config.ParseValkeyOp(exprToString(attr.Expr))
			if err != nil {
				return step, err
			}
			s.Op = op
		}
		if attr, ok := attrs["key"]; ok {
			s.KeyExpr = attr.Expr
		}
		if attr, ok := attrs["value"]; ok {
			s.ValExpr = attr.Expr
		}
		if attr, ok := attrs["ttl"]; ok {
			durStr := exprToString(attr.Expr)
			s.TTL, _ = time.ParseDuration(durStr)
		}
		step.Valkey = s

	case config.StepTypeRespond:
		s := &config.RespondStep{Status: 200, Headers: make(map[string]string)}
		if attr, ok := attrs["status"]; ok {
			s.Status = exprToInt(attr.Expr, 200)
		}
		if attr, ok := attrs["schema"]; ok {
			s.SchemaRef = resolveIdentifier(attr.Expr)
		}
		if attr, ok := attrs["body"]; ok {
			s.BodyExpr = attr.Expr
		}
		if attr, ok := attrs["headers"]; ok {
			s.Headers = decodeHeaders(attr.Expr, ctx)
		}
		step.Respond = s

	case config.StepTypeDocs:
		s := &config.DocsStep{Renderer: config.DocsRendererScalar, SpecURL: "/openapi.json"}
		if attr, ok := attrs["renderer"]; ok {
			r, err := config.ParseDocsRenderer(exprToString(attr.Expr))
			if err != nil {
				return step, err
			}
			s.Renderer = r
		}
		if attr, ok := attrs["spec_url"]; ok {
			s.SpecURL = exprToString(attr.Expr)
		}
		if attr, ok := attrs["title"]; ok {
			s.Title = exprToString(attr.Expr)
		}
		if attr, ok := attrs["template"]; ok {
			s.Template = exprToString(attr.Expr)
		}
		step.Docs = s

	case config.StepTypeSpec:
		s := &config.SpecStep{Format: config.SpecFormatJSON}
		if attr, ok := attrs["format"]; ok {
			f, err := config.ParseSpecFormat(exprToString(attr.Expr))
			if err != nil {
				return step, err
			}
			s.Format = f
		}
		step.Spec = s

	case config.StepTypeGo:
		s := &config.GoStep{}
		if attr, ok := attrs["use"]; ok {
			s.Use = exprToString(attr.Expr)
		}
		if attr, ok := attrs["args"]; ok {
			s.ArgsExpr = attr.Expr
		}
		step.Go = s

	case config.StepTypeStarlark:
		s := &config.StarlarkStep{}
		if attr, ok := attrs["source"]; ok {
			s.Source = exprToString(attr.Expr)
		}
		step.Starlark = s

	case config.StepTypeHTTP:
		s := &config.HTTPStep{
			Method:  "GET",
			Headers: make(map[string]string),
			Timeout: scalar.Duration(15 * time.Second),
		}
		if attr, ok := attrs["method"]; ok {
			s.Method = exprToString(attr.Expr)
		}
		if attr, ok := attrs["url"]; ok {
			s.URLExpr = attr.Expr
		}
		if attr, ok := attrs["timeout"]; ok {
			durStr := exprToString(attr.Expr)
			if d, err := scalar.ParseDuration(durStr); err == nil {
				s.Timeout = d
			}
		}
		if attr, ok := attrs["body"]; ok {
			s.BodyExpr = attr.Expr
		}
		if attr, ok := attrs["headers"]; ok {
			s.Headers = decodeHeaders(attr.Expr, ctx)
		}
		step.HTTP = s
	}

	return step, nil
}

// decodeHeaders evaluates a map or object expression into a standard map[string]string.
func decodeHeaders(expr hcl.Expression, ctx *hcl.EvalContext) map[string]string {
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
				headers[k.AsString()] = fmt.Sprintf("%v", ctyconv.ToNative(v))
			}
		}
	}
	return headers
}

// decodeRequestBlock populates ingress validation rules directly into config.RequestRules.
func decodeRequestBlock(body hcl.Body, ctx *hcl.EvalContext, req *config.RequestRules, schemas map[string]config.Schema) error {
	attrs, _ := body.JustAttributes()

	// Support schema references across all coordinates
	if attr, ok := attrs["body"]; ok {
		req.BodyRef = resolveIdentifier(attr.Expr)
		if s, ok := schemas[req.BodyRef]; ok {
			maps.Copy(req.Body, s.Fields)
		}
	}
	if attr, ok := attrs["headers"]; ok {
		ref := resolveIdentifier(attr.Expr)
		if s, ok := schemas[ref]; ok {
			maps.Copy(req.Headers, s.Fields)
		}
	}
	if attr, ok := attrs["query"]; ok {
		ref := resolveIdentifier(attr.Expr)
		if s, ok := schemas[ref]; ok {
			maps.Copy(req.Query, s.Fields)
		}
	}
	if attr, ok := attrs["path"]; ok {
		ref := resolveIdentifier(attr.Expr)
		if s, ok := schemas[ref]; ok {
			maps.Copy(req.Path, s.Fields)
		}
	}

	content, _ := body.Content(requestBlockSchema)
	for _, block := range content.Blocks {
		var name string
		if len(block.Labels) > 0 {
			name = block.Labels[0]
		}

		switch block.Type {
		case "path":
			f, err := decodeField(name, block.Body, ctx)
			if err != nil {
				return err
			}
			req.Path[name] = f
		case "query":
			f, err := decodeField(name, block.Body, ctx)
			if err != nil {
				return err
			}
			req.Query[name] = f
		case "header":
			f, err := decodeField(name, block.Body, ctx)
			if err != nil {
				return err
			}
			req.Headers[name] = f
		case "body":
			bodyContent, _ := block.Body.Content(fieldsContainerSchema)
			for _, b := range bodyContent.Blocks {
				if b.Type == "field" {
					f, err := decodeField(b.Labels[0], b.Body, ctx)
					if err != nil {
						return err
					}
					req.Body[f.Name] = f
				}
			}
		}
	}
	return nil
}

// decodeField decodes a single validation property directly into config.Field.
func decodeField(name string, body hcl.Body, ctx *hcl.EvalContext) (config.Field, error) {
	f := config.Field{Name: name}
	attrs, _ := body.JustAttributes()

	if attr, ok := attrs["type"]; ok {
		val, _ := attr.Expr.Value(ctx)
		dataType, schemaRef, itemsType, err := config.ParseFieldType(val.AsString())
		if err != nil {
			return f, fmt.Errorf("field %q: %w", name, err)
		}
		f.Type = dataType
		f.SchemaRef = schemaRef
		f.ItemsType = itemsType
	} else {
		return f, fmt.Errorf("field %q is missing required attribute 'type'", name)
	}

	if attr, ok := attrs["format"]; ok {
		val, _ := attr.Expr.Value(ctx)
		formatStr := val.AsString()
		if err := config.ValidateFormat(formatStr); err != nil {
			return f, fmt.Errorf("field %q: %w", name, err)
		}
		f.Format = config.Format(formatStr)
	}

	if attr, ok := attrs["required"]; ok {
		val, _ := attr.Expr.Value(ctx)
		f.Required = val.True()
	}
	if attr, ok := attrs["description"]; ok {
		val, _ := attr.Expr.Value(ctx)
		f.Description = val.AsString()
	}
	if attr, ok := attrs["default"]; ok {
		val, _ := attr.Expr.Value(ctx)
		f.Default = ctyconv.ToNative(val)
	}
	if attr, ok := attrs["min"]; ok {
		val, _ := attr.Expr.Value(ctx)
		n, _ := val.AsBigFloat().Float64()
		f.Min = &n
	}
	if attr, ok := attrs["max"]; ok {
		val, _ := attr.Expr.Value(ctx)
		n, _ := val.AsBigFloat().Float64()
		f.Max = &n
	}
	if attr, ok := attrs["min_length"]; ok {
		val, _ := attr.Expr.Value(ctx)
		i, _ := val.AsBigFloat().Int64()
		n := int(i)
		f.MinLength = &n
	}
	if attr, ok := attrs["max_length"]; ok {
		val, _ := attr.Expr.Value(ctx)
		i, _ := val.AsBigFloat().Int64()
		n := int(i)
		f.MaxLength = &n
	}
	if attr, ok := attrs["enum"]; ok {
		val, _ := attr.Expr.Value(ctx)
		if val.Type().IsTupleType() || val.Type().IsListType() {
			for it := val.ElementIterator(); it.Next(); {
				_, el := it.Element()
				f.Enum = append(f.Enum, el.AsString())
			}
		}
	}

	return f, nil
}

// decodeOpenAPIMetadata populates global documentation metadata from an openapi block.
func decodeOpenAPIMetadata(body hcl.Body, ctx *hcl.EvalContext, meta *config.OpenAPIMetadata) error {
	content, diags := body.Content(openapiBlockSchema)
	if diags.HasErrors() {
		return diags
	}

	attrs, _ := body.JustAttributes()
	if attr, ok := attrs["title"]; ok {
		val, _ := attr.Expr.Value(ctx)
		meta.Title = val.AsString()
	}
	if attr, ok := attrs["version"]; ok {
		val, _ := attr.Expr.Value(ctx)
		meta.Version = val.AsString()
	}
	if attr, ok := attrs["description"]; ok {
		val, _ := attr.Expr.Value(ctx)
		meta.Description = val.AsString()
	}

	for _, block := range content.Blocks {
		switch block.Type {
		case "server":
			var s config.OpenAPIServer
			_ = gohcl.DecodeBody(block.Body, ctx, &s)
			meta.Servers = append(meta.Servers, s)
		case "tag":
			var t config.OpenAPITag
			_ = gohcl.DecodeBody(block.Body, ctx, &t)
			meta.Tags = append(meta.Tags, t)
		case "contact":
			var c config.Contact
			_ = gohcl.DecodeBody(block.Body, ctx, &c)
			meta.Contact = &c
		case "license":
			var l config.License
			_ = gohcl.DecodeBody(block.Body, ctx, &l)
			meta.License = &l
		}
	}
	return nil
}

// validateIntegrity ensures that step targets point to declared connections and schemas,
// and that no duplicate route patterns exist across manifest files.
func validateIntegrity(cfg *config.Config) error {
	seenRoutes := make(map[string]struct{}, len(cfg.Endpoints))

	for _, s := range cfg.Schemas {
		for _, f := range s.Fields {
			if f.SchemaRef != "" {
				if _, exists := cfg.Schemas[f.SchemaRef]; !exists {
					return fmt.Errorf("schema %q field %q references unknown schema %q", s.Name, f.Name, f.SchemaRef)
				}
			}
		}
	}

	for _, ep := range cfg.Endpoints {
		if _, exists := seenRoutes[ep.RoutePattern]; exists {
			return fmt.Errorf("duplicate route pattern %q declared across manifests", ep.RoutePattern)
		}
		seenRoutes[ep.RoutePattern] = struct{}{}

		for _, s := range ep.Pipeline {
			var connName string
			var expectedType config.ConnectionType

			if s.SQL != nil {
				connName = s.SQL.Connection
				expectedType = config.ConnectionTypeSQL
			} else if s.Valkey != nil {
				connName = s.Valkey.Connection
				expectedType = config.ConnectionTypeValkey
			}

			if connName != "" {
				conn, exists := cfg.Connections[connName]
				if !exists {
					return fmt.Errorf("endpoint %q: step %q references undeclared connection %q", ep.RoutePattern, s.Name, connName)
				}
				if conn.Type != expectedType {
					return fmt.Errorf(
						"endpoint %q: step %q (%s) cannot use connection %q of type %s",
						ep.RoutePattern,
						s.Name,
						s.Type,
						connName,
						conn.Type,
					)
				}
			}

			if s.Respond != nil && s.Respond.SchemaRef != "" {
				ref := strings.TrimPrefix(s.Respond.SchemaRef, "[]")
				if _, exists := cfg.Schemas[ref]; !exists {
					return fmt.Errorf("endpoint %q: respond step references unknown schema %q", ep.RoutePattern, s.Respond.SchemaRef)
				}
			}
		}

		if ep.Request.BodyRef != "" {
			if _, exists := cfg.Schemas[ep.Request.BodyRef]; !exists {
				return fmt.Errorf("endpoint %q: request body references unknown schema %q", ep.RoutePattern, ep.Request.BodyRef)
			}
		}
	}
	return nil
}

// deriveTag computes a default operation tag from the first resource path segment.
func deriveTag(path string) string {
	parts := strings.SplitSeq(strings.Trim(path, "/"), "/")
	for p := range parts {
		if p != "api" && !strings.HasPrefix(p, "v") && !strings.HasPrefix(p, "{") {
			return p
		}
	}
	return "default"
}

// resolveIdentifier extracts a clean identifier string from either a string literal or an HCL scope traversal.
func resolveIdentifier(expr hcl.Expression) string {
	if expr == nil {
		return ""
	}
	val, diags := expr.Value(nil)
	if !diags.HasErrors() && val.IsKnown() && !val.IsNull() && val.Type() == cty.String {
		return val.AsString()
	}
	traversal, diags := hcl.AbsTraversalForExpr(expr)
	if !diags.HasErrors() && len(traversal) > 0 {
		lastTraverser := traversal[len(traversal)-1]
		switch step := lastTraverser.(type) {
		case hcl.TraverseAttr:
			return step.Name
		case hcl.TraverseRoot:
			return step.Name
		}
	}
	return ""
}

// exprToString attempts constant expression evaluation or extracts a single attribute string.
func exprToString(expr hcl.Expression) string {
	if expr == nil {
		return ""
	}
	val, diags := expr.Value(nil)
	if !diags.HasErrors() && val.IsKnown() && !val.IsNull() && val.Type() == cty.String {
		return val.AsString()
	}
	return resolveIdentifier(expr)
}

// exprToInt evaluates a constant expression as an integer, falling back on error.
func exprToInt(expr hcl.Expression, fallback int) int {
	if expr == nil {
		return fallback
	}
	val, diags := expr.Value(nil)
	if !diags.HasErrors() && val.IsKnown() && !val.IsNull() && val.Type() == cty.Number {
		i, _ := val.AsBigFloat().Int64()
		return int(i)
	}
	return fallback
}

var rootSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "server"},
		{Type: "openapi"},
		{Type: "telemetry"},
		{Type: "connection", LabelNames: []string{"type", "name"}},
		{Type: "schema", LabelNames: []string{"name"}},
		{Type: "route", LabelNames: []string{"endpoint"}},
	},
}

var openapiBlockSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "title"},
		{Name: "version"},
		{Name: "description"},
	},
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "server"},
		{Type: "tag"},
		{Type: "contact"},
		{Type: "license"},
	},
}

var routeBodySchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "summary"},
		{Name: "tag"},
		{Name: "hidden"},
		{Name: "operation_id"},
	},
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "request"},
		{Type: "step", LabelNames: []string{"type", "name"}},
		{Type: "respond"},
		{Type: "docs"},
		{Type: "spec"},
	},
}

var schemaBlockSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "description"},
	},
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "field", LabelNames: []string{"name"}},
	},
}

var requestBlockSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "path", LabelNames: []string{"name"}},
		{Type: "query", LabelNames: []string{"name"}},
		{Type: "header", LabelNames: []string{"name"}},
		{Type: "body"},
	},
}

var fieldsContainerSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "field", LabelNames: []string{"name"}},
	},
}

var sqlStepSchema = &hcl.BodySchema{
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "catch", LabelNames: []string{"code"}},
	},
}
