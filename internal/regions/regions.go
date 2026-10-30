// Package regions loads the regions an instance owner serves.
package regions

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	defaultRefresh        = 24 * time.Hour
	defaultClientIDPrefix = "jalvio-"
	defaultSyncAt         = "03:00"
	maxIDLength           = 32
)

// id is a region's path segment and folder name, so it is a lowercase slug.
var id = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

type Region struct {
	ID       string
	Name     string
	TimeZone *time.Location
	GTFS     GTFS
	// Live is nil when the region has no live feed.
	Live *Live
	// StopFacilities is nil when the region has no facilities sync.
	StopFacilities *StopFacilities
	// Map is nil when the region has no offline map.
	Map *Map
}

type GTFS struct {
	URL     string
	Refresh time.Duration
}

type Live struct {
	MQTTURL        string
	TLSServerName  string
	DialAddress    string
	ClientIDPrefix string
}

type StopFacilities struct {
	URL        string
	AppVersion string
	// SyncAt is the daily sync time as an offset from midnight in the
	// region's time zone.
	SyncAt time.Duration
}

// Map is the owner's offline map file, passed on to clients as written.
type Map struct {
	URL     string
	Bytes   int64
	Updated string
}

type fileJSON struct {
	Regions []regionJSON `json:"regions"`
}

type regionJSON struct {
	ID             string              `json:"id"`
	Name           string              `json:"name"`
	TimeZone       string              `json:"timeZone"`
	GTFS           gtfsJSON            `json:"gtfs"`
	Live           *liveJSON           `json:"live"`
	StopFacilities *stopFacilitiesJSON `json:"stopFacilities"`
	Map            *mapJSON            `json:"map"`
}

type gtfsJSON struct {
	URL     string `json:"url"`
	Refresh string `json:"refresh"`
}

type liveJSON struct {
	MQTTURL        string `json:"mqttUrl"`
	TLSServerName  string `json:"tlsServerName"`
	DialAddress    string `json:"dialAddress"`
	ClientIDPrefix string `json:"clientIdPrefix"`
}

type stopFacilitiesJSON struct {
	URL        string `json:"url"`
	AppVersion string `json:"appVersion"`
	SyncAt     string `json:"syncAt"`
}

type mapJSON struct {
	URL     string `json:"url"`
	Bytes   int64  `json:"bytes"`
	Updated string `json:"updated"`
}

// Load reads and checks the owner's regions file, which must exist.
func Load(path string) ([]Region, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if err != nil {
		return nil, err
	}
	var f fileJSON
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if len(f.Regions) == 0 {
		return nil, fmt.Errorf("%s: regions: at least one region is required", path)
	}
	seen := map[string]bool{}
	out := make([]Region, 0, len(f.Regions))
	for i, rj := range f.Regions {
		field := fmt.Sprintf("regions[%d]", i)
		r, err := rj.region(field)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if seen[r.ID] {
			return nil, fmt.Errorf("%s: %s.id: %q is used by an earlier region", path, field, r.ID)
		}
		seen[r.ID] = true
		out = append(out, r)
	}
	return out, nil
}

func (rj regionJSON) region(field string) (Region, error) {
	if !id.MatchString(rj.ID) || len(rj.ID) > maxIDLength {
		return Region{}, fmt.Errorf("%s.id: %q is not a lowercase slug of at most %d characters", field, rj.ID, maxIDLength)
	}
	if strings.TrimSpace(rj.Name) == "" {
		return Region{}, fmt.Errorf("%s.name: required", field)
	}
	if rj.TimeZone == "" {
		return Region{}, fmt.Errorf("%s.timeZone: required", field)
	}
	loc, err := time.LoadLocation(rj.TimeZone)
	if err != nil {
		return Region{}, fmt.Errorf("%s.timeZone: %w", field, err)
	}
	r := Region{ID: rj.ID, Name: rj.Name, TimeZone: loc}

	if !isURL(rj.GTFS.URL, "http", "https") {
		return Region{}, fmt.Errorf("%s.gtfs.url: %q is not an http(s) URL", field, rj.GTFS.URL)
	}
	refresh, err := positiveDuration(rj.GTFS.Refresh, defaultRefresh)
	if err != nil {
		return Region{}, fmt.Errorf("%s.gtfs.refresh: %w", field, err)
	}
	r.GTFS = GTFS{URL: rj.GTFS.URL, Refresh: refresh}

	if l := rj.Live; l != nil {
		if !isURL(l.MQTTURL, "ws", "wss") {
			return Region{}, fmt.Errorf("%s.live.mqttUrl: %q is not a ws(s) URL", field, l.MQTTURL)
		}
		if l.DialAddress != "" {
			if _, _, err := net.SplitHostPort(l.DialAddress); err != nil {
				return Region{}, fmt.Errorf("%s.live.dialAddress: %w", field, err)
			}
		}
		r.Live = &Live{MQTTURL: l.MQTTURL, TLSServerName: l.TLSServerName, DialAddress: l.DialAddress, ClientIDPrefix: valueOr(l.ClientIDPrefix, defaultClientIDPrefix)}
	}

	if s := rj.StopFacilities; s != nil {
		if !isURL(s.URL, "http", "https") {
			return Region{}, fmt.Errorf("%s.stopFacilities.url: %q is not an http(s) URL", field, s.URL)
		}
		if s.AppVersion == "" {
			return Region{}, fmt.Errorf("%s.stopFacilities.appVersion: required", field)
		}
		at, err := time.Parse("15:04", valueOr(s.SyncAt, defaultSyncAt))
		if err != nil {
			return Region{}, fmt.Errorf("%s.stopFacilities.syncAt: %q is not a HH:MM time", field, s.SyncAt)
		}
		r.StopFacilities = &StopFacilities{
			URL:        s.URL,
			AppVersion: s.AppVersion,
			SyncAt:     time.Duration(at.Hour())*time.Hour + time.Duration(at.Minute())*time.Minute,
		}
	}

	if m := rj.Map; m != nil {
		if !isURL(m.URL, "http", "https") && !strings.HasPrefix(m.URL, "/") {
			return Region{}, fmt.Errorf("%s.map.url: %q is not an http(s) URL or an absolute path", field, m.URL)
		}
		if m.Bytes <= 0 {
			return Region{}, fmt.Errorf("%s.map.bytes: must be positive", field)
		}
		if _, err := time.Parse(time.DateOnly, m.Updated); err != nil {
			return Region{}, fmt.Errorf("%s.map.updated: %q is not a YYYY-MM-DD date", field, m.Updated)
		}
		r.Map = &Map{URL: m.URL, Bytes: m.Bytes, Updated: m.Updated}
	}
	return r, nil
}

func isURL(raw string, schemes ...string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	for _, s := range schemes {
		if u.Scheme == s {
			return true
		}
	}
	return false
}

func positiveDuration(raw string, fallback time.Duration) (time.Duration, error) {
	if raw == "" {
		return fallback, nil
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return 0, errors.New("not a positive duration")
	}
	return d, nil
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
