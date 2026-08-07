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

**Last reviewed: 2026-08-07 (PUBLIC-SURFACE-S1)** — the second field report answered: ADR-026 public reads, SEC-5 general closure, include/references, up reconciliation, 2 warnings, the static path; UI-2/ENG-40 filed. Previous review: 2026-08-07 (FIRST-TEN-MINUTES-S1) — ENG-38 (the §13
first-ten-minutes proposal) is DONE: `appximo up`/`down`/`new` + the embedded
`/app` back-office + the QUICKSTART rewrite, all verified (see the DONE block);
ENG-39 filed (`--embedded-pg`, deliberately deferred with its reasoning).
Previous review: 2026-08-07 (FIELD-FEEDBACK-S1) — the first third-party
field evaluation answered end to end: 5 new items filed (ENG-36/37/38,
OPS-21/22), OPS-20 refreshed with the session's Windows fixes, and the
session's DONE block below. Previous review: 2026-08-05 (HOUSEKEEPING-S1) — the post-publication
operational sweep: OPS-19 (repo unification), SCHEMA-6 (`is_null` shipped),
SEC-6 (JWT_SECRET floor enforced) closed and moved to DONE; OPS-17 executed to
the edge of Miguel's DNS (demos redeployed on post-rename binaries, Caddy sites
prepared); gh-pages recreated clean and the site is LIVE on GitHub Pages.
Previous review: 2026-08-02 (PHASE3-GUIDE-S1) — the Phase-3 public-material
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

### UI-2 — Studio must author (and provably round-trip) `rbac.public`
- **Origin:** PUBLIC-SURFACE-S1 (ADR-026) added the `rbac.public` block; the
  visual RBAC editor (UI-F2-S1) predates it and does not author it. Worse
  risk than a missing panel: if the editor RE-EMITS the rbac object from its
  internal model on deploy, an existing public block could be silently
  DROPPED — a public site going dark after an unrelated Studio deploy.
- **Impact:** High if the round-trip drops the key (data-loss class), medium
  otherwise (the JSON/Code view can author it; AUDIT-F1-S1's 100%-parity
  claim is reopened either way).
- **Ready:** (1) a pinned test that a schema WITH `rbac.public` deployed from
  Studio keeps the block byte-equivalent; (2) the Roles editor grows a
  "Public (anonymous)" section faithful to validatePublicBlock (read-only,
  literal-only conditions, existing fields).

### ENG-40 — `explain` and the aggregate endpoint don't know the new surface
- **Origin:** PUBLIC-SURFACE-S1. Two small parity gaps noticed while
  building ADR-026: `appximo explain` renders every ROLE's grants but not
  the `rbac.public` block (an owner reviewing an AI-written schema would not
  see what the whole internet can read — exactly the audience explain
  exists for), and `/api/{r}/aggregate` has never been documented in
  /openapi.json (pre-existing; noticed while marking public reads).
- **Impact:** Medium for explain (the public surface is the one a
  non-programmer most needs read back); low for the OpenAPI aggregate.
- **Ready:** explain prints a "Cualquiera, sin iniciar sesión, puede ver…"
  section per public grant (conditions in words, field list); the aggregate
  path appears in the generated spec with its parameter set.

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

### COMMERCE-7 — Partial refunds (nota crédito parcial)
- **Origin:** PHASE4-FIRST-MILE-S1 (closing COMMERCE-1 surfaced it). The refund
  route always reverses the FULL payment and the credit note reverses every
  line; `pagos.monto_reembolsado_centavos` and the payments layer already
  support partial amounts, but no surface asks for one.
- **Impact:** Medium — a merchant returning ONE item of a three-item order has
  to refund everything. Product scope, not an engine gap.
- **Ready:** the refund route accepts an optional line/amount subset, the nota
  crédito carries only the reversed lines, and stock returns only for those
  lines; suite coverage for the partial path.

### COMMERCE-4 — A real DIAN Proveedor Tecnológico adapter
- **Origin:** `docs/DIAN.md` (the interface exists; the implementation is a stub).
- **Impact:** High to go live in Colombia, zero for the engine.
- **Ready:** one PT implemented behind the existing `Issuer` interface, with
  sandbox credentials, the CUFE returned and stored, and the failure modes mapped
  to the `facturas` state machine.

### OPS-18 — The `neodevtrix/appximo` Docker image: first publish pending; org namespace deferred
- **Origin:** RENAME-AND-PUBLISH-PREP-S1; re-scoped by CI-GREEN-S1 (2026-08-05):
  the `appximo` Docker Hub org was not created (Docker Hub charges for orgs), so
  the image publishes under the personal namespace **`neodevtrix/appximo`** —
  every doc/compose/badge now says so, and the publish secrets are loaded.
- **Impact:** Medium. The quick-start docker path 404s until the first green CI
  on main pushes the image (the README says so inline). The namespace can
  migrate to an `appximo` org later if the project justifies the fee — a rename
  in docker-publish.yml + docs plus a deprecation tag on the old image.
- **Ready:** `docker pull neodevtrix/appximo` works anonymously (green CI on
  main → docker-publish.yml pushes). Reconsider the org when there is revenue
  or a second maintainer.

---

### OPS-20 — The Windows path is written but UNVERIFIED (refreshed by FIELD-FEEDBACK-S1)
- **Origin:** PHASE4-FIRST-MILE-S1; **refreshed 2026-08-07**: the first field
  evaluation RAN Windows 11 and its findings drove real fixes — C1 (no more
  per-invocation maxprocs stderr; PowerShell `$?` is trustworthy), F1/F1-bis
  (the binary loads `.env`, BOM-tolerant), W1 (data under
  `%LOCALAPPDATA%\Appximo`, never `C:\var`), W2 (`appximo gen-secret`), C5
  (`version --json`). All are cross-compiled and unit-tested but **NOT
  LIVE-VERIFIED on Windows** (this project has no Windows machine); the
  numbered verification script for Miguel is in
  docs/FIELD_FEEDBACK_RESPONSE.md §Windows.
- **Impact:** Medium — Windows developers are a real slice of the first mile.
- **Ready:** the numbered script executed on a real Windows machine,
  discrepancies fixed, and QUICKSTART's NOT-VERIFIED marker replaced with the
  verification date.

---

### ENG-36 — Warn when a tenant's stored schema diverges from the boot schema (B7 residual)
- **Origin:** field report B7 (FEEDBACK.md): the tenant record was stale
  relative to the boot schema (v2 without the `routes` grants added later),
  so a Studio deploy — which correctly loads the RECORD — would have quietly
  reverted live RBAC. B1's fix removed the two-process workaround that
  CAUSED the divergence, and the operator rule is documented in `appximo
  quickstart` §5 ("after changing the schema, run migrate — never only edit
  the boot file"), but nothing DETECTS the state.
- **Impact:** Medium. Two sources of truth by design (boot = served surface;
  record = per-tenant migration state); a silent divergence turns the next
  well-intentioned deploy into a revert.
- **Ready:** at boot (and in Studio's deploy target list), a tenant whose
  stored schema hash differs from the boot schema is flagged — a log line +
  a visible marker in the deploy modal ("record differs from the running
  surface — review the preview closely"), with zero hot-path cost.

---

### OPS-21 — `appximo files gc` (orphaned uploads collector — M8)
- **Origin:** field report M8: `POST /api/files` runs before the referencing
  record exists, so abandoned forms strand uploads forever (7 of 10 in the
  evaluation). Behavior + the safe manual sweep are documented in
  docs/FILES.md; no collector exists.
- **Impact:** Low-Medium (disk growth on long-lived apps; confusing inventory).
- **Ready:** `appximo files gc --tenant X --older-than 24h [--dry-run]`
  deletes uploads not referenced by ANY `file`-typed column, grouping by
  `sha256` (content-addressing means the blob may back other uploads — the
  safe unit is "every id sharing the hash is orphaned"), dry-run first,
  per-tenant, tested against attach/detach races.

---

### ENG-37 — The consumer dependency graph is disproportionate (B2 + D3)
- **Origin:** field report B2/D3: `go mod tidy` on a two-endpoint backend
  downloads >1.2 GB (full AWS SDK, gcloud, gRPC, Redis, OTel, MaxMind …) and
  the binary is ~78 MB — almost all of it OPTIONAL-by-config features (S3,
  Redis) every consumer compiles anyway.
- **Impact:** Medium: first contact (15 cold minutes reads as a hang), CI
  cost, image size. Not correctness.
- **Ready:** the optional backends live behind build tags or submodules so the
  default graph is core+pgx; measured module-download and binary-size deltas
  published; ADR for the split (it changes the consumer contract).

---

### OPS-22 — Verify the D1/D2 release hardening at the next tag
- **Origin:** FIELD-FEEDBACK-S1 implemented, in release.yml, the version-less
  asset aliases (`releases/latest/download/appximo-<os>-<arch>` — D1) and the
  keyless cosign signature over checksums.txt (trust anchor in Sigstore's
  transparency log, not in the release — D2). A release workflow can only be
  proven by a release; none has run since.
- **Impact:** High for D2 (supply-chain trust for a binary that runs with DB
  credentials), Low effort.
- **Ready:** the next tag's release carries both asset name families +
  `checksums.txt.sigstore.json`; the documented `cosign verify-blob` line
  passes from a clean machine; then a one-line fetch installer (install.ps1 /
  get.sh) can be written against the stable URLs (the rest of D1).

---

### ENG-39 — `appximo up --embedded-pg` for machines without Docker
- **Origin:** FEEDBACK.md §13 names it as the third Postgres path
  (`DATABASE_URL` / Docker / embedded). FIRST-TEN-MINUTES-S1 shipped the first
  two and deliberately deferred this one: an embedded Postgres means a new
  runtime-download dependency (e.g. fergusstrange/embedded-postgres fetches a
  ~30 MB PG binary at first run), exactly the dependency-weight class the same
  field report flags in ENG-37 — adding it deserves its own measured session
  and Miguel's sign-off on the download-at-runtime posture.
- **Impact:** A no-Docker machine today gets an actionable error naming three
  ways out (install Docker / local PG / hosted PG) — a mitigation, not the §13
  promise. Mostly affects corporate laptops where Docker is banned.
- **Ready:** `appximo up --embedded-pg` boots on a machine with no Docker and
  no Postgres, the downloaded runtime is checksum-verified, and `appximo down`
  knows how to stop it; dependency cost measured and accepted.

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
| **Cloudflare proxy on `api.appitools.com`** | Still dead-ends at the proxy (the 58 has no site for it). HOUSEKEEPING-S1's OPS-17 decision: `api.appximo.com` is deliberately NOT created (the bare-engine demo was retired; petfriendly IS the engine demo) — so the only action left is Miguel retiring the old `api.appitools.com` DNS entry. |
| ~~**Cut the first release tag**~~ | **RESOLVED (2026-08-05): v0.1.1 is out** — 4 platform binaries + checksums on Releases. Same day the working clone verified download+checksum+`version` and set `RELEASE_VERSION="v0.1.1"` in install.sh, enabling the documented no-`--binary` download path. |
| ~~**Rotate the 58's PostgreSQL password**~~ | **RESOLVED by PROD-JOURNEY-1B (2026-07-31):** the wipe (`--uninstall --purge`) dropped the role and database; the reinstall generated a fresh password (plus fresh JWT/admin secrets, rotated again on-box after the installer printed them to stdout — see OPS-7). The exposed credential no longer exists. |
| ~~**Publish the Go module**~~ | **RESOLVED in practice (2026-08-05):** the repo is public and v0.1.1 is tagged, so `go get github.com/appximo/appximo@v0.1.1` fetches from the public proxy — **verified live** in a scratch module. backend-spec §3.0 updated; the framework half of the product is reachable anywhere. DOC-2 fully closed. |
| **Give `/root/commerce` a remote** | The technical fix is trivial; the decision (which account, public or private, whether the commerce platform is a product or a demo) is his. See **OPS-5** — until then the field report lives on one disk. |
| ~~**Pick the canonical repo URL**~~ (PHASE3-GUIDE-S1) | **RESOLVED by RENAME-AND-PUBLISH-PREP-S1 (2026-08-04):** everything is **`github.com/appximo/appximo`** — module path, imports, OpenAPI description, specs, badges, site. |
| ~~**Where `site/` lives**~~ (PHASE3-GUIDE-S1) | **RESOLVED by HOUSEKEEPING-S1 (2026-08-05):** GitHub Pages over the repo — https://appximo.github.io/appximo/ is LIVE (gh-pages root; doc links now absolute so they survive Pages). Moving to `appximo.com` later is a DNS + Pages-custom-domain change, nothing structural. |

---

## DONE in PUBLIC-SURFACE-S1 (2026-08-07)

The second field report (the blog evaluator, working ONLY from the distributed
binary + `appximo specs`), answered end to end. Commits 98a7f24 → HEAD.

- **`rbac.public` — declarative anonymous reads (ADR-026).** A blog/catalogue/
  landing needs no Go: per-resource read grants with row conditions + field
  allowlists, compiled into the ONE evaluator as the reserved `$public` role.
  Read-only at load; literal-only conditions; public rate limiter; never
  cached; invalid Bearer stays 401; `security: []`+`x-public` in the contract.
  Verified by 4 integration tests + 5 gate corpus rows + a real-browser run.
- **SEC-5 closed GENERALLY:** filter/sort/order naming a field outside ANY
  role's allowlist is 403 (`query.ErrForbiddenField`); `?search=` sweeps only
  role-readable text columns. The value-oracle over hidden columns is gone.
- **`?include=` honors `references`** (the D divergence): embed + subroute now
  share `FieldDef.ReferencedColumn()`; GraphQL/nested fixed by construction;
  gate rows pin the parity.
- **`up` reconciles a changed schema.json on re-run** (never `ok: true` over
  the old schema): unchanged says so; changed migrates through the real PUT
  path with drops gated; failure is loud. `tenant.schema`/`gated_drops` in
  the JSON card. (Advances ENG-36's spirit on the `up` path; ENG-36's
  boot-level detection stays OPEN.)
- **Two SCHEMA-5 warnings:** `file_field_without_files_grant` and
  `required_text_without_min_length`, in validate/--json/boot/deploy; grammar
  + canonical schemas updated (starter now declares minLength: 1).
- **The static path from the binary:** `serve --static [urlpath=]dir --spa`,
  `APPXIMO_STATIC_{DIR,SPA,CSP}`, `up/new --static`, ParseServeArgs grows the
  flags, and `appximo init` emits a COMPILABLE one-binary project (main.go +
  web/ + .gitignore; pinned by TestInitProjectCompiles). frontend-spec §10
  rewritten per the evaluator's own retraction: the go-get path with measured
  costs instead of the give-up paragraph; CSP docs now declare the SEC-2
  hash-hardened truth; §9 trap 8 documents the Node-on-Windows Host escape.
- **The stale-read-after-DELETE suspicion:** not reproduced live (0/60) but
  CONFIRMED by code reading as the invalidate/refresh race; closed with a
  per-tenant epoch guard (`TestInvalidateDropsInFlightStore`).
- **Error language decision:** engine messages stay English; `rule` is the
  stable contract; the full rule→message map documented (frontend-spec §5.1).
- Gates: lint 0 issues; full lane (integration+e2e, -race) green; binary-diff
  gate phase 1 (old schema/corpus, old-vs-new binary) 108/108 SAME, phase 2
  (new corpus) 117/117; browser verification PASS (Chromium, mobile
  viewport). ABBA bench (k6 constant 100 rps, 20s runs, warmup discarded,
  scratch engine pair on one DB): A=641/621µs p50, B=655/606µs — Δmean
  −0.2%, inside the A↔A spread (3.2%) and far under the max(0.5ms, 3%)
  gate → **no_change**. New OPEN: UI-2, ENG-40.

## DONE in FIRST-TEN-MINUTES-S1 (2026-08-07)

**ENG-38 — the first-10-minutes path — is DONE**, built as pure orchestration
of existing seams (the session's governing thesis: nothing re-implemented).
**(A) `appximo up`**: one command from an empty directory to a running app —
Postgres via `DATABASE_URL` or Docker (`appximo-pg`, loopback-only, volume,
password recoverable from the container), secrets → `./.env` (0600, loaded),
schema (`--schema` / `./schema.json` / embedded starter), in-process boot
driving the engine's own HTTP seams (control-plane `POST /tenants` with the
schema in the body — T2; `/admin/auth/bootstrap` — B8; tenant user; token
mint), a real smoke request, and the card (URLs incl. `/app`, credentials once,
dev token, a working curl). One question block; idempotent re-runs;
nine failure modes each naming the problem + the way out; `--json` = exactly
one JSON object on stdout (`Config.BannerWriter` + `logging.SetDefaultWriter`
added as the purity seams). `appximo down` (label-guarded, volume kept unless
`--destroy-data`). **(B) `/app`**: `pkg/backofficeui` — the backoffice-guide
pattern embedded (no-build vanilla SPA, always present), everything derived
from `/openapi.json` (+ the standard `default` keyword now published, so
required-with-default fields are not natively over-demanded);
browser-verified 9/9 + 15/15 (Playwright) against the starter AND a
never-seen schema (state machine incl. terminal read-only, relation selects,
multi-field 422 painting, work-preserving 409, RBAC dimming). **(C)
`appximo new`**: ai-generate → validate → up; measured live ×3: 100%
first-try valid, ~$0.008/schema (haiku); without a key it prints the
agent-ready §13 prompt (exit 0). **(D)** QUICKSTART.md rewritten with `up` as
act one, the manual path preserved verbatim as §4 (the truth and the net), the
agent paste-prompt, the executable success checklist, real screenshots, and
the measured minute-marks. **(E)** a fresh agent with only the public docs +
canonical binary re-ran the whole thing timed (see the session report for the
raw table). Verified by: unit lane 44/44, full no-`-short` lane, lint, the
binary-diff gate (every DIFF explained), ABBA bench on the JWT-skip touch.

## DONE in FIELD-FEEDBACK-S1 (2026-08-07)

The first third-party field evaluation (FEEDBACK.md + PATRON-BACKOFFICE.md,
appximo v0.1.2, Windows 11 + Ubuntu droplet, install→production <24h) answered
finding by finding — the traceable response is
[docs/FIELD_FEEDBACK_RESPONSE.md](FIELD_FEEDBACK_RESPONSE.md). Closed in code:
**B1** (ADR-025 — the built SPA assets ship in the module; consumer binaries
serve /admin+/editor complete, browser-verified; honest 503 when absent),
**ST1/ST2** (Studio's deploy gate defers to the engine's own validator; the
files grant renders in both RBAC forms), **M1** (hot-migrated columns
filter/sort/search/aggregate without restart — the ENG-12 seam reused),
**C1/F1/W1/W2/C5** (Windows first mile: clean stderr, real .env+BOM, platform
paths, gen-secret, version --json), **T2/C6/B8** (`appximo quickstart` — the
operations contract, printable; specs → five docs), **T3** (no-tenant host =
named 400, was masked 500), **T1** (tenant-rule single source + pin test),
**FE5/Part F** (x-appximo-references/file/transitions/virtual-resources in the
OpenAPI), **Part G** (`appximo backoffice-spec` + examples/backoffice-guide,
zero hardcoded domain knowledge, browser-verified), plus the medium/low sweep
(S1-S4, C2-C4, F2, M2/M3/M7/M8-doc, B3-doc, FE1/FE4, I1/I2/I3, W3, D1/D2).
Filed above: ENG-36, ENG-37, ENG-38, OPS-21, OPS-22; OPS-20 refreshed.

## DONE in PHASE4-FIRST-MILE-S1 (2026-08-05)

| What shipped | Verified by |
|---|---|
| **The /admin dead end is gone** — the login screen detects first-run (`GET /admin/auth/status`, unauth; constant `true` after bootstrap) and offers "Create the first admin": ADMIN_KEY + email + password → `POST /admin/auth/bootstrap` (X-Admin-Key-gated, permanently closed once any admin exists → 409; weak password → 422 naming the minimum), auto-signs-in; the CLI path (`appximo admin create`) stays and the screen names it | integration test (status→403 no key→403 wrong key→201+token→status true→409→422 weak); live probes 6/6; Playwright: first-run screen renders, bootstrap through the UI signs in, 0 console errors |
| **`serve` says it stays in the foreground** — after the bind (ENG-34 order kept): second-terminal hint + docs//admin//editor URLs | live boot probe |
| **Signup 403 names both ways out** (APPXIMO_AUTH_SIGNUP_ROLE, or admin-created users via /admin → Users) | live probe; binary-diff gate row |
| **Missing required configuration reported ALL at once** — DATABASE_URL/JWT_SECRET/ADMIN_KEY each with what it's for + how to generate a value (`openssl rand …`) | live probe with zero env |
| **English-first CLI** — ~91 Spanish strings translated (18 `Short:`s, ai-generate's whole report + flags, migrate/token `Long:`s, init/generate output, fleet Shorts, Prometheus HELP, "Capa 3" logs); `--help` now fully English | `--help` probe; grep for residual Spanish 0 (example prompts deliberately kept) |
| **`appximo explain <schema> [--lang en\|es]`** — the read-back step of the authoring loop: a VALID schema rendered as owner-readable prose (resources, field rules in words, state machines as sentences with terminal states, relations, per-role grants incl. row conditions); deterministic, never guessed; invalid schemas are refused with the validator's errors | live probes over quickstart/state-machine/rbac-per-resource examples in both languages |
| **Windows release asset** — `windows/amd64` .exe added to the release matrix (CGO-free cross-compile); unix-only `syscall.Kill`/`syscall.Exec` isolated behind build tags (fleet supervisor + engine self-restart degrade explicitly on Windows) | `GOOS=windows go build ./cmd/appximo` green; unix build + full lane unchanged |
| **docs/QUICKSTART.md** — the two-track first mile (manual + AI-agent per step), install→schema→live API→first user→custom Go→frontend→production HTTPS→migrate→backup, with real Playwright screenshots, per-step "you should see"/"if it fails", the Windows path marked NOT VERIFIED, and honest (next release) markers for post-v0.1.1 behavior; cold-read by a second agent (10 findings, all fixed) | Linux path executed end-to-end against the v0.1.1 RELEASE binary (install→validate→serve→tenant+schema→token→POST/GET→/docs→migrate dry-run/apply); README + GUIDE link it |
| **COMMERCE-1 — credit notes** (was OPEN above): refund creates the nota crédito row in the SAME tx as the reversal, linked via `factura_origen_id`/`cufe_origen`, enqueues `nota_credito.emitir` through the same outbox+Issuer path (new `EmitCreditNote`; stub records intent, never fabricates a CUFE); idempotent on the note row; worker consumes both fiscal topics in one process | verify-webhook.sh section 8 (5 new checks) 26/26; e2e-1b 50/50; worker run: nota processed via stub |
| **COMMERCE-2 — per-category VAT** (was OPEN above): `tipos_producto.regimen_iva` (gravado/exento/excluido) + `tarifa_iva_pct`; per-line rate+tax stored on `orden_lineas`; checkout sums per line; catalogue reports the effective rate; SPA estimates per line; invoice worker reads the stored rate; `/api/conciliacion` adds `iva_por_tarifa` | e2e-1b 7b (exempt: catalogue 0, checkout IVA 0, line 0/0, gravado still 19%); reconciliation probe |
| **GAPS 4-1 flake closed** — verify.sh's GIN EXPLAIN retries once after re-ANALYZE (the documented cheap fix) | verify.sh 18/18 twice |

Binary-diff gate: **101 cases, 98 SAME / 3 DIFF — all three the intentional
Part-A features** (the signup-403 message; the two new bootstrap routes, which
the base binary answered 404). No data-path semantics changed; hot path
untouched (no bench required — the admin/auth/boot surfaces are off it).

## DONE in HOUSEKEEPING-S1 (2026-08-05)

**OPS-17 CLOSED same day (second half):** Miguel created the A records
(`tiendita.appximo.com` / `petfriendly.appximo.com` → 162.243.64.58, direct)
and DELETED the old `*.appitools.com` records. dig verified both new domains
resolve straight to the 58; the prepared Caddy blocks were applied
(`systemctl reload caddy`, zero-downtime) and both domains serve HTTPS with
fresh Let's Encrypt certs (issued 2026-08-05, `CN = tiendita.appximo.com` /
`petfriendly.appximo.com`). **The planned 301 redirects were NOT applied — a
deliberate reversal:** with the old DNS records gone the old hostnames are
unreachable, so a redirect block would never serve and would only make Caddy
retry ACME issuance for unresolvable names (guaranteed failures against Let's
Encrypt); the old site blocks were removed entirely instead. Demo links in
README, site/ (repo + the published gh-pages copy) and GUIDE.md flipped to the
new domains. Verified end-to-end ON the new domains: a real purchase
(pendiente_pago → pagada/aprobado via the signed webhook) and the vet app's
CRUD+RBAC matrix.

The post-publication operational sweep: the two product decisions Miguel took
(`is_null`, the JWT-secret floor), the repo-flow unification, the demo redeploy
to post-rename binaries, and a clean gh-pages with the site LIVE on Pages.

| What shipped | Verified by |
|---|---|
| **SCHEMA-6 — `is_null` filter operator** — `?filter[field][is_null]=true|false` → `IS NULL`/`IS NOT NULL` on every nullable column; values exactly `true|1|false|0` (ENG-23 vocabulary); the implicit `id` / a `required` field → named 400 (a filter that can never match is refused); `json`/`jsonb` gain explicit operator entries (rejections now list the allowed set); GraphQL parity (`is_null: Boolean` on String/Date/RangeFilter + `NullFilter` for uuid/bool/file/json/jsonb — previously unfilterable) incl. the SDL generator; aggregation inherits via the shared `BuildQuery` core (the ENG-14 lesson); OpenAPI advertises it on nullable fields only. ADR-022 Decision 5 RESOLVED; AGENTS/SCHEMA_REFERENCE/FRONTEND_SPEC/CAPABILITIES/GUIDE updated | unit tests per type + vocabulary + never-null rejections + arg-index contiguity; live probe REST+GraphQL+aggregate 6/6; binary-diff gate 98 cases: 88 SAME + 10 DIFF, every one the feature landing (new-op 400s → feature responses; allowed-set lists grew; OpenAPI advertises the op), explained in the session report; full lane green |
| **SEC-6 — JWT_SECRET floor enforced** — `appximo.New` refuses a secret under `MinJWTSecretLen=32`, naming the variable, the received length and the fix (`openssl rand -hex 32`); one seam covers serve, custom binaries and every fleet app; `appximo token` warns on a short `--secret`; docs (PRODUCTION/README/AGENTS) say "enforced" | boot with a 5-char secret refuses with the actionable message (live probe); 32-char boundary test; test-helper secret lengthened; full lane green |
| **Repo flow unified (the maintainer's OPS-19)** — the public repo IS the working repo now; the internal handoff package moved to its own private repo (reachable via an untracked symlink from the working clone, history preserved via `git filter-repo`); the pre-filter full-history clone retired to a read-only archive; dev tooling defaults updated | build + `make test` + lint green from the unified repo; commit+push flow exercised (this session's pushes); devhub restarted from the new path, :3099 healthy |
| **Demos on post-rename binaries (OPS-17, the executable half)** — tiendita redeployed via `deploy-update.sh` (commerce ca57a48 on the appximo framework), petfriendly on engine 9ebeaa1; env files carry both prefixes during the transition; ops CLI updated; Caddy site blocks for `*.appximo.com` prepared with the activation runbook + 301 templates (DNS is Miguel's — see OPS-17 above) | live e2e purchase (pendiente_pago → pagada/aprobado via signed webhook) on the 58; vet CRUD+RBAC live (row-scoping, mass-assignment 403, delete-not-granted 403); the 4 local commerce suites 43/43+18/18+21/21+21/21 |
| **gh-pages clean + the site LIVE** — orphan-rooted gh-pages (the old branch's `data.js` carried unfiltered-history commit messages and was deliberately NOT republished); `site/` published at the branch root (`.nojekyll`, doc links absolute to the GitHub blob); the Benchmark workflow publishes `dev/bench/` into it | https://appximo.github.io/appximo/ answers 200 (Pages was already enabled); Playwright over the published structure: 390×844 + desktop, 0 h-scroll, 5/5 images, 0 console errors; `dev/bench/data.js` audited — only public-history commits |
| **ABBA bench (hot path untouched in practice)** — four arms (base,new,new,base) × 10 runs on the canonical benchblank fixture | all four MWU group comparisons `no_change` under the max(0.5ms,3%) gate; controls A-A 1.6% / B-B 0.25%; effect 4.7–6.1% — inside the 105's 8.7–12% between-run CV floor |

## DONE in CI-GREEN-S1 (2026-08-05)

CI went green (all 4 jobs + Benchmark) across 4 pushes. The register keeps the
honest trail: two of the fixes were real defects the green pipeline surfaced.

| What shipped | Verified by |
|---|---|
| **govulncheck clean** — x/text v0.39.0 (GO-2026-5970), grpc v1.82.1 (GO-2026-6061), x/net v0.56.0 (GO-2026-5942), otel v1.44.0 (GO-2026-5158), toolchain go1.25.12 (stdlib GO-2026-5856/4970). GO-2026-5932 (x/crypto/openpgp, no fix upstream): zero imports anywhere — module-level only, unreachable | binary-diff gate pre↔post deps **92/92 SAME**; make test 0 FAIL; make test-all EXIT 0; CI govulncheck green |
| **Workflows modernized** — golangci-lint-action v9 pinning the local v2.12.2 (golangci-lint v2 removed `--timeout`; the old args would fail), checkout/setup-go/upload-artifact v7, docker actions v4/v7; benchmark.yml skips its store step with a notice while the gh-pages branch does not exist; release.yml builds the embedded SPAs before cross-compiling (a release binary otherwise serves empty /editor and /admin) | CI lint green; Benchmark green; release build verified (version stamp + arm64/darwin cross-compiles) |
| **trimGoPath fix** — go1.25.12 exposes a testing.tRunner frame; stdlib paths under a hostedtoolcache GOROOT stayed absolute (no /pkg//cmd//internal marker). Anchors `/src/`; also stops server filesystem paths reaching observability stack output for stdlib frames | pkg/observability tests green; gate **92/92 SAME**; CI full suite green next run |
| **.dockerignore re-includes docs/FRONTEND_SPEC_LLM.md** — the embed postdated the file's last edit; the first image publish died on it | full docker build end to end; container smoke green |
| **Image → neodevtrix/appximo** (personal namespace; OPS-18 records the org migration path) | docker-publish triggered on green CI |

**v0.1.0 cannot produce a release**: the tag's commit carries the pre-fix
workflows and a re-run reuses them. The tag stays; the release is **v0.1.1**
on green main.

## DONE in RENAME-AND-PUBLISH-PREP-S1 (2026-08-04)

The product renamed **Appitools → Appximo** (a name collision with Applitools —
same developer-tools space, one letter apart — plus a taken GitHub handle; the
full reasoning lives in the maintainer's internal decision record). This public
repository is the filtered result: internal planning material and
infrastructure identifiers were removed from the entire history before
publication, and the history's commit messages are otherwise preserved.

| What shipped | Verified by |
|---|---|
| **The rename, end to end** — module `github.com/appximo/appximo`, `cmd/appximo{,-worker}`, CLI/binary `appximo`, env prefix `APPXIMO_*`, `$schema` → `https://appximo.com/schema/v1` (the value was never validated — no behavior change), meta-schema `appximo.schema.json`, image name `appximo/appximo`, installer unit `appximo.service` (+ migration note for pre-rename installs, PRODUCTION.md §3), specs/GUIDE/README/site/scripts/CI | binary-diff gate pre↔post rename: **92 cases, 91 SAME + 1 DIFF explained** (OpenAPI `info.description` — the rename itself; byte-diffed, zero structural change); `make test` 0 FAIL; `make test-all` EXIT 0; lint 0 |
| **The gate's own HEAD flake fixed** — a HEAD case's body file IS a header dump (curl --head), compared raw → the random X-Trace-Id flagged DIFF even between identical binaries; now normalized as headers | control run base-vs-base: **92/92 SAME** |
| **Public-repo files** — SECURITY.md, CODE_OF_CONDUCT.md, LICENSE/NOTICE copyright named | filtered repo: build OK, `make test` OK, `make test-all` EXIT 0, lint 0 |

New OPEN above: **OPS-17** (demo-domain DNS migration), **OPS-18** (image
publication under `neodevtrix/appximo`). Resolved: the canonical-URL decision (everything is
`github.com/appximo/appximo`).

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

**`is_null` was NOT added in that session** — it shipped later (HOUSEKEEPING-S1, 2026-08-05; see DONE above).

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
