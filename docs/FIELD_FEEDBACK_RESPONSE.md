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
