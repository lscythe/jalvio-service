package httpapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/community/accounts"
	"github.com/lscythe/jalvio-service/internal/community/httpapi"
)

const (
	riderToken = "rider-token-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	newToken   = "new-token-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

var created = time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)

// fakeService answers like accounts.Service for one rider.
type fakeService struct {
	mu        sync.Mutex
	rider     accounts.Account
	createErr error
	signInErr error
	updateErr error
	opErr     error
	update    *accounts.ProfileUpdate
	signedOut string
	deleted   bool
}

func newFake() *fakeService {
	return &fakeService{rider: accounts.Account{ID: "secret-id", DisplayName: ptr("Rina"), Avatar: json.RawMessage(`{"face":3}`), CreatedAt: created}}
}

func (f *fakeService) CreateAccount(context.Context) (string, error) {
	return newToken, f.createErr
}

func (f *fakeService) Authenticate(_ context.Context, token string) (accounts.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if token != riderToken || f.deleted {
		return accounts.Account{}, accounts.ErrUnauthenticated
	}
	return f.rider, nil
}

func (f *fakeService) SignInWithGoogle(_ context.Context, acc accounts.Account, token, idToken string) (string, error) {
	if acc.ID != f.rider.ID || token != riderToken || idToken != "google-id-token" {
		return "", errors.New("unexpected sign-in arguments")
	}
	return newToken, f.signInErr
}

func (f *fakeService) UpdateProfile(_ context.Context, acc accounts.Account, u accounts.ProfileUpdate) (accounts.Profile, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.update = &u
	if f.updateErr != nil {
		return accounts.Profile{}, f.updateErr
	}
	if u.DisplayName.Set {
		acc.DisplayName = u.DisplayName.Value
	}
	if u.Avatar.Set {
		acc.Avatar = nil
		if u.Avatar.Value != nil {
			acc.Avatar = *u.Avatar.Value
		}
	}
	return acc.Profile(), nil
}

func (f *fakeService) SignOut(_ context.Context, _ accounts.Account, token string) error {
	f.signedOut = token
	return f.opErr
}

func (f *fakeService) Delete(context.Context, accounts.Account) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.opErr != nil {
		return f.opErr
	}
	f.deleted = true
	return nil
}

type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func handler(svc httpapi.Service, ready bool, c *clock) http.Handler {
	if c == nil {
		c = &clock{now: created}
	}
	return httpapi.New(httpapi.Deps{
		Accounts:        svc,
		Ready:           func() bool { return ready },
		AccountsPerHour: 10,
		Clock:           c.Now,
		Logger:          slog.New(slog.DiscardHandler),
	})
}

type request struct {
	method, path, auth, body, remote string
	header                           map[string]string
}

func serve(t *testing.T, h http.Handler, r request) *httptest.ResponseRecorder {
	t.Helper()
	var body io.Reader
	if r.body != "" {
		body = strings.NewReader(r.body)
	}
	req := httptest.NewRequestWithContext(t.Context(), r.method, r.path, body)
	if r.auth != "" {
		req.Header.Set("Authorization", r.auth)
	}
	if r.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if r.remote != "" {
		req.RemoteAddr = r.remote
	}
	for k, v := range r.header {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	checkContract(t, req, rec)
	return rec
}

func authed(method, path, body string) request {
	return request{method: method, path: path, auth: "Bearer " + riderToken, body: body}
}

func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, mention string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, status, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	if !strings.Contains(rec.Body.String(), mention) {
		t.Errorf("body %s does not mention %q", rec.Body, mention)
	}
}

func wantJSON(t *testing.T, rec *httptest.ResponseRecorder, status int, want string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d (body %s)", rec.Code, status, rec.Body)
	}
	var got, expected any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body %s: %v", rec.Body, err)
	}
	if err := json.Unmarshal([]byte(want), &expected); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, expected) {
		t.Errorf("body = %s, want %s", rec.Body, want)
	}
}

func ptr[T any](v T) *T { return &v }

func TestCreateAccount(t *testing.T) {
	rec := serve(t, handler(newFake(), true, nil), request{method: http.MethodPost, path: "/v1/accounts", remote: "192.0.2.1:4000"})
	wantJSON(t, rec, http.StatusCreated, `{"token":"`+newToken+`"}`)
}

func TestAuth(t *testing.T) {
	routes := []request{
		{method: http.MethodGet, path: "/v1/me"},
		{method: http.MethodPatch, path: "/v1/me", body: `{}`},
		{method: http.MethodDelete, path: "/v1/me"},
		{method: http.MethodDelete, path: "/v1/session"},
		{method: http.MethodPost, path: "/v1/session/google", body: `{"idToken":"google-id-token"}`},
	}
	h := handler(newFake(), true, nil)
	for _, auth := range []string{"", "Basic " + riderToken, "Bearer", "Bearer ", "Bearer unknown", riderToken} {
		for _, r := range routes {
			r.auth = auth
			rec := serve(t, h, r)
			wantProblem(t, rec, http.StatusUnauthorized, "Unauthorized")
			if got := rec.Header().Get("WWW-Authenticate"); got != "Bearer" {
				t.Errorf("%s %s with %q: WWW-Authenticate = %q, want Bearer", r.method, r.path, auth, got)
			}
		}
	}
	rec := serve(t, h, request{method: http.MethodGet, path: "/v1/me", auth: "bearer " + riderToken})
	if rec.Code != http.StatusOK {
		t.Errorf("lowercase scheme: status = %d, want 200", rec.Code)
	}
}

func TestAMergedOrDeletedAccountIsUnauthenticated(t *testing.T) {
	svc := newFake()
	h := handler(svc, true, nil)
	if rec := serve(t, h, authed(http.MethodDelete, "/v1/me", "")); rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", rec.Code)
	}
	wantProblem(t, serve(t, h, authed(http.MethodGet, "/v1/me", "")), http.StatusUnauthorized, "Unauthorized")
}

func TestGoogleSignIn(t *testing.T) {
	body := `{"idToken":"google-id-token"}`
	rec := serve(t, handler(newFake(), true, nil), authed(http.MethodPost, "/v1/session/google", body))
	wantJSON(t, rec, http.StatusOK, `{"token":"`+newToken+`"}`)

	for _, tc := range []struct {
		err     error
		status  int
		mention string
	}{
		{accounts.ErrGoogleDisabled, http.StatusServiceUnavailable, "not configured"},
		{accounts.ErrGoogleUnavailable, http.StatusServiceUnavailable, "unavailable"},
		{accounts.ErrInvalidGoogleToken, http.StatusBadRequest, "idToken"},
		{accounts.ErrOtherGoogleAccount, http.StatusConflict, "different Google account"},
		{accounts.ErrUnauthenticated, http.StatusUnauthorized, "Unauthorized"},
		{errors.New("database down"), http.StatusInternalServerError, "Internal Server Error"},
	} {
		svc := newFake()
		svc.signInErr = tc.err
		wantProblem(t, serve(t, handler(svc, true, nil), authed(http.MethodPost, "/v1/session/google", body)), tc.status, tc.mention)
	}
}

func TestGoogleSignInRejectsBadBodies(t *testing.T) {
	h := handler(newFake(), true, nil)
	for _, tc := range []struct{ body, mention string }{
		{`{}`, "idToken"},
		{`{"idToken":""}`, "idToken"},
		{`{"idToken":5}`, "idToken"},
		{`{"idToken":"google-id-token","extra":1}`, "extra"},
		{`not json`, "JSON"},
		{`{"idToken":"google-id-token"} {}`, "JSON"},
	} {
		wantProblem(t, serve(t, h, authed(http.MethodPost, "/v1/session/google", tc.body)), http.StatusBadRequest, tc.mention)
	}
}

func TestGetProfile(t *testing.T) {
	rec := serve(t, handler(newFake(), true, nil), authed(http.MethodGet, "/v1/me", ""))
	wantJSON(t, rec, http.StatusOK, `{"displayName":"Rina","avatar":{"face":3},"google":false,"createdAt":"2026-10-30T09:00:00Z"}`)
	if strings.Contains(rec.Body.String(), "secret-id") {
		t.Error("the profile exposes the account id")
	}
}

func TestPatchProfile(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		want       accounts.ProfileUpdate
		response   string
	}{
		{"empty", `{}`, accounts.ProfileUpdate{}, `{"displayName":"Rina","avatar":{"face":3},"google":false,"createdAt":"2026-10-30T09:00:00Z"}`},
		{"clear", `{"displayName":null,"avatar":null}`, accounts.ProfileUpdate{
			DisplayName: accounts.Change[string]{Set: true},
			Avatar:      accounts.Change[json.RawMessage]{Set: true},
		}, `{"google":false,"createdAt":"2026-10-30T09:00:00Z"}`},
		{"set", `{"displayName":"Budi","avatar":{"hat":1}}`, accounts.ProfileUpdate{
			DisplayName: accounts.Change[string]{Set: true, Value: ptr("Budi")},
			Avatar:      accounts.Change[json.RawMessage]{Set: true, Value: ptr(json.RawMessage(`{"hat":1}`))},
		}, `{"displayName":"Budi","avatar":{"hat":1},"google":false,"createdAt":"2026-10-30T09:00:00Z"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newFake()
			rec := serve(t, handler(svc, true, nil), authed(http.MethodPatch, "/v1/me", tc.body))
			wantJSON(t, rec, http.StatusOK, tc.response)
			if svc.update == nil || !reflect.DeepEqual(*svc.update, tc.want) {
				t.Errorf("update = %+v, want %+v", svc.update, tc.want)
			}
		})
	}
}

func TestPatchProfileRejects(t *testing.T) {
	h := handler(newFake(), true, nil)
	for _, tc := range []struct{ body, mention string }{
		{`{"displayName":5}`, "displayName"},
		{`{"displayName":true}`, "displayName"},
		{`{"avatar":[]}`, "avatar"},
		{`{"avatar":"x"}`, "avatar"},
		{`{"nickname":"x"}`, "nickname"},
		{`[]`, "JSON object"},
		{`null`, "JSON object"},
		{``, "JSON object"},
	} {
		wantProblem(t, serve(t, h, authed(http.MethodPatch, "/v1/me", tc.body)), http.StatusBadRequest, tc.mention)
	}

	svc := newFake()
	svc.updateErr = accounts.FieldError{Field: "displayName", Reason: "must be at most 40 characters"}
	wantProblem(t, serve(t, handler(svc, true, nil), authed(http.MethodPatch, "/v1/me", `{"displayName":"x"}`)), http.StatusBadRequest, "displayName: must be at most 40 characters")
}

func TestSignOutAndDelete(t *testing.T) {
	svc := newFake()
	rec := serve(t, handler(svc, true, nil), authed(http.MethodDelete, "/v1/session", ""))
	if rec.Code != http.StatusNoContent || svc.signedOut != riderToken {
		t.Errorf("sign out = %d with token %q, want 204 with the rider's token", rec.Code, svc.signedOut)
	}

	svc = newFake()
	svc.opErr = accounts.ErrDeviceOnly
	wantProblem(t, serve(t, handler(svc, true, nil), authed(http.MethodDelete, "/v1/session", "")), http.StatusConflict, "device-only")

	svc = newFake()
	if rec := serve(t, handler(svc, true, nil), authed(http.MethodDelete, "/v1/me", "")); rec.Code != http.StatusNoContent || !svc.deleted {
		t.Errorf("delete = %d (deleted %v), want 204", rec.Code, svc.deleted)
	}
}

func TestServiceFailures(t *testing.T) {
	failure := errors.New("database down")
	svc := newFake()
	svc.createErr, svc.updateErr, svc.opErr = failure, failure, failure
	h := handler(svc, true, nil)
	for _, r := range []request{
		{method: http.MethodPost, path: "/v1/accounts", remote: "192.0.2.1:4000"},
		authed(http.MethodPatch, "/v1/me", `{}`),
		authed(http.MethodDelete, "/v1/session", ""),
		authed(http.MethodDelete, "/v1/me", ""),
	} {
		wantProblem(t, serve(t, h, r), http.StatusInternalServerError, "Internal Server Error")
	}
}

func TestBodyLimit(t *testing.T) {
	h := handler(newFake(), true, nil)
	body := func(size int) string {
		prefix, suffix := `{"avatar":{"a":"`, `"}}`
		return prefix + strings.Repeat("x", size-len(prefix)-len(suffix)) + suffix
	}
	if rec := serve(t, h, authed(http.MethodPatch, "/v1/me", body(8192))); rec.Code != http.StatusOK {
		t.Errorf("8192-byte body: status = %d, want 200", rec.Code)
	}
	wantProblem(t, serve(t, h, authed(http.MethodPatch, "/v1/me", body(8193))), http.StatusRequestEntityTooLarge, "8192")
}

func TestHealthz(t *testing.T) {
	if rec := serve(t, handler(newFake(), true, nil), request{method: http.MethodGet, path: "/healthz"}); rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Errorf("ready: %d %q, want bodyless 204", rec.Code, rec.Body)
	}
	if rec := serve(t, handler(newFake(), false, nil), request{method: http.MethodGet, path: "/healthz"}); rec.Code != http.StatusServiceUnavailable || rec.Body.Len() != 0 {
		t.Errorf("not ready: %d %q, want bodyless 503", rec.Code, rec.Body)
	}
}

func TestUnknownRoutesAreProblems(t *testing.T) {
	h := handler(newFake(), true, nil)
	wantProblem(t, serve(t, h, request{method: http.MethodGet, path: "/v1/nothing"}), http.StatusNotFound, "Not Found")
	rec := serve(t, h, request{method: http.MethodPut, path: "/v1/me"})
	wantProblem(t, rec, http.StatusMethodNotAllowed, "Method Not Allowed")
	if rec.Header().Get("Allow") == "" {
		t.Error("405 without an Allow header")
	}
}

func TestAccountLimitPerAddress(t *testing.T) {
	c := &clock{now: created}
	h := handler(newFake(), true, c)
	create := func(remote string, header map[string]string) int {
		return serve(t, h, request{method: http.MethodPost, path: "/v1/accounts", remote: remote, header: header}).Code
	}
	for i := range 10 {
		if got := create("192.0.2.1:4000", nil); got != http.StatusCreated {
			t.Fatalf("creation %d: status = %d, want 201", i+1, got)
		}
	}
	if got := create("192.0.2.1:4001", nil); got != http.StatusTooManyRequests {
		t.Errorf("11th creation: status = %d, want 429", got)
	}
	if got := create("192.0.2.1:4002", map[string]string{"X-Forwarded-For": "198.51.100.7", "X-Real-IP": "198.51.100.7"}); got != http.StatusTooManyRequests {
		t.Errorf("with forwarding headers: status = %d, want 429", got)
	}
	if got := create("198.51.100.7:5000", nil); got != http.StatusCreated {
		t.Errorf("another address: status = %d, want 201", got)
	}
	c.advance(time.Hour)
	if got := create("192.0.2.1:4000", nil); got != http.StatusCreated {
		t.Errorf("after an hour: status = %d, want 201", got)
	}
}
