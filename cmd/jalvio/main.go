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
	"github.com/lscythe/jalvio-service/internal/fleet"
	"github.com/lscythe/jalvio-service/internal/httpapi"
	"github.com/lscythe/jalvio-service/internal/mqttfeed"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const (
	shutdownTimeout  = 10 * time.Second
	feedFetchTimeout = 2 * time.Minute
	feedRetryDelay   = time.Minute
	busFlushInterval = time.Second
	busExpireEvery   = 5 * time.Second
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

	buses := fleet.New()
	var feed httpapi.Feed
	var live *mqttfeed.Feed
	if cfg.MQTTURL != "" {
		clientID, err := mqttfeed.LoadOrCreateClientID(filepath.Join(cfg.DataDir, "mqtt-client-id"))
		if err != nil {
			return errors.Join(err, ln.Close())
		}
		live = mqttfeed.New(mqttfeed.Config{
			URL:           cfg.MQTTURL,
			TLSServerName: cfg.MQTTServerName,
			DialAddress:   cfg.MQTTDialAddress,
			ClientID:      clientID,
		}, buses, logger)
		feed = live
	}

	var draining atomic.Bool
	handler := httpapi.New(httpapi.Deps{
		Info:     httpapi.Info{Version: version, StartedAt: time.Now().UTC()},
		Ready:    func() bool { return !draining.Load() && catalogs.Current() != nil },
		Catalogs: catalogs,
		Feed:     feed,
		Buses:    buses,
	})

	var workers sync.WaitGroup
	workers.Go(func() { catalogs.Run(ctx, cfg.GTFSRefresh, feedRetryDelay) })
	workers.Go(func() { buses.Run(ctx, busFlushInterval, busExpireEvery) })
	if live != nil {
		workers.Go(func() { live.Run(ctx) })
	}
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
