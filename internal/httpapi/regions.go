package httpapi

import (
	"net/http"
	"sync/atomic"
)

// Region is one city the instance serves, with its own catalog and, when it
// has them, its own live feed and stop facilities sync.
type Region struct {
	ID       string
	Name     string
	TimeZone string
	Catalogs Catalogs
	// Feed and Demand are nil when the region has no live feed.
	Feed   Feed
	Buses  Buses
	Demand Demand
	// StopInfo is nil when the region has no facilities sync.
	StopInfo StopInfo
	// Map is nil when the region has no offline map.
	Map *Map
}

// Map is the owner's offline map file, passed on to clients as written.
type Map struct {
	URL     string
	Bytes   int64
	Updated string
}

// regionHandlers serves one region's endpoints.
type regionHandlers struct {
	region Region
	docs   *catalogDocuments
	live   *live
	stream *liveStream
	places *placesHandlers
}

func newRegionHandlers(r Region, d Deps, openStreams *atomic.Int64) *regionHandlers {
	return &regionHandlers{
		region: r,
		docs:   &catalogDocuments{catalogs: r.Catalogs, stopInfo: r.StopInfo},
		live:   &live{catalogs: r.Catalogs, feed: r.Feed, buses: r.Buses, demand: r.Demand},
		stream: newLiveStream(r, d, openStreams),
		places: &placesHandlers{places: d.Places, catalogs: r.Catalogs},
	}
}

// handleRegional registers a route under /v1/regions/{region}/ that runs
// against the region named in the path.
func handleRegional(mux *http.ServeMux, byID map[string]*regionHandlers, pattern string, serve func(*regionHandlers, http.ResponseWriter, *http.Request)) {
	mux.HandleFunc("GET /v1/regions/{region}"+pattern, func(w http.ResponseWriter, r *http.Request) {
		rh, ok := byID[r.PathValue("region")]
		if !ok {
			writeProblemDetail(w, http.StatusNotFound, "unknown region")
			return
		}
		serve(rh, w, r)
	})
}
