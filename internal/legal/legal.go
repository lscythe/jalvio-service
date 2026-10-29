// Package legal loads the terms and privacy policy an instance owner
// publishes.
package legal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

const (
	English    = "en"
	Indonesian = "id"
)

// Documents holds the published documents by kind, "terms" and "privacy".
// The zero value publishes nothing.
type Documents struct {
	byKind map[string]document
}

type document struct {
	Updated string   `json:"updated"`
	EN      *Version `json:"en"`
	ID      *Version `json:"id"`
}

type Version struct {
	Title    string    `json:"title"`
	Summary  []Point   `json:"summary"`
	Sections []Section `json:"sections"`
}

type Point struct {
	Icon string `json:"icon"`
	Text string `json:"text"`
}

// Section has text, rows, or both. Its ID lets the app link to it.
type Section struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Text  string `json:"text"`
	Rows  []Row  `json:"rows"`
}

type Row struct {
	Icon        string `json:"icon"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Tag         string `json:"tag"`
}

// Page is one document in the language it is served in.
type Page struct {
	Kind     string
	Language string
	// Updated is the owner's YYYY-MM-DD date for the document, shared by
	// its languages.
	Updated string
	Version
}

var sectionID = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

// Load reads and checks the owner's file. A missing file publishes nothing.
func Load(path string) (Documents, error) {
	data, err := os.ReadFile(filepath.Clean(path))
	if errors.Is(err, fs.ErrNotExist) {
		return Documents{}, nil
	}
	if err != nil {
		return Documents{}, err
	}
	var file struct {
		Terms   *document `json:"terms"`
		Privacy *document `json:"privacy"`
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return Documents{}, fmt.Errorf("%s: %w", path, err)
	}
	docs := Documents{byKind: map[string]document{}}
	for _, d := range []struct {
		kind string
		doc  *document
	}{{"terms", file.Terms}, {"privacy", file.Privacy}} {
		if d.doc == nil {
			continue
		}
		if err := d.doc.check(d.kind); err != nil {
			return Documents{}, fmt.Errorf("%s: %w", path, err)
		}
		docs.byKind[d.kind] = *d.doc
	}
	return docs, nil
}

// Find returns kind in language, or in English when that translation is
// missing.
func (d Documents) Find(kind, language string) (Page, bool) {
	doc, ok := d.byKind[kind]
	if !ok {
		return Page{}, false
	}
	page := Page{Kind: kind, Language: English, Updated: doc.Updated, Version: *doc.EN}
	if language == Indonesian && doc.ID != nil {
		page.Language, page.Version = Indonesian, *doc.ID
	}
	return page, true
}

func (d document) check(kind string) error {
	if _, err := time.Parse(time.DateOnly, d.Updated); err != nil {
		return fmt.Errorf("%s.updated: %q is not a YYYY-MM-DD date", kind, d.Updated)
	}
	if d.EN == nil {
		return fmt.Errorf("%s.en: required", kind)
	}
	if err := d.EN.check(kind + ".en"); err != nil {
		return err
	}
	if d.ID != nil {
		return d.ID.check(kind + ".id")
	}
	return nil
}

func (v Version) check(at string) error {
	if v.Title == "" {
		return fmt.Errorf("%s.title: required", at)
	}
	for i, p := range v.Summary {
		if p.Text == "" {
			return fmt.Errorf("%s.summary[%d].text: required", at, i)
		}
	}
	seen := map[string]bool{}
	for i, s := range v.Sections {
		if err := s.check(fmt.Sprintf("%s.sections[%d]", at, i), seen); err != nil {
			return err
		}
		seen[s.ID] = true
	}
	return nil
}

func (s Section) check(at string, seen map[string]bool) error {
	switch {
	case !sectionID.MatchString(s.ID):
		return fmt.Errorf("%s.id: %q is not lowercase letters, digits, - and _", at, s.ID)
	case seen[s.ID]:
		return fmt.Errorf("%s.id: %q is already used", at, s.ID)
	case s.Title == "":
		return fmt.Errorf("%s.title: required", at)
	case s.Text == "" && len(s.Rows) == 0:
		return fmt.Errorf("%s: needs text or rows", at)
	}
	for j, r := range s.Rows {
		if r.Title == "" {
			return fmt.Errorf("%s.rows[%d].title: required", at, j)
		}
		if r.Description == "" {
			return fmt.Errorf("%s.rows[%d].description: required", at, j)
		}
	}
	return nil
}
