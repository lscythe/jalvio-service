package geo

import (
	"math"
	"strings"
)

type Point struct {
	Lat float64
	Lon float64
}

type BBox struct {
	West  float64
	South float64
	East  float64
	North float64
}

// Contains reports whether p lies inside b, edges included.
func (b BBox) Contains(p Point) bool {
	return p.Lat >= b.South && p.Lat <= b.North && p.Lon >= b.West && p.Lon <= b.East
}

// Pad grows b by degrees on every side.
func (b BBox) Pad(degrees float64) BBox {
	return BBox{West: b.West - degrees, South: b.South - degrees, East: b.East + degrees, North: b.North + degrees}
}

// EncodePolyline uses Google's encoded polyline format at five decimals,
// which map SDKs decode natively.
func EncodePolyline(points []Point) string {
	var b strings.Builder
	var prevLat, prevLon int64
	for _, p := range points {
		lat, lon := int64(math.Round(p.Lat*1e5)), int64(math.Round(p.Lon*1e5))
		encodeDelta(&b, lat-prevLat)
		encodeDelta(&b, lon-prevLon)
		prevLat, prevLon = lat, lon
	}
	return b.String()
}

const polylineAlphabet = "?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~"

func encodeDelta(b *strings.Builder, delta int64) {
	v := delta << 1
	if delta < 0 {
		v = ^v
	}
	for v >= 0x20 {
		b.WriteByte(polylineAlphabet[0x20|v&0x1f])
		v >>= 5
	}
	b.WriteByte(polylineAlphabet[v])
}

// Distance returns the great-circle distance in meters.
func Distance(a, b Point) float64 {
	const earthRadius = 6371000
	rad := func(deg float64) float64 { return deg * math.Pi / 180 }
	dLat, dLon := rad(b.Lat-a.Lat), rad(b.Lon-a.Lon)
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(rad(a.Lat))*math.Cos(rad(b.Lat))*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadius * math.Asin(math.Sqrt(h))
}
