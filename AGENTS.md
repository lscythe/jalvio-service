# Agent Guidelines

jalvio-service is a self-hosted Go backend that serves public transit data to the Jalvio app.

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

## Tests

- Tests are deterministic and use local fixtures only. They never contact external services.
- Live checks against real upstreams are opt-in and run only when explicitly requested.
- Run formatting, lint, tests, and the race detector before proposing a change.

## Security

- Never commit secrets, tokens, credentials, or real environment files.
- Never log credentials or raw upstream payloads.

## Repository

- The repository holds code, tests, fixtures, and the contract files the code depends on.
- Design docs and plans live outside the repository.
- Use focused branches and conventional commit messages.
- Code, comments, tests, commit messages, and branch names describe what the change does. Never reference planning stages such as slices, phases, or milestones.
