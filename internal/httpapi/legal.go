package httpapi

import (
	"net/http"
	"sync"

	"github.com/lscythe/jalvio-service/internal/legal"
)

type legalDocuments struct {
	docs legal.Documents

	mu sync.Mutex
	// pages holds one encoding per document and served language, so at most
	// two documents in two languages.
	pages map[string]*cachedDocument
}

func (l *legalDocuments) serve(w http.ResponseWriter, r *http.Request) {
	lang := r.URL.Query().Get("lang")
	if lang == "" {
		lang = legal.English
	}
	if lang != legal.English && lang != legal.Indonesian {
		writeBadRequest(w, "lang must be en or id")
		return
	}
	page, ok := l.docs.Find(r.PathValue("document"), lang)
	if !ok {
		writeProblem(w, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Language", page.Language)
	l.cached(page.Kind+" "+page.Language).serve(w, r, "", func() any { return newLegalResponse(page) })
}

func (l *legalDocuments) cached(key string) *cachedDocument {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pages == nil {
		l.pages = map[string]*cachedDocument{}
	}
	c, ok := l.pages[key]
	if !ok {
		c = &cachedDocument{}
		l.pages[key] = c
	}
	return c
}

type legalResponse struct {
	Document string         `json:"document"`
	Language string         `json:"language"`
	Updated  string         `json:"updated"`
	Title    string         `json:"title"`
	Summary  []legalPoint   `json:"summary"`
	Sections []legalSection `json:"sections"`
}

type legalPoint struct {
	Icon string `json:"icon,omitempty"`
	Text string `json:"text"`
}

type legalSection struct {
	ID    string     `json:"id"`
	Title string     `json:"title"`
	Text  string     `json:"text,omitempty"`
	Rows  []legalRow `json:"rows,omitempty"`
}

type legalRow struct {
	Icon        string `json:"icon,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Tag         string `json:"tag,omitempty"`
}

func newLegalResponse(p legal.Page) legalResponse {
	resp := legalResponse{
		Document: p.Kind,
		Language: p.Language,
		Updated:  p.Updated,
		Title:    p.Title,
		Summary:  make([]legalPoint, 0, len(p.Summary)),
		Sections: make([]legalSection, 0, len(p.Sections)),
	}
	for _, point := range p.Summary {
		resp.Summary = append(resp.Summary, legalPoint(point))
	}
	for _, s := range p.Sections {
		section := legalSection{ID: s.ID, Title: s.Title, Text: s.Text}
		for _, r := range s.Rows {
			section.Rows = append(section.Rows, legalRow(r))
		}
		resp.Sections = append(resp.Sections, section)
	}
	return resp
}
