// Package hclapi provides an embeddable declarative API runtime powered by HCL.
package hclapi

import (
	"database/sql"
	"log/slog"

	"github.com/valkey-io/valkey-go"

	"github.com/ju4n97/hclapi/internal/config"
	"github.com/ju4n97/hclapi/internal/engine"
	"github.com/ju4n97/hclapi/internal/manifest"
	"github.com/ju4n97/hclapi/internal/problem"
)

type (
	// Engine handles routing, connection pooling, and pipeline execution.
	Engine = engine.Engine

	// Config represents the verified runtime configuration.
	Config = config.Config

	// Step holds execution parameters and HTTP request context for Go callbacks.
	Step = engine.Step

	// Args provides type-safe, generic argument extraction and coercion for Go steps.
	Args = engine.Args

	// StepHandler defines custom Go step functions.
	StepHandler = engine.StepHandler

	// Problem represents an RFC 9457 Problem Details error payload.
	Problem = problem.Problem

	// Option configures an Engine instance.
	Option = engine.Option
)

// WithLogger sets the structured logger for the engine.
func WithLogger(logger *slog.Logger) Option {
	return engine.WithLogger(logger)
}

// WithValkey registers a pre-configured Valkey client for custom connection management.
func WithValkey(name string, client valkey.Client) Option {
	return engine.WithValkey(name, client)
}

// WithStep registers a named Go step handler available to "go" steps.
func WithStep(name string, handler StepHandler) Option {
	return engine.WithStep(name, handler)
}

// Load discovers, compiles, and validates manifests from files, directories, or globs.
func Load(patterns ...string) (*Config, error) {
	return manifest.Load(patterns...)
}

// Parse compiles an in-memory HCL manifest string into a verified Config.
func Parse(source string) (*Config, error) {
	return manifest.Parse(source)
}

// New initializes an executable Engine from a verified Config.
func New(cfg *Config, opts ...Option) (*Engine, error) {
	return engine.New(cfg, opts...)
}

// SQL retrieves an active *sql.DB connection pool by connection name.
func SQL(e *Engine, name string) (*sql.DB, bool) {
	return e.SQL(name)
}

// Valkey retrieves an active Valkey client by connection name.
func Valkey(e *Engine, name string) (valkey.Client, bool) {
	return e.Valkey(name)
}
