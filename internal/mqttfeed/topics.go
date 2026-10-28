package mqttfeed

import (
	"fmt"
	"math"
	"regexp"

	"github.com/lscythe/jalvio-service/internal/geo"
)

const WholeFleet = "/mobile_armada/#"

// Interest is what clients watch: map areas, routes by code, and buses by
// fleet number.
type Interest struct {
	Areas        []geo.BBox
	RouteCodes   []string
	FleetNumbers []string
}

var topicLevel = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// TileFilters covers box with the broker's 0.01° tile topics. A topic's tile
// can be one off the bus position in either direction, so the range is
// padded by one tile.
func TileFilters(box geo.BBox) []string {
	r := tilesOf(box)
	filters := make([]string, 0, r.count())
	for row := r.firstRow; row <= r.lastRow; row++ {
		for col := r.firstCol; col <= r.lastCol; col++ {
			filters = append(filters, fmt.Sprintf("/mobile_armada/%04d-%04d/#", row, col))
		}
	}
	return filters
}

type tileRange struct{ firstRow, lastRow, firstCol, lastCol int }

func tilesOf(box geo.BBox) tileRange {
	return tileRange{
		firstRow: int(math.Ceil(-603 - 100*box.North)),
		lastRow:  int(math.Ceil(-603-100*box.South)) + 1,
		firstCol: int(math.Ceil(100*box.West - 10663)),
		lastCol:  int(math.Ceil(100*box.East-10663)) + 1,
	}
}

func (r tileRange) count() int {
	return max(0, r.lastRow-r.firstRow+1) * max(0, r.lastCol-r.firstCol+1)
}

func RouteFilter(code string) (string, bool) {
	if !topicLevel.MatchString(code) {
		return "", false
	}
	return "/mobile_armada/+/" + code + "/#", true
}

func BusFilter(fleetNumber string) (string, bool) {
	if !topicLevel.MatchString(fleetNumber) {
		return "", false
	}
	return "/mobile_armada/+/+/+/" + fleetNumber, true
}

// filters maps i to broker filters, or to the whole fleet when that takes
// more than MaxFilters. Values that cannot form a filter are skipped.
func (i Interest) filters() []string {
	seen := map[string]bool{}
	var out []string
	add := func(f string) {
		if !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	for _, box := range i.Areas {
		// Counted before building, so a zoomed-out box never allocates its tiles.
		if tilesOf(box).count() > MaxFilters {
			return []string{WholeFleet}
		}
		for _, f := range TileFilters(box) {
			add(f)
		}
	}
	for _, code := range i.RouteCodes {
		if f, ok := RouteFilter(code); ok {
			add(f)
		}
	}
	for _, n := range i.FleetNumbers {
		if f, ok := BusFilter(n); ok {
			add(f)
		}
	}
	if len(out) > MaxFilters {
		return []string{WholeFleet}
	}
	return out
}
