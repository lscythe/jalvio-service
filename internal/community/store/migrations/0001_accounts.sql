create table community.accounts (
    id uuid primary key default gen_random_uuid(),
    display_name text,
    avatar jsonb,
    google_sub text unique,
    created_at timestamptz not null,
    updated_at timestamptz not null
);

create table community.sessions (
    token_hash bytea primary key,
    account_id uuid not null references community.accounts on delete cascade,
    created_at timestamptz not null,
    last_used_at timestamptz not null
);

create index sessions_account_id on community.sessions (account_id);

alter table community.accounts enable row level security;
alter table community.sessions enable row level security;
