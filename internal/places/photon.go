package places

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"github.com/lscythe/jalvio-service/internal/geo"
)

const maxPhotonResponse = 1 << 20

// Photon is a Provider backed by a Photon geocoder.
type Photon struct {
	http      *http.Client
	baseURL   string
	userAgent string
}

func NewPhoton(httpClient *http.Client, baseURL, userAgent string) *Photon {
	return &Photon{http: httpClient, baseURL: strings.TrimRight(baseURL, "/"), userAgent: userAgent}
}

func (p *Photon) Search(ctx context.Context, q Query) ([]Place, error) {
	v := url.Values{"q": {q.Text}, "limit": {strconv.Itoa(q.Limit)}}
	if q.Near != nil {
		v.Set("lat", formatCoord(q.Near.Lat))
		v.Set("lon", formatCoord(q.Near.Lon))
	}
	if q.Area != (geo.BBox{}) {
		v.Set("bbox", strings.Join([]string{
			formatCoord(q.Area.West), formatCoord(q.Area.South), formatCoord(q.Area.East), formatCoord(q.Area.North),
		}, ","))
	}
	return p.get(ctx, "/api?"+v.Encode())
}

func (p *Photon) Reverse(ctx context.Context, at geo.Point) (Place, error) {
	v := url.Values{"lat": {formatCoord(at.Lat)}, "lon": {formatCoord(at.Lon)}}
	found, err := p.get(ctx, "/reverse?"+v.Encode())
	if err != nil {
		return Place{}, err
	}
	if len(found) == 0 {
		return Place{}, ErrNotFound
	}
	return found[0], nil
}

type featureCollection struct {
	Features []struct {
		Geometry struct {
			Coordinates [2]float64 `json:"coordinates"`
		} `json:"geometry"`
		Properties properties `json:"properties"`
	} `json:"features"`
}

type properties struct {
	OSMType     string `json:"osm_type"`
	OSMID       int64  `json:"osm_id"`
	OSMValue    string `json:"osm_value"`
	Name        string `json:"name"`
	HouseNumber string `json:"housenumber"`
	Street      string `json:"street"`
	District    string `json:"district"`
	City        string `json:"city"`
	State       string `json:"state"`
}

func (p *Photon) get(ctx context.Context, path string) ([]Place, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", p.userAgent)
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %s", resp.Status)
	}
	var fc featureCollection
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxPhotonResponse)).Decode(&fc); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	found := make([]Place, 0, len(fc.Features))
	for _, f := range fc.Features {
		found = append(found, f.Properties.place(geo.Point{Lat: f.Geometry.Coordinates[1], Lon: f.Geometry.Coordinates[0]}))
	}
	return found, nil
}

func (pr properties) place(at geo.Point) Place {
	street := strings.TrimSpace(pr.Street + " " + pr.HouseNumber)
	name := pr.Name
	if name == "" {
		name = street
	}
	var label []string
	for _, part := range []string{street, pr.District, pr.City, pr.State} {
		if part != "" && part != name && !slices.Contains(label, part) {
			label = append(label, part)
		}
	}
	var id string
	if pr.OSMType != "" {
		id = "osm:" + pr.OSMType + strconv.FormatInt(pr.OSMID, 10)
	}
	return Place{ID: id, Name: name, Label: strings.Join(label, ", "), Kind: pr.OSMValue, Position: at}
}

func formatCoord(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
