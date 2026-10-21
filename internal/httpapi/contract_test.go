package httpapi_test

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
)

var contract = sync.OnceValues(func() (*openapi3.T, error) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile("../../api/openapi.yaml")
	if err != nil {
		return nil, err
	}
	return doc, doc.Validate(loader.Context)
})

func TestContractIsValid(t *testing.T) {
	if _, err := contract(); err != nil {
		t.Fatalf("api/openapi.yaml: %v", err)
	}
}

// checkContract fails the test when a response to a documented operation
// doesn't match api/openapi.yaml, including undocumented status codes.
func checkContract(t *testing.T, req *http.Request, rec *httptest.ResponseRecorder) {
	t.Helper()
	doc, err := contract()
	if err != nil {
		t.Fatalf("api/openapi.yaml: %v", err)
	}
	item := doc.Paths.Find(req.URL.Path)
	if item == nil {
		return
	}
	op := item.GetOperation(req.Method)
	if op == nil {
		return
	}
	body := rec.Body.Bytes()
	if rec.Header().Get("Content-Encoding") == "gzip" {
		zr, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Fatalf("gzip body: %v", err)
		}
		if body, err = io.ReadAll(zr); err != nil {
			t.Fatalf("gzip body: %v", err)
		}
	}
	options := &openapi3filter.Options{IncludeResponseStatus: true}
	err = openapi3filter.ValidateResponse(t.Context(), &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request: req,
			Route:   &routers.Route{Spec: doc, Path: req.URL.Path, PathItem: item, Method: req.Method, Operation: op},
			Options: options,
		},
		Status:  rec.Code,
		Header:  rec.Header(),
		Body:    io.NopCloser(bytes.NewReader(body)),
		Options: options,
	})
	if err != nil {
		t.Errorf("%s %s: response breaks the contract: %v", req.Method, req.URL.Path, err)
	}
}
