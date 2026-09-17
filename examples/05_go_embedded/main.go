package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ju4n97/hclapi"
)

// marsOrbitalPeriodRatio is how many Earth years it takes Mars to orbit the Sun once.
const marsOrbitalPeriodRatio = 1.8808

func main() {
	if err := run(); err != nil {
		slog.Error("service terminated", "error", err)
		os.Exit(1)
	}
}

func run() error {
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))

	config, err := hclapi.Load(".")
	if err != nil {
		return fmt.Errorf("load manifests: %w", err)
	}

	marsAgeHandler := func(ctx context.Context, step *hclapi.Step) (any, error) {
		earthYears, ok := step.Args.Get[float64]("earth_years")
		if !ok {
			return nil, errors.New("missing or invalid 'earth_years' argument")
		}

		return map[string]any{
			"earth_years": earthYears,
			"mars_years":  earthYears / marsOrbitalPeriodRatio,
		}, nil
	}

	engine, err := hclapi.New(config,
		hclapi.WithLogger(logger),
		hclapi.WithStep("astronomy.mars_age", marsAgeHandler),
	)
	if err != nil {
		return fmt.Errorf("initialize engine: %w", err)
	}
	defer engine.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})
	mux.Handle("/", engine)

	server := &http.Server{
		Addr:         fmt.Sprintf("%s:%d", config.Server.Host, config.Server.Port),
		Handler:      mux,
		ReadTimeout:  config.Server.ReadTimeout.Duration(),
		WriteTimeout: config.Server.WriteTimeout.Duration(),
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	errCh := make(chan error, 1)
	go func() {
		logger.Info("server listening", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		return err
	case <-stop:
		logger.Info("shutting down server...")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	return server.Shutdown(shutdownCtx)
}
