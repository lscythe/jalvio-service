package catalog

import (
	"cmp"
	"slices"
	"strings"
	"unicode"

	"github.com/lscythe/jalvio-service/internal/geo"
)

type ResultKind int

const (
	StopResult ResultKind = iota
	RouteResult
)

type Result struct {
	Kind ResultKind
	ID   string
}

// Match quality, best first.
const (
	exactCode = iota
	exactName
	namePrefix
	wordPrefix
	substring
)

type searchEntry struct {
	kind ResultKind
	id   string
	name string // normalized
	code string // compacted
	pos  geo.Point
	sort string // tie-break: code for routes, name for stops
}

func newSearchIndex(routes []Route, stops []Stop) []searchEntry {
	entries := make([]searchEntry, 0, len(routes)+len(stops))
	for _, r := range routes {
		entries = append(entries, searchEntry{
			kind: RouteResult, id: r.ID, name: normalize(r.Name), code: compact(r.Code), sort: r.Code,
		})
	}
	for _, s := range stops {
		if s.Kind == Platform {
			continue
		}
		name := normalize(s.Name)
		entries = append(entries, searchEntry{
			kind: StopResult, id: s.ID, name: name, code: compact(s.Code), pos: geo.Point{Lat: s.Lat, Lon: s.Lon}, sort: name,
		})
	}
	return entries
}

// Search ranks stations, roadside stops, and routes by name or code. Ties go
// to stops before routes, then to the stop nearest near (when given), then
// to stop name or route code.
func (c *Catalog) Search(query string, near *geo.Point, limit int) []Result {
	q, qc := normalize(query), compact(query)
	if q == "" || limit <= 0 {
		return nil
	}

	type hit struct {
		entry    *searchEntry
		quality  int
		distance float64
	}
	var hits []hit
	for i := range c.search {
		e := &c.search[i]
		quality, ok := matchQuality(e, q, qc)
		if !ok {
			continue
		}
		h := hit{entry: e, quality: quality}
		if near != nil && e.kind == StopResult {
			h.distance = geo.Distance(*near, e.pos)
		}
		hits = append(hits, h)
	}

	slices.SortFunc(hits, func(a, b hit) int {
		return cmp.Or(
			cmp.Compare(a.quality, b.quality),
			cmp.Compare(a.entry.kind, b.entry.kind),
			cmp.Compare(a.distance, b.distance),
			cmp.Compare(a.entry.sort, b.entry.sort),
			cmp.Compare(a.entry.id, b.entry.id),
		)
	})

	var results []Result
	for _, h := range hits[:min(limit, len(hits))] {
		results = append(results, Result{Kind: h.entry.kind, ID: h.entry.id})
	}
	return results
}

func matchQuality(e *searchEntry, q, qc string) (int, bool) {
	switch {
	case e.code != "" && e.code == qc:
		return exactCode, true
	case e.name == q:
		return exactName, true
	case strings.HasPrefix(e.name, q), e.code != "" && strings.HasPrefix(e.code, qc):
		return namePrefix, true
	case strings.Contains(e.name, " "+q):
		return wordPrefix, true
	case strings.Contains(e.name, q):
		return substring, true
	}
	return 0, false
}

// normalize lowercases s and turns every run of punctuation or space into a
// single space.
func normalize(s string) string {
	var b strings.Builder
	pendingSpace := false
	for _, r := range strings.ToLower(s) {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			pendingSpace = b.Len() > 0
			continue
		}
		if pendingSpace {
			b.WriteByte(' ')
			pendingSpace = false
		}
		b.WriteRune(r)
	}
	return b.String()
}

// compact makes codes such as "JAK.18", "jak 18", and "jak18" equal.
func compact(s string) string {
	return strings.ReplaceAll(normalize(s), " ", "")
}
