package accounts_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/lscythe/jalvio-service/internal/community/accounts"
	"github.com/lscythe/jalvio-service/internal/community/google"
)

var now = time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)

var verifier = fakeVerifier{
	"id-a":      "sub-a",
	"id-b":      "sub-b",
	"id-down":   google.ErrUnavailable,
	"id-forged": google.ErrInvalidToken,
}

func newService(store *fakeStore) *accounts.Service {
	return accounts.New(store, verifier, func() time.Time { return now })
}

// device creates a device account and returns its token and account.
func device(t *testing.T, svc *accounts.Service) (string, accounts.Account) {
	t.Helper()
	ctx := context.Background()
	token, err := svc.CreateAccount(ctx)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	acc, err := svc.Authenticate(ctx, token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	return token, acc
}

// linked creates an account signed in with idToken and returns its token and account.
func linked(t *testing.T, svc *accounts.Service, idToken string) (string, accounts.Account) {
	t.Helper()
	token, acc := device(t, svc)
	token, err := svc.SignInWithGoogle(context.Background(), acc, token, idToken)
	if err != nil {
		t.Fatalf("SignInWithGoogle: %v", err)
	}
	acc, err = svc.Authenticate(context.Background(), token)
	if err != nil {
		t.Fatalf("Authenticate: %v", err)
	}
	return token, acc
}

func TestCreateAccountReturnsAUsableToken(t *testing.T) {
	svc := newService(newFakeStore())
	token, acc := device(t, svc)
	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil || len(decoded) != 32 {
		t.Errorf("token %q decodes to %d bytes (%v), want 32", token, len(decoded), err)
	}
	if acc.GoogleSub != "" || !acc.CreatedAt.Equal(now) {
		t.Errorf("account = %+v, want a device account created now", acc)
	}
	other, _ := device(t, svc)
	if other == token {
		t.Error("two accounts got the same token")
	}
}

func TestAuthenticateRejectsUnknownTokens(t *testing.T) {
	svc := newService(newFakeStore())
	for _, token := range []string{"", "not base64!", base64.RawURLEncoding.EncodeToString(make([]byte, 32)), "c2hvcnQ"} {
		if _, err := svc.Authenticate(context.Background(), token); !errors.Is(err, accounts.ErrUnauthenticated) {
			t.Errorf("Authenticate(%q) = %v, want ErrUnauthenticated", token, err)
		}
	}
}

func TestSignInLinksADeviceAccount(t *testing.T) {
	store := newFakeStore()
	svc := newService(store)
	token, acc := device(t, svc)
	got, err := svc.SignInWithGoogle(context.Background(), acc, token, "id-a")
	if err != nil || got != token {
		t.Fatalf("SignInWithGoogle = %q, %v, want the same token", got, err)
	}
	acc, err = svc.Authenticate(context.Background(), token)
	if err != nil || acc.GoogleSub != "sub-a" {
		t.Errorf("account = %+v, %v, want linked to sub-a", acc, err)
	}
}

func TestSignInMergesIntoTheGoogleAccount(t *testing.T) {
	store := newFakeStore()
	svc := newService(store)
	_, b := linked(t, svc, "id-b")
	token, a := device(t, svc)

	got, err := svc.SignInWithGoogle(context.Background(), a, token, "id-b")
	if err != nil {
		t.Fatalf("SignInWithGoogle: %v", err)
	}
	if got == token {
		t.Fatal("merge returned the device token, want a new one")
	}
	acc, err := svc.Authenticate(context.Background(), got)
	if err != nil || acc.ID != b.ID {
		t.Errorf("new token authenticates as %+v, %v, want account B", acc, err)
	}
	if _, err := svc.Authenticate(context.Background(), token); !errors.Is(err, accounts.ErrUnauthenticated) {
		t.Errorf("device token after merge = %v, want ErrUnauthenticated", err)
	}
	if store.count() != 1 {
		t.Errorf("accounts = %d, want only B", store.count())
	}
}

func TestSignInAgainWithTheLinkedAccount(t *testing.T) {
	svc := newService(newFakeStore())
	token, acc := linked(t, svc, "id-a")
	got, err := svc.SignInWithGoogle(context.Background(), acc, token, "id-a")
	if err != nil || got != token {
		t.Errorf("SignInWithGoogle = %q, %v, want the same token", got, err)
	}
}

func TestSignInWithAnotherGoogleAccount(t *testing.T) {
	svc := newService(newFakeStore())
	token, acc := linked(t, svc, "id-a")
	if _, err := svc.SignInWithGoogle(context.Background(), acc, token, "id-b"); !errors.Is(err, accounts.ErrOtherGoogleAccount) {
		t.Errorf("SignInWithGoogle = %v, want ErrOtherGoogleAccount", err)
	}
}

func TestSignInMergesWhenLinkLosesARace(t *testing.T) {
	store := newFakeStore()
	svc := newService(store)
	token, a := device(t, svc)
	var winner string
	store.beforeLink = func(s *fakeStore) { winner = s.insertLocked("sub-a", now, nil).ID }

	got, err := svc.SignInWithGoogle(context.Background(), a, token, "id-a")
	if err != nil {
		t.Fatalf("SignInWithGoogle: %v", err)
	}
	acc, err := svc.Authenticate(context.Background(), got)
	if err != nil || acc.ID != winner {
		t.Errorf("token authenticates as %+v, %v, want the account that won the link", acc, err)
	}
}

func TestMergeFillsEmptyProfileFields(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		bName    *string
		wantName string
	}{
		{"B without a name", nil, "Rina"},
		{"B with a name", ptr("Budi"), "Budi"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := newService(newFakeStore())
			_, b := linked(t, svc, "id-b")
			if _, err := svc.UpdateProfile(ctx, b, accounts.ProfileUpdate{DisplayName: accounts.Change[string]{Set: true, Value: tc.bName}}); err != nil {
				t.Fatal(err)
			}
			token, a := device(t, svc)
			avatar := raw(`{"face":3}`)
			if _, err := svc.UpdateProfile(ctx, a, accounts.ProfileUpdate{
				DisplayName: accounts.Change[string]{Set: true, Value: ptr("Rina")},
				Avatar:      accounts.Change[json.RawMessage]{Set: true, Value: &avatar},
			}); err != nil {
				t.Fatal(err)
			}
			merged, err := svc.SignInWithGoogle(ctx, a, token, "id-b")
			if err != nil {
				t.Fatal(err)
			}
			acc, err := svc.Authenticate(ctx, merged)
			if err != nil {
				t.Fatal(err)
			}
			if acc.DisplayName == nil || *acc.DisplayName != tc.wantName || string(acc.Avatar) != `{"face":3}` {
				t.Errorf("merged profile = %v %s, want %s with the device avatar", acc.DisplayName, acc.Avatar, tc.wantName)
			}
		})
	}
}

func TestGoogleSignInErrors(t *testing.T) {
	ctx := context.Background()
	disabled := accounts.New(newFakeStore(), nil, func() time.Time { return now })
	token, acc := device(t, disabled)
	if _, err := disabled.SignInWithGoogle(ctx, acc, token, "id-a"); !errors.Is(err, accounts.ErrGoogleDisabled) {
		t.Errorf("without a verifier = %v, want ErrGoogleDisabled", err)
	}

	svc := newService(newFakeStore())
	token, acc = device(t, svc)
	if _, err := svc.SignInWithGoogle(ctx, acc, token, "id-forged"); !errors.Is(err, accounts.ErrInvalidGoogleToken) {
		t.Errorf("invalid token = %v, want ErrInvalidGoogleToken", err)
	}
	if _, err := svc.SignInWithGoogle(ctx, acc, token, "id-down"); !errors.Is(err, accounts.ErrGoogleUnavailable) {
		t.Errorf("keys unavailable = %v, want ErrGoogleUnavailable", err)
	}
}

func TestSignOut(t *testing.T) {
	ctx := context.Background()
	svc := newService(newFakeStore())

	token, acc := linked(t, svc, "id-a")
	if err := svc.SignOut(ctx, acc, token); err != nil {
		t.Fatalf("SignOut: %v", err)
	}
	if _, err := svc.Authenticate(ctx, token); !errors.Is(err, accounts.ErrUnauthenticated) {
		t.Errorf("token after sign-out = %v, want ErrUnauthenticated", err)
	}

	token, acc = device(t, svc)
	if err := svc.SignOut(ctx, acc, token); !errors.Is(err, accounts.ErrDeviceOnly) {
		t.Errorf("device sign-out = %v, want ErrDeviceOnly", err)
	}
	if _, err := svc.Authenticate(ctx, token); err != nil {
		t.Errorf("device token after refused sign-out = %v, want it still valid", err)
	}
}

func TestDeleteEndsEverySession(t *testing.T) {
	ctx := context.Background()
	store := newFakeStore()
	svc := newService(store)
	first, b := linked(t, svc, "id-b")
	token, a := device(t, svc)
	second, err := svc.SignInWithGoogle(ctx, a, token, "id-b")
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Delete(ctx, b); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	for _, token := range []string{first, second} {
		if _, err := svc.Authenticate(ctx, token); !errors.Is(err, accounts.ErrUnauthenticated) {
			t.Errorf("token after delete = %v, want ErrUnauthenticated", err)
		}
	}
	if store.count() != 0 {
		t.Errorf("accounts = %d, want none", store.count())
	}
}

func TestAnAccountDeletedMidRequestIsUnauthenticated(t *testing.T) {
	ctx := context.Background()
	svc := newService(newFakeStore())
	token, acc := device(t, svc)
	_, b := linked(t, svc, "id-b")
	if err := svc.Delete(ctx, acc); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateProfile(ctx, acc, setName("Rina")); !errors.Is(err, accounts.ErrUnauthenticated) {
		t.Errorf("UpdateProfile = %v, want ErrUnauthenticated", err)
	}
	if _, err := svc.SignInWithGoogle(ctx, acc, token, "id-a"); !errors.Is(err, accounts.ErrUnauthenticated) {
		t.Errorf("link = %v, want ErrUnauthenticated", err)
	}
	if _, err := svc.SignInWithGoogle(ctx, acc, token, "id-b"); !errors.Is(err, accounts.ErrUnauthenticated) {
		t.Errorf("merge into %s = %v, want ErrUnauthenticated", b.ID, err)
	}
}

func TestRetentionDeletesIdleDeviceAccounts(t *testing.T) {
	store := newFakeStore()
	store.idleSince = make(chan time.Time)
	svc := newService(store)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		svc.RunRetention(ctx, 10*time.Millisecond, slog.New(slog.DiscardHandler))
		close(done)
	}()
	want := now.AddDate(0, -13, 0)
	for i := range 2 {
		select {
		case got := <-store.idleSince:
			if !got.Equal(want) {
				t.Errorf("run %d idleSince = %v, want %v", i, got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("retention run %d did not happen", i)
		}
	}
	cancel()
	select {
	case <-done:
	case <-store.idleSince:
		<-done
	case <-time.After(2 * time.Second):
		t.Fatal("RunRetention did not return after cancel")
	}
}
