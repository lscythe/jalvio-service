package accounts_test

import (
	"context"
	"encoding/json"
	"strconv"
	"sync"
	"time"

	"github.com/lscythe/jalvio-service/internal/community/accounts"
	"github.com/lscythe/jalvio-service/internal/community/google"
)

// fakeStore keeps accounts in memory with the same rules as the PostgreSQL store.
type fakeStore struct {
	mu       sync.Mutex
	nextID   int
	accounts map[string]*accounts.Account
	sessions map[string]string // token hash to account id
	// beforeLink runs inside LinkGoogle before the uniqueness check, to stage a race.
	beforeLink func(*fakeStore)
	idleSince  chan time.Time
}

func newFakeStore() *fakeStore {
	return &fakeStore{accounts: map[string]*accounts.Account{}, sessions: map[string]string{}}
}

func (s *fakeStore) CreateAccount(_ context.Context, tokenHash []byte, now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.insertLocked("", now, tokenHash)
	return nil
}

func (s *fakeStore) insertLocked(googleSub string, now time.Time, tokenHash []byte) *accounts.Account {
	s.nextID++
	acc := &accounts.Account{ID: "acc-" + strconv.Itoa(s.nextID), GoogleSub: googleSub, CreatedAt: now}
	s.accounts[acc.ID] = acc
	if tokenHash != nil {
		s.sessions[string(tokenHash)] = acc.ID
	}
	return acc
}

func (s *fakeStore) SessionAccount(_ context.Context, tokenHash []byte, _ time.Time) (accounts.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id, ok := s.sessions[string(tokenHash)]
	if !ok {
		return accounts.Account{}, accounts.ErrNoSession
	}
	return *s.accounts[id], nil
}

func (s *fakeStore) AccountByGoogle(_ context.Context, sub string) (accounts.Account, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, acc := range s.accounts {
		if acc.GoogleSub == sub {
			return *acc, true, nil
		}
	}
	return accounts.Account{}, false, nil
}

func (s *fakeStore) LinkGoogle(_ context.Context, accountID, sub string, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.beforeLink != nil {
		s.beforeLink(s)
		s.beforeLink = nil
	}
	for _, acc := range s.accounts {
		if acc.GoogleSub == sub {
			return accounts.ErrGoogleTaken
		}
	}
	acc, ok := s.accounts[accountID]
	if !ok {
		return accounts.ErrNoSession
	}
	acc.GoogleSub = sub
	return nil
}

func (s *fakeStore) Merge(_ context.Context, fromID, intoID string, tokenHash []byte, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	from, into := s.accounts[fromID], s.accounts[intoID]
	if from == nil || into == nil {
		return accounts.ErrNoSession
	}
	if into.DisplayName == nil {
		into.DisplayName = from.DisplayName
	}
	if into.Avatar == nil {
		into.Avatar = from.Avatar
	}
	s.deleteLocked(fromID)
	s.sessions[string(tokenHash)] = intoID
	return nil
}

func (s *fakeStore) UpdateProfile(_ context.Context, accountID string, u accounts.ProfileUpdate, _ time.Time) (accounts.Account, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	acc, ok := s.accounts[accountID]
	if !ok {
		return accounts.Account{}, accounts.ErrNoSession
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
	return *acc, nil
}

func (s *fakeStore) DeleteSession(_ context.Context, tokenHash []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, string(tokenHash))
	return nil
}

func (s *fakeStore) DeleteAccount(_ context.Context, accountID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteLocked(accountID)
	return nil
}

func (s *fakeStore) deleteLocked(accountID string) {
	delete(s.accounts, accountID)
	for hash, id := range s.sessions {
		if id == accountID {
			delete(s.sessions, hash)
		}
	}
}

func (s *fakeStore) DeleteIdleDeviceAccounts(_ context.Context, idleSince time.Time) (int, error) {
	s.idleSince <- idleSince
	return 0, nil
}

func (s *fakeStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.accounts)
}

// fakeVerifier maps ID tokens to Google subjects or errors.
type fakeVerifier map[string]any

func (v fakeVerifier) Verify(_ context.Context, idToken string) (string, error) {
	switch result := v[idToken].(type) {
	case string:
		return result, nil
	case error:
		return "", result
	}
	return "", google.ErrInvalidToken
}

func ptr[T any](v T) *T { return &v }

func raw(s string) json.RawMessage { return json.RawMessage(s) }
