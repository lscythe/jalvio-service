package store

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/lscythe/jalvio-service/internal/community/accounts"
)

const (
	uniqueViolation = "23505"
	// lastUsedResolution limits session writes to one a day; retention only
	// needs to know which month a session was last used.
	lastUsedResolution = 24 * time.Hour
)

const accountColumns = "a.id::text, a.display_name, a.avatar, coalesce(a.google_sub, ''), a.created_at"

type Store struct {
	pool *pgxpool.Pool
}

// Open prepares a connection pool without connecting; Ping checks the server.
func Open(ctx context.Context, databaseURL string, maxConns int) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	cfg.MaxConns = int32(min(maxConns, math.MaxInt32)) // #nosec G115 -- bounded by min
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return &Store{pool: pool}, nil
}

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) Close() { s.pool.Close() }

func (s *Store) CreateAccount(ctx context.Context, tokenHash []byte, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		with account as (
			insert into community.accounts (created_at, updated_at) values ($2, $2) returning id
		)
		insert into community.sessions (token_hash, account_id, created_at, last_used_at)
		select $1, id, $2, $2 from account`, tokenHash, now)
	return err
}

func (s *Store) SessionAccount(ctx context.Context, tokenHash []byte, now time.Time) (accounts.Account, error) {
	var lastUsed time.Time
	acc, err := scanAccount(s.pool.QueryRow(ctx, `
		select `+accountColumns+`, s.last_used_at
		from community.sessions s join community.accounts a on a.id = s.account_id
		where s.token_hash = $1`, tokenHash), &lastUsed)
	if errors.Is(err, pgx.ErrNoRows) {
		return accounts.Account{}, accounts.ErrNoSession
	}
	if err != nil {
		return accounts.Account{}, err
	}
	if now.Sub(lastUsed) >= lastUsedResolution {
		if _, err := s.pool.Exec(ctx, "update community.sessions set last_used_at = $2 where token_hash = $1", tokenHash, now); err != nil {
			return accounts.Account{}, err
		}
	}
	return acc, nil
}

func (s *Store) AccountByGoogle(ctx context.Context, sub string) (accounts.Account, bool, error) {
	acc, err := scanAccount(s.pool.QueryRow(ctx, "select "+accountColumns+" from community.accounts a where a.google_sub = $1", sub))
	if errors.Is(err, pgx.ErrNoRows) {
		return accounts.Account{}, false, nil
	}
	return acc, err == nil, err
}

func (s *Store) LinkGoogle(ctx context.Context, accountID, sub string, now time.Time) error {
	tag, err := s.pool.Exec(ctx, "update community.accounts set google_sub = $2, updated_at = $3 where id = $1", accountID, sub, now)
	if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == uniqueViolation {
		return accounts.ErrGoogleTaken
	}
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return accounts.ErrNoSession
	}
	return nil
}

// Merge moves the device account fromID into intoID in one transaction. Each
// table holding an account's content must move its rows here too.
func (s *Store) Merge(ctx context.Context, fromID, intoID string, tokenHash []byte, now time.Time) error {
	return pgx.BeginFunc(ctx, s.pool, func(tx pgx.Tx) error {
		tag, err := tx.Exec(ctx, `
			update community.accounts as target
			set display_name = coalesce(target.display_name, source.display_name),
				avatar = coalesce(target.avatar, source.avatar),
				updated_at = $3
			from community.accounts as source
			where target.id = $2 and source.id = $1`, fromID, intoID, now)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return accounts.ErrNoSession
		}
		tag, err = tx.Exec(ctx, "delete from community.accounts where id = $1", fromID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return accounts.ErrNoSession
		}
		_, err = tx.Exec(ctx, `
			insert into community.sessions (token_hash, account_id, created_at, last_used_at)
			values ($1, $2, $3, $3)`, tokenHash, intoID, now)
		return err
	})
}

func (s *Store) UpdateProfile(ctx context.Context, accountID string, u accounts.ProfileUpdate, now time.Time) (accounts.Account, error) {
	var avatar *string
	if u.Avatar.Value != nil {
		avatar = new(string(*u.Avatar.Value))
	}
	acc, err := scanAccount(s.pool.QueryRow(ctx, `
		update community.accounts as a
		set display_name = case when $2 then $3 else a.display_name end,
			avatar = case when $4 then $5::jsonb else a.avatar end,
			updated_at = $6
		where a.id = $1
		returning `+accountColumns,
		accountID, u.DisplayName.Set, u.DisplayName.Value, u.Avatar.Set, avatar, now))
	if errors.Is(err, pgx.ErrNoRows) {
		return accounts.Account{}, accounts.ErrNoSession
	}
	return acc, err
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash []byte) error {
	_, err := s.pool.Exec(ctx, "delete from community.sessions where token_hash = $1", tokenHash)
	return err
}

// DeleteAccount removes the account; every table holding its content
// references it with on delete cascade.
func (s *Store) DeleteAccount(ctx context.Context, accountID string) error {
	_, err := s.pool.Exec(ctx, "delete from community.accounts where id = $1", accountID)
	return err
}

func (s *Store) DeleteIdleDeviceAccounts(ctx context.Context, idleSince time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx, `
		delete from community.accounts a
		where a.google_sub is null
		and not exists (
			select 1 from community.sessions s where s.account_id = a.id and s.last_used_at >= $1
		)`, idleSince)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

func scanAccount(row pgx.Row, extra ...any) (accounts.Account, error) {
	var acc accounts.Account
	var avatar []byte
	dest := append([]any{&acc.ID, &acc.DisplayName, &avatar, &acc.GoogleSub, &acc.CreatedAt}, extra...)
	if err := row.Scan(dest...); err != nil {
		return accounts.Account{}, err
	}
	if avatar != nil {
		acc.Avatar = json.RawMessage(avatar)
	}
	acc.CreatedAt = acc.CreatedAt.UTC()
	return acc, nil
}
