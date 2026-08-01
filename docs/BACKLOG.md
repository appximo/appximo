# Appitools — the open-item register

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

**Last reviewed: 2026-08-01 (SILENT-FAILURE-S1)** — every OPEN item below was
re-verified against reality on that date, not carried forward on trust.

---

## OPEN

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
  completed. A nightly `appitools-backup.timer` (03:30) is installed on the 58
  by hand — the installer still doesn't provide it.
- **Ready (what remains):** `appitools restore` as a first-class engine command
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

### ENG-15 — A keyset cursor silently discards sort/order/page, and `meta.page` lies
- **Origin:** SILENT-FAILURE-S1 audit (`pkg/query/builder.go:232`). With `?after=` or
  `?before=`, the builder drops `?sort`, `?order[…]` and `?page` — and the response's
  `meta.page` still echoes the page number it ignored.
- **Impact:** Medium. The response does not merely omit the caller's intent, it
  ASSERTS something false: a client paginating with a cursor reads `meta.page` and
  believes it is on that page.
- **Ready:** combining a cursor with an incompatible parameter is a `400` naming the
  conflict, or the parameter is honored; either way `meta` never reports a page the
  query did not use.

### ENG-16 — Two `order[…]` parameters: the winner is Go map iteration order
- **Origin:** SILENT-FAILURE-S1 audit (`pkg/query/builder.go:158`). With two
  `order[field]` parameters, the loop takes whichever the map yields first and
  `break`s — measured 174/26 across 200 builds of the SAME URL.
- **Impact:** Medium, and unusual: the same request returns rows in a different
  order between calls. It is non-determinism visible to a client, which is worse
  than a wrong-but-stable answer because it defeats caching and reproduction.
- **Ready:** more than one sort field is either a `400` (one sort field is the
  documented surface) or an ordered multi-sort; never a coin flip. Same for the
  GraphQL `order` argument (`pkg/graphql/handler.go:1425`).

### ENG-17 — A repeated query parameter keeps only the first value
- **Origin:** SILENT-FAILURE-S1 audit (`pkg/query/builder.go:136`). `vals[0]`
  everywhere: `?per_page=20&per_page=100` serves 20, `?filter[x][eq]=a&filter[x][eq]=b`
  filters on `a`, and the aggregate functions do the same.
- **Impact:** Low-medium. The common cause is a client appending a corrected value;
  the engine serves the stale one.
- **Ready:** a repeated parameter is a `400` naming it, per ADR-024.

### ENG-18 — Unknown aggregate functions are never looked at
- **Origin:** SILENT-FAILURE-S1 audit (`pkg/query/aggregate.go:104-124`).
  `?count&median=amount` returns `200` with the metric simply absent; `?count=false`
  turns COUNT **on** (presence-only, and REST disagrees with GraphQL here); an empty
  `group_by` silently changes the response SHAPE from `{"groups":[…]}` to a flat
  object.
- **Impact:** Medium. A dashboard reads a total that is not the one it asked for.
- **Ready:** an unknown function key under the aggregate path is a `400` listing
  `count,sum,avg,min,max,group_by`; `count` takes a boolean value; an empty
  `group_by` is a `400`, not a shape change.

### ENG-19 — A `before_*` webhook hook is validated and never dispatched
- **Origin:** SILENT-FAILURE-S1 audit (`pkg/extensions/hook_runner.go:91`). The
  validator ACCEPTS `hooks.before_create = {type: "webhook", url: …}` and requires
  the URL — and the runner never calls it. This is the exact mirror of SEC-AUDIT-V2
  Hallazgo A, which closed the after-hook half of the same asymmetry (a js/wasm
  after-hook is now rejected at load); the before-half was left open.
- **Impact:** Medium. A schema declares a validation webhook, the schema validates,
  and nothing ever runs — the same "accepted and silent" shape, at the layer people
  reach for when they want a guard rail.
- **Ready:** either reject a `webhook` before-hook at load (naming js/wasm as the
  before-hook types, symmetrical to the after-hook rule), or dispatch it. Rejecting
  is the smaller change and matches the existing decision.

### ENG-20 — An unrecognized `$variable` in a row condition becomes a string literal
- **Origin:** SILENT-FAILURE-S1 audit (`pkg/rbac/evaluator.go:110`). Only `$user_id`
  and `$external_client_id` are substituted; `$userid`, `$user`, `$tenant_id` are
  compared as the LITERAL text `"$userid"`, matching nothing.
- **Impact:** **High, and it is authorization.** One typo produces the exact "the app
  shows zero rows forever" failure SCHEMA-5 exists to warn about — and the warning
  cannot see it, because `warnings.go` only fires on an exact `$user_id`.
- **Ready:** a `val` beginning with `$` that is not a known variable is a LOAD ERROR
  listing the known variables. There is no legitimate reason to compare a column
  against a literal dollar-prefixed string.

### OPS-13 — Nineteen config values fall back silently when they fail to parse
- **Origin:** SILENT-FAILURE-S1 audit + measured live. Booting with
  `RATE_LIMIT_RPS=abc RATE_LIMIT_BURST=oops` logs `rate limiter: 1000 RPS / 100 burst
  per tenant` and never says the operator's values were rejected. The same shape in
  `APPITOOLS_AUTH_MIN_PASSWORD`, `APPITOOLS_CONTROL_PORT`, `APPITOOLS_FILES_MAX_BYTES`,
  `DB_MAX_CONNS`, `APPITOOLS_MAX_TX_OPS`, the fleet's per-app `envInt`, and more.
  `envTruthy` maps ANY unrecognized value to false — including
  `APPITOOLS_AUTH_REQUIRE_VERIFIED`, a security toggle. And there is **no inventory
  of the 60+ `APPITOOLS_*` variables**, so a misspelled one is never read at all.
- **Impact:** Medium-high for operators: the box runs with a configuration nobody
  chose, and the only evidence is a log line stating the default as if it were the
  request.
- **Ready:** one `envInt`/`envBool`/`envDuration` helper that logs
  `WARNING: RATE_LIMIT_RPS="abc" is not a number — using 1000`, plus a boot-time
  inventory that warns on an unknown `APPITOOLS_*` variable (the fleet already has
  the pattern for per-app keys).

### ENG-21 — No write body uses `DisallowUnknownFields`
- **Origin:** SILENT-FAILURE-S1 audit. The engine already uses it in `pkg/userauth`,
  `pkg/platformadmin` and `pkg/fleet` — the discipline exists and was never applied
  to the data plane. Unknown keys in the `/api/transaction` envelope, in an
  operation, and in a `guard` are dropped. Worse, the `422 unknown_field` guarantee
  on CREATE is not a key check at all — it is a side effect of Postgres `42703`, so
  it evaporates for a role with a `fields` allowlist (the key is deleted before the
  DB sees it) and for a drift column the additive migration left behind.
- **Impact:** Medium. This session fixed the operator bodies that carry a safety
  flag; the data-plane bodies are a wider contract change.
- **Ready:** strict decode on the transaction envelope/op/guard, and a real key check
  on CREATE that does not depend on the database's error code.

### ENG-22 — GraphQL drops variables and nested jsonb values
- **Origin:** SILENT-FAILURE-S1 audit. `GET /graphql` never reads the `variables`
  query parameter, so a filtered query returns the UNFILTERED result. A variable
  nested inside a `jsonb` inline literal is written as `null`.
- **Impact:** Medium-high for the GET path (a filter that silently does not apply).
- **Ready:** parse `variables` on GET, or reject a GET carrying it; resolve variables
  inside jsonb literals or reject them.

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
- **Class:** the same unrecognized-input family with the sign flipped — input that is
  [redacted while open]
- **Handling:** it is an exploitable information-disclosure vector, so per the
  session's rule the reproduction was reported directly to the maintainer and is
  deliberately NOT written in this file, in the audit document, or in any commit
  message. Ask Miguel for the detail before working on it.
- **Impact:** High. [redacted while open]
- **Ready:** [redacted while open]
  [redacted while open]
  neighbouring file.

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
  `/health` reports `"version":"dev"` and `appitools version` says
  `commit unknown` — the live binary was built without the ldflags the canonical
  build (`scripts/build-engine.sh`) injects.
- **Impact:** Medium for operations. Nobody can tell from outside WHICH build is
  serving `api.appitools.com`, and the DevHub deploy pipeline's smoke check
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
| **Cloudflare proxy on `api.appitools.com`** | Still proxied (dig → Cloudflare IPs), but since PROD-JOURNEY-1B the 58 no longer serves that domain: Caddy's only site is `tiendita.appitools.com` (direct A record, measured clean), so `api.appitools.com` now dead-ends at the proxy. Decide: retire the DNS entry, point it somewhere real, or leave it dark. |
| **Cut the first release tag** | Still zero tags (`git tag` is empty) and `RELEASE_VERSION=""` at `scripts/install.sh:33`, so the documented "download the binary from GitHub Releases" path cannot work yet, and OPS-4 (version traceability) partly depends on it. |
| ~~**Rotate the 58's PostgreSQL password**~~ | **RESOLVED by PROD-JOURNEY-1B (2026-07-31):** the wipe (`--uninstall --purge`) dropped the role and database; the reinstall generated a fresh password (plus fresh JWT/admin secrets, rotated again on-box after the installer printed them to stdout — see OPS-7). The exposed credential no longer exists. |
| **Publish the Go module (the 10 % path is blocked on it)** | `github.com/miguelangel/appitools` is private with no tag, so `go get` / `go mod tidy` FAIL for anyone building a custom backend: the only recipe that works is a local checkout plus an absolute-path `replace`, which does not build on a teammate's machine, in CI, or in a plain `docker build`. This is now written honestly at the top of `backend-spec` §3.0 (with exactly what changes once it is published), but it is a **product blocker, not a doc gap** — the framework half of the product is unreachable outside this machine until the repo is public or a tagged private module + `GOPRIVATE` is set up. Part of **DOC-2**, which is otherwise DONE. |
| **Give `/root/commerce` a remote** | The technical fix is trivial; the decision (which account, public or private, whether the commerce platform is a product or a demo) is his. See **OPS-5** — until then the field report lives on one disk. |

---

## DONE in SILENT-FAILURE-S1 (2026-08-01)

The session's subject was not `is_null`. It was that **in several layers the engine
accepts an input it does not recognize and continues in silence** — a class with four
production instances behind it. The audit
([UNRECOGNIZED_INPUT_AUDIT](audits/UNRECOGNIZED_INPUT_AUDIT.md)) swept ten input
surfaces; the policy is [ADR-024](adr/ADR-024-unrecognized-input.md).

| ID | What shipped | Verified by |
|---|---|---|
| **ENG-14 + the class** | A pattern now decides what an input **is**, never what is **valid** — validation moved into code that can produce an error. Any parameter under a prefix the engine owns (`filter[`, `order[`) must parse or `400`. Measured live: five spellings of one intent used to give **one 400 and four full-table 200s**; all five now name the offending input and list the alternatives. `?sort=ghost`, `?order[ghost]=`, `?order=descending` (which sorted ASCENDING) all rejected. A test that asserted the old fallback was removing the very contract — rewritten. | `TestBuildQuery_UnrecognizedInputIsAlwaysRejected` (11 cases, each asserting the message NAMES the input) + `…_ValidInputStillWorks` (11 valid shapes unchanged); live probes on a running engine |
| **Two more instances, found by the audit** | A misspelled **`dry_run`** (`dryrun`, `dry-run`) decoded to false and turned a PREVIEW into a real migration — measured live against the control plane. The three bodies carrying that flag now decode strictly. And **`appitools serve <path>` served a different app than the one named** (it booted `./schema.json`), now a clean error pointing at `--schema`. | live control-plane probes; CLI output |
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
| **SCHEMA-5** | `schema.Warnings` — a new, non-blocking layer answering "will this do what you meant?" next to the validator's "may this run?". Its first rule catches a `$user_id` row condition pointed at a **relation** column: valid, deployable, **zero rows forever**, no error at any layer. Surfaced in FIVE places: `appitools validate`, `validate --json` (`warnings[]`, `valid` untouched — so the AI correction loop can act on it), the control-plane/Studio deploy response, engine boot, and `ai-generate`'s report. It is a **warning**, not an error: the pattern is legal when the FK genuinely holds login ids — and applying the suggested fix silences it. | `pkg/schema/warnings_test.go` (5 cases incl. no-false-positives and fix-silences-it); live on the exact generated schema |
| **ENG-12** | The write path validates against the tenant's **DEPLOYED** schema, merged with the boot one (a UNION — a deploy can only ever ADD to what was accepted, so a tenant whose record lags the boot file behaves exactly as before). Compiled once per tenant per `pg_notify` invalidation; a request costs one RWMutex read. A field added by a deploy is now **writable with no restart**. And the other half: a resource that genuinely needs a restart answers `resource_not_loaded` **explaining itself**, never a bare 404 or "unknown field". | Live, designed so it could not self-deceive (the previous claim was verified against a field already in the boot schema): `pets.weight_kg` was asserted ABSENT from the file the process booted with, then `PATCH`ed → 200, same PID |
| **ENG-11** | ONE tenant-id alphabet — `^[a-z][a-z0-9]{1,29}$`, the **intersection** of the Postgres-schema alphabet (no hyphens) and the DNS-label alphabet (no underscores) — in the control plane, Studio and the admin console, with the suggestion helper now producing something the engine actually accepts (`mi-clinica` → `miclinica`, not the `mi_clinica` it used to recommend). `401 token tenant mismatch` now names the host that arrived, the tenant it implies, the tenant the token carries, and the address the token WOULD work at. Creating a tenant through `/admin` warns when the id does not match the domain serving the app — the moment both facts are known. | Live: `vet_journey` refused at registration with the fix suggested; the 401 read in full; `pkg/controlplane/tenant_id_test.go` asserts every suggestion satisfies the rule |
| **DOC-2** | (a) The generation grammar now teaches **state machines** and the **per-resource RBAC form**, plus the identity-vs-foreign-key rule — measured on the original Spanish description, 3 runs per arm: **before 0/3 state machines, 2–3 iterations, ~$0.035; after 3/3, 1–2 iterations, ~$0.016** (richer grammar ⇒ correct AND 55 % cheaper, because it stops needing correction rounds). (b) **"Copy AI context"** in Studio + `GET /editor/ai-context` — `appitools spec` plus this app's schema, one click, so the product's most effective feature stops being undiscoverable. (c) `backend-spec` now OPENS with the real dependency recipe (the local checkout + `replace`, its costs stated) and exactly what changes when the module is published. (d) `Ctx.Get(resource, id)` — the sanctioned lookup-by-id that keeps the row rule, with the doc stating that `QueryOpts.Filters` takes declared fields only. | 3 live generation runs per arm with costs; `pkg/aigen` tests; the module recipe is the honest state, and the publishing decision is Miguel's (see below) |
| **OPS-10** | `install.sh --app=NAME` namespaces **everything**: unit, service user, `/etc`, `/opt`, `/var/lib`, database + role, control port, and a per-app Caddy **site file** (`/etc/caddy/sites/<app>.caddy`) that the main Caddyfile only `import`s — so installing an app APPENDS a site and can never erase a sibling's. Default unchanged (`appitools`), so a single-app box is byte-identical. A second install for a DIFFERENT domain without `--app` now **refuses** and prints the exact side-by-side command. `deploy-update.sh`, `backup.sh` and `--uninstall` take the same flag. | Two apps staged side by side (`--dry-run --root`): separate secrets, database, control port (9090 vs 9183), files dir, unit and site; the guard refused the clobbering run; the idempotent re-run proceeded with app 1's config unchanged |

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
| OPS-7 | **The deployable-binary contract** ([ADR-023](adr/ADR-023-deployable-binary-contract.md)): `appitools.ParseServeArgs` (version/serve/fail-loud — the silent flag-discard trap is a hard error), install.sh's behavioral identity check with the contract named in the error, `--cli` installs the engine CLI as the ops companion (`appitools-cli`, symlinked automatically when the binary IS the engine), control-plane port DETECTED from the live service, secrets printed as a PATH (`--show-secrets` to opt in), and `scripts/build-consumer.sh` as the canonical consumer build (SPA + ldflags identity; commerce's build.sh is now a 3-line wrapper resolved from the module graph). The two-artifact truth (serve + operate) documented in PRODUCTION §5 + BACKEND_SPEC. | `cli_test.go` (contract incl. both leftover-arg traps); Parte E of the session: the demo recreated from the runbook alone with the new tooling |
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
| OPS-S1 | Stale ufw rules on the 58 (8080, 3000, from the retired NestJS benchmark) closed | `ufw status` now 22/80/443 only; `api.appitools.com` still 200 with a valid certificate; 8080 refused from outside |
| DOC-S1 | False claims corrected in ESTADO_Y_PLAN_MAESTRO (a dead droplet listed as live, "everything pushed" with commits queued, six shipped features listed as missing) | Each verified against reality before editing |
| OPS-S2 | A stale 46 MB binary from 13 Jun (commit `507f846`, predating the whole `/admin` API) sat in `/root/appitools/` on the 58, next to the git checkout — the obvious-looking one to run, and the wrong one | Verified nothing referenced it (systemd, cron, scripts) and that the live process's `/proc/PID/exe` pointed at `/opt/appitools/bin/appitools`; deleted the file only. Service kept the same PID; site still 200 |
| COMMERCE-S1 | **The reservation sweeper is scheduled** — an abandoned cart no longer holds stock forever | Live: a hold expired, swept within one tick, `stock_reservado` 3 → 0, reservation `liberada`, logged |

---

## DONE in HANDOFF-PACKAGE-S1

| ID | Item | Verification |
|---|---|---|
| DOC-S2 | **The handoff package `nuevo_chat_web/`** — the strategic context (role, tone, decisions and their reasoning, phase, operations, research index) captured in versioned files instead of living only in a chat that ages out | 8 package files + 2 maintenance files written, cross-checked against the repo, and read back end-to-end as if by a new architect with no conversation history |
| DOC-S3 | **The permanent pattern** — AGENTS.md §The handoff-package rule makes updating the package (and the backlog) a session obligation, so the context cannot silently rot again | The rule sits next to the open-item rule it mirrors; `_COMO_MANTENER.md` maps change-type → file to touch |
| DOC-S4 | **Stale state claims swept from ESTADO_Y_PLAN_MAESTRO** — the headline "⚠ HAY COLA DE PUSH" was false (`git ls-remote` shows remote `main` == local `HEAD` == `9e1b529`); the memory footprint conflated the 1M-rows-idle figure with the under-load one; the 4-phase strategic plan was missing entirely; the commerce resource count was wrong (12, not 13) | Each corrected figure re-measured or re-read from `docs/BENCHMARKS.md` / the live repo before editing |
