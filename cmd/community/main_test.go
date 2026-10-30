package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/lscythe/jalvio-service/internal/community/config"
)

var discard = slog.New(slog.DiscardHandler)

func listen(t *testing.T) net.Listener {
	t.Helper()
	ln, err := new(net.ListenConfig).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return ln
}

func waitDone(t *testing.T, done <-chan error) {
	t.Helper()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("service: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("service did not stop")
	}
}

// testDatabaseURL creates an empty database for one test on the server named
// by COMMUNITY_TEST_DATABASE_URL and drops it afterwards.
func testDatabaseURL(t *testing.T) string {
	t.Helper()
	admin := os.Getenv("COMMUNITY_TEST_DATABASE_URL")
	if admin == "" {
		t.Skip("COMMUNITY_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, admin)
	if err != nil {
		if os.Getenv("CI") != "" {
			t.Fatalf("test database unreachable: %v", err)
		}
		t.Skipf("test database unreachable (start it with mise run community-db): %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	suffix := make([]byte, 8)
	_, _ = rand.Read(suffix)
	name := "t_" + hex.EncodeToString(suffix)
	if _, err := conn.Exec(ctx, "create database "+pgx.Identifier{name}.Sanitize()); err != nil {
		t.Fatalf("create database: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		conn, err := pgx.Connect(ctx, admin)
		if err != nil {
			t.Errorf("drop database: %v", err)
			return
		}
		defer func() { _ = conn.Close(context.Background()) }()
		if _, err := conn.Exec(ctx, "drop database "+pgx.Identifier{name}.Sanitize()+" with (force)"); err != nil {
			t.Errorf("drop database: %v", err)
		}
	})
	u, err := url.Parse(admin)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

// startService runs the service on a fresh database until the test ends and
// waits until it is ready.
func startService(t *testing.T, cfg config.Config) string {
	t.Helper()
	cfg.DatabaseURL = testDatabaseURL(t)
	cfg.MaxConns = 2
	cfg.AccountsPerHour = 10
	ln := listen(t)
	base := "http://" + ln.Addr().String()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- runService(ctx, cfg, ln, discard) }()
	t.Cleanup(func() {
		cancel()
		waitDone(t, done)
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if status, _ := send(t, http.MethodGet, base+"/healthz", "", ""); status == http.StatusNoContent {
			return base
		}
		select {
		case err := <-done:
			t.Fatalf("service stopped before becoming ready: %v", err)
		case <-time.After(50 * time.Millisecond):
		}
	}
	t.Fatal("service did not become ready")
	return ""
}

func send(t *testing.T, method, url, token, body string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, url, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err.Error()
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func createAccount(t *testing.T, base string) string {
	t.Helper()
	status, body := send(t, http.MethodPost, base+"/v1/accounts", "", "")
	if status != http.StatusCreated {
		t.Fatalf("POST /v1/accounts = %d %s, want 201", status, body)
	}
	var created struct{ Token string }
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.Token == "" {
		t.Fatalf("POST /v1/accounts body %s: %v", body, err)
	}
	return created.Token
}

func TestRunServiceMigratesAndBecomesReady(t *testing.T) {
	base := startService(t, config.Config{})
	token := createAccount(t, base)
	status, body := send(t, http.MethodPatch, base+"/v1/me", token, `{"displayName":" Rina "}`)
	if status != http.StatusOK || !strings.Contains(body, `"displayName":"Rina"`) {
		t.Errorf("PATCH /v1/me = %d %s, want 200 with Rina", status, body)
	}
	status, body = send(t, http.MethodGet, base+"/v1/me", token, "")
	if status != http.StatusOK || !strings.Contains(body, `"displayName":"Rina"`) {
		t.Errorf("GET /v1/me = %d %s, want 200 with Rina", status, body)
	}
	if status, _ := send(t, http.MethodDelete, base+"/v1/me", token, ""); status != http.StatusNoContent {
		t.Errorf("DELETE /v1/me = %d, want 204", status)
	}
	if status, _ := send(t, http.MethodGet, base+"/v1/me", token, ""); status != http.StatusUnauthorized {
		t.Errorf("GET /v1/me after delete = %d, want 401", status)
	}
}

func TestGoogleSignInDisabledWithoutClientIDs(t *testing.T) {
	base := startService(t, config.Config{})
	token := createAccount(t, base)
	status, body := send(t, http.MethodPost, base+"/v1/session/google", token, `{"idToken":"x"}`)
	if status != http.StatusServiceUnavailable || !strings.Contains(body, "not configured") {
		t.Errorf("sign-in = %d %s, want 503 not configured", status, body)
	}
}

func TestGoogleSignInUsesTheConfiguredKeys(t *testing.T) {
	var fetched atomic.Bool
	keys := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fetched.Store(true)
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer keys.Close()
	base := startService(t, config.Config{GoogleClientIDs: []string{"a.apps"}, GoogleKeysURL: keys.URL})
	token := createAccount(t, base)
	// A well-formed token header makes the verifier look up its key.
	idToken := "eyJhbGciOiJSUzI1NiIsImtpZCI6ImsxIiwidHlwIjoiSldUIn0.e30.c2ln"
	status, body := send(t, http.MethodPost, base+"/v1/session/google", token, `{"idToken":"`+idToken+`"}`)
	if status != http.StatusServiceUnavailable || !strings.Contains(body, "unavailable") || !fetched.Load() {
		t.Errorf("sign-in = %d %s (keys fetched %v), want 503 unavailable after fetching the keys", status, body, fetched.Load())
	}
}

func TestRunServiceFailsWithoutTheDatabase(t *testing.T) {
	closed := listen(t)
	addr := closed.Addr().String()
	_ = closed.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cfg := config.Config{DatabaseURL: "postgres://community@" + addr + "/community?sslmode=disable", MaxConns: 1, AccountsPerHour: 1}
	if err := runService(ctx, cfg, listen(t), discard); err == nil {
		t.Error("runService started without a database")
	}
}

func TestRunRejectsInsecureRemoteDatabase(t *testing.T) {
	getenv := func(key string) string {
		return map[string]string{
			"COMMUNITY_DATABASE_URL": "postgres://u@db.example.com/db",
			"COMMUNITY_LISTEN":       "127.0.0.1:0",
		}[key]
	}
	err := run(t.Context(), nil, getenv, discard)
	if err == nil || !strings.Contains(err.Error(), "sslmode=verify-full") {
		t.Errorf("run = %v, want an error asking for sslmode=verify-full", err)
	}
}

func TestServeDrainsOnCancel(t *testing.T) {
	ln := listen(t)
	url := "http://" + ln.Addr().String() + "/"
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	var draining atomic.Bool
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- serve(ctx, ln, handler, &draining, discard) }()

	if status, _ := send(t, http.MethodGet, url, "", ""); status != http.StatusNoContent {
		t.Fatalf("GET = %d, want %d", status, http.StatusNoContent)
	}
	cancel()
	waitDone(t, done)
	if !draining.Load() {
		t.Error("draining not set during shutdown")
	}
	if status, _ := send(t, http.MethodGet, url, "", ""); status != 0 {
		t.Fatal("server still accepting requests after shutdown")
	}
}

func listenOn(addr string) func(string) string {
	return func(key string) string {
		if key == "COMMUNITY_LISTEN" {
			return addr
		}
		return ""
	}
}

func TestHealthcheck(t *testing.T) {
	base := startService(t, config.Config{})
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(base, "http://"))
	if err := run(t.Context(), []string{"healthcheck"}, listenOn(":"+port), discard); err != nil {
		t.Errorf("healthcheck of a ready service: %v", err)
	}

	unready := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer unready.Close()
	if err := run(t.Context(), []string{"healthcheck"}, listenOn(unready.Listener.Addr().String()), discard); err == nil {
		t.Error("healthcheck of an unready service succeeded")
	}

	closed := listen(t)
	addr := closed.Addr().String()
	_ = closed.Close()
	if err := run(t.Context(), []string{"healthcheck"}, listenOn(addr), discard); err == nil {
		t.Error("healthcheck with nothing listening succeeded")
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	if err := run(t.Context(), []string{"serve-forever"}, listenOn(":0"), discard); err == nil {
		t.Error("run accepted an unknown command")
	}
}
