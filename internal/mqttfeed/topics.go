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
	firstRow, lastRow := int(math.Ceil(-603-100*box.North)), int(math.Ceil(-603-100*box.South))+1
	firstCol, lastCol := int(math.Ceil(100*box.West-10663)), int(math.Ceil(100*box.East-10663))+1
	filters := make([]string, 0, (lastRow-firstRow+1)*(lastCol-firstCol+1))
	for row := firstRow; row <= lastRow; row++ {
		for col := firstCol; col <= lastCol; col++ {
			filters = append(filters, fmt.Sprintf("/mobile_armada/%04d-%04d/#", row, col))
		}
	}
	return filters
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

// filters maps i to broker filters, keeping tiles apart so callers can count
// them. Values that cannot form a filter are skipped.
func (i Interest) filters() (tiles, others []string) {
	seen := map[string]bool{}
	add := func(list *[]string, f string) {
		if !seen[f] {
			seen[f] = true
			*list = append(*list, f)
		}
	}
	for _, box := range i.Areas {
		for _, f := range TileFilters(box) {
			add(&tiles, f)
		}
	}
	for _, code := range i.RouteCodes {
		if f, ok := RouteFilter(code); ok {
			add(&others, f)
		}
	}
	for _, n := range i.FleetNumbers {
		if f, ok := BusFilter(n); ok {
			add(&others, f)
		}
	}
	return tiles, others
}
