package store

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
)

//go:embed migrations/*.sql
var migrations embed.FS

// migrationLock is the advisory lock key that serializes migrations across
// instances starting at once.
const migrationLock = 7_246_011_001

// bootstrap runs before every migration. Row-level security with no policies
// keeps hosted databases' generated APIs from reading the schema.
const bootstrap = `
create schema if not exists community;
create table if not exists community.schema_migrations (
    version text primary key,
    applied_at timestamptz not null default now()
);
alter table community.schema_migrations enable row level security;`

// Migrate applies the embedded migrations that have not run yet, each in its
// own transaction, in file name order.
func (s *Store) Migrate(ctx context.Context) error {
	names, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	slices.Sort(names)
	for _, name := range names {
		if err := s.migrate(ctx, name); err != nil {
			return fmt.Errorf("migration %s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) migrate(ctx context.Context, name string) error {
	sql, err := migrations.ReadFile(name)
	if err != nil {
		return err
	}
	version, _, _ := strings.Cut(strings.TrimPrefix(name, "migrations/"), "_")
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", migrationLock); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, bootstrap); err != nil {
			return err
		}
		var applied bool
		if err := tx.QueryRow(ctx, "select exists (select 1 from community.schema_migrations where version = $1)", version).Scan(&applied); err != nil {
			return err
		}
		if applied {
			return nil
		}
		if _, err := tx.Exec(ctx, string(sql)); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "insert into community.schema_migrations (version) values ($1)", version)
		return err
	})
}
