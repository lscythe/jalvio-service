package accounts

import (
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	maxDisplayName = 40
	maxAvatarBytes = 2048
)

// Profile is what a rider sees of their own account.
type Profile struct {
	DisplayName *string
	Avatar      json.RawMessage
	Google      bool
	CreatedAt   time.Time
}

// Change is one field of an update: unset leaves it unchanged, Set with a nil
// Value clears it.
type Change[T any] struct {
	Set   bool
	Value *T
}

type ProfileUpdate struct {
	DisplayName Change[string]
	Avatar      Change[json.RawMessage]
}

type FieldError struct {
	Field, Reason string
}

func (e FieldError) Error() string { return e.Field + ": " + e.Reason }

func (a Account) Profile() Profile {
	return Profile{DisplayName: a.DisplayName, Avatar: a.Avatar, Google: a.GoogleSub != "", CreatedAt: a.CreatedAt}
}

// normalize validates u and returns it with the display name trimmed.
func normalize(u ProfileUpdate) (ProfileUpdate, error) {
	if name := u.DisplayName.Value; u.DisplayName.Set && name != nil {
		trimmed := strings.TrimSpace(*name)
		if trimmed == "" {
			return u, FieldError{"displayName", "must not be blank"}
		}
		if utf8.RuneCountInString(trimmed) > maxDisplayName {
			return u, FieldError{"displayName", "must be at most 40 characters"}
		}
		if strings.ContainsFunc(trimmed, unicode.IsControl) {
			return u, FieldError{"displayName", "must not contain control characters"}
		}
		u.DisplayName.Value = &trimmed
	}
	if avatar := u.Avatar.Value; u.Avatar.Set && avatar != nil {
		if len(*avatar) > maxAvatarBytes {
			return u, FieldError{"avatar", "must be at most 2048 bytes"}
		}
		var object map[string]json.RawMessage
		if err := json.Unmarshal(*avatar, &object); err != nil || object == nil {
			return u, FieldError{"avatar", "must be a JSON object"}
		}
	}
	return u, nil
}
