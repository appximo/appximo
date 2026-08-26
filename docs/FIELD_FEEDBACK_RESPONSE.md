# Response to the v0.1.2 field evaluation — finding by finding

**Session:** FIELD-FEEDBACK-S1 (2026-08-07), answering the first real
third-party evaluation of Appximo (FEEDBACK.md: 13 high findings, ~30
medium/low, 18 praises, install→production in <24h on Windows 11 + an Ubuntu
droplet; plus PATRON-BACKOFFICE.md, the generated-back-office recipe). Every
finding below is either **closed in code this session** (with how, and how it
was verified) or **filed in docs/BACKLOG.md** with its evidence. None was
dropped.

The evaluator's cross-cutting diagnosis — *duplicated logic that diverges* —
was treated as the class, not the instances:
[docs/audits/DUPLICATED_RULES_AUDIT.md](audits/DUPLICATED_RULES_AUDIT.md) maps
every rule that lives twice and the mechanism that now keeps each honest.

## The high-priority seven

| # | Finding | Outcome |
|---|---|---|
| 1 | **B1** (+B7, B8) consumer binaries serve /admin//editor broken with a 200 | **CLOSED — ADR-025.** The built SPA assets (~2 MB; +0.59 MB compressed on an 8.2 MB module) are COMMITTED and ship in the module: `go get` + `go build` embeds working /admin and /editor. A binary that somehow embeds none answers the shell routes with an honest **503** naming the missing piece and the fix — never a blank shell. Verified: a consumer binary per backend-spec §3.1, both UIs complete in a real browser (Playwright, zero failed requests), including the /admin first-run bootstrap screen — which is B8's consumer half. The two-process workaround (B7's cause) is dead; the boot-file-vs-record residual is **ENG-36**. The demos that corroborated B1 (tiendita/petfriendly) are redeployed with panel serving. The "one binary = backend + frontend + admin + docs" claim is now **true by construction**. |
| 2 | **ST1** Studio's deploy gate rejects the legal `files` grant | **CLOSED.** The gate's judge is now the engine's own validator (`POST /editor/validate` — the same `schema.Validate` the CLI/boot/deploy APIs run); the client mirror is live-hint UX only. The mirror also learned the virtual store (accept + actions-only), and ST2's four silent-drop paths are fixed — the grant renders in both RBAC forms, labeled, actions-only. Verified at the button: the gym-shaped schema reaches the deploy preview. Your suspicion of a server-side path with the same gap: resolved — the message existed only in the embedded JS bundle; no Go path ever rejected the grant. |
| 3 | **M1** hot-migrated fields don't filter until restart | **CLOSED.** The read path's query validation (filters, sort, search, is_null, aggregates) resolves the tenant's DEPLOYED surface through the **same ENG-12 seam** the write path uses (`codegen.readSurface` — one seam, never two). Verified live on one PID: pre-deploy 400 → hot deploy → filter/sort/search/aggregate all serve. The two surfaces that overclaimed (migrate's closing line, Studio's dialog — your ST3) now name exactly what is hot and what restarts. The binary-diff gate hot-migrates its fixture tenant mid-run (+5 corpus rows) so this stays pinned. |
| 4 | **C1** maxprocs stderr on every invocation | **CLOSED.** automaxprocs moved from a package-level blank import into the library's `applyRuntimeLimits`: only a booting server logs it; `version`, `validate --json` etc. are byte-silent on stderr (PowerShell `$?` is trustworthy); custom binaries gain cgroup awareness for free. |
| 5 | **F1** (+F1-bis) ".env you source" that nothing loads | **CLOSED.** The binary loads a `.env` from the working directory — stdlib parser (no new dependency), the real environment always wins, the F1-bis **BOM is stripped**, CRLF/quotes/`export` tolerated — wired into every CLI subcommand and `ParseServeArgs` (consumer binaries behave identically). The missing-config message now describes what actually happens. `appximo init myapp --env` writes a ready one (F2). |
| 6 | **T2** (+B8) tenant registration + first admin in no printable doc | **CLOSED.** `appximo quickstart` (alias `lifecycle-spec`) prints the OPERATIONS contract: registration as STEP 1 (the schema travels in the body — the piece you had to de-minify a bundle to find), the first-admin bootstrap as STEP 2, where users come from, hot-vs-restart truth, production, the operator traps. `appximo specs` now prints **five** documents. |
| 7 | **W1** POSIX paths → `C:\var\` | **CLOSED** (not live-verified on Windows — see the script below). `pkg/platformpath`: Linux keeps `/var/lib/appximo` byte-identical; Windows resolves `%LOCALAPPDATA%\Appximo`; macOS `~/Library/Application Support/Appximo`. Files dir, obs.db and the fleet data dir all derive from it; the boot log prints the resolved (truthful) paths. |

## The rest, by area

- **D1** — release.yml now also uploads **version-less asset aliases**, so
  `releases/latest/download/appximo-<os>-<arch>[.exe]` becomes a stable URL a
  one-line installer can target. Provable only at the next tag → **OPS-22**
  (which also covers writing install.ps1/get.sh against those URLs).
- **D2** — `checksums.txt` is now **cosign keyless-signed** in the release
  workflow (`checksums.txt.sigstore.json`; identity = the workflow's GitHub
  OIDC token, recorded in Sigstore's public transparency log — the trust
  anchor lives OUTSIDE the release, which was your exact point). The verify
  one-liner is documented in the workflow. Not live-verified until the next
  tag → **OPS-22**.
- **D3 + B2** — the 1.2 GB consumer graph / 78 MB binary: filed as **ENG-37**
  (build-tag/submodule split of the optional backends; it changes the
  consumer contract, so it gets its own ADR).
- **W2** — `appximo gen-secret` (crypto/rand, hex, any platform); every
  config error now points at it before openssl.
- **W3** — QUICKSTART's Windows caveats now carry the PATH/Explorer-cache
  note (+the `WM_SETTINGCHANGE` requirement for any future installer) and
  the curl.exe-over-cmdlets rule your sessions kept re-learning.
- **C2** — plain `validate` now runs BOTH layers (structural → semantic) with
  fixes inline; `validate-schema` remains the structural half alone and its
  help says so. **C3** — `serve --help` names the three env vars (+ the
  .env). **C4** — `explain --lang es|en` is featured in README and the
  operations contract. **C5** — `appximo version --json`.
- **C6** — the lifecycle contract IS `appximo quickstart` (see #6).
- **F3, F4** — kept as-is (your praise); the new messages follow their
  pattern.
- **S1** — partially your probe's shape: the relation-declared case DID warn
  on v0.1.2 as shipped (verified live). The remaining — and most
  AI-generated — shape now warns too: a `$user_id` condition on a **plain
  uuid column whose name derives from a sibling resource**
  (`instructor_id` ↔ `instructores`) raises
  `identity_condition_on_implied_relation`, while `owner_id`/`author_id`
  (the documented CORRECT pattern) stay silent.
- **S2** — the grammar now states that `references`-bearing FKs join
  subroutes/relations by the referenced column — declare the relation, don't
  under-design around the doubt. **S3** — `explain` prints lifecycles in
  FLOW order (BFS from initial; terminals grouped last). **S4** — the
  "deliver ONLY JSON" rule is now conditional on pipeline vs conversation.
- **I1** — the Docker-bypasses-ufw trap is documented in PRODUCTION §9 and
  in the operations contract, and the dev compose now **binds loopback**
  (a bare `9090:9090` publish was that trap aimed at the control plane).
  **I2** — the official compose existed (repo root; you evaluated the loose
  binary, which is exactly why T2/C6 mattered) — it now documents I1 in its
  ports block. **I3** — the DO-image 2375/2376 note is in PRODUCTION §9.
- **T1** — the offending text lived in Studio's deploy modal (a hand-written
  message claiming `_` was legal, over a CORRECT regex); the message now
  comes from the per-cause explainer, and
  `TestTenantIDRuleSingleSource` pins both SPA regex literals to the
  controlplane authority and rejects the stale wording forever. Two stale
  doc/comment copies of the old rule were also fixed. **T3** — a
  tenant-scoped request whose Host names no tenant is a **400 naming the
  host and the fix** at the edge (it used to panic and mask as 500); +2
  corpus rows. **T4/T5** — your retractions, nothing owed. **T6/T7** —
  guarded by the gate corpus.
- **M2** — the local-backend signed URL is now **relative**
  (`/files/signed/<token>`): drops into a same-origin `src` on any port; S3
  stays absolute by necessity. **M3** — no `[backfill]` warning for a
  UNIQUE/FK over columns born nullable in the same plan (your credibility
  argument, verbatim, is in the code comment). **M7** — the spec warns that
  `image` includes SVG and shows the exact-types exclusion. **M8** —
  documented in FILES.md (including the group-by-sha256 rule you flagged);
  the collector is **OPS-21**. **M4/M5/M6/M9/M10** — praised behavior,
  pinned by existing tests + the gate.
- **B3** — SCHEMA_REFERENCE now documents that the DB carries no CHECK and
  what that means for direct SQL. **B4/B5/B6** — praised; unchanged.
- **FE1** — the vite.config snippet carries its import (from
  `@sveltejs/kit/vite`, with the wrong-guess warning). **FE4** —
  `DefaultStaticCSP` allows `blob:` in img-src (your analysis that it
  relaxes nothing is the code comment). **FE5** — see Part F below.
  **FE2/FE3/FE6** — praised; the serving rules are pinned by tests.
- **ST3** — both overclaiming surfaces reworded with the M1 fix.
  **ST4** — the gate's architecture is unchanged; only its judge moved.

## The contract extensions (PATRON-BACKOFFICE §6) and the fourth doc

All four extensions shipped, in both the CLI-printed and served OpenAPI
(one generator):

1. `x-appximo-relation` + **`x-appximo-references`** on every FK property
   (defaulted to `"id"` explicitly — the blind spot closed for the common
   case too).
2. **`x-appximo-initial` / `x-appximo-transitions`** on state-machine fields
   (terminal states present with an empty list).
3. **`x-appximo-file`** (+`x-appximo-accept`/`x-appximo-max-bytes`).
4. **`x-appximo-virtual-resources`** at the document root (+
   `x-appximo-virtual-resource` tags on the /api/files operations).

And the recipe is now the **fourth build doc**: `appximo backoffice-spec`
(docs/BACKOFFICE_SPEC_LLM.md), included in `appximo specs`. The proof that
the manual exception map died: `examples/backoffice-guide/` — a no-build SPA
whose bundle **names no resource** (grep-verified) — was exercised in a real
browser against a live engine: the relation select offered the instructor by
name and sent her `user_id` (the FK saved, no 409), the state select offered
only `programada` on create and only legal moves on edit, the file widget
showed "accepts: image · max 2.0 MiB", a partial 422 painted on its input,
and the `recepcion` role saw Instructores dimmed purely from the 403 probe.

## §13 (the 10-minute path)

Filed as **ENG-38**, and **BUILT the following session
(FIRST-TEN-MINUTES-S1, 2026-08-07)** — as orchestration, exactly as §13
argued ("no hay que construirlo, hay que orquestarlo"):

- **`appximo up`** — the one-command first contact in §13's shape: the single
  question block (Postgres? name?), then Postgres resolved (`DATABASE_URL` or
  a loopback-published Dockerized `postgres:16` whose password is recoverable
  from the container), secrets written to `./.env` (0600, no BOM) AND loaded
  (F1/F1-bis), the tenant registered WITH the schema in the body (T2), the
  first admin bootstrapped (B8), a tenant user for `/app`, a dev token, a
  smoke request through the full chain, and the final card. Idempotent;
  `appximo down` undoes the Docker part; every failure mode names the way
  out; `--json` emits exactly one JSON object (your DX-for-agents rule — the
  `validate --json` pattern applied to the first mile).
- **`/app`** — the generic back-office from your PATRON-BACKOFFICE, embedded:
  one prebuilt no-build bundle, everything derived from `/openapi.json`
  (your §6 extensions plus the standard `default` keyword, published now so
  required-with-default fields are not over-demanded). Browser-verified
  against two schemas, one never seen by the bundle.
- **`appximo new "<idea>"`** — `ai-generate` → `validate --json` → `up`;
  without an API key it prints the §13 prompt for the user's own agent.
- **QUICKSTART.md** rewritten with `up` as act one and the manual path
  preserved as the net; the ten-minute script published with measured
  numbers.

ENG-38 is DONE in docs/BACKLOG.md; the one §13 ingredient deliberately
deferred is `--embedded-pg` (ENG-39, with its dependency-weight reasoning).

## The Windows verification script (for Miguel — these fixes are NOT live-verified)

Run on Windows 11, with the next release's `appximo.exe` in PATH, from an
EMPTY PowerShell 5.1 session in an empty folder:

1. `appximo version` → prints the version; **`$?` must be `True`** and
   nothing red appears (C1: stderr must be empty — before, a maxprocs line
   made every call look failed).
2. `appximo version --json 2>$null | ConvertFrom-Json` → parses (C5).
3. `appximo validate --json missing.json; $LASTEXITCODE` → an error is fine;
   confirm no maxprocs line precedes the output (C1).
4. `appximo gen-secret` → 64 hex chars; `appximo gen-secret --bytes 16` → 32
   (W2).
5. Write a `.env` **with PowerShell 5.1's BOM on purpose**:
   `Set-Content -Encoding utf8 .env "DATABASE_URL=postgres://...`r`nJWT_SECRET=<64 hex>`r`nADMIN_KEY=<32 hex>"`
   then `appximo serve --schema schema.json` with NO variables in the
   session → the boot log must print `.env: loaded 3 variable(s)` and start
   (F1 + F1-bis: the BOM'd FIRST line must load — that was the hours-lost
   failure).
6. In the boot log, the files line must read
   `files: local backend at C:\Users\<you>\AppData\Local\Appximo\files` (W1)
   — and `C:\var\` must NOT be created (check after a request that touches
   files/observability).
7. `appximo quickstart | more` → the operations contract prints (T2/C6).
8. Open `http://<tenant>.localhost:8080/admin` in a browser after
   registering a tenant → the panel loads COMPLETE (B1; on a consumer-built
   binary too, if you build one).

Anything that deviates: open it as an issue quoting the step number —
OPS-20 in docs/BACKLOG.md tracks this verification.

## What your evaluation protected

The 18 praised behaviors (F3/F4, S5/S6, T6/T7, M4-M6/M9/M10, B4-B6,
FE2/FE3/FE6, ST4) were treated as regression contracts: the binary-diff gate
(101→108 paired cases), the full DB-backed lane and the browser
verifications ran against every change in this session, and each reported
DIFF is enumerated and explained in the session report. Nothing you verified
green went un-re-verified.

---

# Response to the SECOND field evaluation — PUBLIC-SURFACE-S1 (2026-08-07)

**Context:** the second report came from an agent working ONLY from the
distributed binary + `appximo specs` — the real path of most users. It built a
complete blog (login, lector/editor roles, cover uploads, a state machine,
24/24 in a mobile-viewport browser) and its verdict was: *"the cliff appears
when someone wants their own frontend."* Every finding below is closed in code
this session (commits `98a7f24…`) or filed in docs/BACKLOG.md. GitHub issues
could not be read from this session (no `gh` credential on the box); this
section is the finding-by-finding answer.

| Finding | Outcome |
|---|---|
| **A. `Config.Static` unreachable from the binary** — later **retracted by the evaluator** (the Go module is public; ~40-line main.go verified, 24/24 in browser): the real problem is DISCOVERABILITY (frontend-spec §10 said "hand over a tarball") | **CLOSED, both halves.** §10 rewritten around the verified `go get` path with your measured costs (≈2m36s cold build, ≈80 MB binary) and the mandatory SPA→binary build order — the give-up paragraph is gone. `appximo init` now emits that main.go FOR you (compilable as generated — pinned by an integration test that builds the generated project). And the no-toolchain case got a first-class path: `appximo serve --schema schema.json --static ./web/build --spa` (+ `APPXIMO_STATIC_{DIR,SPA,CSP}` for systemd/Docker, `up/new --static`, `ParseServeArgs` flags) — same mount validation, same CSP, ONE implementation behind every form. Verified in a real browser: SPA at `/`, hardened CSP, fallback, immutable assets, `/api` auth intact. |
| **A-bis. `static.go` serves a STRICTER CSP than documented** (sha256-pins inline scripts, doesn't fall back to `unsafe-inline`) | **CLOSED.** True — the doc under-declared SEC-2. `DefaultStaticCSP`'s comment and frontend-spec §9 trap 1 now state the served truth: no inline scripts → `script-src 'self'`; inline bootstraps → sha256-pinned with `unsafe-inline` DROPPED; only an unparseable shell keeps the permissive form (logged). Plus the consequence: editing an inline shell script needs a restart. |
| **B. The pure binary cannot serve anything anonymous** — blog/catalogue/landing demand Go | **CLOSED — ADR-026, the session's centerpiece.** `rbac.public` declares per-resource anonymous READS with row conditions + field allowlists, compiled into the ONE existing evaluator as the reserved `$public` role. Read-only enforced at load; `$user_id` in a public condition is a load error; the public rate limiter applies; the response cache never touches anonymous responses; an invalid Bearer stays 401. Your exact scenario — an anonymous blog listing only published articles, hiding drafts and private fields, zero Go — is an integration-test suite AND was re-verified end-to-end in a real browser (pure binary + `--static` SPA + anonymous fetches). `/openapi.json` marks the ops `security: []` + `x-public: true`. Bonus hardening the ask surfaced: **field allowlists now bind filters/sort/search for EVERY role** (the SEC-5 value-oracle over hidden columns is closed engine-wide, 403 `ErrForbiddenField`). §7.5's public-images pattern: grant `"files": {"actions": ["read"]}` publicly. |
| **C. `up` twice says ok and serves the old schema** | **CLOSED.** On a re-run `up` reads the tenant's REGISTERED schema back and reconciles: unchanged → says so (`tenant.schema: "unchanged"`); changed → migrates through the SAME `PUT /tenants/{id}/schema` path `migrate` drives (additive live; destructive drops stay gated, the exact `--approve-drops` command printed; `gated_drops[]` in the JSON card); a failed migration exits non-zero naming the way out. Never `ok: true` over the old schema — pinned by `TestReconcileSchema`. Help text and QUICKSTART now say what `up` actually does. Your `titulo: ""` symptom also died twice over: the starter and warning below. |
| **D. `?include=` ignores `references`** (subroute right, embed null) | **CLOSED.** Exactly the diverging-duplicates class: the embed compiler hardcoded `id`. Both now resolve through one source (`FieldDef.ReferencedColumn()`); fixed for belongs_to/has_many/many_to_many, and GraphQL + nested embeds share the same compiler so all were fixed by construction (aggregation does no embeds; audited). 4 gate corpus rows pin embed/subroute parity. The spec's "works everywhere unchanged" promise is now true. |
| **E1. `permissions` role without a `files` grant → every upload 403** | **CLOSED.** New SCHEMA-5 warning `file_field_without_files_grant` (both RBAC forms, wildcard-aware, exact fix in the message) — in `validate`, `validate --json` (the AI loop's oracle), boot and deploy responses. The grammar (`spec`) now teaches the rule. |
| **E2. required text + empty string → blank records with 201** | **CLOSED.** New warning `required_text_without_min_length` (suppressed when minLength/enum/pattern/format already constrain). The starter schema and its four doc mirrors now declare `minLength: 1` — the product dogfoods its own fix. |
| **F1. English-only engine errors** | **DECIDED: they stay English.** One source of truth beats N half-translated catalogs, and no server locale matches every user of every tenant. What you actually need is now documented: frontend-spec §5.1 carries the complete, closed `rule` → message map (12 rules) with interpolation guidance from `/openapi.json`'s own limits — build the map once. |
| **F2. Stale list after DELETE (aggregate already correct, self-healed in seconds)** | **CLOSED as the invalidate/refresh race.** Not reproduced live (0/60 — the window is ~1 query wide) but confirmed by code reading: an in-flight cache refresh could store a pre-DELETE body AFTER the write's invalidation, pinning it until TTL — precisely your symptom. Fixed structurally: every store captures the tenant's invalidation epoch before running and is dropped if an `Invalidate` landed mid-flight (`TestInvalidateDropsInFlightStore`). |
| **New: Node-on-Windows can't reach a tenant** (`.localhost` ENOTFOUND; `fetch` ignores a hand-set Host → 401) | **CLOSED in docs.** frontend-spec §9 trap 8 now documents the escape that actually works — `node:http` against `127.0.0.1` with the explicit `Host` header — and states plainly that `fetch`/undici silently ignores it. |

**Verification for the whole batch:** lint 0 issues · full lane
(integration+e2e, `-race`, no `-short`) green · binary-diff gate 108/108 SAME
against the pre-session binary on the shared surface + 117/117 on the grown
corpus · ABBA bench p50 Δ−0.2% (**no_change**) · the end-to-end scenario
(pure binary, `--static` SPA, anonymous public reads) verified in Chromium at
a mobile viewport. Filed OPEN: **UI-2** (Studio must author + round-trip
`rbac.public`), **ENG-40** (`explain`/OpenAPI parity for the new surface).

---

# Response to the THIRD field evaluation (VecinGo) — CTX-PARITY-S1, 2026-08-09

**What was built:** a neighbourhood-association platform — 18 resources, 8 state
machines, 3 roles, 13 custom Go handlers, a 13-screen SPA embedded in the
binary, weighted voting with quorum, multi-tenant, deployed with HTTPS onto a
VPS **already serving two apps** — in ~3–3.5 h of effective work. Verdict:
*"as a consumer, I would do it again."*

**What worked, and is now protected by a test so it keeps working:** the
validator as an oracle (2 iterations to `valid:true`; **the 8
`required_field_is_rbac_forced` warnings were real future bugs** — every
resident create would have 422'd in production); `explain`; two-layer RBAC with
`routes` validated at boot (a 13-route × 3-role matrix correct on the first
try); `go get …@v0.1.6` resolving from the public proxy on the first attempt
and the Go project compiling first try; `go:embed` + `Config.Static{SPA:true}`;
the multi-app installer isolating `--app=vecingo` from its neighbours;
`OnTenantProvisioned` keeping the first production PQRS from being a 500; and
the five printable documents — *"the best engine→agent interface I have
consumed: I did not invent a single endpoint."*

| Finding | Answer |
|---|---|
| **A1. `ctx.Insert` does not apply schema `default`s** (rows with `estado` NULL; the next transition fails `invalid transition from ""`), while backend-spec promises validation *"exactly like the generated POST"* | **CLOSED — and the audit found three more.** The promise was the bug: two implementations of one contract had drifted. `codegen.PrepareCreate` is now the SINGLE source both paths call (defaults → declarative rules + value types → state-machine initial states, in the generated POST's exact order), `PrepareUpdate` its PATCH counterpart. Auditing the whole class instead of the instance surfaced that `Ctx.Insert` also skipped the create type check, the initial-state check, **and the create-time RBAC** (below). Full table, including the three differences that are deliberate: [docs/audits/CTX_PARITY_AUDIT.md](audits/CTX_PARITY_AUDIT.md). |
| **A2. Read/write asymmetry on numbers** — rows come back as `int64`, but passing an `int64` to `Insert` fails `must be a number`; you must cast to `float64` | **CLOSED.** The rules and the type check accepted float64 only, reasoning that `encoding/json` produces float64 — true of the HTTP path, false of a Go handler. `schema.AsFloat64` / `schema.IsIntegral` are now the ONE decision about what a number is, shared by both validators: `int`, `int8..64`, `uint*`, `float32/64` and `json.Number` are all accepted, and **your exact `int64` reaches the database** (the HTTP path is the lossy one, at JSON's ~2^53 — that asymmetry is now documented as deliberate, not levelled down). |
| **(unreported, found by the audit) `ctx.Insert` skipped the create-time RBAC** | **CLOSED — security-relevant.** The generated POST forces the row-condition column to the caller and rejects a body claiming another principal. `Ctx.Insert` did neither: an owner-scoped role could create a row with no owner, or attributed to somebody else — **201 through a custom route, 403 through `/api`**. It now calls the same `EnforceCreateRBAC`. This is the argument for auditing the class: the two you hit were the two that happened to be visible. |
| **B. `up` has a fixed HTTP deadline that a remote database defeats** (~119 ms RTT, 18 resources → `context deadline exceeded` twice, *while the DDL completed*) | **CLOSED, and the deeper half first.** A client-side deadline is no longer a verdict: on a timeout `up` asks the control plane what actually landed and continues if the work succeeded — your run failed over an operation that had SUCCEEDED. The deadline is now sized from the schema and the **measured** database RTT (~60 round trips per resource over a 30 s floor; `--provision-timeout` overrides), and a genuine failure names the measured RTT, that nothing was rolled back, and both exits (`up` is idempotent; `migrate` converges over a direct connection with no HTTP deadline). Reproduced with a latency proxy at 400 ms RTT: the old binary exits 1 while the tenant and its 18 tables exist. |
| **C1. `install.sh` restarts the shared PostgreSQL even when the tuning does not change** (~3 s blackout for two neighbouring production apps) | **CLOSED.** The desired config is compared against the file on disk; identical → the file is left alone and PostgreSQL is **not** restarted, and the run says so. Verified on a box running a first app: installing a second left the neighbour's postgres PID (6425 → 6425), `NRestarts` (0) and `ActiveEnterTimestamp` untouched, and a 400-sample continuous health probe across the whole install recorded **zero** non-200 responses. |
| **C2. The port check arrives mid-execution; a failed attempt left an orphan `vecingo.env`** | **CLOSED.** The preflight now covers **every** port the install will bind — the CONTROL port was never checked, and it is precisely the one that collides when a second app arrives (the data port is chosen by the operator; the control port is derived from the app name). A collision dies before a single file is written, naming each busy port and the process holding it. Verified: a third install onto occupied ports named both holders by app name and wrote nothing. |
| **C3. `could not change directory to "/root/..."` noise from `runuser -u postgres`** | **CLOSED at the source** — the probes run from a directory postgres can enter, rather than being muted with `2>/dev/null`, which would also hide real errors. |
| **Process note: "return `ctx.Insert/Update` errors verbatim"** (you wrapped them at first and hid the engine's per-field 422) | **ADOPTED, with emphasis.** backend-spec now carries a callout: `return err` is the right handler code and is better than anything you would write; wrap ONLY to add information the engine could not have, and then merge its `Fields` rather than replacing them. Your own recommendation, in the document that would have prevented it. |
| **New (fresh-agent run): `validate --json` omits `warnings` when empty** | **CLOSED.** "No warnings" and "an engine without the warnings feature" were the same JSON, so a clean report could not be used as a positive signal. The key is always emitted now. |
| **New (fresh-agent run): a schema granting a custom `routes` segment cannot be booted by the stock `up`/`serve` binary** | **FILED as OPS-26** with its design. The boot check is correct (a grant for a route nothing serves should be caught early), but it means one schema file cannot be both `up`-bootable for the first mile AND grant a custom route to a scoped role — the two documented halves of the product meet exactly there. |

**Verification for this batch:** unit lane 0 FAIL · full lane (integration +
e2e + resilience, `-race`, no `-short`) exit 0 · root tagged suite ok · lint 0
issues · binary-diff gate **117/117 SAME** (the generated path is byte-identical
after routing it through the shared core) · ABBA write-path bench base vs new
p50 1.114/1.058 vs 1.031/0.954 ms → Δ −0.093 ms, under the 0.5 ms gate, with an
A↔A control of 0.056 ms → **no_change** · a fresh agent with no repo access
rebuilt your scenario (default + state machine + `int64` through `ctx.Insert`,
then the transition through both `ctx.Update` and the generated PATCH) and
needed **zero workarounds**. Filed OPEN: **ENG-42** (write errors reach a
handler as raw driver errors), **ENG-43** (`Ctx` writes against the boot schema,
not the tenant's deployed one), **OPS-26**.

## Addendum — CTX-CLOSE-S1 (2026-08-09): the three filed items, closed same-day

The previous batch filed ENG-42, ENG-43 and OPS-26 as OPEN. This session closed
all three (plus OPS-25, the Windows verification):

| Item | Answer |
|---|---|
| **ENG-42 — write errors reached a handler as raw driver errors** | **CLOSED.** `ctx.Insert`/`Update` now return the engine's TYPED verdicts, rendered from the SAME SQLSTATE ladder the generated path uses (`handlers.ClassifyWriteError` — one source, four renderers: REST, batch, GraphQL, Ctx): `*UniqueViolationError` (→ the same `409 field "x": value already exists`), `*ValidationError` with `unknown_field` / `file_not_found` (→ the S44 422), `*ForeignKeyConflictError` (→ the safe 409). **`return err` now gives your client the generated endpoint's response byte for byte** — the wrap-it-in-a-generic-message trap you named is structurally unnecessary. backend-spec's callout carries the full table. What deliberately stays raw: the class-22 bad-input codes — in a handler the offending value may be one YOUR code computed, and a 400 blaming your client would point at the wrong party. |
| **ENG-43 — `Ctx` validated against the BOOT schema** | **CLOSED.** Same seam as the generated routes (`codegen.ResolveWriteSurface`, the ENG-12 union — never a second resolution), for Insert/Update/Query/Get/BindResource. Verified live: a field deployed to a RUNNING process (PID identical, no restart) answered 422 `unknown_field` through a custom handler before the deploy, and its declared `max` rule after it. |
| **OPS-26 — one schema file couldn't be both `up`-bootable and grant custom routes** | **CLOSED, as an asymmetry.** The STOCK `serve`/`up` binary now WARNS and boots (the grant is INERT there — it authorizes nothing until the consumer binary registers the route; the warning names role, segment, and that binary). A binary that DOES register routes keeps the fail-closed rejection — your typo still fails the boot with the registered segments listed. One schema file now serves the whole journey. ADR-021 §The stock-binary asymmetry has the reasoning. |
| **OPS-25 — the Windows upgrade path was reasoned, not executed** | **CLOSED as a permanent CI gate.** A `windows-latest` job now runs on every push: native build + platform unit lanes + the `.env` BOM case + `validate --json` stdout purity + the four upgrade scenarios against the real pinned release (idle · under a running `serve` on a real PostgreSQL · locked `.old.exe` · unwritable destination). Writing it found a real defect: the permission error advised `sudo` on Windows — now platform-aware. Not covered (recorded): real Program Files elevation UX under a non-admin user, antivirus locks. |

# Response to the FOURTH field evaluation (atina) — FRENTE-COMERCIAL-S1, 2026-08-26

**What was built:** a multi-client recruiting SaaS — 32 schema resources,
48 custom Go routes, a 30+-screen Svelte 5 SPA embedded in the binary, four
roles with schema-declared public reads, a matching engine, a kanban with
per-phase communications, consent-by-link, scheduled jobs and a mail worker
— in production with HTTPS at [atina.appximo.com](https://atina.appximo.com)
(open today; the counts are from its public `/openapi.json`). Built by an
**external developer, with no direction from us**, from the published
documentation alone; the maintainer confirms this in writing. The builder's
own report — including a phase table that totals ≈3 h of wall-clock work —
is reproduced with that label in [CASE_STUDY_ATINA.md](CASE_STUDY_ATINA.md).

**What worked, in the builder's words:** the validator as an oracle ("three
passes of `validate --json`; every error says the path and the fix"), and it
caught three real future bugs before the first request (a `required` field
the RBAC would force → every candidate `POST` a 422; a missing `minLength` on
a required text; the `files` grant an uploading role needs); the five
printable specs ("nothing to guess"); `install.sh --app` onto a box already
serving other apps; `deploy-update.sh` with automatic rollback; adding a
second brand on a second domain as a tenant + a Caddy site.

The four frictions, each with the answer. The honest headline: **three of
the four are not engine defects** — the documentation already prescribed the
fix, and the builder's agent hit the problem anyway. That is a finding about
*reachability* of the docs, and it is recorded as one.

| Finding | Answer |
|---|---|
| **1. "My own SQL, not the engine."** Rows inserted by hand-written `INSERT` skipped the schema defaults; a `uuid` scanned into a Go `any` is not a `string`. | **Not an engine defect — documented behaviour, now stated where it bites.** `ctx.Insert`/`ctx.Update` apply defaults, declarative rules, type checks, state-machine initial states and create-time RBAC (the CTX-PARITY work of the VecinGo evaluation); raw SQL through `ctx.UnsafeTx()` is *by design* the escape hatch that applies none of them — its name is the audit marker. backend-spec already said "`ctx.Insert` first; raw SQL only for what the schema cannot express"; the `UnsafeTx` callout now also lists what you give up (defaults, rules, governed fields, the row condition, pgx's native `[16]byte` for uuid — scan into `pgtype.UUID` or `string` explicitly). |
| **2. Matching performance with the database across the internet** — row-by-row recalculation was ~1,300 round trips; bulk load + batched `INSERT … ON CONFLICT` made it four queries. | **Not an engine defect — the pattern was already in backend-spec §3.4b** (batch reads with `= ANY($1)`, batch writes with `unnest()`, the N+1 warning added in FRESH-AGENT-GAPS-S1), and the builder's final solution is that pattern. What the report shows is that an agent writing a recompute loop does not open a section titled "batch patterns". Recorded in the backlog as a docs-reachability item (DOC-3): the N+1 warning should sit next to `ctx.Query` in the `Ctx` reference, not only in its own section. |
| **3. Being a good citizen of the public rate limit** — an anonymous portal load fired twelve reads and tripped the public-route limit (5 rps / burst 10 per tenant+IP); a custom `/api/catalogos` route with its own `Route.RateLimit` and a one-hour client cache solved it. | **Working as designed, and already documented on both sides**: backend-spec (`Route.RateLimit`, "the conservative default is right for a registration endpoint and wrong for a catalogue") and frontend-spec trap 5 ("a storefront page firing a dozen requests trips it instantly unless the backend declared a per-route budget"). The builder's fix is the documented one; the engine deliberately does not relax the generated public routes. No change. |
| **4. Seeding accents from Git Bash on Windows** — `curl` sent bytes in the system code page; a Go seeder and a small API repair script fixed the data. | **Not Appximo** (the bytes were wrong before they reached the network), but it is the kind of trap the Windows quick-start should name, since the same shell is the one our Windows instructions assume. Added to QUICKSTART's Windows caveats: send JSON from a file (`curl.exe --data-binary @body.json`) saved as UTF-8 without BOM, or seed from Go/Node — never inline non-ASCII in a Git Bash command line. Not live-verified on Windows (the Windows path is still OPS-20). |

**Verification for this batch:** no engine code changed (docs only:
backend-spec callout, QUICKSTART caveat, this section, the case study), so no
binary-diff gate or bench applies; the atina counts were re-derived from the
live `/openapi.json` and bundle on 2026-08-26 (see the case study's first
table); the portal was opened in a real browser at 1366×900 and 390×844 with
0 console errors.
