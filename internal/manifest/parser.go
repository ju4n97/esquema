package manifest

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
	"uuid"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hclparse"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// Parser coordinates file discovery, HCL parsing, and AST compilation using a configurable [StepRegistry].
type Parser struct {
	registry *StepRegistry
}

// NewParser constructs a Parser configured with the supplied [StepRegistry].
// If registry is nil, [DefaultStepRegistry] is used.
func NewParser(registry *StepRegistry) *Parser {
	if registry == nil {
		registry = DefaultStepRegistry()
	}
	return &Parser{registry: registry}
}

// Load discovers, reads, merges, and compiles HCL files into a validated Manifest using default steps.
func Load(patterns ...string) (*Manifest, error) {
	return NewParser(nil).Load(patterns...)
}

// Parse compiles an in-memory HCL manifest string into a validated Manifest using default steps.
func Parse(source string) (*Manifest, error) {
	return NewParser(nil).Parse(source)
}

// Load discovers and compiles files matching the supplied patterns.
func (p *Parser) Load(patterns ...string) (*Manifest, error) {
	files, err := DiscoverFiles(patterns...)
	if err != nil {
		return nil, fmt.Errorf("discover manifest files: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no manifest files found matching patterns: %v", patterns)
	}

	hclParser := hclparse.NewParser()
	var bodies []hcl.Body

	for _, file := range files {
		hclFile, diags := hclParser.ParseHCLFile(file)
		if diags.HasErrors() {
			return nil, fmt.Errorf("parse %s:\n%s", file, diags.Error())
		}
		bodies = append(bodies, hclFile.Body)
	}

	return p.compileBodies(bodies)
}

// Parse compiles an in-memory HCL manifest string.
func (p *Parser) Parse(source string) (*Manifest, error) {
	trimmed := strings.TrimSpace(source)
	if trimmed == "" {
		return nil, errors.New("manifest source cannot be empty")
	}

	hclParser := hclparse.NewParser()
	hclFile, diags := hclParser.ParseHCL([]byte(trimmed), "manifest.hcl")
	if diags.HasErrors() {
		return nil, fmt.Errorf("parse manifest:\n%s", diags.Error())
	}

	return p.compileBodies([]hcl.Body{hclFile.Body})
}

// compileBodies merges HCL bodies, decodes root blocks using gohcl, and resolves route pipelines.
func (p *Parser) compileBodies(bodies []hcl.Body) (*Manifest, error) {
	merged := hcl.MergeBodies(bodies)

	schemaNames, err := collectSchemaNames(merged)
	if err != nil {
		return nil, err
	}

	evalCtx := buildStaticEvalContext(schemaNames)
	exprFuncs := runtimeExprFunctions()

	var root rootDecode
	if diags := gohcl.DecodeBody(merged, evalCtx, &root); diags.HasErrors() {
		return nil, fmt.Errorf("decode manifest root:\n%s", diags.Error())
	}

	m := &Manifest{
		Server: Server{
			Host:         "127.0.0.1",
			Port:         8080,
			ReadTimeout:  Duration(15 * time.Second),
			WriteTimeout: Duration(15 * time.Second),
			MaxBodySize:  ByteSize(10 * 1024 * 1024),
		},
		OpenAPI: OpenAPI{
			Title:   "API Documentation",
			Version: "1.0.0",
		},
		Connections: make(map[string]Connection),
		Schemas:     make(map[string]Schema),
	}

	if root.Server != nil {
		if err := resolveServerConfig(root.Server, &m.Server); err != nil {
			return nil, err
		}
	}

	if root.OpenAPI != nil {
		m.OpenAPI = *root.OpenAPI
		if m.OpenAPI.Title == "" {
			m.OpenAPI.Title = "API Documentation"
		}
		if m.OpenAPI.Version == "" {
			m.OpenAPI.Version = "1.0.0"
		}
	}

	if root.Telemetry != nil {
		m.Telemetry = *root.Telemetry
	}

	for _, conn := range root.Connections {
		resolved, err := resolveConnectionConfig(conn)
		if err != nil {
			return nil, err
		}
		m.Connections[resolved.Name] = resolved
	}

	for _, s := range root.Schemas {
		resolved, err := resolveSchema(s, evalCtx)
		if err != nil {
			return nil, err
		}
		m.Schemas[resolved.Name] = resolved
	}

	for _, rb := range root.Routes {
		route, err := p.resolveRoute(rb, evalCtx, exprFuncs)
		if err != nil {
			return nil, err
		}
		m.Routes = append(m.Routes, route)
	}

	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("manifest integrity check: %w", err)
	}

	return m, nil
}

type rootDecode struct {
	Server      *serverDecode      `hcl:"server,block"`
	OpenAPI     *OpenAPI           `hcl:"openapi,block"`
	Telemetry   *Telemetry         `hcl:"telemetry,block"`
	Connections []connectionDecode `hcl:"connection,block"`
	Schemas     []schemaDecode     `hcl:"schema,block"`
	Routes      []routeDecode      `hcl:"route,block"`
}

type serverDecode struct {
	Host         string `hcl:"host,optional"`
	Port         int    `hcl:"port,optional"`
	ReadTimeout  string `hcl:"read_timeout,optional"`
	WriteTimeout string `hcl:"write_timeout,optional"`
	MaxBodySize  string `hcl:"max_body_size,optional"`
}

type connectionDecode struct {
	Type   string      `hcl:"type,label"`
	Name   string      `hcl:"name,label"`
	Engine string      `hcl:"engine,optional"`
	Source string      `hcl:"source,optional"`
	URL    string      `hcl:"url,optional"`
	Pool   *poolDecode `hcl:"pool,block"`
}

type poolDecode struct {
	MaxOpen *int `hcl:"max_open,optional"`
	MaxIdle *int `hcl:"max_idle,optional"`
}

type schemaDecode struct {
	Name        string        `hcl:"name,label"`
	Description string        `hcl:"description,optional"`
	Fields      []fieldDecode `hcl:"field,block"`
}

type fieldDecode struct {
	Name        string         `hcl:"name,label"`
	TypeExpr    hcl.Expression `hcl:"type"`
	Required    bool           `hcl:"required,optional"`
	Format      string         `hcl:"format,optional"`
	DefaultExpr hcl.Expression `hcl:"default,optional"`
	Description string         `hcl:"description,optional"`
	Min         *float64       `hcl:"min,optional"`
	Max         *float64       `hcl:"max,optional"`
	MinLength   *int           `hcl:"min_length,optional"`
	MaxLength   *int           `hcl:"max_length,optional"`
	Enum        []string       `hcl:"enum,optional"`
}

type routeDecode struct {
	Endpoint string   `hcl:"endpoint,label"`
	Summary  string   `hcl:"summary,optional"`
	Tag      string   `hcl:"tag,optional"`
	Hidden   bool     `hcl:"hidden,optional"`
	Body     hcl.Body `hcl:",remain"`
}

// collectSchemaNames extracts schema labels before full decoding to populate the type scope.
func collectSchemaNames(body hcl.Body) ([]string, error) {
	content, _, diags := body.PartialContent(&hcl.BodySchema{
		Blocks: []hcl.BlockHeaderSchema{
			{Type: "schema", LabelNames: []string{"name"}},
		},
	})
	if diags.HasErrors() {
		return nil, fmt.Errorf("collect schema names:\n%s", diags.Error())
	}

	var names []string
	for _, b := range content.Blocks {
		if len(b.Labels) > 0 {
			names = append(names, b.Labels[0])
		}
	}
	return names, nil
}

// resolveServerConfig parses duration and byte size strings into typed units.
func resolveServerConfig(raw *serverDecode, target *Server) error {
	if raw.Host != "" {
		target.Host = raw.Host
	}
	if raw.Port != 0 {
		target.Port = raw.Port
	}
	if raw.ReadTimeout != "" {
		d, err := ParseDuration(raw.ReadTimeout)
		if err != nil {
			return fmt.Errorf("server.read_timeout: %w", err)
		}
		target.ReadTimeout = d
	}
	if raw.WriteTimeout != "" {
		d, err := ParseDuration(raw.WriteTimeout)
		if err != nil {
			return fmt.Errorf("server.write_timeout: %w", err)
		}
		target.WriteTimeout = d
	}
	if raw.MaxBodySize != "" {
		b, err := ParseByteSize(raw.MaxBodySize)
		if err != nil {
			return fmt.Errorf("server.max_body_size: %w", err)
		}
		target.MaxBodySize = b
	}
	return nil
}

// resolveConnectionConfig normalizes database engines and configures pool defaults.
func resolveConnectionConfig(raw connectionDecode) (Connection, error) {
	connType := strings.ToLower(raw.Type)
	if connType != "sql" && connType != "valkey" {
		return Connection{}, fmt.Errorf("connection %q: invalid type %q (allowed: sql, valkey)", raw.Name, raw.Type)
	}

	conn := Connection{
		Type:    connType,
		Name:    raw.Name,
		MaxOpen: 25,
		MaxIdle: 25,
	}

	if raw.Pool != nil {
		if raw.Pool.MaxOpen != nil {
			conn.MaxOpen = *raw.Pool.MaxOpen
		}
		if raw.Pool.MaxIdle != nil {
			conn.MaxIdle = *raw.Pool.MaxIdle
		}
	}

	if connType == "sql" {
		canonical := strings.ToLower(strings.TrimSpace(raw.Engine))
		switch canonical {
		case "postgres", "mysql", "sqlite", "sqlserver":
			conn.Engine = canonical
		case "postgresql", "pgx":
			return Connection{}, fmt.Errorf("connection %q: use canonical engine name \"postgres\"", raw.Name)
		case "sqlite3":
			return Connection{}, fmt.Errorf("connection %q: use canonical engine name \"sqlite\"", raw.Name)
		case "mariadb":
			return Connection{}, fmt.Errorf("connection %q: use canonical engine name \"mysql\"", raw.Name)
		case "mssql":
			return Connection{}, fmt.Errorf("connection %q: use canonical engine name \"sqlserver\"", raw.Name)
		default:
			return Connection{}, fmt.Errorf(
				"connection %q: unsupported sql engine %q (allowed: postgres, mysql, sqlite, sqlserver)",
				raw.Name,
				raw.Engine,
			)
		}
		conn.Source = raw.Source
	}

	if connType == "valkey" {
		conn.Engine = "valkey"
		conn.Source = raw.URL
		if conn.Source == "" {
			conn.Source = raw.Source
		}
	}

	return conn, nil
}

// resolveSchema resolves field types and constructs the field map.
func resolveSchema(raw schemaDecode, evalCtx *hcl.EvalContext) (Schema, error) {
	s := Schema{
		Name:        raw.Name,
		Description: raw.Description,
		Fields:      make(map[string]Field, len(raw.Fields)),
	}

	for _, fd := range raw.Fields {
		typeVal, diags := fd.TypeExpr.Value(evalCtx)
		if diags.HasErrors() {
			return Schema{}, fmt.Errorf("schema %q field %q type: %w", raw.Name, fd.Name, diags)
		}

		spec, err := TypeSpecFromCty(typeVal)
		if err != nil {
			return Schema{}, fmt.Errorf("schema %q field %q: %w", raw.Name, fd.Name, err)
		}

		field := Field{
			Name:        fd.Name,
			Type:        spec,
			Required:    fd.Required,
			Format:      Format(fd.Format),
			Description: fd.Description,
			Min:         fd.Min,
			Max:         fd.Max,
			MinLength:   fd.MinLength,
			MaxLength:   fd.MaxLength,
			Enum:        fd.Enum,
		}

		if fd.DefaultExpr != nil {
			val, dDiags := fd.DefaultExpr.Value(evalCtx)
			if dDiags.HasErrors() {
				return Schema{}, fmt.Errorf("schema %q field %q default: %w", raw.Name, fd.Name, dDiags)
			}
			field.Default = toNative(val)
		}

		s.Fields[field.Name] = field
	}

	return s, nil
}

// resolveRoute dynamically builds a route schema from registered step keywords and decodes steps in exact declared order.
func (p *Parser) resolveRoute(rb routeDecode, evalCtx *hcl.EvalContext, funcs map[string]function.Function) (Route, error) {
	parts := strings.SplitN(strings.TrimSpace(rb.Endpoint), " ", 2)
	if len(parts) != 2 {
		return Route{}, fmt.Errorf("invalid route label %q: expected 'METHOD /path'", rb.Endpoint)
	}

	r := Route{
		Method:  strings.ToUpper(parts[0]),
		Path:    parts[1],
		Summary: rb.Summary,
		Tag:     rb.Tag,
		Hidden:  rb.Hidden,
	}

	routeBodySchema := &hcl.BodySchema{
		Blocks: append([]hcl.BlockHeaderSchema{
			{Type: "request"},
		}, p.registry.BlockHeaderSchemas()...),
	}

	content, diags := rb.Body.Content(routeBodySchema)
	if diags.HasErrors() {
		return Route{}, fmt.Errorf("route %q:\n%s", rb.Endpoint, diags.Error())
	}

	for _, b := range content.Blocks {
		if b.Type == "request" {
			rules, err := resolveRequest(b.Body, evalCtx)
			if err != nil {
				return Route{}, fmt.Errorf("route %q: %w", rb.Endpoint, err)
			}
			r.Request = rules
			continue
		}

		step, err := p.registry.Decode(b, evalCtx, funcs)
		if err != nil {
			return Route{}, fmt.Errorf("route %q: %w", rb.Endpoint, err)
		}

		r.Steps = append(r.Steps, step)
	}

	return r, nil
}

// resolveRequest decodes ingress validation blocks and assigned schema bodies.
func resolveRequest(body hcl.Body, evalCtx *hcl.EvalContext) (*Request, error) {
	rules := &Request{
		Path:    make(map[string]Field),
		Query:   make(map[string]Field),
		Headers: make(map[string]Field),
		Body:    make(map[string]Field),
	}

	attrs, _ := body.JustAttributes()
	if attr, ok := attrs["body"]; ok {
		val, diags := attr.Expr.Value(evalCtx)
		if diags.HasErrors() {
			return nil, diags
		}
		if val.Type() == cty.String {
			rules.BodyRef = val.AsString()
		}
	}

	content, _, diags := body.PartialContent(requestSchema)
	if diags.HasErrors() {
		return nil, diags
	}

	for _, b := range content.Blocks {
		switch b.Type {
		case "path", "query", "header":
			var fd fieldDecode
			fd.Name = b.Labels[0]
			if diags := gohcl.DecodeBody(b.Body, evalCtx, &fd); diags.HasErrors() {
				return nil, diags
			}

			typeVal, tDiags := fd.TypeExpr.Value(evalCtx)
			if tDiags.HasErrors() {
				return nil, tDiags
			}
			spec, err := TypeSpecFromCty(typeVal)
			if err != nil {
				return nil, err
			}

			f := Field{
				Name:        fd.Name,
				Type:        spec,
				Required:    fd.Required,
				Format:      Format(fd.Format),
				Description: fd.Description,
				Min:         fd.Min,
				Max:         fd.Max,
				MinLength:   fd.MinLength,
				MaxLength:   fd.MaxLength,
				Enum:        fd.Enum,
			}

			if fd.DefaultExpr != nil {
				val, dDiags := fd.DefaultExpr.Value(evalCtx)
				if dDiags.HasErrors() {
					return nil, fmt.Errorf("request %s %q default: %w", b.Type, fd.Name, dDiags)
				}
				f.Default = toNative(val)
			}

			switch b.Type {
			case "path":
				rules.Path[f.Name] = f
			case "query":
				rules.Query[f.Name] = f
			case "header":
				rules.Headers[f.Name] = f
			}

		case "body":
			var bodyBlock struct {
				Fields []fieldDecode `hcl:"field,block"`
			}
			if diags := gohcl.DecodeBody(b.Body, evalCtx, &bodyBlock); diags.HasErrors() {
				return nil, diags
			}

			for _, fd := range bodyBlock.Fields {
				typeVal, tDiags := fd.TypeExpr.Value(evalCtx)
				if tDiags.HasErrors() {
					return nil, tDiags
				}
				spec, err := TypeSpecFromCty(typeVal)
				if err != nil {
					return nil, err
				}

				f := Field{
					Name:        fd.Name,
					Type:        spec,
					Required:    fd.Required,
					Format:      Format(fd.Format),
					Description: fd.Description,
					Min:         fd.Min,
					Max:         fd.Max,
					MinLength:   fd.MinLength,
					MaxLength:   fd.MaxLength,
					Enum:        fd.Enum,
				}

				if fd.DefaultExpr != nil {
					val, dDiags := fd.DefaultExpr.Value(evalCtx)
					if dDiags.HasErrors() {
						return nil, fmt.Errorf("request body field %q default: %w", fd.Name, dDiags)
					}
					f.Default = toNative(val)
				}

				rules.Body[fd.Name] = f
			}
		}
	}

	return rules, nil
}

// buildStaticEvalContext constructs an evaluation context populated with built-in types and functions.
func buildStaticEvalContext(schemas []string) *hcl.EvalContext {
	vars := BuiltinTypeVariables(schemas...)
	funcs := BuiltinTypeFunctions()
	funcs["env"] = envFunction()

	return &hcl.EvalContext{
		Variables: vars,
		Functions: funcs,
	}
}

// runtimeExprFunctions returns helper functions exposed during request-time expression evaluation.
func runtimeExprFunctions() map[string]function.Function {
	return map[string]function.Function{
		"env": envFunction(),
		"now": function.New(&function.Spec{
			Params: []function.Parameter{},
			Type:   function.StaticReturnType(cty.String),
			Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
				return cty.StringVal(time.Now().UTC().Format(time.RFC3339)), nil
			},
		}),
		"uuid": function.New(&function.Spec{
			Params: []function.Parameter{},
			Type:   function.StaticReturnType(cty.String),
			Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
				return cty.StringVal(uuid.New().String()), nil
			},
		}),
		"problem": function.New(&function.Spec{
			Params: []function.Parameter{
				{Name: "status", Type: cty.Number},
				{Name: "detail", Type: cty.String},
			},
			Type: function.StaticReturnType(cty.Object(map[string]cty.Type{
				"status": cty.Number,
				"title":  cty.String,
				"detail": cty.String,
			})),
			Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
				status, _ := args[0].AsBigFloat().Int64()
				detail := args[1].AsString()
				title := http.StatusText(int(status))
				if title == "" {
					title = "Error"
				}
				return cty.ObjectVal(map[string]cty.Value{
					"status": cty.NumberIntVal(status),
					"title":  cty.StringVal(title),
					"detail": cty.StringVal(detail),
				}), nil
			},
		}),
		"upper": function.New(&function.Spec{
			Params: []function.Parameter{{Name: "s", Type: cty.String}},
			Type:   function.StaticReturnType(cty.String),
			Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
				return cty.StringVal(strings.ToUpper(args[0].AsString())), nil
			},
		}),
		"lower": function.New(&function.Spec{
			Params: []function.Parameter{{Name: "s", Type: cty.String}},
			Type:   function.StaticReturnType(cty.String),
			Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
				return cty.StringVal(strings.ToLower(args[0].AsString())), nil
			},
		}),
		"trim": function.New(&function.Spec{
			Params: []function.Parameter{{Name: "s", Type: cty.String}},
			Type:   function.StaticReturnType(cty.String),
			Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
				return cty.StringVal(strings.TrimSpace(args[0].AsString())), nil
			},
		}),
	}
}

// envFunction provides an HCL function reading environment variables with optional fallbacks.
func envFunction() function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{
			{Name: "key", Type: cty.String},
		},
		VarParam: &function.Parameter{
			Name: "default",
			Type: cty.String,
		},
		Type: function.StaticReturnType(cty.String),
		Impl: func(args []cty.Value, retType cty.Type) (cty.Value, error) {
			key := args[0].AsString()
			if val, exists := os.LookupEnv(key); exists && val != "" {
				return cty.StringVal(val), nil
			}
			if len(args) > 1 && !args[1].IsNull() {
				return cty.StringVal(args[1].AsString()), nil
			}
			return cty.StringVal(""), nil
		},
	})
}

var requestSchema = &hcl.BodySchema{
	Attributes: []hcl.AttributeSchema{
		{Name: "body"},
	},
	Blocks: []hcl.BlockHeaderSchema{
		{Type: "path", LabelNames: []string{"name"}},
		{Type: "query", LabelNames: []string{"name"}},
		{Type: "header", LabelNames: []string{"name"}},
		{Type: "body"},
	},
}
