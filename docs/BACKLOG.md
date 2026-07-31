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

**Last reviewed: 2026-07-29 (HANDOFF-PACKAGE-S1)** — every OPEN item below was
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




### OPS-9 — bench-protocol.sh assumes a retired dev fixture
- **Origin:** CONSUMER-PATH-S1 (commerce GAPS 4-2), while measuring itself: the
  default `ENDPOINT` is `/api/guides…` (a schema that no longer ships) and the
  minted token uses role `super_admin` (no current example declares it) — the
  same "assumes the bare-engine fixture" class OPS-8 fixed in the verification
  suites, in the DEV measurement path. The `ENDPOINT`/`BENCH_TOKEN` overrides
  exist and carried this session's measurement.
- **Impact:** Low — a papercut for whoever runs `make bench-protocol` on a
  fresh setup (100 % error rate, "no datapoints", twice this session).
- **Ready:** defaults derived from the SERVED schema (first resource +
  first role, like acceptance-test does), or a hard, actionable pre-flight
  failure naming the overrides.

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

### OPS-3 — `golangci-lint` in CI (and the errcheck policy)
- **Origin:** this session. CI runs `make fmt-check`, `go build`, `go vet` and
  the test suites — **not** `golangci-lint`, despite `make lint` existing.
- **Impact:** Low. The full run (installed and executed this session) reports
  **64** findings: **50 errcheck**, almost all `json.NewEncoder(w).Encode(...)`
  on an error response and `defer Close()` in tests — writing an error response
  has no recovery path, which is why the repo already annotates many of them
  `//nolint:errcheck`; plus 12 staticcheck style suggestions (`QF*`). The three
  substantive ones were **fixed this session** (an unused var, an ineffectual
  assignment, an empty branch).
- **Ready:** a `.golangci.yml` that encodes the decision (errcheck exempted for
  response-writing and test cleanup, or the sites annotated), the run under
  10 minutes on CI hardware, and the gate added to `ci.yml`. Not done now because
  annotating 50 sites is churn that would bury this session's diff.

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
| **Give `/root/commerce` a remote** | The technical fix is trivial; the decision (which account, public or private, whether the commerce platform is a product or a demo) is his. See **OPS-5** — until then the field report lives on one disk. |

---

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
