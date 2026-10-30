package httpapi_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
)

type contractDoc struct {
	doc    *openapi3.T
	router routers.Router
}

var contract = sync.OnceValues(func() (contractDoc, error) {
	loader := openapi3.NewLoader()
	doc, err := loader.LoadFromFile("../../../api/community.openapi.yaml")
	if err != nil {
		return contractDoc{}, err
	}
	if err := doc.Validate(loader.Context); err != nil {
		return contractDoc{}, err
	}
	router, err := legacy.NewRouter(doc)
	return contractDoc{doc: doc, router: router}, err
})

func TestContractIsValid(t *testing.T) {
	if _, err := contract(); err != nil {
		t.Fatalf("api/community.openapi.yaml: %v", err)
	}
}

func TestContractRejectsAnUndocumentedField(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/me", nil)
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", "application/json")
	rec.WriteHeader(http.StatusOK)
	_, _ = rec.WriteString(`{"id":"x","google":false,"createdAt":"2026-10-30T09:00:00Z"}`)
	if err := contractError(t.Context(), req, rec); err == nil {
		t.Error("a profile with an id passed the contract")
	}
}

// checkContract fails the test when a response to a documented operation
// doesn't match api/community.openapi.yaml, including undocumented status codes.
func checkContract(t *testing.T, req *http.Request, rec *httptest.ResponseRecorder) {
	t.Helper()
	if err := contractError(t.Context(), req, rec); err != nil {
		t.Error(err)
	}
}

// contractError is nil for a response that matches the contract, and for
// requests to undocumented paths or methods, which are not checked.
func contractError(ctx context.Context, req *http.Request, rec *httptest.ResponseRecorder) error {
	c, err := contract()
	if err != nil {
		return fmt.Errorf("api/community.openapi.yaml: %w", err)
	}
	route, pathParams, err := c.router.FindRoute(req)
	if routeErr := (*routers.RouteError)(nil); errors.As(err, &routeErr) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("%s %s: %w", req.Method, req.URL.Path, err)
	}
	options := &openapi3filter.Options{IncludeResponseStatus: true, AuthenticationFunc: openapi3filter.NoopAuthenticationFunc}
	err = openapi3filter.ValidateResponse(ctx, &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request:    req,
			PathParams: pathParams,
			Route:      route,
			Options:    options,
		},
		Status:  rec.Code,
		Header:  rec.Header(),
		Body:    io.NopCloser(bytes.NewReader(rec.Body.Bytes())),
		Options: options,
	})
	if err != nil {
		return fmt.Errorf("%s %s: response breaks the contract: %w", req.Method, req.URL.Path, err)
	}
	return nil
}
