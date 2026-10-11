# Contributing

## Setup

```sh
mise install
lefthook install
mise run check
```

`lefthook install` sets up the git hooks:

- **pre-commit:** formats and lints staged Go files, then runs the tests of the packages they belong to.
- **commit-msg:** checks that the subject follows Conventional Commits.

`mise run check` runs formatting, lint, race-enabled tests with coverage, the coverage gate, and the vulnerability scan. CI runs the same checks.

Every package needs tests. Packages outside `cmd/` need at least 80% statement coverage. Both the pre-commit hook and CI enforce this.

## Workflow

1. Open an issue first for anything beyond a small fix.
2. Branch from `main` with a `feat/`, `fix/`, `chore/`, `docs/`, or `refactor/` prefix.
3. Write a failing test, make it pass, then refactor.
4. Use [Conventional Commits](https://www.conventionalcommits.org/) for commit messages and the PR title. PRs are squash-merged, so the title becomes the commit on `main`.
5. Open a PR using the template. CI must pass before merging.

Tests never contact external services. See [AGENTS.md](AGENTS.md) for coding guidelines.

## Security

Never commit secrets or real environment files. Report vulnerabilities as described in [SECURITY.md](SECURITY.md).
