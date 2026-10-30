package google

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrInvalidToken = errors.New("invalid Google ID token")
	ErrUnavailable  = errors.New("google signing keys unavailable")
)

const (
	defaultKeyLifetime = time.Hour
	// unknownKeyRefresh bounds how often a token with an unknown key id can
	// make the verifier fetch the key set again.
	unknownKeyRefresh = time.Minute
)

// issuers are the values Google puts in the iss claim of its ID tokens.
var issuers = []string{"https://accounts.google.com", "accounts.google.com"}

type Verifier struct {
	client    *http.Client
	keysURL   string
	clientIDs []string
	clock     func() time.Time

	mu        sync.Mutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
	expires   time.Time
}

func NewVerifier(client *http.Client, keysURL string, clientIDs []string, clock func() time.Time) *Verifier {
	return &Verifier{client: client, keysURL: keysURL, clientIDs: clientIDs, clock: clock}
}

// Verify checks a Google ID token issued to one of the client IDs and returns
// its subject, Google's stable account id.
func (v *Verifier) Verify(ctx context.Context, idToken string) (string, error) {
	var claims jwt.RegisteredClaims
	_, err := jwt.ParseWithClaims(idToken, &claims,
		func(token *jwt.Token) (any, error) { return v.key(ctx, token) },
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithLeeway(time.Minute),
		jwt.WithTimeFunc(v.clock),
		jwt.WithExpirationRequired(),
	)
	if errors.Is(err, ErrUnavailable) {
		return "", ErrUnavailable
	}
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	if !slices.Contains(issuers, claims.Issuer) {
		return "", fmt.Errorf("%w: issuer %q", ErrInvalidToken, claims.Issuer)
	}
	if !slices.ContainsFunc(claims.Audience, func(aud string) bool { return slices.Contains(v.clientIDs, aud) }) {
		return "", fmt.Errorf("%w: audience not a configured client ID", ErrInvalidToken)
	}
	if claims.Subject == "" {
		return "", fmt.Errorf("%w: no subject", ErrInvalidToken)
	}
	return claims.Subject, nil
}

func (v *Verifier) key(ctx context.Context, token *jwt.Token) (*rsa.PublicKey, error) {
	kid, _ := token.Header["kid"].(string)
	if kid == "" {
		return nil, errors.New("no kid")
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	now := v.clock()
	if v.keys == nil || !now.Before(v.expires) {
		if err := v.fetch(ctx, now); err != nil {
			return nil, err
		}
	}
	if key, ok := v.keys[kid]; ok {
		return key, nil
	}
	if now.Sub(v.fetchedAt) < unknownKeyRefresh {
		return nil, fmt.Errorf("unknown kid %q", kid)
	}
	if err := v.fetch(ctx, now); err != nil {
		return nil, err
	}
	if key, ok := v.keys[kid]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("unknown kid %q", kid)
}

func (v *Verifier) fetch(ctx context.Context, now time.Time) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.keysURL, nil)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: status %d", ErrUnavailable, resp.StatusCode)
	}
	var set struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&set); err != nil {
		return fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	keys := make(map[string]*rsa.PublicKey, len(set.Keys))
	for _, k := range set.Keys {
		if k.Kty != "RSA" {
			continue
		}
		n, errN := base64.RawURLEncoding.DecodeString(k.N)
		e, errE := base64.RawURLEncoding.DecodeString(k.E)
		if errN != nil || errE != nil || len(e) == 0 || len(e) > 4 {
			continue
		}
		keys[k.Kid] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(new(big.Int).SetBytes(e).Int64())}
	}
	v.keys = keys
	v.fetchedAt = now
	v.expires = now.Add(maxAge(resp.Header.Get("Cache-Control")))
	return nil
}

func maxAge(cacheControl string) time.Duration {
	for _, directive := range strings.Split(cacheControl, ",") {
		value, ok := strings.CutPrefix(strings.TrimSpace(directive), "max-age=")
		if !ok {
			continue
		}
		if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
			return time.Duration(seconds) * time.Second
		}
	}
	return defaultKeyLifetime
}
