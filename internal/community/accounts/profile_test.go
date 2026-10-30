package accounts_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/lscythe/jalvio-service/internal/community/accounts"
)

func setName(name string) accounts.ProfileUpdate {
	return accounts.ProfileUpdate{DisplayName: accounts.Change[string]{Set: true, Value: &name}}
}

func setAvatar(avatar string) accounts.ProfileUpdate {
	value := raw(avatar)
	return accounts.ProfileUpdate{Avatar: accounts.Change[json.RawMessage]{Set: true, Value: &value}}
}

func TestUpdateProfile(t *testing.T) {
	ctx := context.Background()
	svc := newService(newFakeStore())
	_, acc := device(t, svc)

	profile, err := svc.UpdateProfile(ctx, acc, setName("  Rina  "))
	if err != nil || profile.DisplayName == nil || *profile.DisplayName != "Rina" {
		t.Fatalf("set name = %+v, %v, want Rina trimmed", profile, err)
	}
	profile, err = svc.UpdateProfile(ctx, acc, setAvatar(`{"face":3}`))
	if err != nil || string(profile.Avatar) != `{"face":3}` || profile.DisplayName == nil || *profile.DisplayName != "Rina" {
		t.Fatalf("set avatar = %+v, %v, want the avatar with the name unchanged", profile, err)
	}
	if profile.Google || !profile.CreatedAt.Equal(now) {
		t.Errorf("profile = %+v, want a device profile created now", profile)
	}

	profile, err = svc.UpdateProfile(ctx, acc, accounts.ProfileUpdate{DisplayName: accounts.Change[string]{Set: true}})
	if err != nil || profile.DisplayName != nil || profile.Avatar == nil {
		t.Fatalf("clear name = %+v, %v, want no name and the avatar kept", profile, err)
	}
	profile, err = svc.UpdateProfile(ctx, acc, accounts.ProfileUpdate{Avatar: accounts.Change[json.RawMessage]{Set: true}})
	if err != nil || profile.Avatar != nil {
		t.Fatalf("clear avatar = %+v, %v, want no avatar", profile, err)
	}
	if _, err := svc.UpdateProfile(ctx, acc, setName(strings.Repeat("😀", 40))); err != nil {
		t.Errorf("40 emoji = %v, want accepted", err)
	}
}

func TestProfileOfALinkedAccount(t *testing.T) {
	_, acc := linked(t, newService(newFakeStore()), "id-a")
	if !acc.Profile().Google {
		t.Error("Profile().Google = false for a linked account")
	}
}

func TestUpdateProfileRejects(t *testing.T) {
	svc := newService(newFakeStore())
	_, acc := device(t, svc)
	for _, tc := range []struct {
		name   string
		update accounts.ProfileUpdate
		field  string
	}{
		{"blank name", setName("   "), "displayName"},
		{"41 emoji", setName(strings.Repeat("😀", 41)), "displayName"},
		{"control character", setName("Ri\u0007na"), "displayName"},
		{"avatar array", setAvatar(`[]`), "avatar"},
		{"avatar string", setAvatar(`"x"`), "avatar"},
		{"avatar JSON null", setAvatar(`null`), "avatar"},
		{"avatar not JSON", setAvatar(`{"face":`), "avatar"},
		{"avatar too large", setAvatar(`{"a":"` + strings.Repeat("x", 2049-8) + `"}`), "avatar"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.UpdateProfile(context.Background(), acc, tc.update)
			var fe accounts.FieldError
			if !errors.As(err, &fe) || fe.Field != tc.field || fe.Reason == "" {
				t.Errorf("UpdateProfile = %v, want a FieldError for %s", err, tc.field)
			}
		})
	}
	if _, err := svc.UpdateProfile(context.Background(), acc, setAvatar(`{"a":"`+strings.Repeat("x", 2048-8)+`"}`)); err != nil {
		t.Errorf("2048-byte avatar = %v, want accepted", err)
	}
}
