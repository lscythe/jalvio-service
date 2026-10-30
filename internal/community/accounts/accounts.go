package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/lscythe/jalvio-service/internal/community/google"
)

// Store errors.
var (
	ErrNoSession   = errors.New("no such session")
	ErrGoogleTaken = errors.New("google account already linked to another account")
)

// Service errors.
var (
	ErrUnauthenticated    = errors.New("unauthenticated")
	ErrGoogleDisabled     = errors.New("google sign-in is not configured")
	ErrGoogleUnavailable  = errors.New("google sign-in is unavailable")
	ErrInvalidGoogleToken = errors.New("invalid Google ID token")
	ErrOtherGoogleAccount = errors.New("signed in with a different Google account")
	ErrDeviceOnly         = errors.New("a device-only account cannot sign out")
)

const (
	tokenBytes = 32
	// idleDeviceMonths is how long a device-only account may go unused before
	// it is taken to belong to an uninstalled app.
	idleDeviceMonths = 13
)

type Account struct {
	ID          string
	DisplayName *string
	Avatar      json.RawMessage
	// GoogleSub is empty for a device-only account.
	GoogleSub string
	CreatedAt time.Time
}

type Store interface {
	CreateAccount(ctx context.Context, tokenHash []byte, now time.Time) error
	SessionAccount(ctx context.Context, tokenHash []byte, now time.Time) (Account, error)
	AccountByGoogle(ctx context.Context, sub string) (Account, bool, error)
	LinkGoogle(ctx context.Context, accountID, sub string, now time.Time) error
	// Merge moves fromID's content into intoID, fills intoID's empty profile
	// fields from fromID, deletes fromID and opens a session for intoID.
	Merge(ctx context.Context, fromID, intoID string, tokenHash []byte, now time.Time) error
	UpdateProfile(ctx context.Context, accountID string, u ProfileUpdate, now time.Time) (Account, error)
	DeleteSession(ctx context.Context, tokenHash []byte) error
	DeleteAccount(ctx context.Context, accountID string) error
	DeleteIdleDeviceAccounts(ctx context.Context, idleSince time.Time) (int, error)
}

type GoogleVerifier interface {
	Verify(ctx context.Context, idToken string) (string, error)
}

type Service struct {
	store    Store
	verifier GoogleVerifier
	clock    func() time.Time
}

// New returns the account rules; a nil verifier turns Google sign-in off.
func New(store Store, verifier GoogleVerifier, clock func() time.Time) *Service {
	return &Service{store: store, verifier: verifier, clock: clock}
}

// CreateAccount opens a device-only account and returns its session token.
func (s *Service) CreateAccount(ctx context.Context) (string, error) {
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}
	if err := s.store.CreateAccount(ctx, hash, s.clock()); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Service) Authenticate(ctx context.Context, token string) (Account, error) {
	hash, ok := hashToken(token)
	if !ok {
		return Account{}, ErrUnauthenticated
	}
	acc, err := s.store.SessionAccount(ctx, hash, s.clock())
	if errors.Is(err, ErrNoSession) {
		return Account{}, ErrUnauthenticated
	}
	return acc, err
}

// SignInWithGoogle links acc to the Google account behind idToken, or merges
// acc into the account already linked to it, and returns the token to use
// from now on.
func (s *Service) SignInWithGoogle(ctx context.Context, acc Account, token, idToken string) (string, error) {
	if s.verifier == nil {
		return "", ErrGoogleDisabled
	}
	sub, err := s.verifier.Verify(ctx, idToken)
	switch {
	case errors.Is(err, google.ErrInvalidToken):
		return "", ErrInvalidGoogleToken
	case err != nil:
		return "", fmt.Errorf("%w: %w", ErrGoogleUnavailable, err)
	}
	if acc.GoogleSub == sub {
		return token, nil
	}
	if acc.GoogleSub != "" {
		return "", ErrOtherGoogleAccount
	}
	owner, found, err := s.store.AccountByGoogle(ctx, sub)
	if err != nil {
		return "", err
	}
	if found {
		return s.merge(ctx, acc, owner)
	}
	err = s.store.LinkGoogle(ctx, acc.ID, sub, s.clock())
	if !errors.Is(err, ErrGoogleTaken) {
		return token, err
	}
	// Another phone linked the same Google account first.
	owner, found, err = s.store.AccountByGoogle(ctx, sub)
	if err != nil {
		return "", err
	}
	if !found {
		return "", fmt.Errorf("google account taken but not found: %w", ErrGoogleTaken)
	}
	return s.merge(ctx, acc, owner)
}

func (s *Service) merge(ctx context.Context, from, into Account) (string, error) {
	token, hash, err := newToken()
	if err != nil {
		return "", err
	}
	if err := s.store.Merge(ctx, from.ID, into.ID, hash, s.clock()); err != nil {
		return "", err
	}
	return token, nil
}

func (s *Service) UpdateProfile(ctx context.Context, acc Account, u ProfileUpdate) (Profile, error) {
	u, err := normalize(u)
	if err != nil {
		return Profile{}, err
	}
	updated, err := s.store.UpdateProfile(ctx, acc.ID, u, s.clock())
	if err != nil {
		return Profile{}, err
	}
	return updated.Profile(), nil
}

// SignOut ends the session behind token. A device-only account has no way
// back in, so it can only be deleted.
func (s *Service) SignOut(ctx context.Context, acc Account, token string) error {
	if acc.GoogleSub == "" {
		return ErrDeviceOnly
	}
	hash, ok := hashToken(token)
	if !ok {
		return ErrUnauthenticated
	}
	return s.store.DeleteSession(ctx, hash)
}

// Delete removes the account and everything tied to it, ending every session.
func (s *Service) Delete(ctx context.Context, acc Account) error {
	return s.store.DeleteAccount(ctx, acc.ID)
}

// RunRetention deletes idle device-only accounts now and then every interval
// until ctx ends.
func (s *Service) RunRetention(ctx context.Context, every time.Duration, logger *slog.Logger) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		deleted, err := s.store.DeleteIdleDeviceAccounts(ctx, s.clock().AddDate(0, -idleDeviceMonths, 0))
		switch {
		case err != nil && ctx.Err() == nil:
			logger.Warn("community retention failed", "error", err)
		case deleted > 0:
			logger.Info("community retention", "deleted", deleted)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func newToken() (token string, hash []byte, err error) {
	raw := make([]byte, tokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	sum := sha256.Sum256(raw)
	return base64.RawURLEncoding.EncodeToString(raw), sum[:], nil
}

// hashToken returns the stored form of a well-formed token.
func hashToken(token string) ([]byte, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(raw) != tokenBytes {
		return nil, false
	}
	sum := sha256.Sum256(raw)
	return sum[:], true
}
