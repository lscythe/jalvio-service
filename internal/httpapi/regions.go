package httpapi

import (
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
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

type regionListResponse struct {
	Regions []regionItem `json:"regions"`
}

type regionItem struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	TimeZone string      `json:"timeZone"`
	Bounds   boundsItem  `json:"bounds"`
	Live     bool        `json:"live"`
	Catalog  catalogLink `json:"catalog"`
	Shapes   link        `json:"shapes"`
	Map      *mapItem    `json:"map,omitempty"`
}

type boundsItem struct {
	SouthWest position `json:"southWest"`
	NorthEast position `json:"northEast"`
}

type catalogLink struct {
	URL       string    `json:"url"`
	Version   string    `json:"version"`
	UpdatedAt time.Time `json:"updatedAt,omitzero"`
}

type link struct {
	URL string `json:"url"`
}

type mapItem struct {
	URL     string `json:"url"`
	Bytes   int64  `json:"bytes"`
	Updated string `json:"updated"`
}

// regionList serves /v1/regions, encoding it again only when a catalog
// changes.
type regionList struct {
	regions []*regionHandlers
	doc     cachedDocument
}

func (l *regionList) serve(w http.ResponseWriter, r *http.Request) {
	resp := regionListResponse{Regions: make([]regionItem, 0, len(l.regions))}
	var key strings.Builder
	for _, rh := range l.regions {
		item, ok, err := rh.listItem()
		if err != nil {
			writeProblem(w, http.StatusInternalServerError)
			return
		}
		if !ok {
			writeProblemDetail(w, http.StatusServiceUnavailable, "region "+rh.region.ID+" has no catalog yet")
			return
		}
		resp.Regions = append(resp.Regions, item)
		fmt.Fprintf(&key, "%s %s %s|", item.ID, item.Catalog.Version, item.Catalog.UpdatedAt.Format(time.RFC3339Nano))
	}
	l.doc.serve(w, r, key.String(), func() any { return resp })
}

func (rh *regionHandlers) listItem() (regionItem, bool, error) {
	r := rh.region
	// One catalog describes the item, so a refresh mid-request cannot pair
	// old bounds with the new version.
	c := r.Catalogs.Current()
	if c == nil {
		return regionItem{}, false, nil
	}
	etag, err := rh.docs.etag(c)
	if err != nil {
		return regionItem{}, false, err
	}
	b := c.Bounds()
	base := "/v1/regions/" + r.ID
	item := regionItem{
		ID:       r.ID,
		Name:     r.Name,
		TimeZone: r.TimeZone,
		Bounds:   boundsItem{SouthWest: position{Lat: b.South, Lng: b.West}, NorthEast: position{Lat: b.North, Lng: b.East}},
		Live:     r.Feed != nil,
		Catalog:  catalogLink{URL: base + "/catalog", Version: etag, UpdatedAt: r.Catalogs.Status().UpdatedAt.UTC()},
		Shapes:   link{URL: base + "/catalog/shapes"},
	}
	if r.Map != nil {
		item.Map = &mapItem{URL: r.Map.URL, Bytes: r.Map.Bytes, Updated: r.Map.Updated}
	}
	return item, true, nil
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
