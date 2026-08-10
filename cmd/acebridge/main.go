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

	"github.com/anubisreal/acebridge/internal/app"
)

var (
	version = "dev"
	commit  = "none"
	builtAt = "unknown"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "version", "--version", "-v":
			fmt.Printf("AceBridge %s (commit %s, built %s)\n", version, commit, builtAt)
			return
		case "healthcheck":
			if err := checkHealth(); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := app.ConfigFromEnv()

	application, err := app.New(cfg, logger)
	if err != nil {
		logger.Error("cannot start application", "error", err)
		os.Exit(1)
	}
	defer application.Close()

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           application.Routes(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		logger.Info("AceBridge ready", "address", cfg.ListenAddr, "version", version, "commit", commit)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server stopped", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}

func checkHealth() error {
	endpoint := os.Getenv("ACEBRIDGE_HEALTHCHECK_URL")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:8080/health/live"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(endpoint)
	if err != nil {
		return fmt.Errorf("AceBridge healthcheck failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("AceBridge healthcheck returned %s", response.Status)
	}
	return nil
}
