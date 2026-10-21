package httpapi

import (
	"cmp"
	"time"

	"github.com/lscythe/jalvio-service/internal/catalog"
	"github.com/lscythe/jalvio-service/internal/geo"
	"github.com/lscythe/jalvio-service/internal/stopinfo"
)

type catalogResponse struct {
	FeedVersion        string        `json:"feedVersion"`
	FacilitiesSyncedAt time.Time     `json:"facilitiesSyncedAt,omitzero"`
	Routes             []routeItem   `json:"routes"`
	Stops              []stopItem    `json:"stops"`
	Patterns           []patternItem `json:"patterns"`
	Services           []serviceItem `json:"services"`
	Fares              []fareItem    `json:"fares"`
}

type routeItem struct {
	ID        string `json:"id"`
	Code      string `json:"code"`
	Name      string `json:"name"`
	Class     string `json:"class,omitempty"`
	Color     string `json:"color,omitempty"`
	TextColor string `json:"textColor,omitempty"`
	FareID    string `json:"fareId,omitempty"`
}

type stopItem struct {
	ID           string              `json:"id"`
	Code         string              `json:"code,omitempty"`
	Name         string              `json:"name"`
	Kind         string              `json:"kind"`
	Position     position            `json:"position"`
	ParentID     string              `json:"parentId,omitempty"`
	ChildIDs     []string            `json:"childIds,omitempty"`
	PlatformCode string              `json:"platformCode,omitempty"`
	Description  string              `json:"description,omitempty"`
	RouteIDs     []string            `json:"routeIds"`
	TransferIDs  []string            `json:"transferIds,omitempty"`
	Facilities   []stopinfo.Facility `json:"facilities,omitempty"`
}

type position struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

type patternItem struct {
	ID        string        `json:"id"`
	RouteID   string        `json:"routeId"`
	Direction int           `json:"direction"`
	Headsign  string        `json:"headsign,omitempty"`
	Name      string        `json:"name,omitempty"`
	ServiceID string        `json:"serviceId"`
	Stops     []patternStop `json:"stops"`
	Headways  []headwayItem `json:"headways"`
}

type patternStop struct {
	StopID           string `json:"stopId"`
	FromStartSeconds int64  `json:"fromStartSeconds"`
}

type headwayItem struct {
	StartSeconds int64 `json:"startSeconds"`
	EndSeconds   int64 `json:"endSeconds"`
	EverySeconds int64 `json:"everySeconds"`
}

type serviceItem struct {
	ID        string   `json:"id"`
	Days      []string `json:"days"`
	StartDate string   `json:"startDate"`
	EndDate   string   `json:"endDate"`
}

type fareItem struct {
	ID       string `json:"id"`
	Price    int64  `json:"price"`
	Currency string `json:"currency"`
}

type shapesResponse struct {
	FeedVersion string      `json:"feedVersion"`
	Shapes      []shapeItem `json:"shapes"`
}

type shapeItem struct {
	PatternID string `json:"patternId"`
	Polyline  string `json:"polyline"`
}

var kindNames = map[catalog.Kind]string{
	catalog.Station:  "station",
	catalog.Platform: "platform",
	catalog.Roadside: "roadside",
}

var weekOrder = []struct {
	day  time.Weekday
	name string
}{
	{time.Monday, "mon"},
	{time.Tuesday, "tue"},
	{time.Wednesday, "wed"},
	{time.Thursday, "thu"},
	{time.Friday, "fri"},
	{time.Saturday, "sat"},
	{time.Sunday, "sun"},
}

// newCatalogResponse adds facilities from snap, which is nil before the
// first sync or when the sync is disabled.
func newCatalogResponse(c *catalog.Catalog, snap *stopinfo.Snapshot) catalogResponse {
	resp := catalogResponse{
		FeedVersion: c.Version(),
		Routes:      make([]routeItem, 0, len(c.Routes())),
		Stops:       make([]stopItem, 0, len(c.Stops())),
		Patterns:    make([]patternItem, 0, len(c.Trips())),
		Services:    make([]serviceItem, 0, len(c.Services())),
		Fares:       make([]fareItem, 0, len(c.Fares())),
	}
	var infos map[string]stopinfo.Info
	if snap != nil {
		resp.FacilitiesSyncedAt = snap.SyncedAt
		infos = snap.Stops
	}
	for _, r := range c.Routes() {
		resp.Routes = append(resp.Routes, routeItem{
			ID: r.ID, Code: r.Code, Name: r.Name, Class: r.Class,
			Color: r.Color, TextColor: r.TextColor, FareID: r.FareID,
		})
	}
	for _, s := range c.Stops() {
		info := infos[s.ID]
		resp.Stops = append(resp.Stops, stopItem{
			ID:           s.ID,
			Code:         s.Code,
			Name:         s.Name,
			Kind:         kindNames[s.Kind],
			Position:     position{Lat: s.Lat, Lng: s.Lon},
			ParentID:     s.ParentID,
			ChildIDs:     s.ChildIDs,
			PlatformCode: cmp.Or(s.PlatformCode, info.PlatformCode),
			Description:  info.Description,
			RouteIDs:     orEmpty(s.RouteIDs),
			TransferIDs:  s.TransferIDs,
			Facilities:   info.Facilities,
		})
	}
	for _, t := range c.Trips() {
		resp.Patterns = append(resp.Patterns, newPatternItem(t))
	}
	for _, s := range c.Services() {
		item := serviceItem{ID: s.ID, Days: []string{}, StartDate: s.Start.Format(time.DateOnly), EndDate: s.End.Format(time.DateOnly)}
		for _, d := range weekOrder {
			if s.Days[d.day] {
				item.Days = append(item.Days, d.name)
			}
		}
		resp.Services = append(resp.Services, item)
	}
	for _, f := range c.Fares() {
		resp.Fares = append(resp.Fares, fareItem{ID: f.ID, Price: f.Price, Currency: f.Currency})
	}
	return resp
}

func newPatternItem(t catalog.Trip) patternItem {
	item := patternItem{
		ID: t.ID, RouteID: t.RouteID, Direction: t.Direction, Headsign: t.Headsign, Name: t.Name, ServiceID: t.ServiceID,
		Stops:    make([]patternStop, 0, len(t.Stops)),
		Headways: make([]headwayItem, 0, len(t.Headways)),
	}
	for _, s := range t.Stops {
		item.Stops = append(item.Stops, patternStop{StopID: s.StopID, FromStartSeconds: seconds(s.FromStart)})
	}
	for _, h := range t.Headways {
		item.Headways = append(item.Headways, headwayItem{
			StartSeconds: seconds(h.Start), EndSeconds: seconds(h.End), EverySeconds: seconds(h.Every),
		})
	}
	return item
}

func newShapesResponse(c *catalog.Catalog) shapesResponse {
	resp := shapesResponse{FeedVersion: c.Version(), Shapes: make([]shapeItem, 0, len(c.Trips()))}
	for _, t := range c.Trips() {
		resp.Shapes = append(resp.Shapes, shapeItem{PatternID: t.ID, Polyline: geo.EncodePolyline(t.Shape)})
	}
	return resp
}

func seconds(d time.Duration) int64 { return int64(d / time.Second) }

func orEmpty[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
