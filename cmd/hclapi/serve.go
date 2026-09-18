package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/ju4n97/hclapi"
)

func newServeCommand() *cli.Command {
	return &cli.Command{
		Name:      "serve",
		Aliases:   []string{"s"},
		Usage:     "Start the HTTP API server from compiled manifests",
		ArgsUsage: "[manifest paths or globs...]",
		Flags: []cli.Flag{
			&cli.StringSliceFlag{
				Name:    "config",
				Aliases: []string{"c", "manifests", "m"},
				Usage:   "Manifest file, directory, or glob pattern (repeatable)",
				Sources: cli.EnvVars("HCLAPI_CONFIG", "HCLAPI_MANIFESTS"),
			},
			&cli.StringFlag{
				Name:    "host",
				Aliases: []string{"H"},
				Usage:   "Host address to bind the listener (overrides manifest)",
				Sources: cli.EnvVars("HCLAPI_HOST", "HOST"),
			},
			&cli.IntFlag{
				Name:    "port",
				Aliases: []string{"p"},
				Usage:   "Port to bind the listener (overrides manifest)",
				Sources: cli.EnvVars("HCLAPI_PORT", "PORT"),
			},
			&cli.StringFlag{
				Name:    "log-level",
				Usage:   "Log level (debug, info, warn, error)",
				Value:   "info",
				Sources: cli.EnvVars("HCLAPI_LOG_LEVEL"),
			},
			&cli.StringFlag{
				Name:    "log-format",
				Usage:   "Log output format (text, json)",
				Value:   "text",
				Sources: cli.EnvVars("HCLAPI_LOG_FORMAT"),
			},
			&cli.BoolFlag{
				Name:    "verbose",
				Aliases: []string{"v"},
				Usage:   "Enable debug logging (shorthand for --log-level=debug)",
			},
		},
		Action: func(ctx context.Context, cmd *cli.Command) error {
			logger := initLogger(cmd)

			patterns := resolvePatterns(cmd)
			logger.Info("compiling hclapi manifests", "targets", patterns)

			m, err := hclapi.Load(patterns...)
			if err != nil {
				return fmt.Errorf("manifest compilation failed:\n%w", err)
			}

			if cmd.IsSet("host") {
				m.Server.Host = cmd.String("host")
			}
			if cmd.IsSet("port") {
				m.Server.Port = cmd.Int("port")
			}

			// Propagate CLI log flags into manifest telemetry configuration
			if cmd.Bool("verbose") {
				m.Telemetry.LogLevel = "debug"
			} else if cmd.IsSet("log-level") {
				m.Telemetry.LogLevel = cmd.String("log-level")
			}
			if cmd.IsSet("log-format") {
				m.Telemetry.LogFormat = cmd.String("log-format")
			}

			app, err := hclapi.New(m)
			if err != nil {
				return fmt.Errorf("engine initialization failed: %w", err)
			}
			defer func() {
				if closeErr := app.Close(); closeErr != nil {
					logger.Warn("failed to cleanly close connection pools", "error", closeErr)
				}
			}()

			// Align top-level daemon logger with engine telemetry instance
			if app.Telemetry() != nil {
				logger = app.Telemetry().Logger()
			}

			addr := fmt.Sprintf("%s:%d", m.Server.Host, m.Server.Port)
			srv := &http.Server{
				Addr:         addr,
				Handler:      app,
				ReadTimeout:  m.Server.ReadTimeout.Duration(),
				WriteTimeout: m.Server.WriteTimeout.Duration(),
			}

			sigCtx, stopSignals := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
			defer stopSignals()

			errCh := make(chan error, 1)
			go func() {
				logger.Info("server listening", "addr", addr, "routes", len(m.Routes), "connections", len(m.Connections))
				if listenErr := srv.ListenAndServe(); listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
					errCh <- listenErr
				}
			}()

			select {
			case srvErr := <-errCh:
				return fmt.Errorf("server crashed: %w", srvErr)
			case <-sigCtx.Done():
				logger.Info("shutting down server gracefully...")
			}

			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if err := srv.Shutdown(shutdownCtx); err != nil {
				return fmt.Errorf("graceful shutdown failed: %w", err)
			}

			logger.Info("server stopped")
			return nil
		},
	}
}

func initLogger(cmd *cli.Command) *slog.Logger {
	level := slog.LevelInfo
	if cmd.Bool("verbose") || strings.EqualFold(cmd.String("log-level"), "debug") {
		level = slog.LevelDebug
	} else {
		switch strings.ToLower(cmd.String("log-level")) {
		case "warn", "warning":
			level = slog.LevelWarn
		case "error":
			level = slog.LevelError
		}
	}

	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler = slog.NewTextHandler(os.Stdout, opts)
	if strings.EqualFold(cmd.String("log-format"), "json") {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}
