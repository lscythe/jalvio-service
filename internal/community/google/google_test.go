package google_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/lscythe/jalvio-service/internal/community/google"
)

const clientID = "a.apps"

var start = time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)

// keyServer serves a key set the way Google's endpoint does and counts fetches.
type keyServer struct {
	*httptest.Server
	requests atomic.Int32
	mu       sync.Mutex
	keys     map[string]*rsa.PrivateKey
}

func newKeyServer(t *testing.T, kids ...string) *keyServer {
	t.Helper()
	ks := &keyServer{keys: map[string]*rsa.PrivateKey{}}
	for _, kid := range kids {
		ks.keys[kid] = newKey(t)
	}
	ks.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		ks.requests.Add(1)
		ks.mu.Lock()
		defer ks.mu.Unlock()
		type jwk struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Use string `json:"use"`
			Alg string `json:"alg"`
			N   string `json:"n"`
			E   string `json:"e"`
		}
		var set struct {
			Keys []jwk `json:"keys"`
		}
		for kid, key := range ks.keys {
			set.Keys = append(set.Keys, jwk{
				Kty: "RSA", Kid: kid, Use: "sig", Alg: "RS256",
				N: base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
				E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
			})
		}
		w.Header().Set("Cache-Control", "public, max-age=3600, must-revalidate")
		if err := json.NewEncoder(w).Encode(set); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(ks.Close)
	return ks
}

func (ks *keyServer) key(kid string) *rsa.PrivateKey {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	return ks.keys[kid]
}

func (ks *keyServer) rotate(t *testing.T, kid string) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	ks.keys = map[string]*rsa.PrivateKey{kid: newKey(t)}
}

func newKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

func claims(mutate func(jwt.MapClaims)) jwt.MapClaims {
	c := jwt.MapClaims{
		"iss": "https://accounts.google.com",
		"aud": clientID,
		"sub": "1234",
		"iat": start.Unix(),
		"exp": start.Add(10 * time.Minute).Unix(),
	}
	if mutate != nil {
		mutate(c)
	}
	return c
}

func sign(t *testing.T, key *rsa.PrivateKey, kid string, c jwt.MapClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, c)
	token.Header["kid"] = kid
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return signed
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

func newVerifier(url string, c *clock) *google.Verifier {
	return google.NewVerifier(&http.Client{Timeout: 2 * time.Second}, url, []string{"other.apps", clientID}, c.Now)
}

func TestVerifyAcceptsAGoogleToken(t *testing.T) {
	ks := newKeyServer(t, "k1")
	v := newVerifier(ks.URL, &clock{now: start})
	for _, iss := range []string{"https://accounts.google.com", "accounts.google.com"} {
		token := sign(t, ks.key("k1"), "k1", claims(func(c jwt.MapClaims) { c["iss"] = iss }))
		sub, err := v.Verify(context.Background(), token)
		if err != nil || sub != "1234" {
			t.Errorf("Verify(iss %s) = %q, %v, want 1234", iss, sub, err)
		}
	}
}

func TestVerifyAcceptsATokenExpiredWithinTheLeeway(t *testing.T) {
	ks := newKeyServer(t, "k1")
	v := newVerifier(ks.URL, &clock{now: start})
	token := sign(t, ks.key("k1"), "k1", claims(func(c jwt.MapClaims) { c["exp"] = start.Add(-30 * time.Second).Unix() }))
	if _, err := v.Verify(context.Background(), token); err != nil {
		t.Errorf("Verify = %v, want a token 30 s past expiry accepted", err)
	}
}

func TestVerifyRejects(t *testing.T) {
	ks := newKeyServer(t, "k1")
	v := newVerifier(ks.URL, &clock{now: start})
	k1 := ks.key("k1")
	hs256, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims(nil)).SignedString([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, token string
	}{
		{"other audience", sign(t, k1, "k1", claims(func(c jwt.MapClaims) { c["aud"] = "someone.apps" }))},
		{"other issuer", sign(t, k1, "k1", claims(func(c jwt.MapClaims) { c["iss"] = "https://issuer.example.com" }))},
		{"expired", sign(t, k1, "k1", claims(func(c jwt.MapClaims) { c["exp"] = start.Add(-2 * time.Minute).Unix() }))},
		{"no expiry", sign(t, k1, "k1", claims(func(c jwt.MapClaims) { delete(c, "exp") }))},
		{"empty subject", sign(t, k1, "k1", claims(func(c jwt.MapClaims) { c["sub"] = "" }))},
		{"another key with the same kid", sign(t, newKey(t), "k1", claims(nil))},
		{"no kid", sign(t, k1, "", claims(nil))},
		{"HS256", hs256},
		{"not a JWT", "not-a-token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := v.Verify(context.Background(), tc.token); !errors.Is(err, google.ErrInvalidToken) {
				t.Errorf("Verify = %v, want ErrInvalidToken", err)
			}
		})
	}
}

func TestVerifyCachesKeys(t *testing.T) {
	ks := newKeyServer(t, "k1")
	c := &clock{now: start}
	v := newVerifier(ks.URL, c)
	token := sign(t, ks.key("k1"), "k1", claims(func(cl jwt.MapClaims) { cl["exp"] = start.Add(3 * time.Hour).Unix() }))
	for range 2 {
		if _, err := v.Verify(context.Background(), token); err != nil {
			t.Fatalf("Verify: %v", err)
		}
	}
	if got := ks.requests.Load(); got != 1 {
		t.Errorf("key requests = %d, want 1", got)
	}
	c.advance(time.Hour + time.Second)
	if _, err := v.Verify(context.Background(), token); err != nil {
		t.Fatalf("Verify after max-age: %v", err)
	}
	if got := ks.requests.Load(); got != 2 {
		t.Errorf("key requests after max-age = %d, want 2", got)
	}
}

func TestVerifyRefreshesForAnUnknownKey(t *testing.T) {
	ks := newKeyServer(t, "k1")
	c := &clock{now: start}
	v := newVerifier(ks.URL, c)
	if _, err := v.Verify(context.Background(), sign(t, ks.key("k1"), "k1", claims(nil))); err != nil {
		t.Fatalf("Verify k1: %v", err)
	}
	ks.rotate(t, "k2")
	c.advance(2 * time.Minute)
	if _, err := v.Verify(context.Background(), sign(t, ks.key("k2"), "k2", claims(nil))); err != nil {
		t.Fatalf("Verify k2: %v", err)
	}
	if got := ks.requests.Load(); got != 2 {
		t.Errorf("key requests = %d, want 2", got)
	}
	_, err := v.Verify(context.Background(), sign(t, newKey(t), "k3", claims(nil)))
	if !errors.Is(err, google.ErrInvalidToken) {
		t.Errorf("Verify k3 = %v, want ErrInvalidToken", err)
	}
	if got := ks.requests.Load(); got != 2 {
		t.Errorf("key requests after a second unknown kid within a minute = %d, want 2", got)
	}
}

func TestVerifyUnavailable(t *testing.T) {
	key := newKey(t)
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "down", http.StatusInternalServerError)
	}))
	t.Cleanup(failing.Close)
	hanging := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	t.Cleanup(hanging.Close)
	invalid := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html>"))
	}))
	t.Cleanup(invalid.Close)

	for name, url := range map[string]string{"failing": failing.URL, "hanging": hanging.URL, "not JSON": invalid.URL} {
		t.Run(name, func(t *testing.T) {
			v := newVerifier(url, &clock{now: start})
			began := time.Now()
			_, err := v.Verify(context.Background(), sign(t, key, "k1", claims(nil)))
			if !errors.Is(err, google.ErrUnavailable) {
				t.Errorf("Verify = %v, want ErrUnavailable", err)
			}
			if elapsed := time.Since(began); elapsed > 3*time.Second {
				t.Errorf("Verify took %v, want under 3 s", elapsed)
			}
		})
	}
}
