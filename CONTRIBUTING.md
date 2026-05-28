# Contributing to Appitools

Thank you for your interest. Appitools is early-stage — your feedback shapes the roadmap.

---

## Reporting bugs

1. **Search first** — check [open issues](https://github.com/miguelangel/appitools/issues) before opening a new one.
2. **Minimal reproduction** — include the `schema.json` that triggers the bug and the exact command you ran.
3. **Environment** — Go version (`go version`), OS, PostgreSQL version if relevant.

Use the issue title format: `[bug] short description of what broke`.

---

## Requesting features

1. **Open an issue first** — describe the use case, not just the API you want.  
   We need to understand *why* before deciding *how*.
2. Wait for a maintainer to mark it `accepted` before writing code.  
   This prevents duplicate work and wasted effort.

Use the issue title format: `[feature] short description`.

---

## Running the tests locally

**Prerequisites:** Go 1.22+, Docker (for testcontainers).

```bash
# All tests (testcontainers pulls postgres:16-alpine on first run — takes ~30s)
go test ./... -timeout 300s

# Specific package
go test ./pkg/rbac/... -v
go test ./pkg/extensions/... -v -timeout 30s
go test ./internal/handlers/... -v -timeout 120s

# Build only (fast sanity check)
go build ./...
```

Tests that spin up containers:
- `pkg/db/` — 3 tests, ~6 s
- `internal/handlers/` — 1 test, ~3 s

The existing PostgreSQL on your machine is **never touched** — testcontainers creates isolated containers.

---

## Commit conventions

We use [Conventional Commits](https://www.conventionalcommits.org/):

| Prefix | Use for |
|---|---|
| `feat:` | New feature or capability |
| `fix:` | Bug fix |
| `chore:` | Build, deps, tooling — no production code change |
| `test:` | Adding or fixing tests |
| `docs:` | Documentation only |
| `refactor:` | Code change that neither fixes a bug nor adds a feature |

Examples:
```
feat: add before_create JS hook to generated handlers
fix: prevent connection leak in tenantRows.Close()
docs: add benchmark methodology
test: add integration test for full CRUD cycle
chore: upgrade pgx to v5.9.2
```

**One logical change per commit.** If you need two prefixes, you need two commits.

---

## Code style

- `gofmt` is non-negotiable — format before committing.
- No comments that describe *what* the code does — names do that. Only explain *why* when it's non-obvious.
- No new abstraction unless three concrete use cases exist.
- Security-sensitive code (schema validation, SQL building, RBAC) requires a test for each invariant.

---

## Pull request checklist

- [ ] `go build ./...` passes
- [ ] `go test ./...` passes
- [ ] No new linter warnings (`golangci-lint run`)
- [ ] Schema changes validated with `appitools validate`
- [ ] If template changed: `appitools generate testdata/logistics/schema.json` regenerated

---

## License

By contributing, you agree that your contributions will be licensed under the [Apache 2.0 License](LICENSE).
