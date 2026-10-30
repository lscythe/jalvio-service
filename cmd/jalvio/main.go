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
	_ "time/tzdata" // lets a region name its time zone in images without zoneinfo

	"github.com/lscythe/jalvio-service/internal/config"
	"github.com/lscythe/jalvio-service/internal/httpapi"
	"github.com/lscythe/jalvio-service/internal/legal"
	"github.com/lscythe/jalvio-service/internal/places"
	"github.com/lscythe/jalvio-service/internal/regions"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const (
	shutdownTimeout    = 10 * time.Second
	healthcheckTimeout = 3 * time.Second
	feedFetchTimeout   = 2 * time.Minute
	feedRetryDelay     = time.Minute
	busFlushInterval   = time.Second
	busExpireEvery     = 5 * time.Second
	stopAPITimeout     = 15 * time.Second
	stopRetryDelay     = 15 * time.Minute
	placesTimeout      = 3 * time.Second
	placesCacheSize    = 10000
	placesCacheTTL     = 24 * time.Hour
)

// Upstream request spacing for the stop facilities sync; tests shorten it.
var (
	stopSearchGap = 5 * time.Second
	stopDetailGap = time.Second
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
	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		return errors.Join(err, ln.Close())
	}
	legalDocs, err := legal.Load(cfg.LegalFile)
	if err != nil {
		return errors.Join(err, ln.Close())
	}
	configured, err := regions.Load(cfg.RegionsFile)
	if err != nil {
		return errors.Join(err, ln.Close())
	}

	// Workers stop before runService returns, including when a later region
	// fails to start.
	var workers sync.WaitGroup
	defer workers.Wait()
	ctx, stopWorkers := context.WithCancel(ctx)
	defer stopWorkers()
	served := make([]httpapi.Region, 0, len(configured))
	for _, r := range configured {
		region, err := startRegion(ctx, r, cfg.DataDir, &workers, logger)
		if err != nil {
			return errors.Join(err, ln.Close())
		}
		served = append(served, region)
	}

	var placeSearch httpapi.Places
	if cfg.PhotonURL != "" {
		photon := places.NewPhoton(&http.Client{Timeout: placesTimeout}, cfg.PhotonURL, cfg.PhotonUserAgent)
		placeSearch = places.NewService(photon, places.Config{
			RequestsPerMinute: cfg.PlacesRequestsPerMinute,
			CacheSize:         placesCacheSize,
			CacheTTL:          placesCacheTTL,
			Timeout:           placesTimeout,
		})
	}

	var draining atomic.Bool
	handler := httpapi.New(httpapi.Deps{
		Info:    httpapi.Info{Version: version, StartedAt: time.Now().UTC()},
		Ready:   func() bool { return !draining.Load() && everyCatalogLoaded(served) },
		Regions: served,
		Places:  placeSearch,
		Legal:   legalDocs,
		Live:    httpapi.LiveConfig{MaxConnections: cfg.MaxLiveConnections, BrowserOrigins: cfg.BrowserOrigins},
		Done:    ctx.Done(),
	})
	return serve(ctx, ln, handler, &draining, logger)
}

func everyCatalogLoaded(served []httpapi.Region) bool {
	for _, r := range served {
		if r.Catalogs.Current() == nil {
			return false
		}
	}
	return true
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
