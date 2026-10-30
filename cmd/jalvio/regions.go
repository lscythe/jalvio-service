package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/lscythe/jalvio-service/internal/catalog"
	"github.com/lscythe/jalvio-service/internal/fleet"
	"github.com/lscythe/jalvio-service/internal/httpapi"
	"github.com/lscythe/jalvio-service/internal/mqttfeed"
	"github.com/lscythe/jalvio-service/internal/regions"
	"github.com/lscythe/jalvio-service/internal/stopapi"
	"github.com/lscythe/jalvio-service/internal/stopinfo"
)

// startRegion loads what r has stored under dataDir/regions/<id>, starts its
// workers, and returns the parts its endpoints serve.
func startRegion(ctx context.Context, r regions.Region, dataDir string, workers *sync.WaitGroup, logger *slog.Logger) (httpapi.Region, error) {
	dir := filepath.Join(dataDir, "regions", r.ID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return httpapi.Region{}, err
	}
	logger = logger.With("region", r.ID)

	fetcher := catalog.NewHTTPFetcher(&http.Client{Timeout: feedFetchTimeout}, r.GTFS.URL, "jalvio-service/"+version)
	catalogs := catalog.NewSource(fetcher, filepath.Join(dir, "gtfs.zip"), logger)
	if err := catalogs.Load(); err != nil {
		logger.Warn("ignoring stored feed", "error", err)
	}
	buses := fleet.New()
	out := httpapi.Region{ID: r.ID, Name: r.Name, TimeZone: r.TimeZone.String(), Catalogs: catalogs, Buses: buses}
	if r.Map != nil {
		out.Map = &httpapi.Map{URL: r.Map.URL, Bytes: r.Map.Bytes, Updated: r.Map.Updated}
	}

	var live *mqttfeed.Feed
	if l := r.Live; l != nil {
		clientID, err := mqttfeed.LoadOrCreateClientID(filepath.Join(dir, "mqtt-client-id"), l.ClientIDPrefix)
		if err != nil {
			return httpapi.Region{}, err
		}
		watched := mqttfeed.NewDemand(time.Now)
		live = mqttfeed.New(mqttfeed.Config{
			URL:           l.MQTTURL,
			TLSServerName: l.TLSServerName,
			DialAddress:   l.DialAddress,
			ClientID:      clientID,
		}, buses, watched, logger)
		// Assigned only with a live feed, so a region without one leaves nil
		// interfaces rather than typed nils.
		out.Feed = live
		out.Demand = watched
	}

	var stops *stopinfo.Syncer
	if s := r.StopFacilities; s != nil {
		deviceID, err := stopapi.LoadOrCreateDeviceID(filepath.Join(dir, "device-id"))
		if err != nil {
			return httpapi.Region{}, err
		}
		api := stopapi.New(&http.Client{Timeout: stopAPITimeout}, stopapi.Config{
			BaseURL:    s.URL,
			AppVersion: s.AppVersion,
			DeviceID:   deviceID,
			UserAgent:  "jalvio-service/" + version,
		})
		stops = stopinfo.New(api, catalogs, stopinfo.Config{
			Path:      filepath.Join(dir, "stopinfo.json"),
			SearchGap: stopSearchGap,
			DetailGap: stopDetailGap,
		}, logger)
		if err := stops.Load(); err != nil {
			logger.Warn("ignoring stored stop facilities", "error", err)
		}
		out.StopInfo = stops
	}

	workers.Go(func() { catalogs.Run(ctx, r.GTFS.Refresh, feedRetryDelay) })
	workers.Go(func() { buses.Run(ctx, busFlushInterval, busExpireEvery) })
	if live != nil {
		workers.Go(func() { live.Run(ctx) })
	}
	if stops != nil {
		at := r.StopFacilities.SyncAt
		syncAt := stopinfo.DailyAt(int(at/time.Hour), int(at%time.Hour/time.Minute), r.TimeZone)
		workers.Go(func() {
			// The sync matches against the catalog, so it waits for one.
			select {
			case <-catalogs.Loaded():
				stops.Run(ctx, syncAt, stopRetryDelay)
			case <-ctx.Done():
			}
		})
	}
	return out, nil
}
