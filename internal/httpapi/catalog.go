package httpapi

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lscythe/jalvio-service/internal/stopinfo"
)

type catalogDocuments struct {
	catalogs Catalogs
	stopInfo StopInfo
	catalog  cachedDocument
	shapes   cachedDocument
}

func (d *catalogDocuments) serveCatalog(w http.ResponseWriter, r *http.Request) {
	c := d.catalogs.Current()
	if c == nil {
		writeProblem(w, http.StatusServiceUnavailable)
		return
	}
	var snap *stopinfo.Snapshot
	if d.stopInfo != nil {
		snap = d.stopInfo.Current()
	}
	key := c.Version()
	if snap != nil {
		key += " " + snap.SyncedAt.Format(time.RFC3339Nano)
	}
	d.catalog.serve(w, r, key, func() any { return newCatalogResponse(c, snap) })
}

func (d *catalogDocuments) serveShapes(w http.ResponseWriter, r *http.Request) {
	c := d.catalogs.Current()
	if c == nil {
		writeProblem(w, http.StatusServiceUnavailable)
		return
	}
	d.shapes.serve(w, r, c.Version(), func() any { return newShapesResponse(c) })
}

// cachedDocument encodes a document once per key, so requests between feed
// or facilities updates only copy bytes.
type cachedDocument struct {
	mu      sync.Mutex
	key     string
	encoded *encodedDocument
}

type encodedDocument struct {
	etag    string
	body    []byte
	gzipped []byte
}

func (c *cachedDocument) serve(w http.ResponseWriter, r *http.Request, key string, build func() any) {
	doc, err := c.get(key, build)
	if err != nil {
		writeProblem(w, http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("ETag", doc.etag)
	h.Set("Cache-Control", "no-cache")
	h.Set("Vary", "Accept-Encoding")
	if etagMatches(r.Header.Get("If-None-Match"), doc.etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	body := doc.body
	if acceptsGzip(r.Header.Get("Accept-Encoding")) {
		h.Set("Content-Encoding", "gzip")
		body = doc.gzipped
	}
	h.Set("Content-Type", "application/json")
	h.Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}

func (c *cachedDocument) get(key string, build func() any) (*encodedDocument, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.encoded != nil && c.key == key {
		return c.encoded, nil
	}
	doc, err := encodeDocument(build())
	if err != nil {
		return nil, err
	}
	c.key, c.encoded = key, doc
	return doc, nil
}

func encodeDocument(v any) (*encodedDocument, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	body = append(body, '\n')
	var gz bytes.Buffer
	zw, err := gzip.NewWriterLevel(&gz, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(body); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	sum := sha256.Sum256(body)
	// Weak, because the plain and gzipped bodies share it.
	return &encodedDocument{etag: `W/"` + hex.EncodeToString(sum[:16]) + `"`, body: body, gzipped: gz.Bytes()}, nil
}

func etagMatches(ifNoneMatch, etag string) bool {
	for candidate := range strings.SplitSeq(ifNoneMatch, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || strings.TrimPrefix(candidate, "W/") == strings.TrimPrefix(etag, "W/") {
			return true
		}
	}
	return false
}

func acceptsGzip(acceptEncoding string) bool {
	for coding := range strings.SplitSeq(acceptEncoding, ",") {
		name, params, _ := strings.Cut(coding, ";")
		if strings.TrimSpace(name) == "gzip" && strings.ReplaceAll(params, " ", "") != "q=0" {
			return true
		}
	}
	return false
}
