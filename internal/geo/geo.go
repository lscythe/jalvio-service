package geo

import "math"

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

// Distance returns the great-circle distance in meters.
func Distance(a, b Point) float64 {
	const earthRadius = 6371000
	rad := func(deg float64) float64 { return deg * math.Pi / 180 }
	dLat, dLon := rad(b.Lat-a.Lat), rad(b.Lon-a.Lon)
	h := math.Sin(dLat/2)*math.Sin(dLat/2) + math.Cos(rad(a.Lat))*math.Cos(rad(b.Lat))*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadius * math.Asin(math.Sqrt(h))
}
