# Contributing to Appximo

Thank you for your interest. Appximo is early-stage — your feedback shapes the roadmap.

---

## Reporting bugs

1. **Search first** — check [open issues](https://github.com/appximo/appximo/issues) before opening a new one.
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

## Daily flow

`make help` lists every annotated target. The day-to-day ones:

```bash
make dev          # build Studio + engine, load dev secrets (DEV_ENV env-file),
                  # serve on :8080 — boots a BLANK app; SCHEMA=/PORT= to override
make dev-fast     # skip the Studio SPA rebuild (when the editor didn't change)
make stop         # stop the dev server by its exact PID (PORT= to target another)
make spec         # regenerate appximo-spec.md (the agent grammar pack)
make install      # version-stamped `appximo` CLI into /usr/local/bin (sudo if needed)
```

---

## Running the tests locally

**Prerequisites:** Go 1.25+, Docker (for testcontainers), Node 20+ (only to
build the embedded UIs: `make editor-ui admin-ui` before `go build` — without
them the binary serves empty `/editor` and `/admin` shells).

```bash
# All tests (testcontainers pulls postgres:16-alpine on first run — takes ~30s)
go test ./... -timeout 300s

# Specific package
go test ./pkg/rbac/... -v
go test ./pkg/extensions/... -v -timeout 30s
go test ./internal/handlers/... -v -timeout 120s

# Build only (fast sanity check)
go build ./...

# Engine binary for deploy — ALWAYS use the canonical script:
# scripts/build-engine.sh <output> [version] [revision]
# Direct `go build` omits the version ldflags. The deploy smoke check
# asserts version == deployed SHA; a versionless binary fails that check.
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

- `gofmt` is non-negotiable — format before committing. `make fmt-check` runs
  the exact gate CI enforces (it fails listing every unformatted file).
- No comments that describe *what* the code does — names do that. Only explain *why* when it's non-obvious.
- Docs that cite code use **file + symbol** (`pkg/schema/validator.go` `validateRBAC`), never `file:line` — line numbers drift, symbol names don't (see `docs/SCHEMA_REFERENCE.md`, `docs/MENTAL_MODEL.md`).
- No new abstraction unless three concrete use cases exist.
- Security-sensitive code (schema validation, SQL building, RBAC) requires a test for each invariant.

---

## Pull request checklist

- [ ] `make fmt-check` passes (gofmt — CI gates on it)
- [ ] `go build ./...` passes
- [ ] `go test ./...` passes — the FULL lane, without `-short`. The `-short`
      unit lane (`make test`) skips every DB-backed test, and that lane green
      has already shipped a broken POST once (a create check rejecting the
      engine's own `default:"now"` injection, 2026-08-01) — CI runs the full
      lane; run it locally before trusting a data-path change
- [ ] Data-path changes (query building, write validation, auth, tenant
      resolution, response shaping) additionally ran
      `scripts/binary-diff-gate.sh <old-binary> <new-binary>` and **every
      reported DIFF is explained in the PR description**, case by case. Add a
      corpus row (`scripts/binary-diff/corpus.jsonl`) for each contract you
      touched
- [ ] No new linter warnings (`golangci-lint run`)
- [ ] Schema changes validated with `appximo validate`
- [ ] If template changed: `appximo generate testdata/logistics/schema.json` regenerated
- [ ] Engine binary built via `scripts/build-engine.sh` (not `go build`), if deploying

---

## Acceptance smoke (against a running instance)

`scripts/acceptance-test.sh` is the end-to-end acceptance smoke — the
clean-droplet manual test session, codified. It exercises health, CRUD,
declarative validation (422), filters/sort/keyset, GraphQL, auth/RBAC
(deny-by-default and, if the boot schema declares a `viewer` role, the
read-only + field-allowlist path), multi-tenant isolation (API, SSE and
physical Postgres schemas), observability, hot column reload and strict
schema-key validation. It is version-aware: on an older binary the
newer-engine checks downgrade to `INFO` instead of failing.

Against the docker compose quickstart (the default):

```bash
cd <dir with docker-compose.yml + .env>
bash scripts/acceptance-test.sh
```

Against any other running instance (native binary, remote host):

```bash
JWT_SECRET=… ADMIN_KEY=… \
BASE=http://host:8080 ADMIN=http://host:9090 \
APPXIMO_CLI=./appximo SCHEMA_FILE=examples/quickstart/schema.json \
TENANT_A=smoke1 TENANT_B=smoke2 \
bash scripts/acceptance-test.sh
```

Notes: it needs the quickstart `todo-api` schema (a `tasks` resource);
it is idempotent (tenants may already exist, titles are unique per run)
and restores the stored schema at the end — but it does create/write
data in the two smoke tenants, so don't point it at tenants you care
about. Exit code = number of failures.

---

## Releases (maintainers)

A release is cut by pushing a version tag — nothing else:

```bash
git tag v0.1.0 && git push --tags
```

CI runs the full suite on the tag; only if it finishes green does
`.github/workflows/release.yml` build the static binaries (linux/darwin ×
amd64/arm64, version stamped via ldflags — `appximo version` reports the
tag) and attach them with `checksums.txt` to a GitHub Release. The Docker
image for the tag is published the same way (`docker-publish.yml`, also gated
on green CI). A red tag publishes nothing.

---

## License

By contributing, you agree that your contributions will be licensed under the [Apache 2.0 License](LICENSE).
