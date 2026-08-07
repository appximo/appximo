# ADR-025 — The embedded UI assets ship in the published module

**Status:** accepted · **Date:** 2026-08-07 · **Session:** FIELD-FEEDBACK-S1
**Reverses:** the "assets stay gitignored, left as Miguel's call" note in
AGENTS.md (ADMIN-UI-V1) · **Closes:** field report finding **B1** (+the B7/B8
workaround it forced).

---

## Context

The product's central claim is "one binary = backend + frontend + admin + docs"
(backend-spec §3.7, README). The claim was false for exactly the consumer the
backend-spec targets: anyone who runs `go get github.com/appximo/appximo` and
compiles their own binary (§3.1, the documented path for the non-declarative
10%).

The built SPA bundles (`pkg/adminui/web/dist/assets/`,
`pkg/editorui/web/build/assets/`) were **gitignored**; only the placeholder
`index.html` was committed so `//go:embed` resolves. Consequences, all verified
by the first real third-party field evaluation (FEEDBACK.md, B1):

- A consumer binary serves `/admin` and `/editor` **broken with a 200**: the
  530-byte shell loads, its hashed bundle 404s, the browser shows a blank page.
  The only warning is one line in the boot log.
- The suggested remedy — `make admin-ui` — requires the engine repo and its npm
  toolchain, which a module consumer does not have. **There is no escape hatch**
  (no `APPXIMO_*` variable points at external assets).
- The trap is not consumer-only: `scripts/build-engine.sh` (the official deploy
  build) never ran the npm steps either, so **the project's own production
  demos** (tiendita.appximo.com, petfriendly.appximo.com) served the same
  blank-panel 200 — corroborated live by the evaluator, and re-verified at the
  start of this session (`/admin` 200 + bundle 404 on both).
- The forced workaround (B7/B8) was running a **second** stock-binary process on
  another port with a **divergent copy of the schema** — two processes, two
  control planes, a schema whose RBAC drifts, and a deploy path that could
  silently revert production grants.

The evaluator posed the decision precisely: (a) embed the built assets in the
published module, (b) don't mount the routes and fail honestly, or (c) leave it.
(c) — serving a shell that cannot load — is the worst of the three and is not an
option.

## Measurement (the argument is weighed, not estimated)

Measured on this repo at `f7911af`, fresh builds of both SPAs:

| What | Size |
|---|---|
| `pkg/adminui/web/dist` (SolidJS panel, built) | 760 KB |
| `pkg/editorui/web/build` (Studio, built) | 1.3 MB |
| Both together, tar | 1.92 MB |
| Both together, tar.gz (what a module download adds) | **0.59 MB** |
| Module archive today (tar / tar.gz) | 18.1 MB / 8.2 MB |
| Engine binary | ~65–78 MB |

Committing the assets grows the compressed module download by **~7%** and the
binary by **~2.5%**. Against losing two of the product's four headline surfaces
for every module consumer, this is noise.

## Decision

**Both (a) and (b), in that order of importance:**

1. **(a) The built assets are committed and ship in the module.** The gitignore
   entries for `pkg/adminui/web/dist/assets/` and `pkg/editorui/web/build/*`
   are removed and the built bundles are tracked. `go get` +
   `go build` now produces a binary with the full `/admin` and `/editor` — the
   one-binary claim is **true by construction** for consumers, and
   `scripts/build-engine.sh` no longer depends on a manual npm step it never
   ran.
2. **(b) A binary with no embedded assets fails honestly.** If the embed holds
   only the placeholder (a fork that deleted the bundles, a broken tree), the
   shell routes answer **503** with a self-describing page — what is missing,
   why, and the exact fix — instead of a 200 shell that renders blank. The
   asset routes keep their real 404s. The boot warning stays.

Rejected alternatives:

- **Only (b), keep assets out of the module.** Honest, but it makes the
  one-binary claim permanently false for consumers — the panel would *require*
  the engine repo. The claim would have to be retracted from README,
  backend-spec §3.7 and the site. The 0.59 MB saved does not buy that.
- **An `APPXIMO_ADMIN_ASSETS_DIR` escape hatch.** With assets in the module the
  normal case needs no hatch, and an external-assets mode adds a serving path,
  a cache story and a support surface for a state that no longer occurs.
- **A separate assets artifact / submodule.** Distribution complexity for the
  consumer (two things to fetch and version-match) to save ~2 MB — the exact
  trade the one-binary positioning exists to avoid.

## Consequences

- **The dist↔src coherence duty moves to the SPA developer**: a session that
  touches `pkg/adminui/web/src` or `pkg/editorui/web/src` must run
  `make admin-ui` / `make editor-ui` and commit the rebuilt assets *with* the
  src change (the Makefile targets and AGENTS.md say so). Release builds keep
  running both npm builds (release.yml), so a forgotten rebuild is corrected at
  the next release rather than shipped forever.
- Hashed asset filenames churn in git history on each SPA change. Accepted: the
  SPAs change in dedicated sessions, each rebuild replaces a handful of files,
  and the alternative was a broken product surface.
- A bare `go build` from a fresh clone now ships working UIs; the
  "⚠ build trap" sections in AGENTS.md / 05_SERVIDORES describing the opposite
  are rewritten by this session.
- `tools/devhub/` keeps the old pattern deliberately: it is an internal dev
  dashboard, not part of the engine or the module (AGENTS.md already says so).

## Reconsider when

The embedded UIs grow past ~10 MB compressed (e.g. heavy chart/font payloads),
at which point a build-tag split (`go build -tags noui`) for size-critical
consumers is the first thing to evaluate — not un-committing the assets.
