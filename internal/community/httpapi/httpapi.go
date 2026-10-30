package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/lscythe/jalvio-service/internal/community/accounts"
)

const maxBodyBytes = 8192

// Service is the part of accounts.Service the API uses.
type Service interface {
	CreateAccount(ctx context.Context) (string, error)
	Authenticate(ctx context.Context, token string) (accounts.Account, error)
	SignInWithGoogle(ctx context.Context, acc accounts.Account, token, idToken string) (string, error)
	UpdateProfile(ctx context.Context, acc accounts.Account, u accounts.ProfileUpdate) (accounts.Profile, error)
	SignOut(ctx context.Context, acc accounts.Account, token string) error
	Delete(ctx context.Context, acc accounts.Account) error
}

type Deps struct {
	Accounts        Service
	Ready           func() bool
	AccountsPerHour int
	Clock           func() time.Time
	Logger          *slog.Logger
}

type api struct {
	accounts Service
	limiter  *accountLimiter
	logger   *slog.Logger
}

type tokenResponse struct {
	Token string `json:"token"`
}

type profileResponse struct {
	DisplayName *string         `json:"displayName,omitempty"`
	Avatar      json.RawMessage `json:"avatar,omitempty"`
	Google      bool            `json:"google"`
	CreatedAt   time.Time       `json:"createdAt"`
}

// authedHandler serves a request whose bearer token resolved to acc.
type authedHandler func(w http.ResponseWriter, r *http.Request, acc accounts.Account, token string)

func New(d Deps) http.Handler {
	a := &api{accounts: d.Accounts, limiter: newAccountLimiter(d.AccountsPerHour, d.Clock), logger: d.Logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if !d.Ready() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /v1/accounts", a.createAccount)
	mux.HandleFunc("POST /v1/session/google", a.authed(a.signInWithGoogle))
	mux.HandleFunc("DELETE /v1/session", a.authed(a.signOut))
	mux.HandleFunc("GET /v1/me", a.authed(a.getProfile))
	mux.HandleFunc("PATCH /v1/me", a.authed(a.updateProfile))
	mux.HandleFunc("DELETE /v1/me", a.authed(a.deleteAccount))
	fallback := problemFallback(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
		fallback.ServeHTTP(w, r)
	})
}

func (a *api) createAccount(w http.ResponseWriter, r *http.Request) {
	// Forwarding headers are not trusted: any client could set them.
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !a.limiter.allow(host) {
		writeProblemDetail(w, http.StatusTooManyRequests, "too many accounts created from this address; retry later")
		return
	}
	token, err := a.accounts.CreateAccount(r.Context())
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, tokenResponse{Token: token})
}

func (a *api) authed(h authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		scheme, token, _ := strings.Cut(r.Header.Get("Authorization"), " ")
		if !strings.EqualFold(scheme, "Bearer") || token == "" {
			a.writeError(w, r, accounts.ErrUnauthenticated)
			return
		}
		acc, err := a.accounts.Authenticate(r.Context(), token)
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		h(w, r, acc, token)
	}
}

func (a *api) signInWithGoogle(w http.ResponseWriter, r *http.Request, acc accounts.Account, token string) {
	fields, ok := readObject(w, r, "idToken")
	if !ok {
		return
	}
	var idToken string
	if err := json.Unmarshal(fields["idToken"], &idToken); err != nil || idToken == "" {
		writeProblemDetail(w, http.StatusBadRequest, "idToken must be a non-empty string")
		return
	}
	newToken, err := a.accounts.SignInWithGoogle(r.Context(), acc, token, idToken)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, tokenResponse{Token: newToken})
}

func (a *api) signOut(w http.ResponseWriter, r *http.Request, acc accounts.Account, token string) {
	if err := a.accounts.SignOut(r.Context(), acc, token); err != nil {
		a.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (a *api) getProfile(w http.ResponseWriter, _ *http.Request, acc accounts.Account, _ string) {
	writeJSON(w, http.StatusOK, newProfileResponse(acc.Profile()))
}

func (a *api) updateProfile(w http.ResponseWriter, r *http.Request, acc accounts.Account, _ string) {
	fields, ok := readObject(w, r, "displayName", "avatar")
	if !ok {
		return
	}
	update, err := profileUpdate(fields)
	if err != nil {
		writeProblemDetail(w, http.StatusBadRequest, err.Error())
		return
	}
	profile, err := a.accounts.UpdateProfile(r.Context(), acc, update)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newProfileResponse(profile))
}

func (a *api) deleteAccount(w http.ResponseWriter, r *http.Request, acc accounts.Account, _ string) {
	if err := a.accounts.Delete(r.Context(), acc); err != nil {
		a.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// profileUpdate tells an absent field, which stays unchanged, from an
// explicit null, which clears it.
func profileUpdate(fields map[string]json.RawMessage) (accounts.ProfileUpdate, error) {
	var u accounts.ProfileUpdate
	if raw, ok := fields["displayName"]; ok {
		u.DisplayName.Set = true
		if !isNull(raw) {
			var name string
			if err := json.Unmarshal(raw, &name); err != nil {
				return u, errors.New("displayName must be a string or null")
			}
			u.DisplayName.Value = &name
		}
	}
	if raw, ok := fields["avatar"]; ok {
		u.Avatar.Set = true
		if !isNull(raw) {
			if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
				return u, errors.New("avatar must be a JSON object or null")
			}
			u.Avatar.Value = &raw
		}
	}
	return u, nil
}

func isNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// readObject reads a JSON object body with only the allowed keys, writing a
// problem response and returning false when it is anything else.
func readObject(w http.ResponseWriter, r *http.Request, allowed ...string) (map[string]json.RawMessage, bool) {
	body, err := io.ReadAll(r.Body)
	if maxErr := (*http.MaxBytesError)(nil); errors.As(err, &maxErr) {
		writeProblemDetail(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the body is larger than %d bytes", maxBodyBytes))
		return nil, false
	}
	if err != nil {
		writeProblemDetail(w, http.StatusBadRequest, "the body could not be read")
		return nil, false
	}
	var fields map[string]json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(&fields); err != nil || fields == nil || dec.More() {
		writeProblemDetail(w, http.StatusBadRequest, "the body must be one JSON object")
		return nil, false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeProblemDetail(w, http.StatusBadRequest, "the body must be one JSON object")
		return nil, false
	}
	for key := range fields {
		if !slices.Contains(allowed, key) {
			writeProblemDetail(w, http.StatusBadRequest, fmt.Sprintf("unknown field %s", key))
			return nil, false
		}
	}
	return fields, true
}

func (a *api) writeError(w http.ResponseWriter, r *http.Request, err error) {
	var fieldErr accounts.FieldError
	switch {
	case errors.Is(err, accounts.ErrUnauthenticated):
		w.Header().Set("WWW-Authenticate", "Bearer")
		writeProblem(w, http.StatusUnauthorized)
	case errors.Is(err, accounts.ErrGoogleDisabled):
		writeProblemDetail(w, http.StatusServiceUnavailable, "Google sign-in is not configured")
	case errors.Is(err, accounts.ErrGoogleUnavailable):
		writeProblemDetail(w, http.StatusServiceUnavailable, "Google sign-in is unavailable; retry later")
	case errors.Is(err, accounts.ErrInvalidGoogleToken):
		writeProblemDetail(w, http.StatusBadRequest, "idToken is not a valid Google ID token for this app")
	case errors.Is(err, accounts.ErrOtherGoogleAccount):
		writeProblemDetail(w, http.StatusConflict, "signed in with a different Google account; sign out first")
	case errors.Is(err, accounts.ErrDeviceOnly):
		writeProblemDetail(w, http.StatusConflict, "a device-only account cannot sign out; delete it instead")
	case errors.As(err, &fieldErr):
		writeProblemDetail(w, http.StatusBadRequest, fieldErr.Error())
	default:
		a.logger.Error("community request failed", "method", r.Method, "route", r.Pattern, "error", err)
		writeProblem(w, http.StatusInternalServerError)
	}
}

func newProfileResponse(p accounts.Profile) profileResponse {
	return profileResponse{DisplayName: p.DisplayName, Avatar: p.Avatar, Google: p.Google, CreatedAt: p.CreatedAt.UTC()}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
