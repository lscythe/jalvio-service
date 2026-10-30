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
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/lscythe/jalvio-service/internal/community/accounts"
	"github.com/lscythe/jalvio-service/internal/community/config"
	"github.com/lscythe/jalvio-service/internal/community/google"
	"github.com/lscythe/jalvio-service/internal/community/httpapi"
	"github.com/lscythe/jalvio-service/internal/community/store"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const (
	shutdownTimeout    = 10 * time.Second
	healthcheckTimeout = 3 * time.Second
	migrateTimeout     = 30 * time.Second
	readyTimeout       = time.Second
	googleKeysTimeout  = 5 * time.Second
	retentionInterval  = 24 * time.Hour
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(ctx, os.Args[1:], os.Getenv, logger); err != nil {
		logger.Error("exit", "error", err)
		os.Exit(1)
	}
}

// run serves by default; "healthcheck" instead probes a running instance,
// for container images that have no HTTP client.
func run(ctx context.Context, args []string, getenv func(string) string, logger *slog.Logger) error {
	if len(args) > 0 {
		if args[0] != "healthcheck" {
			return fmt.Errorf("unknown command %q", args[0])
		}
		return healthcheck(ctx, config.ListenAddress(getenv))
	}
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
	db, err := store.Open(ctx, cfg.DatabaseURL, cfg.MaxConns)
	if err != nil {
		return errors.Join(err, ln.Close())
	}
	defer db.Close()
	migrateCtx, cancel := context.WithTimeout(ctx, migrateTimeout)
	err = db.Migrate(migrateCtx)
	cancel()
	if err != nil {
		return errors.Join(err, ln.Close())
	}

	// Assigned only with client IDs, so disabled sign-in leaves a nil
	// interface rather than a typed nil.
	var verifier accounts.GoogleVerifier
	if len(cfg.GoogleClientIDs) > 0 {
		verifier = google.NewVerifier(&http.Client{Timeout: googleKeysTimeout}, cfg.GoogleKeysURL, cfg.GoogleClientIDs, time.Now)
	}
	service := accounts.New(db, verifier, time.Now)

	var draining atomic.Bool
	handler := httpapi.New(httpapi.Deps{
		Accounts: service,
		Ready: func() bool {
			if draining.Load() {
				return false
			}
			pingCtx, cancel := context.WithTimeout(ctx, readyTimeout)
			defer cancel()
			return db.Ping(pingCtx) == nil
		},
		AccountsPerHour: cfg.AccountsPerHour,
		Clock:           time.Now,
		Logger:          logger,
	})

	var workers sync.WaitGroup
	workers.Go(func() { service.RunRetention(ctx, retentionInterval, logger) })
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

// healthcheck succeeds when the instance listening on addr reports ready.
func healthcheck(ctx context.Context, addr string) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "" {
		host = "127.0.0.1"
	}
	ctx, cancel := context.WithTimeout(ctx, healthcheckTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort(host, port)+"/healthz", nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("not ready: %s", resp.Status)
	}
	return nil
}
