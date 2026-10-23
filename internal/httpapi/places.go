package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/lscythe/jalvio-service/internal/geo"
	"github.com/lscythe/jalvio-service/internal/places"
)

// osmAttribution is required wherever OpenStreetMap data is shown.
const osmAttribution = "© OpenStreetMap contributors"

type placesHandlers struct {
	places Places
}

type placesResponse struct {
	Attribution string      `json:"attribution"`
	Places      []placeItem `json:"places"`
}

type reverseResponse struct {
	Attribution string    `json:"attribution"`
	Place       placeItem `json:"place"`
}

type placeItem struct {
	ID       string   `json:"id,omitempty"`
	Name     string   `json:"name"`
	Label    string   `json:"label,omitempty"`
	Kind     string   `json:"kind,omitempty"`
	Position position `json:"position"`
}

func newPlaceItem(p places.Place) placeItem {
	return placeItem{
		ID: p.ID, Name: p.Name, Label: p.Label, Kind: p.Kind,
		Position: position{Lat: p.Position.Lat, Lng: p.Position.Lon},
	}
}

func (h *placesHandlers) serveSearch(w http.ResponseWriter, r *http.Request) {
	if h.places == nil {
		writeProblemDetail(w, http.StatusServiceUnavailable, "place search is disabled")
		return
	}
	q := r.URL.Query()
	var near *geo.Point
	if q.Has("near") {
		p, err := parsePoint(q.Get("near"))
		if err != nil {
			writeBadRequest(w, "near must be lat,lng")
			return
		}
		near = &p
	}
	var limit int
	if q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n < 1 || n > places.MaxLimit {
			writeBadRequest(w, "limit must be 1 to 20")
			return
		}
		limit = n
	}
	found, err := h.places.Search(r.Context(), q.Get("q"), near, limit)
	if errors.Is(err, places.ErrQueryTooShort) {
		writeBadRequest(w, "q must have at least 3 characters")
		return
	}
	if err != nil {
		writePlacesError(w, err)
		return
	}
	resp := placesResponse{Attribution: osmAttribution, Places: make([]placeItem, 0, len(found))}
	for _, p := range found {
		resp.Places = append(resp.Places, newPlaceItem(p))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (h *placesHandlers) serveReverse(w http.ResponseWriter, r *http.Request) {
	if h.places == nil {
		writeProblemDetail(w, http.StatusServiceUnavailable, "place search is disabled")
		return
	}
	at, err := parsePoint(r.URL.Query().Get("at"))
	if err != nil {
		writeBadRequest(w, "at must be lat,lng")
		return
	}
	p, err := h.places.Reverse(r.Context(), at)
	if err != nil {
		writePlacesError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, reverseResponse{Attribution: osmAttribution, Place: newPlaceItem(p)})
}

// writePlacesError keeps upstream details out of the response.
func writePlacesError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, places.ErrNotFound):
		writeProblem(w, http.StatusNotFound)
	case errors.Is(err, places.ErrRateLimited):
		writeProblem(w, http.StatusTooManyRequests)
	default:
		writeProblem(w, http.StatusBadGateway)
	}
}

var errPoint = errors.New("point must be lat,lng")

func parsePoint(s string) (geo.Point, error) {
	lat, lng, ok := strings.Cut(s, ",")
	if !ok {
		return geo.Point{}, errPoint
	}
	p := geo.Point{}
	var err error
	if p.Lat, err = strconv.ParseFloat(lat, 64); err != nil {
		return geo.Point{}, errPoint
	}
	if p.Lon, err = strconv.ParseFloat(lng, 64); err != nil {
		return geo.Point{}, errPoint
	}
	if p.Lat < -90 || p.Lat > 90 || p.Lon < -180 || p.Lon > 180 {
		return geo.Point{}, errPoint
	}
	return p, nil
}
