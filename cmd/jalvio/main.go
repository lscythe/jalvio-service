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
	_ "time/tzdata" // lets TZ name a zone in images without zoneinfo

	"github.com/lscythe/jalvio-service/internal/catalog"
	"github.com/lscythe/jalvio-service/internal/config"
	"github.com/lscythe/jalvio-service/internal/fleet"
	"github.com/lscythe/jalvio-service/internal/geo"
	"github.com/lscythe/jalvio-service/internal/httpapi"
	"github.com/lscythe/jalvio-service/internal/mqttfeed"
	"github.com/lscythe/jalvio-service/internal/places"
	"github.com/lscythe/jalvio-service/internal/stopapi"
	"github.com/lscythe/jalvio-service/internal/stopinfo"
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
	// placesAreaMargin (about 10 km) keeps destinations just past the last
	// stop searchable.
	placesAreaMargin = 0.1
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
	fetcher := catalog.NewHTTPFetcher(&http.Client{Timeout: feedFetchTimeout}, cfg.GTFSURL, "jalvio-service/"+version)
	catalogs := catalog.NewSource(fetcher, filepath.Join(cfg.DataDir, "gtfs.zip"), logger)
	if err := catalogs.Load(); err != nil {
		logger.Warn("ignoring stored feed", "error", err)
	}

	buses := fleet.New()
	var feed httpapi.Feed
	var live *mqttfeed.Feed
	if cfg.MQTTURL != "" {
		clientID, err := mqttfeed.LoadOrCreateClientID(filepath.Join(cfg.DataDir, "mqtt-client-id"), cfg.MQTTClientIDPrefix)
		if err != nil {
			return errors.Join(err, ln.Close())
		}
		live = mqttfeed.New(mqttfeed.Config{
			URL:           cfg.MQTTURL,
			TLSServerName: cfg.MQTTServerName,
			DialAddress:   cfg.MQTTDialAddress,
			ClientID:      clientID,
		}, buses, mqttfeed.NewDemand(time.Now), logger)
		feed = live
	}

	var stopInfo httpapi.StopInfo
	var stops *stopinfo.Syncer
	if cfg.StopAPIURL != "" {
		deviceID, err := stopapi.LoadOrCreateDeviceID(filepath.Join(cfg.DataDir, "device-id"))
		if err != nil {
			return errors.Join(err, ln.Close())
		}
		api := stopapi.New(&http.Client{Timeout: stopAPITimeout}, stopapi.Config{
			BaseURL:    cfg.StopAPIURL,
			AppVersion: cfg.StopAPIAppVersion,
			DeviceID:   deviceID,
			UserAgent:  "jalvio-service/" + version,
		})
		stops = stopinfo.New(api, catalogs, stopinfo.Config{
			Path:      filepath.Join(cfg.DataDir, "stopinfo.json"),
			SearchGap: stopSearchGap,
			DetailGap: stopDetailGap,
		}, logger)
		if err := stops.Load(); err != nil {
			logger.Warn("ignoring stored stop facilities", "error", err)
		}
		stopInfo = stops
	}

	var placeSearch httpapi.Places
	if cfg.PhotonURL != "" {
		photon := places.NewPhoton(&http.Client{Timeout: placesTimeout}, cfg.PhotonURL, cfg.PhotonUserAgent)
		area := func() (geo.BBox, bool) {
			c := catalogs.Current()
			if c == nil {
				return geo.BBox{}, false
			}
			return c.Bounds().Pad(placesAreaMargin), true
		}
		placeSearch = places.NewService(photon, area, places.Config{
			RequestsPerMinute: cfg.PlacesRequestsPerMinute,
			CacheSize:         placesCacheSize,
			CacheTTL:          placesCacheTTL,
			Timeout:           placesTimeout,
		})
	}

	var draining atomic.Bool
	handler := httpapi.New(httpapi.Deps{
		Info:     httpapi.Info{Version: version, StartedAt: time.Now().UTC()},
		Ready:    func() bool { return !draining.Load() && catalogs.Current() != nil },
		Catalogs: catalogs,
		Feed:     feed,
		Buses:    buses,
		StopInfo: stopInfo,
		Places:   placeSearch,
		Live:     httpapi.LiveConfig{MaxConnections: cfg.MaxLiveConnections, BrowserOrigins: cfg.BrowserOrigins},
		Done:     ctx.Done(),
	})

	var workers sync.WaitGroup
	workers.Go(func() { catalogs.Run(ctx, cfg.GTFSRefresh, feedRetryDelay) })
	workers.Go(func() { buses.Run(ctx, busFlushInterval, busExpireEvery) })
	if live != nil {
		workers.Go(func() { live.Run(ctx) })
	}
	if stops != nil {
		syncAt := stopinfo.DailyAt(int(cfg.StopInfoSyncAt/time.Hour), int(cfg.StopInfoSyncAt%time.Hour/time.Minute))
		workers.Go(func() {
			// The sync matches against the catalog, so it waits for one.
			select {
			case <-catalogs.Loaded():
				stops.Run(ctx, syncAt, stopRetryDelay)
			case <-ctx.Done():
			}
		})
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
