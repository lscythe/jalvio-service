package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/lscythe/jalvio-service/internal/config"
	"github.com/lscythe/jalvio-service/internal/httpapi"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const shutdownTimeout = 10 * time.Second

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(ctx, os.Getenv, logger); err != nil {
		logger.Error("exit", "error", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, getenv func(string) string, logger *slog.Logger) error {
	cfg, err := config.Load(getenv)
	if err != nil {
		return err
	}
	ln, err := new(net.ListenConfig).Listen(ctx, "tcp", cfg.Listen)
	if err != nil {
		return err
	}
	return serve(ctx, ln, logger)
}

// serve handles requests on ln until ctx is cancelled, then reports unready
// and drains in-flight requests before returning.
func serve(ctx context.Context, ln net.Listener, logger *slog.Logger) error {
	var ready atomic.Bool
	srv := &http.Server{
		Handler:           httpapi.New(httpapi.Info{Version: version, StartedAt: time.Now().UTC()}, ready.Load),
		ReadHeaderTimeout: 5 * time.Second,
	}

	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	ready.Store(true)
	logger.Info("listening", "addr", ln.Addr().String(), "version", version)

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}

	ready.Store(false)
	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-served; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
