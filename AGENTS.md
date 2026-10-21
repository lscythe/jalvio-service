# Agent Guidelines

jalvio-service is a self-hosted Go backend that serves public transit data to the Jalvio app. One process loads the static GTFS feed, follows live bus positions from an MQTT broker, syncs stop facilities from the stop API once a day, and serves anonymous JSON over HTTP.

## Layout

- `cmd/jalvio`: the service entry point and the `healthcheck` subcommand.
- `cmd/covergate`: the per-package test and coverage gate.
- `internal/config`: environment configuration.
- `internal/catalog`: the static GTFS catalog (routes, stops, trips) and search.
- `internal/mqttfeed`, `internal/fleet`: the live feed and the in-memory fleet built from it.
- `internal/stopapi`, `internal/stopinfo`: the stop API client and the daily facilities sync.
- `internal/places`: destination search and reverse geocoding.
- `internal/httpapi`: the public HTTP API.
- `internal/geo`, `internal/atomicfile`, `internal/idfile`: small shared helpers.
- `test/live`: opt-in checks against real upstreams, built only with the `live` tag.

## Commands

- `mise install`: install the pinned toolchain from `mise.toml`.
- `lefthook install`: set up the pre-commit and commit-msg hooks.
- `mise run fmt`: format with gofumpt.
- `mise run check`: formatting, lint, race-enabled tests, coverage gate, and vulnerability scan. CI runs the same.
- `mise run live`: check the real upstreams named in the environment. Run it only when explicitly asked.

## Working style

- Test-driven: write a failing test, make it pass with the smallest change, then refactor.
- Keep changes small and focused. One concern per change.
- Prefer the standard library. Add a dependency only when it clearly pays for itself.
- Don't add abstractions, options, retries, or fallbacks without a concrete need.
- Remove dead code, stale comments, and unused files instead of leaving them behind.

## Code

- Code is the documentation: clear names, small functions, obvious control flow.
- Comment only a non-obvious *why*, briefly. Never restate what the code does.
- Pass dependencies through constructors. No global state, no post-construction setters.
- Handle every error; wrap with context where it helps the caller.
- Match the existing formatting, naming, and package boundaries.
- Configuration comes from environment variables. A new variable is added to `internal/config`, the README table, and `.env.example` in the same change.

## Public API

- Access is anonymous. Don't add authentication, sessions, or tokens.
- JSON fields are camelCase, times are RFC 3339 in UTC, and coordinates are `{"lat": …, "lng": …}`.
- Errors are RFC 9457 `application/problem+json` with a stable `type`.
- `/healthz` returns a bodyless `204` when ready and `503` otherwise.
- Keep the README endpoint table and the contract files in step with behaviour.

## Tests

- Tests are deterministic and use local fixtures only. They never contact external services.
- Live checks against real upstreams are opt-in, need explicit approval each time, and print counts only.
- Every package has tests; packages outside `cmd/` keep at least 80% statement coverage.
- Run `mise run check` before proposing a change. Report the commands you ran and their result; compiling is not verification.

## Security

- Never commit secrets, tokens, credentials, or real environment files.
- Never commit real upstream endpoints (broker, API, or feed URLs and hostnames), not even as defaults, in tests, or in docs. They come from the environment; tests use reserved names such as `example.com` and `.invalid`.
- Refer to upstream services generically (the broker, the static feed, the stop API). Don't name the operator in code, docs, commit messages, or pull requests.
- Never log credentials or raw upstream payloads.

## Repository

- The repository holds code, tests, fixtures, and the contract files the code depends on.
- Design docs and plans live outside the repository.
- Code, comments, tests, commit messages, and branch names describe what the change does. Never reference planning stages such as slices, phases, or milestones.

## Git and pull requests

- Start from an up-to-date `main` on a `feat/`, `fix/`, `chore/`, `docs/`, or `refactor/` branch.
- Check `git status --short` before staging and stage only the intended files.
- Commit messages and PR titles follow Conventional Commits. PRs are squash-merged, so the title becomes the commit on `main`.
- Fill in the pull request template.
- Merge only when the maintainer says so. That includes the release pull requests opened by release-please.
