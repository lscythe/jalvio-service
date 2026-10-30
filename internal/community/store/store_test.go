package store_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lscythe/jalvio-service/internal/community/accounts"
	"github.com/lscythe/jalvio-service/internal/community/store"
)

var t0 = time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)

func hash(token string) []byte {
	sum := sha256.Sum256([]byte(token))
	return sum[:]
}

// create opens a device account with a session for token.
func create(t *testing.T, s *store.Store, token string) accounts.Account {
	t.Helper()
	ctx := context.Background()
	if err := s.CreateAccount(ctx, hash(token), t0); err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	acc, err := s.SessionAccount(ctx, hash(token), t0)
	if err != nil {
		t.Fatalf("SessionAccount: %v", err)
	}
	return acc
}

func profile(t *testing.T, s *store.Store, id, name, avatar string) {
	t.Helper()
	u := accounts.ProfileUpdate{}
	if name != "" {
		u.DisplayName = accounts.Change[string]{Set: true, Value: &name}
	}
	if avatar != "" {
		value := json.RawMessage(avatar)
		u.Avatar = accounts.Change[json.RawMessage]{Set: true, Value: &value}
	}
	if _, err := s.UpdateProfile(context.Background(), id, u, t0); err != nil {
		t.Fatalf("UpdateProfile: %v", err)
	}
}

func sameJSON(t *testing.T, got json.RawMessage, want string) bool {
	t.Helper()
	var a, b any
	if err := json.Unmarshal(got, &a); err != nil {
		return false
	}
	if err := json.Unmarshal([]byte(want), &b); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(a, b)
}

func lastUsed(t *testing.T, db *pgxpool.Pool, token string) time.Time {
	t.Helper()
	var at time.Time
	if err := db.QueryRow(context.Background(), "select last_used_at from community.sessions where token_hash = $1", hash(token)).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

func accountExists(t *testing.T, db *pgxpool.Pool, id string) bool {
	t.Helper()
	var exists bool
	if err := db.QueryRow(context.Background(), "select exists (select 1 from community.accounts where id = $1)", id).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	return exists
}

func TestMigrateIsRepeatable(t *testing.T) {
	s, db := testStore(t)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	var versions []string
	rows, err := db.Query(context.Background(), "select version from community.schema_migrations order by version")
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, v)
	}
	if !reflect.DeepEqual(versions, []string{"0001"}) {
		t.Errorf("versions = %v, want [0001]", versions)
	}
}

func TestTablesHaveRowLevelSecurity(t *testing.T) {
	_, db := testStore(t)
	ctx := context.Background()
	rows, err := db.Query(ctx, `select c.relname, c.relrowsecurity from pg_class c
		join pg_namespace n on n.oid = c.relnamespace
		where n.nspname = 'community' and c.relkind = 'r'`)
	if err != nil {
		t.Fatal(err)
	}
	tables := 0
	for rows.Next() {
		var name string
		var secured bool
		if err := rows.Scan(&name, &secured); err != nil {
			t.Fatal(err)
		}
		tables++
		if !secured {
			t.Errorf("table community.%s has row-level security off", name)
		}
	}
	if tables < 3 {
		t.Errorf("found %d tables in schema community, want accounts, sessions and schema_migrations", tables)
	}
	var policies int
	if err := db.QueryRow(ctx, "select count(*) from pg_policies where schemaname = 'community'").Scan(&policies); err != nil {
		t.Fatal(err)
	}
	if policies != 0 {
		t.Errorf("schema community has %d policies, want none", policies)
	}
}

func TestSessionLifecycle(t *testing.T) {
	s, db := testStore(t)
	ctx := context.Background()
	acc := create(t, s, "a")
	if acc.ID == "" || acc.GoogleSub != "" || acc.DisplayName != nil || acc.Avatar != nil || !acc.CreatedAt.Equal(t0) {
		t.Errorf("new account = %+v, want an empty device account created at t0", acc)
	}
	if _, err := s.SessionAccount(ctx, hash("unknown"), t0); !errors.Is(err, accounts.ErrNoSession) {
		t.Errorf("unknown session = %v, want ErrNoSession", err)
	}

	if _, err := s.SessionAccount(ctx, hash("a"), t0.Add(23*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := lastUsed(t, db, "a"); !got.Equal(t0) {
		t.Errorf("last_used_at after 23 h = %v, want unchanged %v", got, t0)
	}
	later := t0.Add(24 * time.Hour)
	if _, err := s.SessionAccount(ctx, hash("a"), later); err != nil {
		t.Fatal(err)
	}
	if got := lastUsed(t, db, "a"); !got.Equal(later) {
		t.Errorf("last_used_at after 24 h = %v, want %v", got, later)
	}

	if err := s.DeleteSession(ctx, hash("a")); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}
	if _, err := s.SessionAccount(ctx, hash("a"), t0); !errors.Is(err, accounts.ErrNoSession) {
		t.Errorf("deleted session = %v, want ErrNoSession", err)
	}
}

func TestLinkGoogle(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	a := create(t, s, "a")
	b := create(t, s, "b")
	if _, found, err := s.AccountByGoogle(ctx, "sub-1"); err != nil || found {
		t.Errorf("AccountByGoogle before linking = %v, %v, want not found", found, err)
	}
	if err := s.LinkGoogle(ctx, a.ID, "sub-1", t0); err != nil {
		t.Fatalf("LinkGoogle: %v", err)
	}
	got, found, err := s.AccountByGoogle(ctx, "sub-1")
	if err != nil || !found || got.ID != a.ID || got.GoogleSub != "sub-1" {
		t.Errorf("AccountByGoogle = %+v, %v, %v, want account A", got, found, err)
	}
	if err := s.LinkGoogle(ctx, b.ID, "sub-1", t0); !errors.Is(err, accounts.ErrGoogleTaken) {
		t.Errorf("second link = %v, want ErrGoogleTaken", err)
	}
	if err := s.LinkGoogle(ctx, "00000000-0000-0000-0000-000000000000", "sub-2", t0); !errors.Is(err, accounts.ErrNoSession) {
		t.Errorf("link a missing account = %v, want ErrNoSession", err)
	}
}

func TestMerge(t *testing.T) {
	s, db := testStore(t)
	ctx := context.Background()
	a := create(t, s, "a")
	profile(t, s, a.ID, "Rina", `{"face":3}`)
	b := create(t, s, "b")
	if err := s.LinkGoogle(ctx, b.ID, "sub-b", t0); err != nil {
		t.Fatal(err)
	}

	if err := s.Merge(ctx, a.ID, b.ID, hash("merged"), t0); err != nil {
		t.Fatalf("Merge: %v", err)
	}
	got, err := s.SessionAccount(ctx, hash("merged"), t0)
	if err != nil || got.ID != b.ID {
		t.Fatalf("new session = %+v, %v, want account B", got, err)
	}
	if got.DisplayName == nil || *got.DisplayName != "Rina" || !sameJSON(t, got.Avatar, `{"face":3}`) {
		t.Errorf("B after merge = %v %s, want A's name and avatar", got.DisplayName, got.Avatar)
	}
	if _, err := s.SessionAccount(ctx, hash("a"), t0); !errors.Is(err, accounts.ErrNoSession) {
		t.Errorf("A's session after merge = %v, want ErrNoSession", err)
	}
	if accountExists(t, db, a.ID) {
		t.Error("A still exists after merge")
	}
	if err := s.Merge(ctx, a.ID, b.ID, hash("again"), t0); !errors.Is(err, accounts.ErrNoSession) {
		t.Errorf("merging a deleted account = %v, want ErrNoSession", err)
	}
}

func TestMergeRefusesALinkedOrTheSameAccount(t *testing.T) {
	s, db := testStore(t)
	ctx := context.Background()
	a := create(t, s, "a")
	if err := s.LinkGoogle(ctx, a.ID, "sub-a", t0); err != nil {
		t.Fatal(err)
	}
	b := create(t, s, "b")

	if err := s.Merge(ctx, a.ID, b.ID, hash("merged"), t0); !errors.Is(err, accounts.ErrOtherGoogleAccount) {
		t.Errorf("merging a linked account = %v, want ErrOtherGoogleAccount", err)
	}
	if !accountExists(t, db, a.ID) {
		t.Error("a linked account was deleted by a merge")
	}
	if err := s.Merge(ctx, b.ID, b.ID, hash("self"), t0); err == nil {
		t.Error("merging an account into itself succeeded")
	}
	if !accountExists(t, db, b.ID) {
		t.Error("an account merged into itself was deleted")
	}
}

func TestMergeKeepsTheGoogleAccountsProfile(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	a := create(t, s, "a")
	profile(t, s, a.ID, "Rina", `{"face":3}`)
	b := create(t, s, "b")
	profile(t, s, b.ID, "Budi", "")

	if err := s.Merge(ctx, a.ID, b.ID, hash("merged"), t0); err != nil {
		t.Fatal(err)
	}
	got, err := s.SessionAccount(ctx, hash("merged"), t0)
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName == nil || *got.DisplayName != "Budi" || !sameJSON(t, got.Avatar, `{"face":3}`) {
		t.Errorf("B after merge = %v %s, want Budi with A's avatar", got.DisplayName, got.Avatar)
	}
}

func TestMergeIsOneTransaction(t *testing.T) {
	s, db := testStore(t)
	ctx := context.Background()
	a := create(t, s, "a")
	profile(t, s, a.ID, "Rina", "")
	b := create(t, s, "b")

	if err := s.Merge(ctx, a.ID, b.ID, hash("b"), t0); err == nil {
		t.Fatal("Merge with a token hash already in use succeeded")
	}
	if !accountExists(t, db, a.ID) {
		t.Error("A was deleted by a failed merge")
	}
	got, err := s.SessionAccount(ctx, hash("b"), t0)
	if err != nil || got.DisplayName != nil {
		t.Errorf("B after a failed merge = %+v, %v, want its empty profile", got, err)
	}
}

func TestUpdateProfile(t *testing.T) {
	s, db := testStore(t)
	ctx := context.Background()
	a := create(t, s, "a")
	name := "Rina"
	avatar := json.RawMessage(`{"face":3}`)
	later := t0.Add(time.Hour)

	got, err := s.UpdateProfile(ctx, a.ID, accounts.ProfileUpdate{
		DisplayName: accounts.Change[string]{Set: true, Value: &name},
		Avatar:      accounts.Change[json.RawMessage]{Set: true, Value: &avatar},
	}, later)
	if err != nil || got.DisplayName == nil || *got.DisplayName != "Rina" || !sameJSON(t, got.Avatar, `{"face":3}`) {
		t.Fatalf("set = %+v, %v, want Rina with the avatar", got, err)
	}
	var updated time.Time
	if err := db.QueryRow(ctx, "select updated_at from community.accounts where id = $1", a.ID).Scan(&updated); err != nil {
		t.Fatal(err)
	}
	if !updated.Equal(later) {
		t.Errorf("updated_at = %v, want %v", updated, later)
	}

	got, err = s.UpdateProfile(ctx, a.ID, accounts.ProfileUpdate{DisplayName: accounts.Change[string]{Set: true}}, later)
	if err != nil || got.DisplayName != nil || !sameJSON(t, got.Avatar, `{"face":3}`) {
		t.Errorf("clear name = %+v, %v, want no name and the avatar kept", got, err)
	}
	got, err = s.UpdateProfile(ctx, a.ID, accounts.ProfileUpdate{Avatar: accounts.Change[json.RawMessage]{Set: true}}, later)
	if err != nil || got.Avatar != nil {
		t.Errorf("clear avatar = %+v, %v, want no avatar", got, err)
	}
	if _, err := s.UpdateProfile(ctx, "00000000-0000-0000-0000-000000000000", accounts.ProfileUpdate{}, later); !errors.Is(err, accounts.ErrNoSession) {
		t.Errorf("update a missing account = %v, want ErrNoSession", err)
	}
}

func TestDeleteAccountCascades(t *testing.T) {
	s, db := testStore(t)
	ctx := context.Background()
	a := create(t, s, "a")
	if err := s.Merge(ctx, create(t, s, "other").ID, a.ID, hash("a2"), t0); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAccount(ctx, a.ID); err != nil {
		t.Fatalf("DeleteAccount: %v", err)
	}
	var sessions int
	if err := db.QueryRow(ctx, "select count(*) from community.sessions").Scan(&sessions); err != nil {
		t.Fatal(err)
	}
	if sessions != 0 {
		t.Errorf("sessions after delete = %d, want 0", sessions)
	}
}

func TestDeleteIdleDeviceAccounts(t *testing.T) {
	s, db := testStore(t)
	ctx := context.Background()
	idle := create(t, s, "idle")
	recent := create(t, s, "recent")
	if err := s.CreateAccount(ctx, hash("recent-2"), t0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionAccount(ctx, hash("recent"), t0.AddDate(1, 2, 0)); err != nil {
		t.Fatal(err)
	}
	google := create(t, s, "google")
	if err := s.LinkGoogle(ctx, google.ID, "sub-g", t0); err != nil {
		t.Fatal(err)
	}

	deleted, err := s.DeleteIdleDeviceAccounts(ctx, t0.AddDate(1, 1, 0))
	if err != nil {
		t.Fatalf("DeleteIdleDeviceAccounts: %v", err)
	}
	if deleted != 2 {
		t.Errorf("deleted = %d, want 2 (idle and recent-2)", deleted)
	}
	if accountExists(t, db, idle.ID) {
		t.Error("idle device account kept")
	}
	if !accountExists(t, db, recent.ID) {
		t.Error("recently used device account deleted")
	}
	if !accountExists(t, db, google.ID) {
		t.Error("idle Google account deleted")
	}
}

func TestPing(t *testing.T) {
	s, _ := testStore(t)
	if err := s.Ping(context.Background()); err != nil {
		t.Errorf("Ping: %v", err)
	}
}

func TestOpenRejectsAnInvalidURL(t *testing.T) {
	if _, err := store.Open(context.Background(), "postgres://u@localhost:5432/db?pool_max_conns=x", 1); err == nil {
		t.Error("Open accepted an invalid URL")
	}
}
