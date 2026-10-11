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
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/lscythe/jalvio-service/internal/catalog"
	"github.com/lscythe/jalvio-service/internal/config"
	"github.com/lscythe/jalvio-service/internal/httpapi"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const (
	shutdownTimeout  = 10 * time.Second
	feedFetchTimeout = 2 * time.Minute
	feedRetryDelay   = time.Minute
)

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
	return runService(ctx, cfg, ln, logger)
}

func runService(ctx context.Context, cfg config.Config, ln net.Listener, logger *slog.Logger) error {
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return errors.Join(err, ln.Close())
	}
	fetcher := catalog.NewHTTPFetcher(&http.Client{Timeout: feedFetchTimeout}, cfg.GTFSURL, "jalvio-service/"+version)
	catalogs := catalog.NewSource(fetcher, filepath.Join(cfg.DataDir, "gtfs.zip"), logger)
	if err := catalogs.Load(); err != nil {
		logger.Warn("ignoring stored feed", "error", err)
	}

	var draining atomic.Bool
	ready := func() bool { return !draining.Load() && catalogs.Current() != nil }
	handler := httpapi.New(httpapi.Info{Version: version, StartedAt: time.Now().UTC()}, ready, catalogs)

	var workers sync.WaitGroup
	workers.Go(func() { catalogs.Run(ctx, cfg.GTFSRefresh, feedRetryDelay) })
	defer workers.Wait()

	return serve(ctx, ln, handler, &draining, logger)
}

// serve handles requests on ln until ctx is cancelled, then sets draining and
// finishes in-flight requests before returning.
func serve(ctx context.Context, ln net.Listener, handler http.Handler, draining *atomic.Bool, logger *slog.Logger) error {
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(ln) }()
	logger.Info("listening", "addr", ln.Addr().String(), "version", version)

	select {
	case err := <-served:
		return err
	case <-ctx.Done():
	}

	draining.Store(true)
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
