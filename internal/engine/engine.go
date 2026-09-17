package engine

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	_ "github.com/go-sql-driver/mysql"  // MySQL driver
	_ "github.com/jackc/pgx/v5/stdlib"  // PostgreSQL driver
	_ "github.com/microsoft/go-mssqldb" // Microsoft SQL Server driver
	"github.com/valkey-io/valkey-go"
	"go.starlark.net/starlark"
	"go.starlark.net/syntax"
	_ "modernc.org/sqlite" // SQLite driver

	"github.com/ju4n97/hclapi/internal/config"
	"github.com/ju4n97/hclapi/internal/manifest"
	"github.com/ju4n97/hclapi/internal/problem"
	"github.com/ju4n97/hclapi/internal/scalar"
	"github.com/ju4n97/hclapi/internal/telemetry"
)

// sqlConnection binds an active database connection pool to its SQL driver dialect.
type sqlConnection struct {
	db     *sql.DB
	driver string
}

// StepHandler defines custom Go step function signatures.
type StepHandler func(ctx context.Context, step *Step) (any, error)

// Step represents the runtime state provided to a Go step callback.
type Step struct {
	Name    string
	Args    Args
	Request *http.Request
}

// Engine coordinates routing, connection pools, and pipeline step execution.
type Engine struct {
	cfg           *config.Config
	mux           *http.ServeMux
	handler       http.Handler
	telemetry     *telemetry.Telemetry
	sqls          map[string]sqlConnection
	valkeys       map[string]valkey.Client
	logger        *slog.Logger
	regMu         sync.RWMutex
	registry      map[string]StepHandler
	starlarkFuncs map[string]starlark.Callable

	specJSON     []byte
	specJSONETag string
	specYAML     []byte
	specYAMLETag string
}

// Option configures an Engine instance.
type Option func(*Engine)

// WithLogger sets the structured logger for the engine.
func WithLogger(logger *slog.Logger) Option {
	return func(e *Engine) { e.logger = logger }
}

// WithValkey registers an existing Valkey client instance by connection name.
func WithValkey(name string, client valkey.Client) Option {
	return func(e *Engine) { e.valkeys[name] = client }
}

// WithStep registers a named Go step callback.
func WithStep(name string, h StepHandler) Option {
	return func(e *Engine) { e.registry[name] = h }
}

// New initializes an Engine from a verified Config and functional options.
func New(cfg *config.Config, opts ...Option) (*Engine, error) {
	tel := telemetry.New(cfg.Telemetry)

	e := &Engine{
		cfg:           cfg,
		mux:           http.NewServeMux(),
		telemetry:     tel,
		logger:        tel.Logger(),
		sqls:          make(map[string]sqlConnection),
		valkeys:       make(map[string]valkey.Client),
		registry:      make(map[string]StepHandler),
		starlarkFuncs: make(map[string]starlark.Callable),
	}

	for _, opt := range opts {
		opt(e)
	}

	if err := e.openConnections(); err != nil {
		_ = e.Close()
		return nil, err
	}

	if err := e.compileStarlarkScripts(); err != nil {
		_ = e.Close()
		return nil, err
	}

	if jsonBytes, err := manifest.GenerateOpenAPI(cfg, string(config.SpecFormatJSON)); err == nil {
		e.specJSON = jsonBytes
		h := sha256.Sum256(jsonBytes)
		e.specJSONETag = fmt.Sprintf("%q", hex.EncodeToString(h[:]))
	}
	if yamlBytes, err := manifest.GenerateOpenAPI(cfg, string(config.SpecFormatYAML)); err == nil {
		e.specYAML = yamlBytes
		h := sha256.Sum256(yamlBytes)
		e.specYAMLETag = fmt.Sprintf("%q", hex.EncodeToString(h[:]))
	}

	e.bindRoutes()

	e.handler = e.telemetry.Middleware(e.mux)

	return e, nil
}

// sqlDriverRegistry maps canonical SQLEngine names to registered database/sql driver packages.
var sqlDriverRegistry = map[config.SQLEngine]string{
	config.SQLEnginePostgres:  "pgx",
	config.SQLEngineMySQL:     "mysql",
	config.SQLEngineSQLite:    "sqlite",
	config.SQLEngineSQLServer: "sqlserver",
}

// openConnections initializes all database pools and cache clients declared in the config.
func (e *Engine) openConnections() error {
	for name, conn := range e.cfg.Connections {
		switch conn.Type {
		case config.ConnectionTypeSQL:
			engine := config.SQLEngine(conn.Engine)
			driver, ok := sqlDriverRegistry[engine]
			if !ok {
				// Allow custom drivers registered directly with database/sql
				driver = conn.Engine
			}

			db, err := sql.Open(driver, conn.Source)
			if err != nil {
				return fmt.Errorf("open sql pool %q (%s): %w", name, driver, err)
			}

			maxOpen := 25
			maxIdle := 25
			if conn.Pool != nil {
				if conn.Pool.MaxOpen > 0 {
					maxOpen = conn.Pool.MaxOpen
				}
				if conn.Pool.MaxIdle > 0 {
					maxIdle = conn.Pool.MaxIdle
				}
			}

			db.SetMaxOpenConns(maxOpen)
			db.SetMaxIdleConns(maxIdle)
			db.SetConnMaxLifetime(15 * time.Minute)
			db.SetConnMaxIdleTime(5 * time.Minute)

			e.sqls[name] = sqlConnection{
				db:     db,
				driver: string(engine),
			}

		case config.ConnectionTypeValkey:
			if _, exists := e.valkeys[name]; exists {
				continue
			}
			opt, err := valkey.ParseURL(conn.URL)
			if err != nil {
				return fmt.Errorf("parse valkey url %q: %w", name, err)
			}

			client, err := valkey.NewClient(opt)
			if err != nil {
				return fmt.Errorf("open valkey client %q: %w", name, err)
			}

			e.valkeys[name] = client
		}
	}
	return nil
}

// compileStarlarkScripts precompiles and validates all Starlark step scripts at engine initialization.
func (e *Engine) compileStarlarkScripts() error {
	opts := &syntax.FileOptions{
		Set:       true,
		While:     true,
		Recursion: true,
	}

	for _, ep := range e.cfg.Endpoints {
		for _, s := range ep.Pipeline {
			if s.Type != config.StepTypeStarlark || s.Starlark == nil {
				continue
			}

			thread := &starlark.Thread{Name: "hclapi-compile"}
			filename := fmt.Sprintf("%s_%s.star", ep.Method, s.Name)
			globals, err := starlark.ExecFileOptions(opts, thread, filename, s.Starlark.Source, nil)
			if err != nil {
				return fmt.Errorf("compile starlark step %q in %s: %w", s.Name, ep.RoutePattern, err)
			}

			execVal, exists := globals["execute"]
			if !exists {
				return fmt.Errorf("starlark step %q in %s must define an execute(ctx) function", s.Name, ep.RoutePattern)
			}

			callable, ok := execVal.(starlark.Callable)
			if !ok {
				return fmt.Errorf("starlark step %q in %s: execute must be callable", s.Name, ep.RoutePattern)
			}

			key := ep.RoutePattern + ":" + s.Name
			e.starlarkFuncs[key] = callable
		}
	}
	return nil
}

// ServeHTTP satisfies the standard http.Handler interface through the telemetry middleware.
func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.handler.ServeHTTP(w, r)
}

// SQL returns a managed *sql.DB connection pool by connection name.
func (e *Engine) SQL(name string) (*sql.DB, bool) {
	conn, ok := e.sqls[name]
	if !ok {
		return nil, false
	}
	return conn.db, true
}

// Valkey returns an active Valkey client by connection name.
func (e *Engine) Valkey(name string) (valkey.Client, bool) {
	client, ok := e.valkeys[name]
	return client, ok
}

// Close gracefully closes all active database pools and cache connections.
func (e *Engine) Close() error {
	var errs []error

	for _, conn := range e.sqls {
		if conn.db != nil {
			if err := conn.db.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}

	for _, client := range e.valkeys {
		if client != nil {
			client.Close()
		}
	}

	return errors.Join(errs...)
}

// bindRoutes wires all compiled endpoints to the HTTP router.
func (e *Engine) bindRoutes() {
	for _, ep := range e.cfg.Endpoints {
		pipeline := ep.Pipeline
		pattern := ep.RoutePattern

		e.mux.HandleFunc(pattern, func(w http.ResponseWriter, r *http.Request) {
			maxBodyBytes := e.cfg.Server.MaxBodySize.Bytes()
			if maxBodyBytes <= 0 {
				maxBodyBytes = int64(10 * scalar.MB)
			}

			ctx, err := NewContext(r, pattern, maxBodyBytes)
			if err != nil {
				status := http.StatusBadRequest
				if strings.Contains(err.Error(), "exceeds limit") {
					status = http.StatusRequestEntityTooLarge
				}
				problem.Write(w, problem.New(status, err.Error()))
				return
			}

			if prob := e.validateIngress(ctx, ep); prob != nil {
				problem.Write(w, *prob)
				return
			}

			for _, s := range pipeline {
				if err := ctx.RequestContext().Err(); err != nil {
					problem.Write(w, problem.New(http.StatusGatewayTimeout, "Request aborted"))
					return
				}

				if s.WhenExpr != nil {
					ok, err := ctx.EvalBool(s.WhenExpr)
					if err != nil {
						problem.Write(w, problem.New(http.StatusInternalServerError, err.Error()))
						return
					}
					if !ok {
						continue
					}
				}

				stepCtx, endStepSpan := e.telemetry.StartStepSpan(ctx.RequestContext(), string(s.Type), s.Name)
				ctx.req = ctx.req.WithContext(stepCtx)

				var stepErr error
				switch s.Type {
				case config.StepTypeRespond:
					endStepSpan(nil)
					e.executeRespond(ctx, w, s.Respond)
					return

				case config.StepTypeSpec:
					endStepSpan(nil)
					e.executeSpec(ctx, w, s.Spec)
					return

				case config.StepTypeDocs:
					endStepSpan(nil)
					e.executeDocs(ctx, w, s.Docs)
					return

				case config.StepTypeSQL:
					stepErr = e.executeSQL(ctx, w, s.Name, s.SQL)

				case config.StepTypeValkey:
					stepErr = e.executeValkey(ctx, w, s.Name, s.Valkey)

				case config.StepTypeStarlark:
					stepErr = e.executeStarlark(ctx, w, pattern, s.Name)

				case config.StepTypeGo:
					stepErr = e.executeGo(ctx, w, s.Name, s.Go)

				case config.StepTypeHTTP:
					stepErr = e.executeHTTP(ctx, w, s.Name, s.HTTP)
				}

				endStepSpan(stepErr)
				if stepErr != nil {
					return
				}
			}
		})
	}
}
