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

**Last reviewed: 2026-08-18 (SHOWCASE-TRUTH-S1)** — the honest audit of which
live apps a STRANGER can actually visit and touch (the measured fact that
governs the landing's trust-bar copy: 1 of the 4 claimed systems passes the
full rubric — the table lives in the session report and the handoff package),
plus the four DEMO-SHOWCASE follow-ups: the golden demo set rebuilt to a
believable order mix (the 58-pending-payment residue premise was REFUTED — the
Aug-17 golden dump was already clean; what remained was 7 verification-residue
orders, swept by email criterion), the catalogue trimmed to a coherent 10
(museum scarf + Minsk hoodie out, licenses updated), OPS-5 closed (commerce
pushed to the private remote after a clean full-history secret sweep), and the
Security Scan nightly turned genuinely green (it now boots its own throwaway
engine instead of failing red by design). New OPEN: OPS-28, COMMERCE-8,
COMMERCE-9. Previous review: 2026-08-18 (DEMO-SHOWCASE-S1) — the showcase pass: /app
became a product-grade panel (Spanish/English, mobile-first, dark mode,
consumer theme tokens, generic demo mode), the tiendita got real licensed
photos + the brand palette + the fiscal/coupon surface made visible + a
verified no-persistence demo mode. New OPEN: OPS-27 (crisblogs demo accounts —
third-party box). Previous review: 2026-08-17 (LAUNCH-ASSETS-S1) — the launch material
produced: the 47 s demo (reproducible), the VecinGo case study, the README/site
rebuilt around layer discovery, and the demo box measured + protected for the
peak. New OPEN: ENG-44. Previous review: 2026-08-09 (CTX-PARITY-S1) — the third field report
answered: the Ctx-vs-generated parity CLASS audited (5 divergences, not the 2
reported) and closed from one source; up survives a remote database; the
installer stops disturbing neighbours. New OPEN: ENG-42, ENG-43, OPS-26.
Previous review: 2026-08-08 (INSTALL-PROMPT-S1) — the front door split in
two (install, then build); the update class closed in the product; OPS-24 DONE
(Miguel cut v0.1.5); OPS-25 filed (the Windows upgrade path is unexecuted).
Previous review: 2026-08-08 (LAUNCHPAD-S1) — the front door shipped and
verified with two fresh agents; UI-2 and ENG-40 closed (UI-2 WAS the data-loss
it was filed as); OPS-23, ENG-41 and OPS-24 filed. Previous review: 2026-08-07 (PUBLIC-SURFACE-S1) — the second field report answered: ADR-026 public reads, SEC-5 general closure, include/references, up reconciliation, 2 warnings, the static path; UI-2/ENG-40 filed. Previous review: 2026-08-07 (FIRST-TEN-MINUTES-S1) — ENG-38 (the §13
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

### OPS-27 — crisblogs demo-account buttons fail, and the box is not ours

- **Origin:** LANDING-COMMERCIAL-S1 field note (2026-08-17); investigated by
  DEMO-SHOWCASE-S1 (2026-08-18).
- **What:** the editor/lector demo buttons on `crisblogs.appximo.com` answer
  «Correo o contraseña incorrectos» — the public third-party showcase does not
  let a visitor in. The technical site links the blog (which works, 200); the
  broken part is inside crisblogs' own login.
- **Why this session could NOT fix it:** `crisblogs.appximo.com` resolves to
  **147.182.163.170** — NOT the 58 (tiendita/petfriendly live there; verified
  against the 58's Caddyfile). It is the third-party evaluator's own droplet;
  no SSH key on the dev box opens it and no admin key is on file. Restoring
  the demo users requires whoever operates that droplet (or Miguel asking
  them) — `POST /admin/tenants/{id}/users` with their ADMIN_KEY, or a seed
  re-run.
- **Impact:** a prospect who clicks the demo buttons on a public showcase hits
  a dead end. **SHOWCASE-TRUTH-S1 (2026-08-18) re-verified from a clean
  browser and hardened the finding:** the buttons fill
  `editor@crisblogs.local` / `lector@…` and the server answers 401 for BOTH;
  crisblogs declares NO `rbac.public` block (its /openapi.json carries zero
  `x-public` operations), so there is no anonymous reading path either — a
  stranger cannot see a single article. Audit verdict: **ROTA** as a showcase
  (alive, valid TLS, mobile-clean — but nothing behind the door). One
  leverage fact: the box is the evaluator's (147.182.163.170), but
  `crisblogs.appximo.com` is OUR DNS zone — we can repoint the subdomain
  without touching their machine.
- **The three options (measured, decision is Miguel's — recorded, not taken):**
  (a) **repair** — ask the crisblogs operator to re-seed the two demo users
  (`POST /admin/tenants/{id}/users` with their ADMIN_KEY, or their seed);
  minutes of THEIR time, zero of ours, but stays hostage to a third-party box
  for the landing's credibility. (b) **replace** — stand a fourth app on the
  58 (it holds ~760 rps with two apps; a blog/portal is static-light) and
  repoint the subdomain we already control; ~a session of work, full control,
  but the "built by a third party" credential dilutes unless the case study
  stays linked as the story. (c) **discount it** — the trust bar publishes
  the number without crisblogs until (a) or (b) lands; zero work, honest
  today.
- **Ready when:** the two demo accounts log in from the buttons, or the
  buttons are removed from crisblogs' login, or the subdomain points at a
  replacement we operate. Needs: Miguel's call between (a)/(b)/(c).

### OPS-28 — petfriendly's front door is a raw 401 JSON, and its binary predates `/app`

- **Origin:** SHOWCASE-TRUTH-S1 (2026-08-18), the outside-in audit. The README
  links `https://petfriendly.appximo.com` (the root); a stranger who clicks it
  gets `{"error":"missing token"}` — a raw API 401 as the first impression of
  a "live demo". `/app` ALSO answers 401: the vetapp binary is `appximo
  9ebeaa1` (2026-08-05), which predates the embedded back-office
  (FIRST-TEN-MINUTES-S1, 2026-08-07), so the JWT-skipped `/app` shell simply
  does not exist in that build. What DOES work for a stranger: `/docs` (200,
  the Swagger the technical site links as "Browse its API") and `/editor`
  (200 — full Studio over the production schema, anonymous; deploy still
  gated by super-admin auth). Audit verdict: **VISITABLE PERO NO TOCABLE**.
- **Impact:** two of the three public promise-surfaces (README root link, the
  landing's "una agenda veterinaria… en internet hoy") land on an error body
  or need insider knowledge. It also inflates the trust-bar number if counted
  as "visitable".
- **Options (Miguel's call — it is a live production box):** deploy a current
  engine binary to vetapp (brings `/app`; needs demo-mode config + a
  read-only role in its schema to be TOUCHABLE, the tiendita pattern), or
  re-link the README/site to `/docs` only and stop calling it visitable, or
  give it a static landing at `/` via `APPXIMO_STATIC_DIR`.
- **Ready when:** the URL the public material links answers 200 with
  something a stranger can DO (or the material stops linking a 401).

### OPS-23 — `install.sh` has no `--static` flag for a frontend directory
- **Origin:** LAUNCHPAD-S1, second fresh-agent run. An agent deploying an app
  with its own SPA (served by the stock binary, not a consumer build) has to
  hand-add `APPXIMO_STATIC_DIR=` to `/etc/<app>/<app>.env` after the install,
  because the installer exposes no `--static`/`--spa` flag even though
  `serve` and `ParseServeArgs` both do. It cost a two-minute detour, not a
  wall (the env equivalent IS documented in `serve --help`), but it is the
  one place where the deploy path is narrower than the run path.
- **Impact:** Low-medium. Self-solvable and documented, yet it breaks the
  "the installer prints every name it created; don't re-derive paths" promise
  for the most common frontend case.
- **Ready:** `install.sh --static=<dir> [--spa]` copies the directory under
  `/opt/<app>/web`, writes `APPXIMO_STATIC_DIR`/`APPXIMO_STATIC_SPA` into the
  env file, and the summary names the served root — verified by installing an
  app with a SPA and getting a 200 at `/` with no manual env edit.

### ENG-44 — `/health` and `/healthz` share the per-tenant rate-limit bucket
- **Origin:** LAUNCH-ASSETS-S1, measured on the production demo box. During the
  deliberate 1,200 rps saturation probe (tenant limiter at the default 1000),
  the health canary probing `https://tiendita…/health` was answered **429**
  twice — the health endpoints count against the same per-tenant token bucket
  as data traffic, so under exactly the load where an operator most wants a
  health verdict, the probe reads as failure. Box-local probes (`127.0.0.1`,
  no tenant Host) and the systemd/Caddy checks are unaffected, which is why
  this never surfaced before.
- **Impact:** Low-medium. External uptime monitors pointed at a tenant domain
  will report an outage during a traffic spike that the engine is actually
  shedding correctly (429 ≠ down; recovery was instant when load stopped).
- **Ready:** `/healthz` and `/health` (constant-time, no tenant work) are
  exempted from the per-tenant limiter — or the docs state plainly that
  external monitors must probe a non-tenant host / the box directly. Either
  closes it; the exemption is the better product.

### ENG-41 — the create path validates `required` before RBAC fills the ownership column
- **Origin:** LAUNCHPAD-S1. `ValidateWrite` (required/rules) runs at
  builder.go:561, `EnforceCreateRBAC` (which forces the row-condition column
  to the caller's identity) at :609 — so a column that is BOTH `required` and
  identity-scoped answers 422 "<field> is required" on every create by that
  role, for a value the client was never meant to send.
- **Impact:** Medium, and now WARNED rather than silent: this session shipped
  the SCHEMA-5 warning `required_field_is_rbac_forced`, which names the
  symptom and the fix (drop `required`). The warning closes the discovery
  problem; this item tracks the deeper fix.
- **Why deferred:** reordering the two phases is a real data-path change with
  a non-obvious side effect — EnforceCreateRBAC also DROPS fields outside the
  role's allowlist, so running it first would remove those fields from
  validation, turning today's 422 on a badly-typed non-allowlisted field into
  a silent 201. That trade needs its own session with the binary-diff gate,
  not a tail-end edit.
- **Ready:** either (a) the injection of condition VALUES moves before
  validation while the allowlist DROP stays after it, with the gate showing
  no other behavioral change, or (b) an ADR records that the warning is the
  permanent answer and why.

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

### COMMERCE-8 — The coupon discount does not reduce the IVA base

- **Origin:** SHOWCASE-TRUTH-S1, observed while completing a real checkout as
  a stranger: Ruana $220.000 + IVA $41.800 (19% of the FULL price), coupon
  BIENVENIDA10 −$22.000 → charged $239.800. The discount is subtracted from
  the grand total while the IVA stays computed on the undiscounted base;
  Colombian VAT practice reduces the base by the discount (IVA should be 19%
  of $198.000 = $37.620, total $235.620).
- **Impact:** Low for a demo with mock payments; real for any merchant going
  live — it overstates the tax by the discount × rate.
- **Ready:** the checkout computes IVA per line over the discounted base, the
  invoice (nota) carries the same, and a suite case pins coupon+IVA.

### COMMERCE-9 — Attribute labels render raw snake_case glued to the value

- **Origin:** SHOWCASE-TRUTH-S1, product detail as a stranger: the Sombrero
  Vueltiao detail shows «Hecho_a_manosí» — the attribute key `hecho_a_mano`
  rendered verbatim and concatenated with the value «sí» without a
  separator. Cosmetic, but it is on the storefront a manager inspects.
- **Ready:** attribute keys humanized (`hecho_a_mano` → «Hecho a mano») with
  a visible key/value separation, verified in the browser suite.

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
| ~~**Give `/root/commerce` a remote**~~ | **RESOLVED (2026-08-18):** Miguel created the private repo `miguel09acosta/latiendita`; SHOWCASE-TRUTH-S1 swept the full history for secrets (clean), documented the go.mod `replace`, and pushed. OPS-5 is DONE. |
| ~~**Pick the canonical repo URL**~~ (PHASE3-GUIDE-S1) | **RESOLVED by RENAME-AND-PUBLISH-PREP-S1 (2026-08-04):** everything is **`github.com/appximo/appximo`** — module path, imports, OpenAPI description, specs, badges, site. |
| ~~**Where `site/` lives**~~ (PHASE3-GUIDE-S1) | **RESOLVED by HOUSEKEEPING-S1 (2026-08-05):** GitHub Pages over the repo — https://appximo.github.io/appximo/ is LIVE (gh-pages root; doc links now absolute so they survive Pages). Moving to `appximo.com` later is a DNS + Pages-custom-domain change, nothing structural. |

---

## DONE in SHOWCASE-TRUTH-S1 (2026-08-18, night)

- **The visitability audit (the deliverable).** Outside-in, clean browser,
  zero insider knowledge, screenshots in `/root/shots-audit/` on the 105. The
  measured fact: of the landing's "4 sistemas funcionando hoy en internet",
  **1 passes the full stranger-enters-touches-believes rubric** (tiendita:
  full anonymous purchase — cart → coupon → mock gateway → webhook →
  tracking — plus the read-only demo panel; 0 console errors, 0 horizontal
  scroll at 390×844, load 164–280 ms). petfriendly = VISITABLE PERO NO
  TOCABLE (→ OPS-28), crisblogs = ROTA (→ OPS-27 updated with options),
  VecinGo = NO ENLAZABLE (no published URL; private production for a real
  community — real residents' data by its nature; the case study itself says
  why there are no screenshots). Copy candidates per scenario are in the
  session report; the number that gets published is Miguel's call.
- **OPS-5 CLOSED.** Miguel had already created the private remote
  (`miguel09acosta/latiendita`) with an old head (27e48bc); this session
  swept the FULL history for secrets (34 commits: no .env-like files ever
  committed, the only DSN is a literal `PASS` placeholder, only published
  demo creds + test mocks), documented the go.mod `replace` in the README
  (the Ready criterion), and pushed — remote now at f130182, verified
  private (anonymous 404). `docs/GAPS.md` finally lives off-box.
- **The golden demo set is now believable (Parte B — premise REFUTED, work
  redirected).** The prompt assumed 58 `pendiente_pago` residual orders; the
  live DB had ZERO (the Aug-17 golden dump was already clean). What it did
  have: 7 orders ALL verification residue by email criterion
  (`*@example.com`, `verificacion-*@appximo.com`, session tags hk-/cps1/icc
  — 5 of them identical $77.350 Pañoleta pickups). Swept in one transaction
  (the verify.sh sweep template), then a 13-order believable history seeded
  through the REAL flow (public checkout + signed mock webhook + panel
  transitions — never SQL for the orders themselves): 5 entregada, 1
  cerrada, 2 enviada, 1 preparando, 1 pagada, 2 pendiente_pago, 1 cancelada;
  distinct plausible Colombian customers, varied products/amounts, one
  coupon use, mixed retiro/envío; same-day times spread 09:00–21:36 (one
  cosmetic SQL pass on `created_at` only — the ORD-YYYYMMDD numbers keep the
  day coherent). Golden dump regenerated; a live `demo-reset.service` drill
  restores exactly this state; «Ventas confirmadas» moved $552.260 →
  $2.379.570 deliberately (the old sales WERE the residue — understood, not
  accidental).
- **The catalogue reads as ONE store (Parte C).** PAN-SED (Cooper Hewitt
  museum scarf) and SUD-CAP (Minsk shop hoodie) removed from seed, live DB
  (API DELETE as dueño, after the residue sweep left zero order lines
  referencing them), licenses (4 attributions now, was 5) and
  `/creditos-fotos.md` (200 live, updated). 7 orphan file rows cleaned.
  **Found and fixed: the seed's photo idempotence was FALSE** — the engine
  dedups the blob but every `POST /api/files` mints a new file row, so every
  seed run re-attached and orphaned a row; now it compares the local sha256
  against the attached file's strong ETag and re-runs upload NOTHING
  (verified in dev and prod). Suites: verify 18/18 · e2e-1b 50/50 · browser
  21/21 (B1c re-pointed at the suite's own pre-photo product — it targeted a
  photo-less «Camiseta Básica» that stopped existing by design).
- **The Security Scan nightly is genuinely green (Parte E — premise
  corrected).** The red was NOT govulncheck (CI green on the latest push);
  it was the ZAP nightly failing at "no target, no scan" by design since
  S39. Fix, not suppression: with no explicit target the job now boots a
  THROWAWAY engine in the runner (scratch Postgres service, scratch tenant,
  `scan.localtest.me` Host) and actively scans that — the prod-domain guard
  stays; a push touching the workflow now triggers it, so the green was
  verified IN Actions (run on a91833b). The nightly went from
  red-and-scanning-nothing to green-and-scanning-a-real-engine.

## DONE in DEMO-SHOWCASE-S1 (2026-08-18)

The showcase pass: the tiendita becomes the commercial standard-bearer and the
generic back-office becomes a product every consumer inherits. Engine data
path untouched (binary-diff gate 120/120 SAME).

- **/app grew up** (`pkg/backofficeui`, commit 3e4a13f): Spanish/English chrome
  (browser-derived, persisted toggle; schema vocabulary never translated),
  mobile-first responsive (≤720px: card lists, drawer nav, sheet form; 0
  horizontal scroll at 390×844), light/dark (prefers-color-scheme + persisted
  toggle), consumer theming (`--app-*` tokens; `Config.AppThemeCSS` /
  `APPXIMO_APP_THEME_CSS` → `/app/theme.css`), and a generic DEMO MODE
  (`Config.AppDemoRoles` / `APPXIMO_APP_DEMO_ROLES`: session-overlay simulated
  writes, reload resets, RBAC read-only role as the boundary). Playwright
  18/18 against two schemas incl. one never seen; the five form rules and
  x-appximo readers pinned unchanged. backoffice-spec §10b documents it.
- **The tiendita, credible** (commerce ed48037): 12 REAL product photos
  (Wikimedia Commons, per-image license in `assets/fotos/LICENSES.md`,
  CC BY/BY-SA attribution in the storefront footer), the appximo.com palette
  (carbon-green header, green/gold accents, warm paper, serif display), and
  the hidden system made visible — confirmed-sales card on the dashboard
  (engine aggregate), a Config screen for per-type IVA + coupons, the coupon
  field in the checkout (the backend supported it; the UI never offered it),
  the IVA note on the product detail. verify 18/18 · e2e-1b 50/50 ·
  Playwright móvil+desktop 14/14 · load 119 ms / grid 650 ms local.
- **Demo mode that cannot be vandalized:** schema role `demo` (read-only
  per-resource permissions, no route grants) + a per-session in-memory
  overlay in the panel. Verified BOTH ways: real writes with the demo token →
  403 on POST/PATCH/DELETE/files/transitions; a full browser session touching
  everything → zero non-GET requests left the browser, zero new rows in
  Postgres, overlay coherent across SPA navigation, reload = pristine.
  «Probar el panel» button on the login; discreet permanent notice.
- Also: `minLength:1` on all 17 required strings in the commerce schema
  (closes the SCHEMA-5 empty-string warning); two stray E2E products swept
  from the live vitrina; the seed now attaches photos to all 12 products and
  creates the `BIENVENIDA10` coupon + the `demo@` account.

## DONE in LAUNCH-ASSETS-S1 (2026-08-17)

The session that produced the material the launch ships with — no engine code
touched; the data path is unchanged.

- **The motion demo.** `appximo new "<a real-estate listings portal…>"` recorded
  in one real, unedited take: **46.7 s** from typed command to the running-app
  card (AI schema valid on the FIRST try, stats on screen) plus the graceful
  Ctrl+C. Assets in `docs/demo/`: the raw asciinema cast (timing embedded in the
  format), a 1.3 MB terminal GIF, a browser-tour mp4+GIF (`/app`, `/docs`, the
  Studio ERD, `/admin` — data seeded by the committed `seed.sh`, including two
  real state-machine transitions), six stills, and a README whose whole job is
  reproducibility: the honesty notes (typing effect, idle-cap, why `--port`
  appears) and the exact steps for a third party to re-run and re-time it.
- **Follow-up pass (same session, from Miguel reading the published README):
  the hero was the least usable asset on the page** — the GIF raced and, being
  a GIF, could not be paused; trying restarts it, exactly at the two moments
  that convert. Fixed by giving the recording a real player: asciinema-player
  vendored into `site/` (no CDN) with controls, seek, speed and **selectable,
  copyable terminal text**, verified live (play → Space → the clock stays
  frozen). GitHub sanitizes `<video>` out of READMEs (verified against their
  markdown API), so there the GIF stays — re-rendered at **real speed** (was
  1.25×), idle capped at 3 s, final card held 6 s, same 1.2 MB — wrapped in a
  link to the player, with `asciinema play docs/demo/appximo-new.cast` offered
  as the terminal alternative. The two moments are now named: **0:17** the
  schema validates first try, **0:22** the app is running and verified. That
  surfaced a real imprecision now corrected everywhere: **the app is up at
  0:22**; the 47 s take includes the card dwelling plus the graceful `Ctrl+C`.
  Both clocks are stated wherever the demo is mentioned (the player caps dead
  air, so its bar reads 0:06 / 0:11 — said explicitly rather than left to
  collide with the real clock). Also in the pass: the `/app` screenshot with
  raw UUID columns replaced by a real record open in the form (foreign key
  resolved to the agent's NAME, `status` offering only the transitions its
  state machine allows — better looking and more engine on display), the
  demo's `schema.json` committed for reproduction, and a visual-execution
  pass on `site/` (one shared media frame for player and video, a poster on
  the tour video which was rendering as a blank white box, demo cards evened
  to one height with aligned CTAs, tabular figures, section rhythm, ad-hoc
  inline styles replaced by classes). Structure untouched.
- **The VecinGo case study** (`docs/CASE_STUDY_VECINGO.md`): the third field
  evaluation presented at publication level — why the domain is hard, the
  numbers, where the time went (the declarative 90% in minutes; the custom 10%
  taking ~70% of the ~3–3.5 h, "exactly where the cost should be"), the four
  frictions WITH their closures, the fifth (security) finding the audit added,
  both verbatim verdicts, and why there are no screenshots (private production).
  crisblogs — public, clickable — is the short second case.
- **The README rebuilt for the first impression**: hero = the demo GIF; the
  layer-discovery table + three screenshots of one generated schema through
  `/app`/`/editor`/`/admin`; the two prompts copyable; **who it's NOT for** as a
  prominent section; the numbers as a table where no figure appears without its
  condition; the proof section (3 field evaluations, 4 fresh-agent runs with
  disclosed conditions, 3 live demos, certification + audits); CONTRIBUTING and
  CI named. `site/` updated in kind (hero video + tour video + case-study card)
  and republished to gh-pages; verified in a real browser at 1366×900 and
  390×844 — no horizontal scroll, videos load, the GitHub README renders the
  hero GIF.
- **The demo box sized for the peak, with data.** Measured from an external
  box against production: static path ≥700 rps clean (p95 17.8 ms, 0 errors);
  authenticated DB path ~760 rps clean (p50 2.2 ms, p95 21.6 ms, 0×429);
  overload at 1,200 rps → **honest 429 shedding** (18,328×200 + 3,476×429,
  0 other errors, p50 2.3 ms — no latency collapse) with the neighbour app
  100% green throughout. **Decision, documented: the default limiter
  (1000 rps/tenant) STAYS** — it sits just above the demonstrated clean
  capacity, so raising it would trade clean 429s for latency collapse; the
  public catalogue already carries its own per-route limit override.
  **Anti-vandalism shipped and tested**: anonymous writes to core resources are
  401 (verified); the deliberate public flows reset nightly — a golden dump
  (`/var/backups/appitools/golden-demo.dump`) + `demo-reset.timer` (04:15 UTC,
  after the 03:30 backup) driving the proven `restore.sh`, executed live once:
  restore + ownership + health + row-count verification green, site back in
  seconds. Found while measuring: ENG-44 (health under the tenant bucket),
  filed above.

## DONE in CTX-CLOSE-S1 (2026-08-09)

The session that closed what the parity audit left open — the audit's table is
now fully closed (17/17 rows accounted for) — plus the two operational items
blocking the first mile and the Windows verification.

- **ENG-42 — typed write errors on the library path.** ONE SQLSTATE ladder
  (`handlers.ClassifyWriteError`) compiled into FOUR renderers: REST single-op
  `WriteDBError` (absorbing builder.go's two unique pre-checks), batch
  `dbTxError`, GraphQL `safeDBErr` (absorbing its two unique pre-checks), and
  the new `Ctx` translation returning `*UniqueViolationError` (409, the
  field), `*ValidationError` `unknown_field`/`file_not_found` (S44 422) and
  `*ForeignKeyConflictError` (409, safe message) — a handler that `return err`s
  produces the generated path's response byte for byte. Class-22 bad input and
  missing-tenant deliberately stay RAW on the library path (a handler may have
  computed the value itself; a 400 would blame its client for the handler's
  bug). Every classified code observed in the parity suite's own runs; nothing
  classified from theory. Pinned by
  `TestParity_WriteErrorShapesMatchGenerated` (4 shapes, same payload both
  paths, same status + decoded body); backend-spec's "return them verbatim"
  section now carries the full typed-error table.
- **ENG-43 — `Ctx` resolves the tenant's DEPLOYED surface.** The ENG-12 seam
  exported (`codegen.ResolveWriteSurface`), consumed by
  Insert/Update/Query/Get/BindResource — never a second resolution; the union
  guarantee (lagging tenant ⇒ boot pair) carries over; a deployed-only
  resource stays unknown (routes/GraphQL/docs are boot-compiled). Pinned by
  `TestParity_CtxWritesDeployedSurface` (self-deception-guarded: asserts the
  boot fixture LACKS the field and demands the DECLARED rule in the 422), and
  verified live on the 105: custom handler, live tenant, control-plane deploy,
  PID identical (2499798) before/after — pre-deploy 422 `unknown_field`,
  post-deploy the declared `max` rule and a working write.
- **OPS-26 — the inert grant no longer blocks the first mile.** The asymmetry
  (Miguel's decision): the STOCK binary (zero custom routes registered) warns
  and boots — one actionable warning per grant naming the role, segment,
  INERT, and the consumer binary that activates it; a binary that registers
  routes keeps the fail-closed rejection (ADR-021 §The stock-binary
  asymmetry). Verified live with both binaries and ONE schema file: stock
  warned + booted; the consumer authorized `recepcion` on `/api/checkout`
  (201); the consumer with an unmatched grant refused the boot naming its
  registered segments. Applied on boot AND the `POST /admin/engine/schema`
  deploy path.
- **OPS-25 — Windows verified FOREVER: the `windows` CI job, GREEN after 7
  iterations that each taught something real.** `windows-latest` in ci.yml
  (parallel, real gate, 30-min cap): whole-module native build,
  platform-sensitive unit lanes (cmd, platformpath — %LOCALAPPDATA% —, schema,
  query), the `.env` BOM case, `validate --json` stdout purity, the released
  .exe BOOTING and serving /healthz on Windows, and the upgrade scenarios
  against the real pinned release (v0.1.5): idle
  (download+checksum+rename-aside), the swap under a RUNNING `serve` (real
  PostgreSQL from the runner image; the old process keeps serving from its
  renamed image), a `.old.exe` whose image still RUNS, a hard-locked
  `.old.exe` (no-sharing handle — the antivirus/editor class), and an
  unwritable destination (deny-ACL; actionable permission error). What the
  gate FOUND, run by run: (1) `notWritable` advised `sudo` on Windows — now
  platform-aware ("Run as administrator"); (2) two platform-blind tests
  (0600-permission-bits and the sudo-literal pin); (3) the pinned v0.1.5
  PREDATES `upgrade` (unknown command) — the self-replace scenarios run on the
  dev build until a release ships the command, then UPGRADE_TAG goes
  release→release; (4) the runner kills a step's child processes — the
  running-serve chain must live in one step; (5) **the platform truth,
  including a self-deception the method caught:** with serve genuinely alive,
  a running `.old.exe` DOES block the next upgrade (Windows denies DELETE and
  rename-over on an executing image) with the documented loud actionable
  failure, binary intact, serve untouched — an intermediate iteration had
  "proven" the opposite because the runner had already killed serve between
  steps. Windows-runner craft that stays in the workflow comments: autocrlf
  neutralized BEFORE checkout, and the black-box artifacts (win-gotest.txt +
  scenario.txt/serve logs via nightly.link) because step logs need a token
  this box does not have. **NOT covered, recorded:** a real Program Files ACL
  under a NON-admin user (the runner user is an administrator; the deny-ACL
  simulates the class, not the elevation UX) — reopen from a field report.

Gates: unit lane green · full lane (no `-short`, Docker) green · lint 0 ·
binary-diff gate **120/120 SAME** (3 new corpus rows for the touched write
contracts — the four-renderer consolidation is byte-identical on the generated
surface) · ABBA write-path bench (A-B-B-A, RUNS=5, 20 rps/15 s, :8580) —
see the session report for the verdict.

## DONE in CTX-PARITY-S1 (2026-08-09)

The third field report (VecinGo: 18 resources, 8 state machines, 13 custom Go
handlers, multi-tenant, HTTPS on a box already serving two apps; ~3–3.5 h of
work; verdict "as a consumer, I would do it again") answered end to end.

- **The parity class, audited and closed.** The report named TWO divergences
  between `Ctx.Insert`/`Update` and the generated POST/PATCH. The audit found
  **five**, one of them security-relevant and unreported: `Ctx.Insert` applied
  the field allowlist but NOT the row-condition forcing or the mass-assignment
  block, so an owner-scoped role could create a row with no owner, or attributed
  to another principal — 201 through a custom route, 403 through `/api`. All
  five closed from a SINGLE SOURCE (`codegen.PrepareCreate`/`PrepareUpdate`,
  the pre-existing `EnforceCreateRBAC`, and `schema.AsFloat64`/`IsIntegral`),
  never a third implementation. Full table, including the three differences
  that are DELIBERATE and why: docs/audits/CTX_PARITY_AUDIT.md.
- **A1 defaults:** rows written by a custom handler landed with a NULL status
  and the next transition failed with `invalid transition from ""` — a row
  outside its own declared lifecycle, with no error at write time.
- **A2 numerics:** the rules and the type check accepted float64 only (true of
  JSON, false of a Go handler), so an `int64` READ from the engine could not be
  written back. Now every Go numeric type is accepted, and the caller's exact
  value reaches the database.
- **The anti-drift instrument:** `ctx_parity_integration_test.go` runs the same
  payload through both paths and asserts identical rows, plus the read→write
  round trip and the RBAC-on-create matrix. Red on every case before, green now.
- **backend-spec** stops promising "exactly like the generated POST" and names
  what is shared, what is not, and why — plus, with emphasis, the evaluator's
  own lesson: RETURN the engine's write errors verbatim; wrapping them throws
  away the per-field 422 the engine already computed.
- **`up` against a remote database (B):** a client-side deadline is no longer a
  verdict. On a timeout `up` asks what actually landed and continues if the work
  succeeded; the deadline is sized from the schema and the MEASURED database RTT
  instead of a fixed 30 s (`--provision-timeout` overrides); a genuine failure
  names the RTT, that nothing was rolled back, and both exits. Reproduced with a
  latency proxy: the pre-fix binary exits 1 with `context deadline exceeded`
  while the tenant and its 18 tables exist — a false failure over work that
  succeeded.
- **The installer (C):** PostgreSQL is no longer restarted when its tuning
  needs no change (verified: installing a second app left the neighbour's
  postgres PID, NRestarts and ActiveEnterTimestamp untouched and a 400-sample
  health probe recorded zero non-200s); the port preflight covers the CONTROL
  port too and names who holds each busy port, before writing anything; the
  `runuser -u postgres` cwd noise is fixed at the source.
- **`validate --json` always emits `warnings`**, even empty — a fresh agent
  reported that "no warnings" and "an engine without the warnings feature" were
  the same JSON, so it could not use a clean report as a positive signal.
- **Verified by a fresh agent** with no repo access, rebuilding the reported
  scenario (schema default + state machine + int64 through `ctx.Insert`, then
  the transition through both `ctx.Update` and the generated PATCH): **zero
  workarounds** — no manual `estado`, no float64 cast, no transition table in Go.

Gates: unit lane 0 FAIL · full lane (integration + e2e + resilience, no
`-short`) exit 0 · root tagged suite ok · lint 0 issues · binary-diff gate
**117/117 SAME** (the generated path is byte-identical after the refactor) ·
ABBA write-path bench base vs new p50 1.114/1.058 vs 1.031/0.954 ms → Δ −0.093 ms,
under the 0.5 ms gate, A↔A control 0.056 ms → **no_change**. New OPEN: ENG-42,
ENG-43, OPS-26.

## DONE in INSTALL-PROMPT-S1 (2026-08-08)

The session that split the front door in two, because ONE prompt could not own
both jobs.

- **The problem, found by Miguel using the product:** the master prompt assumed
  a clean machine ("appximo version first — if it prints a version, skip
  this"). Once releases exist, the most common state is the opposite — an OLD
  appximo on the PATH — and an agent handed the build prompt uses it happily,
  then fails on commands that binary lacks, in a way that reads like a typo.
- **`appximo prompt --install`** (docs/INSTALL_PROMPT.md, embedded): owns the
  three starting states (absent / older / already the requested one → say so
  and change nothing) and the three platforms. Resolves the newest tag from
  the /releases/latest redirect (one request, no token, no rate limit),
  downloads through the UNVERSIONED aliases (D1 — verified live: all four
  resolve 200), verifies the checksum (the release's checksums.txt lists both
  the alias and the versioned name for the same bytes), documents the cosign
  verification, and gives Windows the real procedure for a binary that cannot
  be overwritten while running.
- **The master prompt stops installing** and instead VERIFIES with the one test
  an old binary cannot fake (`appximo prompt` must print), stopping and
  pointing at the install prompt when it fails.
- **The update class closed in the PRODUCT:** `version` reports a newer release
  (anonymous GET of a public URL — no telemetry; human runs only, never at
  serve boot, 2 s timeout, silent on failure, off with
  APPXIMO_NO_VERSION_CHECK / --no-check / CI, and never with --json);
  `appximo upgrade` downloads, verifies the checksum and replaces the running
  binary (atomic rename on Unix, rename-aside on Windows — see OPS-25),
  refusing to install without a matching checksum entry and naming the
  privileged command when the destination is unwritable; an unknown command
  now says the binary may simply be too old, deliberately WITHOUT a
  per-command version catalogue that would be wrong in exactly the binaries
  too old to carry it.
- **QUICKSTART §1-bis "Already had Appximo installed?"** — how to see your
  version, how to upgrade, and a table of what a previous attempt left behind
  (.env, schema.json, the Docker Postgres, tenants) with how to clear each.
- **The entry page rebuilt visually** (the second explicit ask): two numbered
  prompt steps, platform tabs, the prompt markdown RENDERED (headings as
  section rules, fenced blocks as highlighted inset panels, backticks as
  chips), and an amber "✏ REPLACE THIS LINE" marker on what the reader must
  edit. A copy button per box copying the exact .md body from a hidden raw
  carrier, verified by reading the clipboard back in a real browser.
- **Verified by a chained fresh-agent run** on a container with a stale v0.1.2
  on the PATH: prompt 1 identified state (b) and updated to v0.1.5 in **41 s**
  with the three-command checklist green; prompt 2 then reached a verified
  running app **7 minutes** later, never trying to install anything itself.
  Its three recorded doubts are closed as prompt lines (the `sudo`-as-root
  case, unreachable-Postgres guidance, the conditional rbac.public row that
  could be silently skipped). Notably, the SCHEMA-5 warning shipped in
  LAUNCHPAD-S1 (`required_field_is_rbac_forced`) caught a real bug in that
  agent's first schema draft.
- **OPS-24 — DONE, by Miguel.** He cut **v0.1.5** (2026-08-08) from commit
  8c9e9b3, so the published binaries now carry `appximo prompt`; verified by
  downloading the release through the `latest` alias, checking its sha256
  against checksums.txt, and running `prompt`. The site's version caveat is
  gone. (`upgrade` and the version check land in the NEXT tag.)

Gates: full lane (unit + integration + e2e + resilience, no `-short`) exit 0 ·
root tagged suite ok · lint 0 issues · binary-diff gate **117/117 SAME, zero
DIFFs** (this session touched the CLI, not the data path) · commerce 1-B suite
50/50 against the local store · the three live demos healthy · CI green · the
page verified on the LIVE URL at both viewports including the clipboard. 105
left clean, disk 76% → 73%.

## DONE in LAUNCHPAD-S1 (2026-08-08)

The session that gave the product a FRONT DOOR and verified it with two
fresh agents, plus the two items PUBLIC-SURFACE-S1 left open.

- **UI-2 — DONE.** The round-trip DID drop the block (confirmed in code and
  live): `cleanRBACPolicy` re-emitted only `roles`, so any export or deploy
  from Studio deleted `rbac.public` — a public site going dark after an
  unrelated deploy — and a roles-less public-only schema lost the whole rbac
  block. Fixed in `transform.ts`; Studio now AUTHORS the block (a pinned
  "Public (anonymous)" entry: read-only actions, literal-only row filter,
  field allowlist, files actions-only, issues mirroring
  `validatePublicBlock`); rename/delete propagation walks `public`; role
  names starting with `$` are refused. Pinned by the editor's first JS test
  harness (vitest, 5 round-trip tests, wired into `make editor-ui`) and
  verified live: canonical byte-equal through the BUILT editor's Code view,
  a real `PUT /admin/tenants/{id}/schema` keeping the block, and the
  anonymous surface still serving only published rows afterwards. (4612a08)
- **ENG-40 — DONE.** `explain` opens the RBAC section with the anonymous
  surface in words (EN/ES), and a public-only schema no longer prints the
  "every request will be denied" warning; `/api/{r}/aggregate` is in
  `/openapi.json` with its own parameter vocabulary (mirroring
  `query.BuildAggregate`), an `AggregateResponse` component, and `x-public`
  inherited when the resource is anonymously readable. (af16cbd)
- **The MASTER PROMPT + `appximo prompt`** — the front door: one paste, one
  question block, zero questions after it, executable checklists, two acts
  (local, then production with HTTPS). Embedded from
  `docs/MASTER_PROMPT.md`, named first in the root help and in `specs`.
  VERIFIED with two fresh agents on two different ideas, neither with repo
  access: a board-game lending library (Act 1 green in 3m28s, production
  HTTPS at ~17 min) and a recipe box (22 min end to end, first-try valid
  schema, zero questions). Both reached a real certificate chain validated
  with `--cacert`, never `-k`. (51453db, v2 in the same commit)
- **`up` hands over the MOST privileged role** — it used to be "admin" by
  name else the ALPHABETICALLY FIRST role, so a {member, staff} schema gave
  the operator `member`: the printed token could not write the app's own
  main resource. Found by fresh agent #1. (7d1d04b)
- **`install.sh` hardening** — umask-proof explicit modes (the 750 root:root
  `/etc/<app>` that leaves a service crash-looping in `activating` with the
  cause one journal line away), `/etc/<app>` in `ReadWritePaths` (which also
  unblocks Studio's one-click restart in production), an install-time
  readability check impersonating the service user, and `--harden` probes
  that tolerate a missing `ss`/`sshd_config` and NAME what is missing
  instead of dying with a bare 127/2 (four paths exercised against the
  shipped function on a minimal image). (f765a5a, 004e1ab)
- **The Docker-image trap closed structurally** — `docs/MASTER_PROMPT.md` is
  `//go:embed`ed and `docs/` is excluded from the build context, so Docker
  Publish died for the FOURTH time on the same mechanism.
  `TestDockerignoreKeepsEmbeddedDocs` now fails the UNIT lane whenever an
  embedded doc has no `!docs/...` re-include, quoting the exact error and
  the line to add; mutation-checked. (4ee8f73)
- **Three frictions from fresh agent #2** (3e2ba3e): the SCHEMA-5 warning
  `required_field_is_rbac_forced` (see ENG-41 for the deeper fix);
  `/app.js` no longer shadowed by the reserved-prefix dot rule, which
  belongs to `/openapi` alone; `backup.sh` namespaces per app when invoked
  with just its env file, and its rotation glob matches what it writes.
- **The entry page rebuilt** — hero = the copyable master prompt with the
  measured 1m53s badge and agent/by-hand tabs, three numbered steps with
  real screenshots, architecture below the fold, and a third live demo:
  `crisblogs.appximo.com`, built end to end by a third party. Published to
  gh-pages and verified on the LIVE URL at 390x844 and 1366x900 (no
  horizontal scroll, no console errors, all 8 images 200). (65fce17)
- **The consumer-binary deploy on a NON-EMPTY box — verified.** A binary
  built from `appximo init` (82 MB, ADR-023 contract) installed as the THIRD
  app on a box already running two others: own service/user/db/port/Caddy
  site, HTTPS with a valid chain, `POST /api/items` → 201, its own embedded
  frontend at `/` → 200, `/admin` `/docs` `/app` → 200 (ADR-025 assets
  travel in the module), and both neighbours untouched (active/enabled,
  health 200). The namespaced backup inference verified in the same run.

Gates: full lane (unit + integration + e2e + resilience, no `-short`) exit 0
· root tagged suite ok · lint 0 issues · binary-diff gate 117 cases = 116
SAME + 1 DIFF, explained: `openapi-served-contract` differs by EXACTLY the
two new `/api/{r}/aggregate` path items and the `AggregateResponse`
component (both OpenAPI documents diffed directly — zero shared paths
changed, zero other top-level keys changed). 105 left clean.

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
