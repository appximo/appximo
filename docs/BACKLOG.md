# Appximo — the open-item register

**This file is the ONLY place an open item lives.** Not a chat report, not a
session summary, not somebody's memory. If work is left undone, it is written
here with a reason and a definition of "ready"; if it is decided against, it is
written here with a link to the ADR that says why.

Created by **LOOSE-ENDS-SWEEP-S1**, which swept every report, doc and ADR in the
repo (and in `/root/commerce`) and consolidated what it found. Before it, open
items were scattered across session reports and were re-litigated from memory
each time.

## How to use it

**Any session that leaves something undone adds a row.** An item has exactly one
of three states — there is no fourth, and "pending, we'll see" is not a state:

| State | Means |
|---|---|
| **OPEN** | Not done. Has an impact, and a **Ready** criterion precise enough that a future session knows when it is finished. |
| **CLOSED** | Decided against, with a written justification (an ADR or a documented note) **and** the condition under which it would be reconsidered. |
| **DONE** | Implemented and verified. Kept for one release cycle so the history is readable, then dropped. |

IDs are stable and never reused: `ENG-*` engine, `SCHEMA-*` schema grammar,
`RBAC-*` authorization, `OPS-*` operations/tooling, `DOC-*` documentation,
`COMMERCE-*` the commerce backend (a separate repo, tracked here because the
engine's roadmap depends on what building it revealed).

**Last reviewed: 2026-08-02 (PHASE3-GUIDE-S1)** — the Phase-3 public-material
pass: `docs/GUIDE.md` (the third-party master guide, distilled from the five
field journeys) and `site/` (the official page, every number condition-attached)
written and browser-verified; the zero-to-API path re-executed live; three stale
claims corrected in CAPABILITIES and the internal pitch base (see the
certification's PHASE3-GUIDE-S1 addendum). No engine change. Previous review:
NIGHT-SWEEP-S1 (same day) — the accepted-and-silent class
EXHAUSTED on the known input surfaces: ENG-15/16/17/18/19/20/23/24/34 and UI-1
closed and moved to DONE, the UNRECOGNIZED_INPUT_AUDIT checklist run to
completion (19 probe/verify agents over 9 surfaces against a live engine), six
CONFIRMED new same-class findings fixed in the same night, and the deferred
residue filed below with evidence (ENG-35, SEC-6, OPS-16; ENG-21/22 evidence
refreshed).

---

## OPEN

### ENG-35 — GraphQL String-typed variables silently coerce any scalar
- **Origin:** NIGHT-SWEEP-S1 audit (GraphQL surface), CONFIRMED adversarially.
  A `query($t: String!)` executed with `variables: {"t": 5}` runs — the library
  coerces the Int to `"5"` instead of raising the spec-required request error —
  while Int-typed variables ARE strict (a string is rejected). Asymmetric
  coercion inside graphql-go, not our resolvers.
- **Impact:** Medium. A client bug (wrong variable type) executes with a
  stringified value instead of failing loudly; the same bug on an Int variable
  fails correctly. Same family as ENG-22 (GraphQL value handling) — fix them in
  one GraphQL-coercion pass.
- **Why deferred:** changing variable coercion is a library-level contract wider
  than a sweep session (the audit's own precedent: "GraphQL argument coercion
  changes a contract wider than one release").
- **Ready:** a wrongly-typed variable for ANY declared type is a request error
  naming the variable and both types, or the tolerance is documented per type.

### SEC-6 — No JWT-secret strength floor anywhere
- **Origin:** NIGHT-SWEEP-S1 audit (CLI surface). `appximo token --secret
  short` mints, and `serve` BOOTS, with a 5-character HS256 secret — while every
  doc says "at least 32 characters". Rule 8 of ADR-024: when the engine states a
  rule it must enforce it.
- **Impact:** Medium (security posture). A weak secret makes every tenant's JWTs
  forgeable; nothing warns.
- **Why deferred:** a boot-refusal is a breaking change for dev setups and the
  right floor (warn vs refuse, 32 chars vs entropy) is a product decision —
  Miguel's call.
- **Ready:** `serve` refuses (or at minimum WARNS loudly at boot) below a
  documented floor; `token` warns; docs and the floor agree.

### OPS-16 — Small named-rejection residue from the NIGHT-SWEEP audit
- **Origin:** NIGHT-SWEEP-S1 audit, all LOW, verified live, none silent-harmful
  enough to fix at 4 a.m.:
  (a) fleet manifest unknown-key error surfaces the raw decoder message (names
  the field, lists no valid keys); (b) control-plane `plan` accepts any value
  and is read by nothing — an enumerated-looking field with no set (needs a
  product decision on what plan means); (c) trailing data after the first JSON
  value is ignored on /auth bodies (`{}garbage` parses — needs a dec.More()
  check per body or a written exception); (d) MFA session endpoints answer
  "authentication required" to a VALID engine token that carries no user
  identity (message could name the real problem); (e) a tx create HONORS a
  caller-supplied `id` in data (echoed — reported tolerance) while update's own
  error calls id read_only — decide and write the create-id contract.
- **Impact:** Low each; they are message-quality/contract-wording items, not
  silences (everything silent got fixed in the session).
- **Ready:** each sub-item either fixed with a test or written down as an
  ADR-024 exception with its reason.

### ENG-1 — Embed cache for `?include=` relations
- **Origin:** ADR-019 §cache invalidation; carried in ESTADO_Y_PLAN_MAESTRO as
  "the only piece of relations not implemented".
- **Impact:** Medium. A nested read runs its LATERAL every time. Correct, and
  measured at +0.01 ms p50 for ~15 children, so this is throughput headroom on
  read-heavy embeds, not a defect.
- **Ready:** an embed result is cached per (tenant, parent-set, role) and
  INVALIDATED by a write to either the parent or the child resource; a stale
  embed is impossible after a write in the same tenant; benchmarked `no_change`
  on the non-embed path and a measured win on the embed path.

### ENG-2 — Zero-downtime binary upgrade
- **Origin:** PROD_PATH_AUDIT §1.2 ("Zero-downtime (binario) — no existe").
- **Impact:** Medium. `deploy-update.sh` does SIGTERM → wait → start, measured at
  **0.47 s** of 502s under live traffic (docs/BENCHMARKS.md). Acceptable for a
  single box; visible for an API with a strict SLO.
- **Consumer numbers (re-measured in CONSUMER-PATH-S1, 2026-07-31):** with the
  reworked deploy-update.sh (250 ms health polling; crash-loop detection via
  NRestarts — `Restart=always` never reaches systemd's "failed" state; the
  rollback branch VERIFIES recovery), a normal deploy costs **0.28 s of 502s
  (15/3001 at 50 rps)** — was 0.58 s — and a BROKEN deploy rolls back verified
  in **17 s end-to-end with 10.75 s user-visible** — was 35 s / 30.7 s. The
  cheap tooling wins are done; what remains is the actual zero-downtime work.
- **Ready:** a deploy costs **zero** failed requests under 500 rps, verified by
  the chaos suite. Candidates: socket hand-off (`SO_REUSEPORT`) between old and
  new process, or a symlink + blue/green pair behind the existing `/readyz` drain.

### ENG-3 — Restore command + scheduled backups (NARROWED — the drill now exists and passed)
- **Origin:** PROD_PATH_AUDIT §1.5 / §2.8; docs/CAPABILITIES.md ("Backup has no
  restore command and no scheduling").
- **Impact:** was **High**; now Medium. PROD-JOURNEY-1B (2026-07-31) wrote and
  EXECUTED a restore on the 58 with real data: commerce `scripts/restore.sh`
  (stop engine → drop/recreate DB → `pg_restore --exit-on-error` → re-own
  schemas/tables/sequences/**functions** → health-wait → row counts). The drill
  caught a real bug theory couldn't: restored functions owned by `postgres`
  crash-loop the engine's bootstrap (`CREATE OR REPLACE notify_schema_updated`,
  SQLSTATE 42501). Full cycle verified: backup → DROP SCHEMA CASCADE → restore
  1.8 s → identical counts → pre-backup order retrievable → NEW purchase
  completed. A nightly `appximo-backup.timer` (03:30) is installed on the 58
  by hand — the installer still doesn't provide it.
- **Ready (what remains):** `appximo restore` as a first-class engine command
  (per-tenant selective restore included), the backup timer written by
  `install.sh`, and the restore drill wired into `scripts/verify-production/`.

### ENG-4 — OTLP / OpenTelemetry export
- **Origin:** README "Known limits"; docs/CAPABILITIES.md.
- **Impact:** Low for the target user (the in-binary observability is the
  differentiator — ADR-020), blocking for anyone standardized on OTel.
- **Ready:** an opt-in exporter behind a build tag or config, with the hot path
  measured `no_change` when it is off (the reason it has not shipped: the SDK's
  weight must not reach the request path by default).


### MIG-1 — A gin index's `opclass` change is a silent no-op
- **Origin:** AUTHORING-GAPS-S1, the ENG-13 class audit
  ([docs/audits/MIGRATION_HONESTY_AUDIT.md](audits/MIGRATION_HONESTY_AUDIT.md)
  finding "Left open"). An index's `opclass` (`jsonb_ops` → `jsonb_path_ops`) is
  DELIBERATELY excluded from the diff key because the introspector cannot read one
  back, so declaring a different opclass on an EXISTING index does nothing — and,
  like ENG-13, says nothing.
- **Impact:** Low and narrow (one optional key on gin indexes only), but it is the
  last known member of the "declared ≠ applied, silently" class the session closed.
  The post-apply verification cannot catch it: the desired model has no opclass to
  compare against.
- **Ready:** either the declared opclass is recorded (a comment on the index, or a
  side table) so a change can be detected and applied as drop+recreate, or the
  validator REFUSES to change the opclass of an index that already exists, naming
  the manual `DROP INDEX` + re-apply. Silence is the one option ruled out.

### MIG-2 — A `schema_history` append failure is log-only
- **Origin:** AUTHORING-GAPS-S1, the ENG-13 class audit. `schemahistory.Append` is
  best-effort at every call site: on failure the deploy proceeds and a WARNING goes
  to the log, so the version trail silently gains a gap. `EnsureSeeded` (this
  session) reduces the blast radius — the schema being REPLACED is now always
  recorded first — but the write itself still has no signal to the caller.
- **Impact:** Low today. The trail is used by ENG-9's ownership classifier and by
  Studio's History/rollback; a gap degrades a dry-run's drop classification and
  removes a rollback target, both quietly.
- **Ready:** the deploy response carries a `history_warning` when the append fails
  (the deploy still succeeds — the DDL is applied and the record is authoritative),
  and Studio's History view shows it. Not an error: failing a correct deploy over a
  bookkeeping row would be worse than the gap.

### OPS-11 — `install.sh --app` has not been run on a real multi-app box
- **Origin:** AUTHORING-GAPS-S1. The two-app isolation, the refuse-to-clobber guard
  and the per-app Caddy site were verified in staged `--dry-run --root` mode. The
  live path — systemd units, the postgres role/database per app, and above all the
  **migration of an existing monolithic `/etc/caddy/Caddyfile` to `import
  sites/*.caddy`** — has not run on a real box.
- **Impact:** Medium. The migration only strips the block for the domain being
  installed and backs the file up first, so the failure mode is "Caddy refuses to
  reload", not data loss — but it would be an outage on a box serving live apps.
- **Ready:** install a third, ephemeral app on the 58 with `--app`, confirm the
  tienda and petfriendly do not blink (a purchase + a CRUD call through each), then
  `--uninstall --app=<that app>` and confirm the two survivors are untouched.


### OPS-12 — The NestJS comparative benchmark cannot be re-run
- **Origin:** CERTIFY-S1. The harness survives (`benchmark-lab/nestjs-baseline/`
  with `dist/` + `node_modules`, plus `/root/archives-58/nestjs-bench-58-20260731.tgz`),
  but the published measurement's conditions are gone: the 58 has no Node, no pm2 and
  no Docker (so no 0.5-vCPU PostgreSQL cap, which was a declared, symmetric condition
  of the original), and it now serves two production apps.
- **Impact:** **High for publication.** "~4.8× faster than NestJS" is the most
  quotable claim the project has, and it is currently unverifiable. It is marked as
  historical (2026-06-10) in the README and the benchmark doc.
- **Ready:** a dedicated, disposable SUT with both stacks installed and PostgreSQL
  constrained identically for both arms, run through the existing ABBA protocol —
  then the claim is restored with a fresh date, or dropped.

### OPS-13 — Fifteen config values fall back silently when they fail to parse
- **Count corrected 2026-08-01** (was "nineteen"): a dedicated re-count found **15
  numeric env vars across 18 code sites**, and — the number that actually matters —
  **8 of them print nothing at all afterwards**. Those 8 are the fully silent class;
  the other 10 at least log the effective value, so an operator who reads the boot
  log can notice the discrepancy. Split the fix accordingly: the 8 silent ones first.
- **Origin:** SILENT-FAILURE-S1 audit + measured live. Booting with
  `RATE_LIMIT_RPS=abc RATE_LIMIT_BURST=oops` logs `rate limiter: 1000 RPS / 100 burst
  per tenant` and never says the operator's values were rejected. The same shape in
  `APPXIMO_AUTH_MIN_PASSWORD`, `APPXIMO_CONTROL_PORT`, `APPXIMO_FILES_MAX_BYTES`,
  `DB_MAX_CONNS`, `APPXIMO_MAX_TX_OPS`, the fleet's per-app `envInt`, and more.
  `envTruthy` maps ANY unrecognized value to false — including
  `APPXIMO_AUTH_REQUIRE_VERIFIED`, a security toggle. And there is **no inventory
  of the 60+ `APPXIMO_*` variables**, so a misspelled one is never read at all.
- **Impact:** Medium-high for operators: the box runs with a configuration nobody
  chose, and the only evidence is a log line stating the default as if it were the
  request.
- **Ready:** one `envInt`/`envBool`/`envDuration` helper that logs
  `WARNING: RATE_LIMIT_RPS="abc" is not a number — using 1000`, plus a boot-time
  inventory that warns on an unknown `APPXIMO_*` variable (the fleet already has
  the pattern for per-app keys).

### ENG-21 — No write body uses `DisallowUnknownFields`
- **Origin:** SILENT-FAILURE-S1 audit. The engine already uses it in `pkg/userauth`,
  `pkg/platformadmin` and `pkg/fleet` — the discipline exists and was never applied
  to the data plane. Unknown keys in the `/api/transaction` envelope, in an
  operation, and in a `guard` are dropped. Worse, the `422 unknown_field` guarantee
  on CREATE is not a key check at all — it is a side effect of Postgres `42703`, so
  it evaporates for a role with a `fields` allowlist (the key is deleted before the
  DB sees it) and for a drift column the additive migration left behind.
- **Impact:** Medium. SILENT-FAILURE-S1 fixed the operator bodies that carry a
  safety flag; the data-plane bodies are a wider contract change.
- **Narrowed by NIGHT-SWEEP-S1:** the DECLARED-but-irrelevant keys are no longer
  silent — `guard` on a create and `data` on a delete are named 400s, a tx
  create with a non-uuid id is the same 400 as single-op (was a masked 500), and
  the envelope was re-probed live: `{"atomic": false}` and a misspelled
  `"operation"` are still dropped (the remaining hole is exactly UNKNOWN keys).
- **Ready:** strict decode on the transaction envelope/op/guard, and a real key check
  on CREATE that does not depend on the database's error code.

### ENG-22 — GraphQL drops variables and nested jsonb values
- **Origin:** SILENT-FAILURE-S1 audit. `GET /graphql` never reads the `variables`
  query parameter, so a filtered query returns the UNFILTERED result. A variable
  nested inside a `jsonb` inline literal is written as `null`.
- **Impact:** Medium-high for the GET path (a filter that silently does not apply).
- **Re-verified by NIGHT-SWEEP-S1** (live, base binary): the GET `variables`
  drop reproduces exactly as filed. The audit also REFUTED a related claim (an
  unknown POST-body key like a misspelled `variables` — the GraphQL-over-HTTP
  envelope tolerance applies); and split off the String-variable coercion
  asymmetry as **ENG-35** (fix the two together — one GraphQL value-handling pass).
- **Ready:** parse `variables` on GET, or reject a GET carrying it; resolve variables
  inside jsonb literals or reject them.

### OPS-14 — `api.appximo.com` (the gold-path demo) is down: Cloudflare 525
- **Origin:** observed 2026-08-01 while checking server state at the end of
  SILENT-FAILURE-S1. NOT caused by this session — nothing was deployed to the 58.
- **Evidence:** `GET https://api.appximo.com/healthz` → **525** on 3/3 attempts,
  `server: cloudflare`. The host resolves to a **Cloudflare** address
  (`2606:4700:3030::6815:4d8c`), while the two live apps resolve **directly** to the
  origin (`the production VPS`) and both answer `/healthz` → **200**. A 525 is
  "Cloudflare could not complete the TLS handshake to the origin".
- **Impact:** the public demo URL from PROD-PATH-GOLD-S1 — the one that proved the
  official install path end to end with a real Let's Encrypt certificate — is dead
  from the internet, while the newer apps (added later with direct DNS) are fine.
  Anyone following that write-up hits an error page.
- **Likely cause, to confirm before touching anything:** the origin's Caddy no
  longer holds a certificate for `api.appximo.com` (the host was probably dropped
  from the Caddyfile when tiendita/petfriendly were set up with direct DNS), so
  Cloudflare's strict origin check fails. Alternatively the Cloudflare SSL mode was
  changed.
- **Ready:** either point the DNS record directly at the origin like the other two
  apps (matching what already works), or restore the origin certificate for that
  hostname and confirm `https://api.appximo.com/healthz` → 200 from outside.
  **Miguel's call** — it is also fair to retire the hostname if the demo has moved.


### SCHEMA-6 — Filtering by NULL has no declarative surface
- **Origin:** SILENT-FAILURE-S1. Closing ENG-14 forced the question explicitly: now
  that `?filter[x][is_null]=true` returns a clean `400` instead of the whole table,
  **should the operator exist at all?**
- **The decision this session made: NOT NOW, deliberately.** The session's mandate
  was to close a defect class, and adding a filter operator is a capability, not a
  fix: it touches the type×operator matrix, the GraphQL filter arguments (which must
  stay in parity), `SCHEMA_REFERENCE.md`, the LLM grammar in `pkg/aigen` (so
  generated schemas learn it), the AGENTS table and `CAPABILITIES.md`. Shipping that
  inside an audit pass is the scope creep the audit exists to prevent.
- **But the gap is real, and this is the honest part:** there is currently **no way
  to filter by null in the declarative surface at all**. The veterinary app hit it.
  The workarounds are a custom Go handler (framework mode, which needs the
  unpublished module — DOC-2) or modelling the column as non-nullable with a
  sentinel. So the clean `400` is, today, a dead end rather than a redirection.
- **Impact:** Medium. "Show me the rows with no assigned vet / no invoice / no
  closing date" is an ordinary business question.
- **Ready:** `is_null` (and its negation) as a real operator on every nullable type,
  rendering `IS NULL` / `IS NOT NULL` — value ignored or restricted to `true`/`false`
  — with REST and GraphQL parity, a test per type, and the doc + LLM-grammar updates
  that make it discoverable. Until then the `400` should name the limitation rather
  than only listing the operators that do exist.

### SCHEMA-7 — Schema KEYS are strict; VALUES and key×type combinations are not
- **Origin:** SILENT-FAILURE-S1 audit. The strict-key claim was verified and HOLDS at
  all 17 levels. One level down it does not: `auto: true` silently discards the
  field's declared `type` and creates a TIMESTAMPTZ; `enum` on a non-string field
  loads and makes the field permanently unwritable; role-global `actions` are the
  only action list the meta-schema does not enumerate, so a typo becomes a permission
  that grants nothing; `hooks.<event>.timeout` is accepted at every layer and read by
  no code.
- **Impact:** Medium. Each is a schema that validates and then behaves differently
  from what it says.
- **Ready:** each combination validated at load with an error naming the conflict —
  the mechanism already exists (`validateFilterOp` is the model), it simply has not
  been applied to these pairs.

### SEC-5 — (details delivered to the maintainer directly)
- **Origin:** SILENT-FAILURE-S1 audit, verified live by the session.
- **Handling:** an exploitable finding, so per the session's rule the
  reproduction — and everything that would narrow it down — was reported
  directly to the maintainer and is deliberately NOT written in this file, in
  the audit document, or in any commit message (the class, scope and fix sketch
  live in the maintainer's internal handoff package). Ask Miguel for the detail
  before working on it.
- **Impact:** High.
- **Ready:** fixed per the privately-delivered description, with a regression
  test and a binary-diff-gate corpus row.

### SCHEMA-1 — Computed / derived fields
- **Origin:** docs/MODEL_LAB.md G7 ("order totals as a computed field").
- **Impact:** Medium. Totals, counts and balances are recomputed by the client or
  a handler; the aggregation endpoint (G3) covers the reporting case but not "a
  field on the row".
- **Ready:** a declarative computed field materializes as a generated column or a
  view-backed read, is validated at load (an expression grammar, not raw SQL —
  the same rule as SCHEMA-2), and never appears on the write path.

### SCHEMA-2 — Structured index predicates (partial indexes)
- **Origin:** commerce docs/GAPS.md #4; **decided in
  [ADR-022](adr/ADR-022-declarative-surface-boundaries.md) Decision 2**.
- **Impact:** Low-medium. A partial index today lives in the user's boot DDL
  (`Config.BeforeStart`), which works and is documented.
- **Ready:** a STRUCTURED predicate (`{"where": {"field","op","value"}}` — never
  raw SQL), validated at load, rendered through `pkg/schemadiff`, diffed by
  structure, and surviving an introspect→diff→apply cycle with an empty plan.

### SCHEMA-3 — Per-transition RBAC on state machines
- **Origin:** AGENTS.md §state machines "Out of scope (documented)"; **decided in
  [ADR-022](adr/ADR-022-declarative-surface-boundaries.md) Decision 3**.
- **Impact:** Medium for commerce/finance (who may refund vs who may ship). The
  documented pattern today — one custom route per privileged transition, granted
  by the `routes` block — covers it with a single authorization surface.
- **Ready:** enforced identically on REST, GraphQL **and** inside
  `POST /api/transaction`, expressible in Studio's state-machine designer and in
  the LLM grammar, benchmarked `no_change` on the update path.
- **Evidence from 1-B (2026-07-30):** the pattern carried the real case
  (empleado despacha / solo dueño reembolsa) but cost three things — `empleado`
  lost `update` on `ordenes` entirely, the transition table is re-stated in Go,
  and two authorization surfaces must stay in sync. See commerce
  `docs/GAPS.md` 1B-4 and **ENG-7** (the cheap fix that keeps this deferred).

### SCHEMA-4 — GraphQL keyset pagination
- **Origin:** docs/SCHEMA_REFERENCE.md §GraphQL ("no keyset cursors — a
  documented future increment").
- **Impact:** Low-medium. GraphQL forwards only `page`/`per_page` (OFFSET) while
  REST has keyset; a deep GraphQL page is slower than the same REST page.
- **Ready:** `after`/`before` on the GraphQL list args, mapped to the same keyset
  builder REST uses, with the offset path kept for compatibility.

### RBAC-1 — Read-scoped row-condition operators (`in`, `neq`, `is_null`)
- **Origin:** docs/SCHEMA_REFERENCE.md §7.7; **decided in
  [ADR-022](adr/ADR-022-declarative-surface-boundaries.md) Decision 1** — `eq`
  stays the only operator because the create path FORCES the condition value
  (the mass-assignment block), which only equality can mean.
- **Impact:** Low. Per-resource `permissions` and custom routes cover the cases
  seen so far.
- **Ready:** a real case that neither covers; then `in`/`neq`/`is_null` permitted
  **only** on actions listed in `condition_actions` that exclude `create`,
  type-validated at load, with negative tests and an ABBA benchmark.

### OPS-6 — Orphan ufw ALLOW rules on the 105 (2375/2376 = Docker API without TLS)
- **Origin:** LIBRARY-GAPS-S2 Parte 0, the port-exposure review. `ufw status`
  on the 105 shows ALLOW rules for **2375/tcp and 2376/tcp** (the Docker daemon
  API — 2375 is the UNENCRYPTED variant, root-equivalent if anything ever
  listens there), plus 5678 and 8000. **Nothing listens on any of them today**,
  so they are landmines, not active holes. Deliberately NOT touched this
  session: they may belong to a workflow of Miguel's (a remote docker context,
  n8n on 5678?), and firewall edits on a box he works on are his call.
- **Impact:** Low today, high the day something binds those ports. The rest of
  the review landed: commerce now binds 127.0.0.1 (data + control plane) via
  the new `Config.Host`/`ControlHost`, and ufw's default-deny covers
  8099/9099/3099.
- **Ready:** Miguel confirms whether anything needs 2375/2376/5678/8000; the
  unneeded rules are deleted; `ufw status` on the 105 is re-verified and
  recorded in `05_SERVIDORES_Y_OPERATIVA.md`.

### OPS-1 — A measurement box without CPU steal
- **Origin:** ESTADO_Y_PLAN_MAESTRO "DEUDA TÉCNICA CONOCIDA"; reconfirmed by
  LIBRARY-GAPS-S1 (a base-vs-base control measured a **±8 %** noise floor on the
  105) and again by this session.
- **Impact:** Medium for engineering confidence. Fine deltas (a few ns on the
  RBAC path) are unmeasurable here; the ABBA + control protocol works around it
  but costs a full extra arm every time.
- **Ready:** a dedicated box (or a pinned/isolated cgroup) where a base-vs-base
  control lands under ±2 %, wired into the bench protocol.

### OPS-2 — Statistical benchmark gate in CI
- **Origin:** ESTADO_Y_PLAN_MAESTRO FASE 4.
- **Impact:** Medium. Regressions are caught by hand today, per session.
- **Ready:** a push runs the protocol against a stable baseline and fails on a
  verdict of `CHANGED` beyond max(0.5 ms, 3 %). Depends on OPS-1.


### OPS-4 — The deployed binary is not traceable to a commit
- **Origin:** this session, while bootstrapping the admin account on the 58.
  `/health` reports `"version":"dev"` and `appximo version` says
  `commit unknown` — the live binary was built without the ldflags the canonical
  build (`scripts/build-engine.sh`) injects.
- **Impact:** Medium for operations. Nobody can tell from outside WHICH build is
  serving `api.appximo.com`, and the DevHub deploy pipeline's smoke check
  (`/health` version == pushed SHA) cannot pass, so a deploy's own verification
  step is degraded. It also makes a rollback decision guesswork.
- **Ready:** every path that produces a shipped binary (the installer's download,
  `deploy-update.sh`, the Docker image, a manual build documented in
  PRODUCTION.md) goes through `scripts/build-engine.sh`, and `/health` on the 58
  reports a real SHA. Related to the release tag (see "Requires Miguel" below).

### OPS-5 — `/root/commerce` has no git remote (its only copy is one disk)
- **Origin:** this session, checking the repo state for the handoff package.
  `git remote -v` in `/root/commerce` is **empty**; its 3 commits
  (`a685cc6` commerce core, `3541b31` native migration, `178d46f` sweeper) exist
  ONLY on the 105's disk.
- **Impact:** **High, and it is not a code problem.** That repo holds slice 1 of
  the commerce platform AND `docs/GAPS.md` — the field report that drove
  LIBRARY-GAPS-S1 and the most valuable artifact the project has produced, because
  it came from building something real. A disk failure or a rebuilt droplet loses
  it with no way to reconstruct it. Every OTHER asset (the engine, its docs, its
  ADRs) is pushed to GitHub; this one is not.
- **Ready:** a remote exists (private GitHub repo, or at minimum a bundle pushed
  off-box), the 3 commits are pushed, and `docs/GAPS.md` is reachable from
  somewhere that is not the 105. Note the repo carries a `replace` directive to the
  local engine path — the remote must document how to build it (or the replace
  becomes a broken clone for anyone else).

### COMMERCE-1 — Credit notes (fiscally correct refunds)
- **Origin:** COMMERCE-CORE-S1 report; the DIAN interface (`docs/DIAN.md`).
- **Impact:** **High for a Colombian merchant.** A refund today reverses the
  payment and the stock but issues no *nota crédito* — the invoice stays as
  issued, so the tax record is wrong.
- **Ready:** an approved refund emits a credit note through the same outbox +
  issuer path an invoice uses, linked to the original invoice's CUFE, with the
  same at-least-once idempotency; verified in `verify-webhook.sh`.

### COMMERCE-2 — Tax categories per product
- **Origin:** COMMERCE-CORE-S1 report. IVA is hardcoded at 19 % in the checkout.
- **Impact:** Medium — product scope, not a defect. Wrong for exempt/excluded
  goods, which is most food retail.
- **Ready:** a tax category per product type resolving a rate at checkout, with
  exempt (0 %) and excluded (no tax) distinguished, and the reconciliation report
  broken down by rate.

### COMMERCE-4 — A real DIAN Proveedor Tecnológico adapter
- **Origin:** `docs/DIAN.md` (the interface exists; the implementation is a stub).
- **Impact:** High to go live in Colombia, zero for the engine.
- **Ready:** one PT implemented behind the existing `Issuer` interface, with
  sandbox credentials, the CUFE returned and stored, and the failure modes mapped
  to the `facturas` state machine.

---

## CLOSED (decided, with the reasoning written down)

| ID | Item | Decision & where it is justified |
|---|---|---|
| **RBAC-C1** | Join / subquery row conditions | **No.** Unbounded per-row cost inside the embed LATERAL, and an unauditable compiled policy. Denormalize the ownership column — [ADR-022](adr/ADR-022-declarative-surface-boundaries.md) Decision 1b. Reconsider if a case needs ownership through a relation AND the denormalized column is genuinely unmaintainable. |
| **SCHEMA-C1** | Raw-SQL index predicates | **No.** Arbitrary SQL rendered into DDL from a surface that `ai-generate` and Studio also write. (The previously-stated churn objection was measured and **retracted** — normalization is deterministic.) [ADR-022](adr/ADR-022-declarative-surface-boundaries.md) Decision 2. Reopens as SCHEMA-2 (structured form). |
| **SCHEMA-C2** | A `decimal`/`money` field type | **No.** `int64` in minor units is the industry representation and what payment APIs speak; a `numeric` would still reach a JSON client as a float, recreating the bug. Documented in AGENTS.md, SCHEMA_REFERENCE §3.2, BACKEND_SPEC §2 and the LLM grammar so generated schemas use it. Reconsider if a client needs exact decimals the JSON layer can carry (a string-typed money scalar). |
| **DOC-C1** | "The binary cannot serve a frontend" | **Fixed, not merely closed** — `Config.Static` ships (this session). Listed here because PROD_PATH_AUDIT §1.4 and PRODUCTION.md §6(c) both asserted it; both are corrected. |

---

## Requires a decision from Miguel (not technical blockers)

All three were **re-verified as still open on 2026-07-29**.

| Item | Why it needs him |
|---|---|
| **Cloudflare proxy on `api.appximo.com`** | Still proxied (dig → Cloudflare IPs), but since PROD-JOURNEY-1B the 58 no longer serves that domain: Caddy's only site is `tiendita.appitools.com` (direct A record, measured clean), so `api.appximo.com` now dead-ends at the proxy. Decide: retire the DNS entry, point it somewhere real, or leave it dark. |
| **Cut the first release tag** | Still zero tags (`git tag` is empty) and `RELEASE_VERSION=""` at `scripts/install.sh:33`, so the documented "download the binary from GitHub Releases" path cannot work yet, and OPS-4 (version traceability) partly depends on it. |
| ~~**Rotate the 58's PostgreSQL password**~~ | **RESOLVED by PROD-JOURNEY-1B (2026-07-31):** the wipe (`--uninstall --purge`) dropped the role and database; the reinstall generated a fresh password (plus fresh JWT/admin secrets, rotated again on-box after the installer printed them to stdout — see OPS-7). The exposed credential no longer exists. |
| **Publish the Go module (the 10 % path is blocked on it)** | `github.com/appximo/appximo` is private with no tag, so `go get` / `go mod tidy` FAIL for anyone building a custom backend: the only recipe that works is a local checkout plus an absolute-path `replace`, which does not build on a teammate's machine, in CI, or in a plain `docker build`. This is now written honestly at the top of `backend-spec` §3.0 (with exactly what changes once it is published), but it is a **product blocker, not a doc gap** — the framework half of the product is unreachable outside this machine until the repo is public or a tagged private module + `GOPRIVATE` is set up. Part of **DOC-2**, which is otherwise DONE. |
| **Give `/root/commerce` a remote** | The technical fix is trivial; the decision (which account, public or private, whether the commerce platform is a product or a demo) is his. See **OPS-5** — until then the field report lives on one disk. |
| **Pick the canonical repo URL** (PHASE3-GUIDE-S1) | Two names coexist today: the README badges and installer point at `github.com/appximo/appximo`; the Go module path — and therefore every import, the served OpenAPI `info.description` and the specs — says `github.com/appximo/appximo`. Publishing under either makes the other wrong; a module-path change is a breaking edit to every consumer `go.mod`. The page (`site/`) ships the repo link as an explicit placeholder until this is decided. |
| **Where `site/` lives** (PHASE3-GUIDE-S1) | The official page is finished, self-contained and browser-verified, with the repo/Releases/domain spots as dashed placeholders. Publishing venue (GitHub Pages over the repo, `appximo.com`, or a host) is his call; `site/README.md` documents what each choice changes (the relative `../docs/` links). |

---

## DONE in PHASE3-GUIDE-S1 (2026-08-02)

Phase 3 delivered: the public entry material, written FROM the five field
journeys with every claim tied to the certification. Docs/page/assets only —
zero engine-behavior change (`git diff` is docs + site + handoff).

| What shipped | Verified by |
|---|---|
| **`docs/GUIDE.md`** — the third-party master guide: 10 chapters ordered by "what a newcomer hits" (GAPS 3-17/4-4 + the Cancha Ya frictions as the index); "who it is NOT for" as a chapter; the today-honest availability state (no release tag, module unpublished); every number with condition + date; ends with "verify everything yourself" | the §2 zero-to-API path EXECUTED live on a fresh DB (scratch :8501, outputs pasted real); every figure traced to CERTIFICATION_2026-08-01 |
| **`site/`** — the official page: one self-contained `index.html` (no build, no runtime deps, system fonts, inline CSS) + 5 REAL screenshots (tiendita mobile, Studio ERD, /docs, /admin, petfriendly /docs); numbers table with a Condition column (limiter caveat inline); "who it's NOT for" section; honest comparison (no NestJS figures); repo/Releases/domain as dashed placeholders | Playwright: mobile 390×844 + desktop 1440×900 — no horizontal scroll, 5/5 images, zero console errors |
| **Claim corrections** — CAPABILITIES (~45 MB → certified sizes; stale ENG-16 line; GraphQL `update` mutation missing from the list) and 02_QUE_ES §1/§6 (pre-certification numbers, NestJS in present tense) | certification addendum §PHASE3-GUIDE-S1 lists each |
| **Certification addendum** — the functional front-door claims re-verified live 2026-08-02 (self-bootstrap, tenant, token refusal, 422/400 shapes, docs/openapi, signup 403, specs trilogy, plain build 85.2 MB) | the addendum itself |

New rows above in "Requires a decision from Miguel": the canonical repo URL
(RESOLVED by RENAME-AND-PUBLISH-PREP-S1: github.com/appximo/appximo) and where
`site/` gets published.

## DONE in NIGHT-SWEEP-S1 (2026-08-02, overnight)

The session's subject was the CLASS — "the engine inspects an input, does not
recognize it, and continues in silence" — run to exhaustion on the known input
surfaces: the audit checklist completed (19 probe/verify agents, 9 surfaces,
every SILENT claim adversarially re-verified), the nine open ENG items of the
family closed, and every CONFIRMED new same-class finding fixed the same night.
Every fix: unit test + full lane + binary-diff gate (92-row corpus: 61 SAME,
31 DIFF — all 31 intended and enumerated in the session report) + live
before/after probes; the hot path measured `no_change` (ABBA ×4 windows,
Δp50 −1.42 %, MWU p=0.880, both controls moved MORE than the effect).

| ID | What shipped | Verified by |
|---|---|---|
| **ENG-20** (authorization, first) | A `$…` condition val that is not `$user_id`/`$external_client_id` REJECTS the schema at load listing both (it was compared as the literal text — zero rows forever, invisible to SCHEMA-5); a bare `user_id`/`external_client_id` (forgotten `$`) raises the new `bare_condition_variable` WARNING suggesting the variable. GrammarCore/spec updated so the LLM loop knows the closed set. | validator + warnings tests; live CLI probe base-accepts vs new-rejects |
| **ENG-19** | A `webhook` before-hook is rejected at LOAD in both validators (semantic + formal meta-schema) — it validated, required a URL, and was never dispatched. Studio no longer offers webhook for before events; grammar + SCHEMA_REFERENCE updated; the runner branch is documented defense-in-depth. | validator + meta-schema parity tests; live probe: base boots the schema, new names the hook |
| **ENG-15** | A cursor request owns its shape: `after`+`before`, cursor+`sort`/`order[…]`, cursor+`page`, cursor+`count` are named 400s (each used to discard one side silently — and `meta.page` echoed the page it ignored); empty `?after=` is a named 400; a cursor response's meta carries `per_page`+`has_next` ONLY (no page/has_prev). OpenAPI PaginationMeta updated to match. | unit + integration (meta shape) + 6 corpus rows + live probes |
| **ENG-16** | Two `order[…]` parameters are a 400 naming both (the winner was Go map-iteration order — measured 174/26); same rule on the GraphQL `order` argument (>1 field → error naming them). | unit tests both surfaces + corpus row |
| **ENG-17** | A repeated engine-owned parameter is a 400 naming it and the count, everywhere the engine reads one (page/per_page/sort/order/order[…]/filter[…]/search/after/before/count/include + the aggregate functions). Identical duplicates rejected too — one parameter, one meaning. | unit + integration + 4 corpus rows |
| **ENG-18 + ENG-23** (one flag) | `count` is read BY VALUE on REST (false/0 = off; bare `?count` and true/1 = on; garbage = named 400) matching GraphQL's Boolean; it WORKS with `?include=` (the embed path dropped it); a failed COUNT(*) is an error response, never a 200 missing its total; count+cursor is a named 400 (the total would mean "rows past the cursor"). Unknown aggregate functions are named 400s listing the set; empty `group_by` is a 400 instead of a silent shape change. | unit + integration + 7 corpus rows + live probes |
| **ENG-24** | The aggregate endpoint OWNS its parameter namespace (the written ADR-024 exception-to-the-exception): list params it cannot honor (`page`/`sort`/`order`/cursors/`include`) are rejected BY NAME with the reason instead of validated-then-discarded; an empty entry in an active CSV list (`sum=a,`) names the extra comma (REST and GraphQL — stringList keeps empties so one validator serves both). | unit + integration + 5 corpus rows |
| **ENG-34** | Bind FIRST, announce after: `shutdown.Listen` + `Serve` split; the data plane, control plane, fleet proxy, fleet status API and in-process fleet all announce only AFTER a successful bind; a lost bind race prints ONLY the error. Live-proven: base printed `serving on :8590` then died; new prints only `cannot listen`. backend-spec's trap note updated. | shutdown unit tests ×3; live busy-port probe both binaries |
| **UI-1** | Studio authors `accept`/`max_bytes` on file fields (family list or exact types; both engine shapes round-trip — single value stays a string), clears them when a field stops being `file`, and mirrors the engine's policy validation in live issues. | svelte-check 0 errors; transform round-trip proof (string + array forms) |
| **Audit findings (CONFIRMED, fixed same night)** | (1) single-record routes (GET/{id}, PUT/PATCH/DELETE/{id}, relation subroute) and the SSE stream reject the engine-owned list params they used to silently discard; (2) empty/`,,,` `?include=` named 400s and the unknown-relation error lists the available relations; (3) `?search=` on a text-less resource is a named 400 (was a silent full list); (4) 405 carries the JSON error contract (body suppressed on HEAD per RFC); (5) /auth bodies name the unknown key (mirror of the /admin fix) and refresh no longer swallows decode errors nor lets body/header tokens conflict silently; a missing verify token is named; (6) tenant-REGISTER bodies strict-decode on BOTH operator planes; (7) tx: guard-on-create / data-on-delete are named 400s, non-uuid create id is the same 400 as single-op (was a masked 500); (8) a second multipart "file" part is a named 400 with rollback (was data loss behind a 201) and the extra-form-fields tolerance is a written ADR-024 exception. | unit tests per fix + full lane + 8 corpus rows + live probes; multipart rollback verified (0 rows after the 400) |

## DONE in THIRD-PARTY-READY-S1 (2026-08-02)

The five items standing between a THIRD PARTY and building alone — the
discoverable surface, the trilogy that announces itself, and the file-stack
field-report items — plus the final proof: a fresh agent with NO repo access
building an app from only the printed specs (verdict recorded in the session
report and 04_ESTADO_ACTUAL §7).

| Item | What shipped | Proof |
|---|---|---|
| **ENG-33 — the served surface is discoverable** | Every registered custom route is published in the running app's `/openapi.json`: method, path, auth mode (`x-public` + `security:[]` / Bearer + the RBAC segment-action in the description), `x-required-role`, `x-byte-serving`, flagged `x-appximo-custom-route`, plus the new optional `Route.Description` as summary. Shapes stay the contract sheet's job (existence vs shapes division, backend-spec §3.6b rewritten). The 401-probe semantics stay DELIBERATELY: with the contract public, probing is not the discovery mechanism, and a pre-auth 404 would need a second route matcher that can only drift. | unit tests (incl. nil-routes byte-identical); the tiendita's `/openapi.json` lists its 12 custom routes; frontend-guide's lists `public-photo`; binary-diff gate 64/65 SAME + the 1 diff = the intended info.description change |
| **ENG-32 — HEAD on custom GET routes** | Same handler, same auth (Public HEAD rides the same skip), RBAC maps HEAD→read, `http.ServeContent` answers natively. Generated routes unchanged (pinned by a new corpus row: 405 both binaries). | integration test (200 + ETag + empty body); live probe on the tiendita image route: was 401, now 200 headers-only |
| **FILES-1 — per-FIELD attach policy** | `accept` (family/`pdf`/exact type; string or array) + `max_bytes` on file fields, enforced at ATTACH on all five write shapes (REST create/update, GraphQL ×2, batch, Ctx.Insert/Update) against the SNIFFED stored metadata → 422 `file_policy` naming the policy. Load-validated, meta-schema + LLM grammar updated (pinned). Existence stays the FK's verdict. | schema+core unit tests; live: the tiendita's `imagen_id` declares image/5 MiB, the instance-wide .env.dev workaround RETIRED, and e2e-1b runs the exact backlog probe (6 MB real PNG and a real PDF both upload fine, both fail the attach 422 `file_policy`) — 43/43 |
| **FILES-2 — declarable ServeFile cache policy** | `ServeFile(id, WithCacheControl(...))` + `CacheControlImmutable` (safe: the store is content-addressed). Sent only on the success path — never on the 404. | integration test (header on stream, absent on miss); live: the tiendita image serves `public, max-age=31536000, immutable` |
| **Trilogy discoverability (Parte B)** | New `appximo specs` (the three docs in one paste, pure concatenation); root `--help` names the trilogy; README gained a prominent "building with an AI agent" section after the quick start; each spec's header names its siblings; `/docs` shows the pointer via OpenAPI `info.description`; `install.sh`'s summary points at the commands + the live `/openapi.json`. | `appximo --help` / `specs` output; the gate's one explained diff IS the /docs pointer |
| **COMMERCE-6 — the browser suite cleans up** | `e2e-browser.mjs` deletes its stamped product/photo/orders/clients (order-first dance, PSQL_CMD-parameterized, runs on failure too); the RULE written in commerce `docs/TESTING.md` (a suite that runs against a live shop cleans up or it is broken). | two consecutive runs: counts 12/15/6/5 → 12/15/6/5, 21/21 both |

Session-wide gates: lint 0 · unit + FULL lane green · binary-diff gate 64/65
SAME (1 diff = intended) · write-path ABBA (the touched hot path): Δp50
+0.055 ms (+2.20 %), MWU p=0.096 → **no_change**, with the base-vs-base
control moving 3× more (+0.179 ms) than the effect.

## DONE in FRONTEND-SPEC-S1 (2026-08-02)

| Item | What shipped | Proof |
|---|---|---|
| **The agent trilogy closes** | `docs/FRONTEND_SPEC_LLM.md` + `appximo frontend-spec` (embedded, single source) — the frontend guide distilled from the SHIPPED tiendita, not theory: embedded-vs-apart, the stack argument (adapter-static SPA, the cheap-AI criterion), the full API contract (incl. the live-verified cursor⊘sort caveat and the count presence-flag), errors→screen-states, the six mandatory states, the files pattern end to end, the browser-only traps (incl. the empty-string-passes-`required` form trap found by this session's own browser run). Registered in README/CAPABILITIES/AGENTS. | `appximo frontend-spec` prints it; an external-read pass (agent given ONLY the doc) ran as the acceptance act |
| **`Ctx.ServeFile` + `Route.ByteServing`** | Custom routes can stream tenant files through the engine store (Range/strong ETag→304/sendfile; uniform 404 malformed/unknown/foreign; stream runs AFTER commit; buffered Error wins). ByteServing (GET-only, literal path) bypasses the response cache AND compression — both proven landmines (RAM-buffered blobs, stripped `Content-Disposition`, dead sendfile). Nil-cost when undeclared. | unit + integration (bytes/ETag/304/206/cross-tenant/compression/cache asserted); binary-diff gate **63/63 SAME**; ABBA+2 controls **`no_change`** (Δp50 +0.49 %, MWU p=0.821, both controls louder) |
| **`permissions` can grant the built-in `files` store** | The ADR-021 asymmetry, alive on `/api/files`: scoped roles could not upload. Now a known key, ACTIONS ONLY (conditions/fields = dead config, rejected at load with the reason); a schema with its OWN `files` resource keeps normal validation. | validator tests + runtime integration (scoped upload 201 / read 200 / delete 403); live: the tiendita's empleado uploads |
| **`examples/frontend-guide/`** | One binary, no-build vanilla SPA: login/signup, multi-field 422 mapped in place, upload→attach→display, the public-image route. | 6/6 in a real mobile-viewport browser, console-error-strict |
| **The tiendita has product photos** (first real front on the file stack) | Public byte-serving image route (authorize-by-relationship), panel upload with XHR progress + signed-URL preview, storefront `<img>`+placeholder, idempotent photo seed. Official migration path: dry-run clean, counts identical. | commerce e2e-1b **40/40** · browser **21/21** · verify 18/18 · webhook 21/21 · isolation probe 6/6 · field report **PARTE CINCO** |

## DONE in INPUT-CLASS-CLOSE-S1 (2026-08-01)

The seven items the adversarial review opened (ENG-25…ENG-31, ENG-27 first —
it is security), closed under ADR-024, **plus the session's real deliverable:
the technique that found what the tests did not — diffing two binaries over a
paired corpus — baked into the repo as `scripts/binary-diff-gate.sh`**. Every
fix below was verified by unit test + the full lane (no `-short`) + that gate
(63 cases, base = pre-session HEAD; diff set stable across runs; every DIFF
mapped to an intended change in the session report). Details:
[UNRECOGNIZED_INPUT_AUDIT §INPUT-CLASS-CLOSE-S1](audits/UNRECOGNIZED_INPUT_AUDIT.md).

| ID | What shipped | Verified by |
|---|---|---|
| **ENG-27** | The deliberate asymmetry: an RBAC deny logs `rbac: denied … role %q is not declared by any schema role` vs `…declared but not permitted %q on %q` (`Policy.DenyDetail`, at the REST middleware + transaction per-op + GraphQL denies) while the RESPONSE stays byte-identical `403 forbidden` for both (an enumeration oracle otherwise; same family as SEC-5). Both deny paths do the same work — no timing/length channel. `appximo token --schema <file>` refuses an undeclared role listing the declared set. | `TestDeny_UndeclaredRoleIndistinguishableToClient` (bodies byte-equal + logs distinguish + logs never cross); gate rows `rbac-undeclared-role` / `rbac-declared-denied`; live CLI probe |
| **ENG-25** | `validateFilterValue` — a wrongly-typed filter value is a 400 naming the parameter, value and type, BEFORE any SQL exists. Acceptors reproduce **Postgres's grammar, never Go's** (`yes` is a bool, unique prefixes, whitespace, `Infinity`/`NaN`, PG16 literal forms). **`time` is the written exception** (grammar too wide to reproduce safely — stays an anonymous 400, in the audit + visible in the gate corpus). Reached the aggregate path for free (`BuildAggregate` delegates) — verified, not assumed. | `TestFilterValue_PostgresConformance` (unit corpus) + `TestFilterValueLivePostgresConformance` (**asks a real Postgres** per value, asserts PG-accepts ⇒ engine-accepts); gate rows incl. `filter-bool-yes-still-works`, `agg-filter-wrong-typed` |
| **ENG-26** | `?filter[id][eq]=<uuid>` works — the implicit PK filters as the uuid it is (`eq` only), consistent with `?sort=id` and the cursors; the `available:` list names `id` truthfully again. Inherited by the aggregate path (verified). | `TestBuildQuery_FilterByID`; gate rows `filter-by-id`, `agg-filter-by-id` |
| **ENG-28** | `analyzeQuery` charges a fragment's cost at EVERY spread site (memoized, cycle-guarded); the measured ~46× bypass document (50 aliases × 45-field fragment: counted ~95, resolved ~92,500) is rejected. Counted ≥ resolved — over-counts repeated same-alias spreads, the safe direction. The AGENTS 2000-selection claim re-verified against the fixed counter. | `TestAnalyzeQuery_FragmentSpreadCharged` (bypass doc rejected, under-cap passes, cycles safe, introspection in unused fragments still detected); gate row `graphql-fragment-amplified` |
| **ENG-29** | `CollectUpdate` returns the S44 `fields[]` shape — EVERY violation at once (`type`/`unknown_field`/`read_only`/`required`), sorted — and all three callers emit it: REST PUT/PATCH (`writeValidationErrs`), GraphQL `update…` (`errors[].extensions.fields`), batch transaction op. One 422 contract for both verbs; the OpenAPI `ValidationErrorResponse` is now TRUE for updates (no spec change needed — the fix made the existing claim honest). AGENTS.md's "bodies differ in shape" paragraph replaced. | `TestCollectUpdate_S44Shape` (all 6 violations in one response, deterministic order); gate rows `update-*`, `tx-update-decimal-to-int`, `graphql-update-bad-uuid`, `update-multi-errors-at-once` |
| **ENG-30** | Presence, not non-emptiness, gates `page`/`per_page`/`sort`/`order`: `?page=` (an empty form field) is the same named 400 as `?page=0`; `?sort=` names the empty value + available fields; `?order=` with sort → `use asc or desc`; and `?order=desc` with NO sort — read by nothing before — is a 400 explaining it requires sort. Absent parameters still default silently (presence is the gate). | `TestBuildQuery_EmptyOwnedParamsAreRejected`; gate rows `page-empty`, `per-page-empty`, `sort-empty`, `order-empty-with-sort`, `order-without-sort` |
| **ENG-31** | `validateFieldValue` treats `file` as the uuid column it is (`file_not_found` stays the FK's job — this checks the SHAPE); a wrongly-typed file value names the field instead of dying downstream as an unnamed FK/cast error. | `TestValidateFieldValue_File`; covered on update/guard paths via `CollectUpdate` |
| **Part B — the gate** | `scripts/binary-diff-gate.sh` + `scripts/binary-diff/corpus.jsonl` (63 cases: the previous session's before/after table, ADR-024's staples, one row per fix above, and rows that PIN OPEN ITEMS — ENG-15's discarded sort, ENG-17's first-value-wins — so a change to them gets noticed). Kills only its own PIDs, refuses busy ports, compares list bodies as SETS (random-uuid order differs between correct instances — measured), excludes cache hit/miss markers as volatile (measured flipping between identical binaries). Documented in AGENTS §Conventions + CONTRIBUTING checklist. | Run 6× this session; diff set stable at 23, all intended; the harness's own two nondeterminism bugs were found by running it twice |
| **The `make test` decision** | Unambiguous: the lying lane is LOCAL `make test` (`-short`); CI's full lane already runs `pkg/integration` (Short-gated, not build-tag-gated — verified in ci.yml). (a) The `default:"now"` class is now covered IN the unit lane (`TestValidateCreateTypes_EngineInjectedDefaultsAreNotCallerInput`, real `ApplyDefaults`, no DB); (b) `make test` demoted in AGENTS + CONTRIBUTING to the fast inner loop — a data-path change's bar is unit + full lane + the binary-diff gate. | the test exists and runs in `-short`; docs updated |

**Contract changes shipped (breaking for silent-reliance clients, per ADR-024
§Consequences):** empty `page`/`per_page`/`sort`/`order` and bare `order` now
400; wrongly-typed filter values now name themselves; update's 422 body changed
shape from flat string to `validation_failed`+`fields[]`; `filter[id]` is new
surface; fragment-heavy GraphQL documents above the true cap are rejected.

## DONE in SILENT-FAILURE-S1 (2026-08-01)

The session's subject was not `is_null`. It was that **in several layers the engine
accepts an input it does not recognize and continues in silence** — a class with four
production instances behind it. The audit
([UNRECOGNIZED_INPUT_AUDIT](audits/UNRECOGNIZED_INPUT_AUDIT.md)) swept ten input
surfaces; the policy is [ADR-024](adr/ADR-024-unrecognized-input.md).

| ID | What shipped | Verified by |
|---|---|---|
| **ENG-14 + the class** | A pattern now decides what an input **is**, never what is **valid** — validation moved into code that can produce an error. Any parameter under a prefix the engine owns (`filter[`, `order[`) must parse or `400`. Measured live: five spellings of one intent used to give **one 400 and four full-table 200s**; all five now name the offending input and list the alternatives. `?sort=ghost`, `?order[ghost]=`, `?order=descending` (which sorted ASCENDING) all rejected. A test that asserted the old fallback was removing the very contract — rewritten. | `TestBuildQuery_UnrecognizedInputIsAlwaysRejected` (11 cases, each asserting the message NAMES the input) + `…_ValidInputStillWorks` (11 valid shapes unchanged); live probes on a running engine |
| **Two more instances, found by the audit** | A misspelled **`dry_run`** (`dryrun`, `dry-run`) decoded to false and turned a PREVIEW into a real migration — measured live against the control plane. The three bodies carrying that flag now decode strictly. And **`appximo serve <path>` served a different app than the one named** (it booted `./schema.json`), now a clean error pointing at `--schema`. | live control-plane probes; CLI output |
| **SEC-1** | Security headers survive the response cache. The cache stored only `Content-Type`/`Etag`, so a cached `/api/*` read lost its CSP while a `no-cache` bypass kept it — the same URL answering with two postures depending on cache state. An **allowlist**, deliberately: replaying every captured header would replay `X-Trace-Id` too, poisoning the trace ring. | `TestSecurityHeadersSurviveTheCache`, `TestPerRequestHeadersAreNotReplayed`, `TestNoSecurityHeadersCostsNothing` |
| **SEC-2** | **Hash-based CSP.** At boot the engine parses each static mount's `index.html`, computes the sha256 of every inline `<script>`, and emits `'sha256-…'` instead of `'unsafe-inline'` — so the bundler's shell still runs and injected script does not. No inline scripts → `script-src 'self'` (strictest). Anything a hash cannot cover (an `onclick=` attribute) keeps the permissive policy **and logs why**. `style-src` keeps `'unsafe-inline'` on purpose: component libraries inject styles at runtime, where no boot-time hash can reach. | **Real browser, with a control arm**: legacy policy → injected script EXECUTED; hardened → bootstrap still ran, external module still ran, **injected script blocked**, 1 CSP violation logged. Plus 8 unit tests incl. the engine's own built shells |
| **SEC-3** | `Route.Public`'s optional authentication, exercised **live** through a booted App over real HTTP: no token → 200 anonymous; valid token → 200 with claims populated; garbage / wrong-secret / expired / foreign-tenant → **401, never a silent downgrade to anonymous**. Plus the exact-match check (a sibling path and another method are not public). | `route_public_live_integration_test.go` (7 subtests) |
| **OPS-3** | `.golangci.yml` with **every exclusion stating why**, and the gate wired into CI next to fmt-check/vet/tests/govulncheck. `make lint` goes from 62 findings / exit 1 to **0 issues / exit 0**. The QF* quickfixes are excluded as a class (editor suggestions, not defects); ST1xxx would have been 107 "add a package comment" findings — a different decision from "hold the line on correctness", not one to make in the pass that first turns the gate on. Two real findings were fixed, not silenced: an empty branch that had been hollowed out (now the assertion it was meant to be) and the `rows.Close` sites. | `golangci-lint run ./...` → `0 issues`; `.github/workflows/ci.yml` job `lint` |

**Measured (the filter parser is in the hot path):** ABBA, four windows alternating
pre-session and session binaries, canonical baseline (dev-fast + `examples/blank` +
`benchblank` + 100 rps/30 s). **Δ p50 +0.017 ms (+2.87 %), Mann-Whitney p = 0.437 →
`no_change`.** The control arm — the two BASELINE windows against each other — moved
**−3.30 %**, so the host's own drift is larger than the measured delta. All runs are
in `benchmarks/history.tsv`.

**`is_null` was NOT added** — see the note under SCHEMA-6.

## DONE in CERTIFY-S1 (2026-08-01)

A certification pass: no engine behavior was changed. Everything below is a
measurement, a corrected claim, or measurement infrastructure. Full evidence in
[docs/CERTIFICATION_2026-08-01.md](CERTIFICATION_2026-08-01.md).

| ID | What | Verified by |
|---|---|---|
| **OPS-9** | **Closed.** `bench-protocol.sh` now **self-heals its fixture**: a missing bench tenant is recreated from the schema the target engine actually serves (`GET /editor/current-schema`), so a routine cleanup can no longer cut the historical series — it had done so twice. It only ever CREATES a missing tenant. Plus **`benchmarks/history.tsv`**, an append-only log every protocol run writes to (date, commit, label, fixture, rate, p50/p95, CV, error rate, condition note). | The tenant was deleted and the next run recreated it and measured with no intervention; this session's runs are the log's first entries |
| **Flagship benchmark re-certified** | 2000 rps → p50 **1.600 ms** CI95 [1.568, 1.665], **0 errors in 597,461 requests** — reproducing the published 1.58 ms [1.52, 1.62]. 500 rps → 1.532 ms. Cache bypassed → 2.436 ms (better than published; today's PG is uncapped). **New caveat found:** on engine DEFAULTS (1000 rps/tenant) a 2000 rps single-tenant load is ~50 % `429` — 10,080 × 200 / 9,921 × 429 / 0 × 5xx | 10-run protocol per arm on the original hardware, external loader |
| **Security posture re-certified** | All **9 original OWASP findings** still fixed, verified live. **24 live isolation/RBAC probes** clean: cross-tenant across REST/GraphQL/search/filter/aggregate/SSE/files, BOLA by direct id (404, never 403), deny-by-default, field allowlist on REST *and* GraphQL, row conditions scoping lists *and* aggregates, the mass-assignment block, and an empty `$user_id` failing CLOSED. `UnsafeTx` is still a complete grep audit (9 hits, none in request-handling code) | inline in the report §3 |
| **Documentation claims audited** | 34 verified, **6 corrected**, 4 not verifiable — binary size (~60 → ~64 MB release), the 2000 rps limiter caveat, the NestJS ratio re-dated as historical, the `is_null` claim, the `--tenant 10` reproduction recipe (ENG-11 made it unregistrable), and the layer cost (+0.97 → +1.22 ms) | each against the running engine |
| **CAPABILITIES' honest list completed** | Five real limits from the field reports were missing and are now listed: no zero-downtime upgrade, no restore command, `install.sh --app` untested live, the unpublished Go module, and the terminal-only super-admin | re-read against PARTS ONE–FIVE |

**Findings opened, not fixed** (this session certifies; a dedicated pass fixes):
**SEC-1**, **SEC-2**, **SEC-3**, **ENG-14**, **OPS-12** — all above with evidence.

**Toolchain state, measured:** `go vet` and `gofmt` clean; `make test` and the
integration lane green; **`golangci-lint` 62 issues / exit 1 and NOT wired into CI**
(OPS-3, now with a number); **`govulncheck` could not run** on the 105 (OOM-killed,
exit 137, even scoped to one package) — the CI gate remains the authority.

## DONE in AUTHORING-GAPS-S1 (2026-08-01)

The session's subject was not six bugs: it was ONE pattern — **the engine accepts
and carries on in silence**. Each item below turned a "valid and wrong" into a
"loud and actionable", with a message written for someone who knows neither SQL
nor Go.

| ID | What shipped | Verified by |
|---|---|---|
| **ENG-13** | An FK whose DEFINITION changed is a **replacement**, not a gated drop: the drop is un-gated and runs in the SAME transaction as the `ADD … NOT VALID`, so the `42710` collision that made a `references` change a silent no-op cannot happen, and a failure rolls back to the old constraint instead of leaving the column unprotected. Plus the class-level guard: **`verifyApplied` re-introspects the database after every apply** and reports anything declared-but-missing in `ApplyOutcome.Unapplied`; a PARTIAL apply is a FAILURE everywhere (the CLI exits non-zero and does NOT persist the schema, the control plane restores the previous one, the fan-out marks the tenant failed). | `TestIntegration_ENG13_ReferencesChangeActuallyApplies` / `…_OnDeleteChangeActuallyApplies` (both asserting against `pg_constraint`, never the log) + `TestIntegration_DeclaredEqualsApplied`; and live: the veterinary app's relation repointed for real, with the backfill that used to be blocked going through (§Part G) |
| **ENG-13 (audit)** | The **class**, not the instance: [docs/audits/MIGRATION_HONESTY_AUDIT.md](audits/MIGRATION_HONESTY_AUDIT.md) enumerates every place the migrator could report success and be wrong. It found three more, all fixed — a blocked `renamed_from` discarded in silence (values stayed in the old column while the schema claimed they moved), the control plane persisting a schema BEFORE the DDL (a failed migration left the record describing a database it did not have), and a gated drop erasing its own approvability (which was live on `main` as a FAILING test: `--approve-drops` reported `no-op` and the column stayed). | `TestIntegration_BlockedRenameIsReported`; `TestFanout_DestructiveGatedThenMassApproved` (red on `main` before this session, green after) |
| **SCHEMA-5** | `schema.Warnings` — a new, non-blocking layer answering "will this do what you meant?" next to the validator's "may this run?". Its first rule catches a `$user_id` row condition pointed at a **relation** column: valid, deployable, **zero rows forever**, no error at any layer. Surfaced in FIVE places: `appximo validate`, `validate --json` (`warnings[]`, `valid` untouched — so the AI correction loop can act on it), the control-plane/Studio deploy response, engine boot, and `ai-generate`'s report. It is a **warning**, not an error: the pattern is legal when the FK genuinely holds login ids — and applying the suggested fix silences it. | `pkg/schema/warnings_test.go` (5 cases incl. no-false-positives and fix-silences-it); live on the exact generated schema |
| **ENG-12** | The write path validates against the tenant's **DEPLOYED** schema, merged with the boot one (a UNION — a deploy can only ever ADD to what was accepted, so a tenant whose record lags the boot file behaves exactly as before). Compiled once per tenant per `pg_notify` invalidation; a request costs one RWMutex read. A field added by a deploy is now **writable with no restart**. And the other half: a resource that genuinely needs a restart answers `resource_not_loaded` **explaining itself**, never a bare 404 or "unknown field". | Live, designed so it could not self-deceive (the previous claim was verified against a field already in the boot schema): `pets.weight_kg` was asserted ABSENT from the file the process booted with, then `PATCH`ed → 200, same PID |
| **ENG-11** | ONE tenant-id alphabet — `^[a-z][a-z0-9]{1,29}$`, the **intersection** of the Postgres-schema alphabet (no hyphens) and the DNS-label alphabet (no underscores) — in the control plane, Studio and the admin console, with the suggestion helper now producing something the engine actually accepts (`mi-clinica` → `miclinica`, not the `mi_clinica` it used to recommend). `401 token tenant mismatch` now names the host that arrived, the tenant it implies, the tenant the token carries, and the address the token WOULD work at. Creating a tenant through `/admin` warns when the id does not match the domain serving the app — the moment both facts are known. | Live: `vet_journey` refused at registration with the fix suggested; the 401 read in full; `pkg/controlplane/tenant_id_test.go` asserts every suggestion satisfies the rule |
| **DOC-2** | (a) The generation grammar now teaches **state machines** and the **per-resource RBAC form**, plus the identity-vs-foreign-key rule — measured on the original Spanish description, 3 runs per arm: **before 0/3 state machines, 2–3 iterations, ~$0.035; after 3/3, 1–2 iterations, ~$0.016** (richer grammar ⇒ correct AND 55 % cheaper, because it stops needing correction rounds). (b) **"Copy AI context"** in Studio + `GET /editor/ai-context` — `appximo spec` plus this app's schema, one click, so the product's most effective feature stops being undiscoverable. (c) `backend-spec` now OPENS with the real dependency recipe (the local checkout + `replace`, its costs stated) and exactly what changes when the module is published. (d) `Ctx.Get(resource, id)` — the sanctioned lookup-by-id that keeps the row rule, with the doc stating that `QueryOpts.Filters` takes declared fields only. | 3 live generation runs per arm with costs; `pkg/aigen` tests; the module recipe is the honest state, and the publishing decision is Miguel's (see below) |
| **OPS-10** | `install.sh --app=NAME` namespaces **everything**: unit, service user, `/etc`, `/opt`, `/var/lib`, database + role, control port, and a per-app Caddy **site file** (`/etc/caddy/sites/<app>.caddy`) that the main Caddyfile only `import`s — so installing an app APPENDS a site and can never erase a sibling's. Default unchanged (`appximo`), so a single-app box is byte-identical. A second install for a DIFFERENT domain without `--app` now **refuses** and prints the exact side-by-side command. `deploy-update.sh`, `backup.sh` and `--uninstall` take the same flag. | Two apps staged side by side (`--dry-run --root`): separate secrets, database, control port (9090 vs 9183), files dir, unit and site; the guard refused the clobbering run; the idempotent re-run proceeded with app 1's config unchanged |

**OPS-9 stays OPEN, with new evidence.** The canonical `benchblank` tenant did not
exist (a previous session's cleanup took it), so the pre-flight failed — but it
failed *well*: it named the endpoint, the tenant, the role and the overrides, which
is half of OPS-9's own Ready criterion. It was recreated per the documented recipe.
The remaining half (defaults derived from the SERVED schema, so a fresh box needs no
overrides at all) is untouched.

**Measured:** `make bench-protocol RUNS=10 LABEL=authoring-gaps-s1`, canonical
baseline (dev-fast + `examples/blank` + tenant `benchblank` + 100 rps / 30 s),
against a 10-run control arm built from the pre-session binary in the same
session: baseline median p50 **0.5885 ms**, session median p50 **0.5915 ms** →
**Δ +0.003 ms (+0.51 %), Mann-Whitney p = 0.571 → `no_change`** (gate:
max(0.5 ms, 3 %)). The write path (ENG-12's one RWMutex read per create/update)
and the RBAC read path are both inside the measured surface.

**Not verified live, and deliberately so:** OPS-10's third-app install was
exercised in the installer's staged `--dry-run --root` mode, not on the 58. The 58
runs two production assets (the tienda and petfriendly) and the session's value did
not require putting them at risk; the Caddy migration path is written to be
additive (it strips only the block for the domain being installed) and was read
line by line, but a REAL third-app install on a box with a pre-OPS-10 monolithic
Caddyfile has not been executed. Anyone doing it first should snapshot
`/etc/caddy/Caddyfile` — see **OPS-11**.


## DONE in CONSUMER-PATH-S1 (2026-07-31)

| ID | Item | Verification |
|---|---|---|
| OPS-7 | **The deployable-binary contract** ([ADR-023](adr/ADR-023-deployable-binary-contract.md)): `appximo.ParseServeArgs` (version/serve/fail-loud — the silent flag-discard trap is a hard error), install.sh's behavioral identity check with the contract named in the error, `--cli` installs the engine CLI as the ops companion (`appximo-cli`, symlinked automatically when the binary IS the engine), control-plane port DETECTED from the live service, secrets printed as a PATH (`--show-secrets` to opt in), and `scripts/build-consumer.sh` as the canonical consumer build (SPA + ldflags identity; commerce's build.sh is now a 3-line wrapper resolved from the module graph). The two-artifact truth (serve + operate) documented in PRODUCTION §5 + BACKEND_SPEC. | `cli_test.go` (contract incl. both leftover-arg traps); Parte E of the session: the demo recreated from the runbook alone with the new tooling |
| OPS-8 | **Every verification suite points at ANY instance**: acceptance-test detects the served surface (`/openapi.json` + schema) — generic checks run everywhere (401/deny-by-default/strict-keys over the schema's own first resource, `ADMIN_ROLE` names the broad role), quickstart-contract checks SKIP with a stated reason (a FAIL means broken, never "another app"), and the smoke DELETES the tenants it created; `load.sh --path` for the read arm; `VP_DB_PROBE_PATH` for the chaos DB cases (prev. session); commerce `verify.sh` sweeps its ~5000 BULK products + its own orders. Interface documented in ONE place: verify-production/README §Pointing the verification at ANY instance. | Acceptance vs the tienda: **22 PASS / 0 FAIL / 8 INFO** (was 23/18/2 — every FAIL was spurious); its smoke tenants deleted by the run itself, `tenant list` = tiendita only |
| ENG-8 | **`Config.OnTenantProvisioned`** — the per-tenant twin of BeforeStart, run inside every registration (control plane + /admin, one Service), all-or-nothing on error. Commerce wires it; a fresh tenant's `/api/catalogo` answers 200 with NO restart. | `provision_hook_test.go` (DDL ran after engine tables; failure rolls the whole registration back, id reusable); live on the 58 in Parte E |
| ENG-9 | **Consumer-owned objects are EXTERNAL drift, never proposed drops.** Ownership = "declared by ANY deployed schema version" (schema_history + current json_schema): schema-owned removals stay fully approvable (incl. declined-now-approve-later), never-declared objects are reported as External (preview/outcome/CLI), never approvable (their key → unmatched). Standalone use (no control plane) keeps legacy behavior byte-identical. The rule "never drop drift" is intact. | `external_integration_test.go` (generated column + side table protected even when explicitly "approved"; historical field stays approvable and droppable); dry-run on the 58 shows attr_marca under EXTERNAL, zero destructive (Parte E) |
| ENG-10 | **Custom handlers inherit the honest 503**: `db.IsUnavailable` recognizes raw unclassified causes, and `Ctx.Error` reclassifies a ≥500 with a DB-unavailable cause to 503+Retry-After (message kept; 4xx never overridden). `/readyz` stays process-readiness by design (a DB blip must not amplify into load-balancer ejection — the per-request 503 is the dependency signal). | `ctx_unavailable_test.go` (raw ECONNREFUSED → 503+Retry-After; classified sentinel; 4xx/non-DB-500/nil-cause kept); postgres-stop on the 58: catalogue answers 503, not 500 (Parte E) |
| DOC-1 | **The migrate/restart model is now one truth, half by CODE**: the single-tenant CLI `migrate` PERSISTS the applied schema (+history, +pg_notify) like the PUT/fan-out paths — a new FIELD serves hot; a new RESOURCE still needs the restart. AGENTS.md, MENTAL_MODEL §4, PRODUCTION §5 corrected to the measured model ("write keys follow the tenant's DEPLOYED schema", not "the DB is the source of truth"). | `TestExternal_HistoricalFieldStaysApprovable` exercises PersistTenantSchema; live hot-field check via CLI migrate in Parte E |

## DONE in PROD-JOURNEY-1B (2026-07-31)

| ID | Item | Verification |
|---|---|---|
| — | **The full consumer lifecycle walked on the 58**: wipe (old bare-engine demo inventoried + removed; nestjs-bench harness archived to the 105) → official install of the COMMERCE binary → live-data migration (0 loss, counts verified) → redeploy (6.6 s) → measured downtime (0.58 s @50 rps) → auto-rollback drill (35 s, 30.7 s user-visible) → destructive gate held → 4 regression suites green against prod (28+18+21+16) → 7-case chaos matrix (reboot 38 s, full self-recovery) → **restore drill EXECUTED** (1.8 s, functional verification incl. a new purchase). The tienda is the new public demo: https://tiendita.appitools.com | commerce `docs/GAPS.md` PART THREE (18 findings with numbers); commerce commits 438c39a…4a8bd3a; engine commit for `VP_DB_PROBE_PATH` |
| OPS-S3 | The 58's exposed PostgreSQL password eliminated (purge + fresh secrets, re-rotated after install printed them) | Requires-Miguel table row closed above |

## DONE in LIBRARY-GAPS-S2 (2026-07-31)

| ID | Item | Verification |
|---|---|---|
| ENG-5 | **Static mounts own their CSP** — both forms serve `DefaultStaticCSP` (override `StaticMount.CSP`, disable `CSPOff`); the API keeps `default-src 'none'`; assets-only mounts need no index (1B-2). Includes the browser-suite-forced correction: the default carries `script-src 'unsafe-inline'` because SvelteKit/Next/Astro boot from an inline script — the strict first draft re-created the blank-page bug. | `static_csp_test.go` asserts the DOCUMENT header through a production-faithful router (StrictCSP group included); commerce runs ONE root mount again, Playwright 16/16 |
| ENG-6 | **`Route.Public` optionally authenticated** — absent→anonymous, valid→Claims populated, invalid/expired/foreign-tenant→401 (never a silent downgrade). One shared `resolveClaims` with the enforced path. | `TestJWTMiddleware_PublicOptionalAuth` (5 branches); commerce collapsed its twin checkouts into one `POST /api/checkout`; e2e-1b 28/28 incl. the garbage-token 401 case |
| ENG-7 | **`Ctx.Update` enforces declared state machines** — `codegen.AppendStateTransitionGuard` exported (ONE place transitions become SQL: REST/GraphQL/batch/Ctx), `ExplainTransitionFailureTx` + shared classifier (+ row condition on the explain SELECT — no state leak), `*InvalidTransitionError` (identical 422) / `ErrUpdateConflict` (409). | Integration test: illegal move via Ctx.Update == generated PATCH byte for byte, terminal immutable, self-set no-op; commerce deleted its `opTransitions` table |
| — | **`Config.Host`/`ControlHost`** bind addresses (defaults unchanged); commerce on 127.0.0.1 both planes. Credentials rotated (super-admin pwd + ADMIN_KEY, values only in `.env.dev` 0600). Carried docs committed apart. | `ss -ltnp`: commerce on `127.0.0.1:8099/9099`; bench A/B `no_change` (MWU p=0.064, Δp50 +18µs ≪ max(0.5ms,3%) gate); `make test` green; acceptance 38 PASS / 0 FAIL |

The residue of the per-transition pattern (coarse loss of `update` grant, two
auth surfaces) remains **SCHEMA-3**'s case — still correctly deferred, now
cheap: the handler decides WHO, the engine owns WHAT moves exist.

## DONE in COMMERCE-1B-S1 (2026-07-30)

| ID | Item | Verification |
|---|---|---|
| COMMERCE-5 | **Slice 1-B: the storefront + mobile back-office** — SvelteKit `adapter-static` SPA compiled into the commerce binary (`Config.Static`, same origin), storefront (vitrina → variante → carrito → checkout invitado con dirección+envío → pasarela mock → confirmación pending→paid) + `/panel` (login real `/auth/login`, tablero, productos+variantes CRUD, órdenes con transiciones por rol, envío). All error states handled (loading/vacío/red/422 multi-campo/409 sin-stock/decline). | `scripts/e2e-1b.sh` **27/27** (incl. 10-way concurrent no-oversell, decline, pending→paid, RBAC 401/403 matrix) + Playwright móvil **16/16** with screenshots; regression: `verify.sh` 18/18, `verify-webhook.sh` 21/21, `e2e.sh` full |
| COMMERCE-3 | **Shipping address in the checkout** — `envio_config` resource (retiro + tarifa plana, dueño-editable from `/panel/envio`), checkout takes `envio_metodo` + `direccion` (real `direcciones` row linked via `ordenes.direccion_id`), server computes the fee (never the client), included in the total. | e2e-1b case 2 (envio $12.000 sumado + dirección en la confirmación); zones/carriers stay rebanada 2 |

The frontend field report is **commerce `docs/GAPS.md` PART TWO (1B-1..1B-7)** —
it spawned ENG-5/ENG-6/ENG-7 above and is the input FRONTEND_SPEC/FASE 3 needed.

## DONE in LOOSE-ENDS-SWEEP-S1

| ID | Item | Verification |
|---|---|---|
| ENG-S1 | **`Config.Static`** — serve a frontend from the binary (root or sub-path, opt-in SPA fallback, immutable hashed assets, no tenant tx / RBAC / cache buffering, boot-checked against engine prefixes, traversal impossible by construction) | Unit tests incl. traversal + shadowing; live matrix against `examples/fullstack` (shell + assets + deep link served with no token, `/api` still 401, engine UIs intact) |
| ENG-S2 | **503 instead of 500 when the database cannot serve** (connection failures, SQLSTATE class 08/53, 57P01-03) + `Retry-After` | `pkg/db/unavailable_test.go` — 21 unavailable causes and 11 that must NOT be reclassified (40001/40P01 stay caller errors) |
| OPS-S1 | Stale ufw rules on the 58 (8080, 3000, from the retired NestJS benchmark) closed | `ufw status` now 22/80/443 only; `api.appximo.com` still 200 with a valid certificate; 8080 refused from outside |
| DOC-S1 | False claims corrected in ESTADO_Y_PLAN_MAESTRO (a dead droplet listed as live, "everything pushed" with commits queued, six shipped features listed as missing) | Each verified against reality before editing |
| OPS-S2 | A stale 46 MB binary from 13 Jun (commit `507f846`, predating the whole `/admin` API) sat in `/root/appitools/` on the 58, next to the git checkout — the obvious-looking one to run, and the wrong one | Verified nothing referenced it (systemd, cron, scripts) and that the live process's `/proc/PID/exe` pointed at `/opt/appximo/bin/appximo`; deleted the file only. Service kept the same PID; site still 200 |
| COMMERCE-S1 | **The reservation sweeper is scheduled** — an abandoned cart no longer holds stock forever | Live: a hold expired, swept within one tick, `stock_reservado` 3 → 0, reservation `liberada`, logged |

---

## DONE in HANDOFF-PACKAGE-S1

| ID | Item | Verification |
|---|---|---|
| DOC-S2 | **The handoff package `nuevo_chat_web/`** — the strategic context (role, tone, decisions and their reasoning, phase, operations, research index) captured in versioned files instead of living only in a chat that ages out | 8 package files + 2 maintenance files written, cross-checked against the repo, and read back end-to-end as if by a new architect with no conversation history |
| DOC-S3 | **The permanent pattern** — AGENTS.md §The handoff-package rule makes updating the package (and the backlog) a session obligation, so the context cannot silently rot again | The rule sits next to the open-item rule it mirrors; `_COMO_MANTENER.md` maps change-type → file to touch |
| DOC-S4 | **Stale state claims swept from ESTADO_Y_PLAN_MAESTRO** — the headline "⚠ HAY COLA DE PUSH" was false (`git ls-remote` shows remote `main` == local `HEAD` == `9e1b529`); the memory footprint conflated the 1M-rows-idle figure with the under-load one; the 4-phase strategic plan was missing entirely; the commerce resource count was wrong (12, not 13) | Each corrected figure re-measured or re-read from `docs/BENCHMARKS.md` / the live repo before editing |
