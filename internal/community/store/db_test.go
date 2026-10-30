package store_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lscythe/jalvio-service/internal/community/store"
)

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

// testStore returns a migrated store on a fresh database and a pool for
// inspecting it directly.
func testStore(t *testing.T) (*store.Store, *pgxpool.Pool) {
	t.Helper()
	databaseURL := testDatabaseURL(t)
	ctx := context.Background()
	s, err := store.Open(ctx, databaseURL, 4)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	db, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	return s, db
}
