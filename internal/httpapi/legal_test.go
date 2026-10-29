package httpapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/lscythe/jalvio-service/internal/httpapi"
	"github.com/lscythe/jalvio-service/internal/legal"
)

func legalHandler(t *testing.T, path string) http.Handler {
	t.Helper()
	docs, err := legal.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	d := deps(true)
	d.Legal = docs
	return httpapi.New(d)
}

func TestLegalDocuments(t *testing.T) {
	h := legalHandler(t, "testdata/legal.json")
	for _, tc := range []struct {
		target, language, body string
	}{
		{
			"/v1/legal/terms", "en",
			`{"document":"terms","language":"en","updated":"2026-10-29","title":"Terms",` +
				`"summary":[{"icon":"gavel","text":"Use it kindly."}],` +
				`"sections":[{"id":"t1","title":"Using the app","text":"It is free to use."}]}`,
		},
		{
			"/v1/legal/terms?lang=id", "id",
			`{"document":"terms","language":"id","updated":"2026-10-29","title":"Ketentuan","summary":[],` +
				`"sections":[{"id":"t1","title":"Memakai aplikasi","text":"Gratis."}]}`,
		},
		{
			"/v1/legal/privacy?lang=id", "en",
			`{"document":"privacy","language":"en","updated":"2026-10-28","title":"Privacy","summary":[],"sections":[` +
				`{"id":"collect","title":"What we collect","rows":[{"icon":"my_location","title":"Location","description":"Only while the app is open.","tag":"While using"}]},` +
				`{"id":"choices","title":"Your choices","text":"Turn analytics off in Settings.","rows":[{"title":"Crash reports","description":"Off unless you turn them on."}]}]}`,
		},
	} {
		t.Run(tc.target, func(t *testing.T) {
			rec := get(t, h, tc.target, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			if got := strings.TrimSpace(rec.Body.String()); got != tc.body {
				t.Errorf("body = %s\nwant   %s", got, tc.body)
			}
			for header, want := range map[string]string{
				"Content-Type":     "application/json",
				"Content-Language": tc.language,
				"Cache-Control":    "no-cache",
			} {
				if got := rec.Header().Get(header); got != want {
					t.Errorf("%s = %q, want %q", header, got, want)
				}
			}
		})
	}
}

func TestLegalFallbackSharesTheEnglishETag(t *testing.T) {
	h := legalHandler(t, "testdata/legal.json")
	english := get(t, h, "/v1/legal/privacy", nil).Header().Get("ETag")
	fallback := get(t, h, "/v1/legal/privacy?lang=id", nil).Header().Get("ETag")
	if english == "" || fallback != english {
		t.Errorf("ETag = %q for the fallback, want the English %q", fallback, english)
	}
	if translated := get(t, h, "/v1/legal/terms?lang=id", nil).Header().Get("ETag"); translated == get(t, h, "/v1/legal/terms", nil).Header().Get("ETag") {
		t.Error("the Indonesian terms share the English ETag")
	}
}

func TestLegalDocumentIsConditional(t *testing.T) {
	h := legalHandler(t, "testdata/legal.json")
	etag := get(t, h, "/v1/legal/terms", nil).Header().Get("ETag")
	rec := get(t, h, "/v1/legal/terms", http.Header{"If-None-Match": {etag}})
	if rec.Code != http.StatusNotModified || rec.Body.Len() != 0 {
		t.Errorf("status = %d with %d bytes, want 304 and no body", rec.Code, rec.Body.Len())
	}
}

func TestLegalDocumentProblems(t *testing.T) {
	published := legalHandler(t, "testdata/legal.json")
	unpublished := legalHandler(t, "testdata/no-legal.json")
	for _, tc := range []struct {
		name   string
		h      http.Handler
		target string
		status int
	}{
		{"unsupported language", published, "/v1/legal/terms?lang=fr", http.StatusBadRequest},
		{"unknown document", published, "/v1/legal/cookies", http.StatusNotFound},
		{"not published", unpublished, "/v1/legal/terms", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := get(t, tc.h, tc.target, nil)
			if rec.Code != tc.status {
				t.Fatalf("status = %d, want %d", rec.Code, tc.status)
			}
			if p := decodeProblem(t, rec); p.Status != tc.status {
				t.Errorf("problem status = %d, want %d", p.Status, tc.status)
			}
		})
	}
}
