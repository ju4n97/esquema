package engine

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"  // MySQL driver
	_ "github.com/jackc/pgx/v5/stdlib"  // PostgreSQL driver
	_ "github.com/microsoft/go-mssqldb" // MSSQL driver
	"github.com/valkey-io/valkey-go"
	_ "modernc.org/sqlite" // SQLite driver

	"github.com/ju4n97/hclapi/internal/manifest"
	"github.com/ju4n97/hclapi/internal/openapi"
	"github.com/ju4n97/hclapi/internal/telemetry"
)

// Option configures an [Engine] during initialization.
type Option func(*Engine)

// WithGoHandler registers a custom native Go callback by its identifier.
func WithGoHandler(name string, h manifest.GoHandler) Option {
	return func(e *Engine) {
		e.handlers[name] = h
	}
}

// WithHTTPClient overrides the default HTTP client used for outbound requests.
func WithHTTPClient(client *http.Client) Option {
	return func(e *Engine) {
		e.httpClient = client
	}
}

// WithTelemetry overrides the default telemetry instance configured by the manifest.
func WithTelemetry(t *telemetry.Telemetry) Option {
	return func(e *Engine) {
		if t != nil {
			e.telemetry = t
		}
	}
}

// Engine coordinates HTTP routing, connection pool lifecycles, and request execution.
// It implements the standard [http.Handler] interface.
type Engine struct {
	handler       http.Handler
	mux           *http.ServeMux
	manifest      *manifest.Manifest
	sqlPools      map[string]*sql.DB
	valkeyPools   map[string]valkey.Client
	httpTransport *http.Transport
	httpClient    *http.Client
	handlers      map[string]manifest.GoHandler
	spec          *openapi.Spec
	telemetry     *telemetry.Telemetry
}

// New initializes an Engine from a validated [manifest.Manifest], opening connection pools,
// configuring telemetry, precompiling OpenAPI specifications, and binding routes.
func New(m *manifest.Manifest, opts ...Option) (*Engine, error) {
	if m == nil {
		return nil, errors.New("manifest is nil")
	}

	if err := m.Validate(); err != nil {
		return nil, fmt.Errorf("manifest validation failed: %w", err)
	}

	spec, err := openapi.Compile(m)
	if err != nil {
		return nil, fmt.Errorf("compile openapi specification: %w", err)
	}

	trans := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}

	tel := telemetry.New(m.Telemetry)

	e := &Engine{
		mux:           http.NewServeMux(),
		manifest:      m,
		sqlPools:      make(map[string]*sql.DB),
		valkeyPools:   make(map[string]valkey.Client),
		httpTransport: trans,
		httpClient:    &http.Client{Transport: trans},
		handlers:      make(map[string]manifest.GoHandler),
		spec:          spec,
		telemetry:     tel,
	}

	for _, opt := range opts {
		opt(e)
	}

	if err := e.initConnections(); err != nil {
		_ = e.Close()
		return nil, err
	}

	e.bindRoutes()
	e.handler = e.telemetry.Middleware(e.mux)
	return e, nil
}

// ServeHTTP satisfies the standard [http.Handler] interface by dispatching through telemetry middleware.
func (e *Engine) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	e.handler.ServeHTTP(w, r)
}

// Close cleanly terminates all managed database pools, Valkey clients, and idle outbound HTTP connections.
func (e *Engine) Close() error {
	var errs []error

	for _, db := range e.sqlPools {
		if db != nil {
			if err := db.Close(); err != nil {
				errs = append(errs, err)
			}
		}
	}

	for _, client := range e.valkeyPools {
		if client != nil {
			client.Close()
		}
	}

	if e.httpTransport != nil {
		e.httpTransport.CloseIdleConnections()
	}

	return errors.Join(errs...)
}

// SQL returns an active database pool by connection name.
func (e *Engine) SQL(name string) (*sql.DB, bool) {
	db, exists := e.sqlPools[name]
	return db, exists
}

// Valkey returns an active Valkey client by connection name.
func (e *Engine) Valkey(name string) (valkey.Client, bool) {
	client, exists := e.valkeyPools[name]
	return client, exists
}

// Telemetry returns the active [telemetry.Telemetry] instance managing logging and tracing.
func (e *Engine) Telemetry() *telemetry.Telemetry {
	return e.telemetry
}

// initConnections opens database connection pools and Valkey clients declared in the manifest.
func (e *Engine) initConnections() error {
	for name, conn := range e.manifest.Connections {
		switch conn.Type {
		case "sql":
			driverName := resolveSQLDriverName(conn.Engine)
			db, err := sql.Open(driverName, conn.Source)
			if err != nil {
				return fmt.Errorf("open sql connection %q (%s): %w", name, driverName, err)
			}

			maxOpen := conn.MaxOpen
			if maxOpen <= 0 {
				maxOpen = 25
			}
			maxIdle := conn.MaxIdle
			if maxIdle <= 0 {
				maxIdle = 25
			}

			db.SetMaxOpenConns(maxOpen)
			db.SetMaxIdleConns(maxIdle)
			db.SetConnMaxLifetime(15 * time.Minute)
			db.SetConnMaxIdleTime(5 * time.Minute)

			e.sqlPools[name] = db

		case "valkey":
			opt, err := valkey.ParseURL(conn.Source)
			if err != nil {
				return fmt.Errorf("parse valkey connection url %q: %w", name, err)
			}

			client, err := valkey.NewClient(opt)
			if err != nil {
				return fmt.Errorf("create valkey client %q: %w", name, err)
			}

			e.valkeyPools[name] = client
		}
	}

	return nil
}

// bindRoutes wires all compiled routes to the internal [http.ServeMux].
func (e *Engine) bindRoutes() {
	deps := Dependencies{
		SQL:        e.sqlPools,
		Valkey:     e.valkeyPools,
		Handlers:   e.handlers,
		Schemas:    e.manifest.Schemas,
		Spec:       e.spec,
		HTTPClient: e.httpClient,
		Telemetry:  e.telemetry,
	}

	maxBodySize := e.manifest.Server.MaxBodySize.Bytes()
	if maxBodySize <= 0 {
		maxBodySize = 10 * 1024 * 1024
	}

	for _, route := range e.manifest.Routes {
		handler := buildRouteHandler(route, deps, maxBodySize)
		e.mux.HandleFunc(route.Pattern(), handler)
	}
}

// resolveSQLDriverName maps canonical database engines to registered database/sql driver packages.
func resolveSQLDriverName(engine string) string {
	switch strings.ToLower(strings.TrimSpace(engine)) {
	case "postgres":
		return "pgx"
	case "mysql":
		return "mysql"
	case "sqlite":
		return "sqlite"
	case "sqlserver":
		return "sqlserver"
	default:
		return engine
	}
}
