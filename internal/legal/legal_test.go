package legal_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lscythe/jalvio-service/internal/legal"
)

const published = `{
  "terms": {
    "updated": "2026-10-29",
    "en": {
      "title": "Terms",
      "summary": [{"icon": "gavel", "text": "Use it kindly."}],
      "sections": [{"id": "t1", "title": "Using the app", "text": "It is free to use."}]
    },
    "id": {
      "title": "Ketentuan",
      "summary": [],
      "sections": [{"id": "t1", "title": "Memakai aplikasi", "text": "Gratis."}]
    }
  },
  "privacy": {
    "updated": "2026-10-28",
    "en": {
      "title": "Privacy",
      "summary": [],
      "sections": [{
        "id": "collect",
        "title": "What we collect",
        "rows": [{"icon": "my_location", "title": "Location", "description": "Only while the app is open.", "tag": "While using"}]
      }]
    }
  }
}`

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "legal.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func load(t *testing.T, content string) legal.Documents {
	t.Helper()
	docs, err := legal.Load(writeFile(t, content))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return docs
}

func TestFindReturnsTheRequestedLanguage(t *testing.T) {
	docs := load(t, published)
	page, ok := docs.Find("terms", legal.Indonesian)
	if !ok {
		t.Fatal("terms in Indonesian not found")
	}
	if page.Kind != "terms" || page.Language != legal.Indonesian || page.Updated != "2026-10-29" || page.Title != "Ketentuan" {
		t.Errorf("page = %+v, want the Indonesian terms updated 2026-10-29", page)
	}
	if len(page.Sections) != 1 || page.Sections[0].Title != "Memakai aplikasi" {
		t.Errorf("sections = %+v, want the Indonesian section", page.Sections)
	}
}

func TestFindFallsBackToEnglish(t *testing.T) {
	docs := load(t, published)
	page, ok := docs.Find("privacy", legal.Indonesian)
	if !ok {
		t.Fatal("privacy not found")
	}
	if page.Language != legal.English || page.Title != "Privacy" {
		t.Errorf("page = %+v, want the English privacy policy", page)
	}
	want := legal.Row{Icon: "my_location", Title: "Location", Description: "Only while the app is open.", Tag: "While using"}
	if got := page.Sections[0].Rows; len(got) != 1 || got[0] != want {
		t.Errorf("rows = %+v, want [%+v]", got, want)
	}
}

func TestFindUnpublished(t *testing.T) {
	docs := load(t, `{"terms": {"updated": "2026-10-29", "en": {"title": "Terms", "summary": [], "sections": []}}}`)
	if _, ok := docs.Find("privacy", legal.English); ok {
		t.Error("found a privacy policy the file does not have")
	}
	if _, ok := docs.Find("cookies", legal.English); ok {
		t.Error("found an unknown document")
	}
}

func TestLoadWithoutAFilePublishesNothing(t *testing.T) {
	docs, err := legal.Load(filepath.Join(t.TempDir(), "legal.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := docs.Find("terms", legal.English); ok {
		t.Error("found terms without a file")
	}
}

func TestExampleFileIsValid(t *testing.T) {
	docs, err := legal.Load("../../legal.example.json")
	if err != nil {
		t.Fatalf("legal.example.json: %v", err)
	}
	for _, kind := range []string{"terms", "privacy"} {
		if _, ok := docs.Find(kind, legal.English); !ok {
			t.Errorf("legal.example.json has no %s", kind)
		}
	}
}

func TestLoadRejectsInvalidFiles(t *testing.T) {
	terms := func(updated, en string) string {
		return `{"terms": {"updated": "` + updated + `", "en": ` + en + `}}`
	}
	version := func(sections string) string {
		return `{"title": "Terms", "summary": [], "sections": [` + sections + `]}`
	}
	for _, tc := range []struct {
		name, content, mention string
	}{
		{"not JSON", `{`, "legal.json"},
		{"unknown document", `{"cookies": {}}`, "cookies"},
		{"unknown field", terms("2026-10-29", `{"title": "Terms", "summary": [], "sections": [], "footer": "x"}`), "footer"},
		{"bad date", terms("29 Oct", version("")), "terms.updated"},
		{"no English", `{"terms": {"updated": "2026-10-29"}}`, "terms.en"},
		{"no title", terms("2026-10-29", `{"title": "", "summary": [], "sections": []}`), "terms.en.title"},
		{"empty point", terms("2026-10-29", `{"title": "Terms", "summary": [{"icon": "gavel"}], "sections": []}`), "terms.en.summary[0]"},
		{"bad section id", terms("2026-10-29", version(`{"id": "Using It", "title": "Use", "text": "x"}`)), "terms.en.sections[0].id"},
		{"repeated section id", terms("2026-10-29", version(`{"id": "t1", "title": "A", "text": "x"}, {"id": "t1", "title": "B", "text": "y"}`)), "terms.en.sections[1].id"},
		{"section without title", terms("2026-10-29", version(`{"id": "t1", "text": "x"}`)), "terms.en.sections[0].title"},
		{"empty section", terms("2026-10-29", version(`{"id": "t1", "title": "A"}`)), "terms.en.sections[0]"},
		{"row without title", terms("2026-10-29", version(`{"id": "t1", "title": "A", "rows": [{"description": "x"}]}`)), "terms.en.sections[0].rows[0].title"},
		{"row without description", terms("2026-10-29", version(`{"id": "t1", "title": "A", "rows": [{"title": "x"}]}`)), "terms.en.sections[0].rows[0].description"},
		{"bad Indonesian", `{"terms": {"updated": "2026-10-29", "en": ` + version("") + `, "id": {"title": ""}}}`, "terms.id.title"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := legal.Load(writeFile(t, tc.content))
			if err == nil {
				t.Fatal("Load accepted an invalid file")
			}
			if !strings.Contains(err.Error(), tc.mention) {
				t.Errorf("error %q does not mention %q", err, tc.mention)
			}
		})
	}
}
