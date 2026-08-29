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

**Last reviewed: 2026-08-29 (CENTINELA-C-S1)** — Module C of the Centinela package BUILT (the engine observes its own resources out of band and attributes the bottleneck with eight ranked deterministic verdicts, each provoked and verified; `/admin` → Resources; the overhead stated on allocs/op + CPU-seconds + RSS with the p99 as an upper bound — ADR-030) and two spec promises corrected (A-53 the personal-data default, A-54 the unmeasurable "< 1 % of p99"); new OPEN: OPS-35 (the tail test in `compare-groups`). Before that: **DOC-VITRINA-S1 (2026-08-28, 5th session)** — the technical
site and `docs/` synchronized with the released **v0.1.13**: the browser tour
re-recorded on the current `/app` (87.6 s, real time, ES+EN subtitles; the
2026-08-17 tour ARCHIVED, not deleted), every capture of the pre-redesign
panel / the "Appitools" brand re-taken (site + QUICKSTART + README), the six
sessions since APP-VITRINA told where an HN visitor reads them (site
§"What changed", GUIDE §4/§9, CAPABILITIES, README) with the known limits
written (ENG-50, 1 MiB, no `COPY`, `?include=` whole, OFFSET, the guard
degrades), `?fields=` with its measured row, stale numbers fixed (92→163-case
corpus, ~22→~41 MB image, v0.1.1→v0.1.13, `is_null` "does not exist" gone,
1.58→1.60 ms), the migration report answered point by point in PUBLIC
(FIELD_FEEDBACK_RESPONSE §5) and the evaluation count kept at four under
A-25 pending one written confirmation. Every claim re-verified with
requests against the v0.1.13 binary. Before that: **MIGRACION-CONFIANZA-S1** — a real
migration's nine findings, closed by DAMAGE order: `install.sh` reproduced
(the summary's own "Update" line kept another app's schema → `GET
/api/asambleas → 200` on the wrong domain) and now VERIFIES installed == asked
under a written upgrade criterion (three real paths in LXD); the validator's
identity check reworded as a question that never asks for a rename (rule
written); `json` canonicalization documented with the verified values, the
exact door and the parity recipe; `/api/transaction` proven present in every
published version and PUBLISHED in `/openapi.json` (the hole was the
contract); RAM+swap warning in the installer; the minimal host memory guard
(`MemAvailable+SwapFree`) as degradation, never capacity; the six
migration-front findings registered as MIG-FRONT for Miguel's decision.
Gate 149 SAME + 1 DIFF (the contract). Before that: **APP-PODER-S1** — the embedded `/app` uses what
the contract already allowed, still derived from `/openapi.json` alone: honest
page-numbered paging with the engine's exact COUNT («Página 3 de 47 · 15 de
703»), the engine's query time on screen (a new `Server-Timing` header on
every generated read — a small engine change), a detail with parents and
children resolved from the FKs (no `?include=`, so legacy non-JSON text
cannot break it), a JSON editor that warns the 1 MiB cap and the ENG-50
precision loss before sending, columns + saved views + the view in the URL,
CSV, batched bulk actions with named partial failure, and an API-search
relation selector past 100 rows. Gate 150 SAME, ABBA `no_change` ×8 (the
read crossings +5–8 % with a same-binary drift of ±2 % — not resolvable
above host noise, said as such), four schemas in a browser desktop + 390×844,
both demos deployed with rollback drilled. New OPEN: ENG-51. Before that: an external report on
v0.1.9 (`POST {"data": {"nit":"900"}}` on a `json` field → 500; the escaped
string → 201; the GET returns the escaped string) AUDITED request by request
against HEAD, v0.1.9 and v0.1.8 before any fix (docs/audits/
JSON_TYPE_AUDIT_S1.md): identical in the three, as old as the type, the
driver (pgx cannot bind a Go map into TEXT), no read parity anywhere, and a
collateral class defect — six client 422s opened the query breaker and every
write of the process answered 503 for 8 s. The design decision that had never
been written was taken (ADR-028): a `json` field holds a JSON VALUE on every
door and every HTTP read; the breaker counts only unavailability (ENG-49).
Gate 150 = 141 SAME + 9 DIFF (all the feature), full lane + integration + e2e
+ resilience green, ABBA `no_change` ×8 (PATCH and read protocols), 1-B
suites 23/26/50/21, four schemas in a browser, both demos deployed with the
report's case reproduced live before (500) and after (201 + native GET),
rollback drilled both ways. New OPEN: ENG-50 (number fidelity beyond float64,
engine-wide). Before that: the class of fields
the client must never write, AUDITED request by request through every write
door against HEAD and the published v0.1.8/v0.1.9 (docs/audits/
AUTHZ_WRITE_AUDIT_S1.md), then closed as ONE policy: the identity column of
an owner-scoped role is server-owned on UPDATE as it was on create — the
row give-away (ENG-45 #1, exploitable in every published version) is a 403
at every door (ADR-027); a state field is never null (named 422, was 500);
ENG-47 gets its valve with the default untouched. Gate 142 = 133 SAME + 9
DIFF (all the fix), ABBA `no_change` on the PATCH protocol, both demos
deployed with rollback drilled, the attack reproduced from outside and
blocked. New OPEN: ENG-48 (the admin login throttle, same knob-less shape),
RBAC-2 (the field allowlist drops silently — a documented contract the audit
flags as the "hidden attempt" class). Previous review: 2026-08-26 (TIENDITA-VITRINA-S1) — the two public demos
made to show what they SELL: a persistent two-mode control («Así lo ven sus
clientes» / «Así lo ve usted») on tiendita.appximo.com and petfriendly's
portada, the storefront ported to the atina design system with a
photo-independent treatment for the products with no admissible photo, and
CSS-only motion. No engine code (commerce rebuilt against the SAME engine
commit the box runs, `dec6614`, via `-modfile` + worktree). New OPEN: ENG-47
(the login limiter has no env knob, and a public demo shares ONE identity),
OPS-33 (an env value with spaces must be quoted — it broke the nightly golden
reset), COMMERCE-11 (the six product photos weigh 1.55 MB). Previous review: 2026-08-26 (REDISENO-VISUAL-S2)** — appximo.com, conjuntos
and caso REBUILT on the atina system with the brand decisions D1–D6 (internal
A-37): Inter only, ink/white/zinc surfaces alternating per section, green
as the only accent, the D3 graphic components built (58.2 % quorum ring,
bars with numbers, initials, chips, stat blocks), the hero as a treated grid
of real captures, the A-27 hero copy restored and the AI chip out. No engine
code; 58 untouched; nothing new opened. Previous review: 2026-08-26 (REDISENO-VISUAL-S1) — the visual execution
of both commercial pages and the technical site rebuilt on the atina design
system (tokens by variable, bundled Fraunces+Inter under the same CSP, the
semantic-class layer, reveal/count-up/tilt, the two-level proof hierarchy:
cases first, demos as the invitation; ERP card out; GSAP replaced by CSS
under the same three motion contracts; no external host on any property).
No engine code; the 58 untouched (demos re-verified e2e in the browser).
Nothing new opened; decision A-36 in the internal package. Previous
review: 2026-08-26 (FRENTE-COMERCIAL-S1) — a commercial-front
session (no engine code): indexation infrastructure on both properties
(robots/sitemap/canonical/JSON-LD — there was NO noindex; there was nothing
at all), the third-party testimonial removed from the commercial pages under
the "show me" rule, the stats band reduced to what survives a click, every
`wa.me` CTA in first person with its own origin, the return bar live on both
demos of the 58 (rollback drilled on each), atina as the fourth external
evaluation (counts verified in its public `/openapi.json`, findings answered
point by point, case study + design guide in docs/), and ONE segment page
(conjuntos). New OPEN: OPS-31 (Google-account steps — Miguel), OPS-32 (no
lead-capture endpoint for the subordinate path), ENG-46 (`/app` cannot carry
a consumer banner), DOC-3 (batch-pattern reachability in backend-spec). New
decisions for Miguel in the table below. Previous review: 2026-08-21 (WRITE-ASYMMETRY-S1) — ENG-45 family 1 (the
POST/PATCH asymmetry over engine-governed fields) closed as a class: `id` +
`auto` fields now answer the same 422 `read_only` at EVERY write door from ONE
source (`schema.GovernedFieldViolations`), the import use case became a
declared, role-enumerated schema contract (`"import"`, doctrine C-DOCTRINA-3),
Ctx.Update's id/auto gap closed with it, and the bench-fixture restore runs
through the declared path (proven live on nimbus). The matrix audit surfaced
ONE new family (owner-scoped roles can reassign their rows on update — create
forbids the same attribution), recorded below with its disposition, not
opened. This was the last engine session before launch; ENG-45 is the map of
what follows AFTER publishing. Previous review: 2026-08-21 (SILENT-CORRUPTION-S1) — the two ENG-45 families
that corrupt data silently over a validator-approved schema, closed as classes:
the `auto` timestamp role is now DECLARED (`"create"`/`"update"`, any field
name — `modificado_en` works; legacy `true` keeps the literal-`updated_at`
magic and warns on update-intent names), with ONE refresh source consumed by
REST/batch/Ctx.Update; and the relation-subroute collision (`customer` +
`customer_id` → per-boot-random winner) is a load error from a single
derivation, with the whole map-iteration-order class swept (sorted validation
errors, deterministic 400s, `id` reserved, GraphQL self-shadow warned).
ENG-45 re-prioritized by damage (silent corruption > non-determinism > loud
failure > friction), each family with a written disposition; OPS-30 stays
deferred untouched. Previous review: 2026-08-21 (FRESH-AGENT-GAPS-S1) — the four fresh-agent
gaps closed as CLASSES: `created_at` is a load-time SCHEMA-5 warning (never
implicit magic — doctrine C-DOCTRINA / decision, validator not engine
autofill); the implicit-requirement class was AUDITED (8-reader sweep +
adversarial verify, 27 findings, 22 silent) and a boot-panic member fixed at
load (a GraphQL type-name collision like `categoria`+`categorias` now errors
at validate instead of crashing at startup, from a single naming source);
`?include=` on an undeclared relation now says HOW; the CLI token warns on a
missing `--user-id` AND an empty `--tenant` (a cross-tenant wildcard — OPS-30);
`Ctx.MintToken` gives a custom registration endpoint the auto-login the engine
already does; `backend-spec` gained the batch/unnest section + the N+1 warning
where the loop is written (example verified live); `frontend-spec` §11 is now a
mandatory visual-verification PROCEDURE (the mobile layout gate) that
`backoffice-spec` references as its single source. New OPEN: OPS-30, ENG-45
(the audited inventory). Previous review: 2026-08-19 (PRELAUNCH-TRUTH-S1) — the last "show me"
claims closed before launch: the evaluator claim rewritten to what the
linkable material shows (three independent field EVALUATIONS, one driven by
the evaluator's agent — never "three developers"; the counting rule is now
decision A-25), every VPS price without a run behind it replaced by "a cheap
VPS" or the measured $16/mo (site meta, GUIDE, QUICKSTART, the embedded
LIFECYCLE spec, BENCHMARKS), the site's stale "current release v0.1.5" and
"25/25 checks" fixed, and OPS-29 advanced (cause narrowed to a ~1 s create
failure, release.yml made idempotent, `appximo upgrade` proven from a real
v0.1.6 install, Docker badge semver-sorted — the re-run of run 32055036588
is the owner's). Before that, SHOWHN-MATERIAL-S1 — the Show HN launch
material written and every claim in it verified live (titles, author's first
comment, objection FAQ — in the internal repo's `SHOWHN_MATERIAL.md`; launch
date is Miguel's). Before that, LINKABLE-TRUTH-S1 — the distance between what
the public material promises and what survives a skeptical click, closed:
COMMERCE-8 DONE (the coupon reduces the taxable base — ONE computation in
checkout.go, every other surface reads stored values, 5 arithmetic cases
pinned in verify.sh section E, verified on-screen in production), COMMERCE-9
DONE (labels humanized; category chips show accented names), OPS-28 DONE
(petfriendly runs the current engine, has a minimal portada, a published-creds
demo panel over the verified /app demo mode, a plausible clinic dataset, and
passes the full stranger rubric — verdict VITRINA), the golden demo set no
longer ages (redate-demo.sh re-anchors every reset; vetapp got its own
nightly re-anchor), the photo catalogue passed a NEW brand/face filter
(Lacoste polo out entirely; Levi's jacket, Ugmonk tee, portrait dress and
fair-stall ruana photos out — ruana replaced by a vetted one; real brand
names in attrs replaced by fictional ones), and every dead crisblogs link is
swept from the public material (the story stays). New OPEN: COMMERCE-10.
Previous review: 2026-08-18 (SHOWCASE-TRUTH-S1) — the honest audit of which
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

### MIG-FRONT — The "receive a system that already exists" front (six findings from a real migration, a PRODUCT decision pending Miguel)

**Origin:** MIGRACION-CONFIANZA-S1 (2026-08-28). An external agent migrated a
real system to Appximo v0.1.10 — Symfony 7.2 / API Platform, 23 tables,
46,119 tax declarations, 1.2 GB — in five sessions and wrote a nine-point
report (internal: `FEEDBACK_APPXIMO_MIGRACION-RT.md`). Every finding came from
ONE path the engine had never exercised: **receiving an existing system**.
Building new apps (atina, VecinGo, the vitrinas) never touches it. Three of
the nine were closed in that session (the installer, the validator false
positive, the `/api/transaction` documentation — DONE table below); the six
below are **registered, not built**: opening this front is a product
decision, not a sprint. Each carries the report's evidence and a one-line
reading of whose problem it is (engine / documentation / environment).

| # | Finding (report's severity) | Evidence from the report | Reading |
|---|---|---|---|
| **#1 Bulk import** (blocking) | "The only write path is one row per request; 46,119 declarations are 46,119 HTTP requests." Half of it was FALSE — `/api/transaction` exists in every version, 100 ops/request, measured 100 creates ≈ 50–70 ms on the dev box — and is now published in `/openapi.json` and backend-spec §2b. What remains true: no `COPY`, no file importer, no streaming door. 46k rows = ~460 batches = minutes. | **Documentation first, engine second.** The cost of a real import with batches is minutes; a `COPY`-class door is worth building only if a customer's volume makes minutes unacceptable. Decision: Miguel. |
| **#2 Memory backpressure** (blocking, "the most expensive") | Postgres on a 957 MiB box shared by five apps died `oom-kill` during the load; the engine "kept accepting writes until the kernel killed the database". The migrator's own correction: the box had NO SWAP; with 2 GB of swap the same load was absorbed. | **Environmental, with an engine seam.** The engine did not cause the OOM and no engine change makes 957 MiB hold five apps + a firehose — that must never be promised. The seam that WAS the engine's — no notion of host pressure — is closed minimally (D-ter: `APPXIMO_MEMORY_GUARD_MIN_MB`, writes 503 under `MemAvailable+SwapFree`), and the installer warns about swap. What stays open: a host-derived pool/concurrency limit instead of the fixed 10 connections (`DB_MAX_CONNS`) — an engine item, low priority until a second box shows it. |
| **#4 Historical timestamps** (high for migrations) | `created_at`/`updated_at` declared `auto` reject the value with 422; 4,715 users and every declaration got the migration date. The agent identified `import` (WRITE-ASYMMETRY-S1) as the legitimate door and did not use it because it changes the schema. | **Documentation.** The door exists by design (`"import": {"roles": [...], "fields": ["id","created_at"]}` — create-only, role-named, greppable) — it is the DECLARED way to bring history in, and the report reads as if it did not exist. Put it in backend-spec §2b and the lifecycle spec's migration checklist; no engine work unless a migration needs update-side history (which `import` refuses on purpose). |
| **#5 Heavy fields in lists** (medium, performance) — **DONE in MOTOR-FIELDS-S1 (2026-08-28)** | `GET /api/declarations` returns the whole `data` column per row: ~940 KB per page of 20, p99 3,821 ms. | **Built:** `?fields=a,b` pushed down to the SQL `SELECT` on every row-returning read (list/get/subroute/include root/admin browse/`ctx.Query`) and GraphQL's selection set pushed the same way (ADR-029). Unknown → 400 naming it; hidden by the allowlist → omitted (the read contract). Measured on the rebuilt case (46,119 × 52 KB): page 961 KB / 53 ms → 3 KB / 1.2 ms; 10 rps p99 2.8 s → 175 ms (BENCHMARKS §4b). The `/app` sends it. The default-omission alternative is registered as SCHEMA-8 (a declaration, not a flipped default). |
| **#6 `Ctx` limits for custom routes** (medium) | (a) no response-header setter → no `ETag` from a handler, and the response cache skips custom routes; (b) no event subscription to invalidate an in-memory cache; (c) a `Public` route leaves the caller anonymous so `ctx.Query` is `forbidden` unless `rbac.public` is declared, which also opens the generated endpoints → they used `UnsafeTx` with hand-enumerated columns. | **Engine, three small seams.** (a) `Ctx.SetHeader`/`Ctx.ETag` + opt-in caching for custom GETs; (b) an in-process `OnChange(resource, fn)` fed by the same post-commit fan-out SSE uses; (c) a `Route.AsRole` (run `ctx.Query` as a NAMED read-only role for a public handler) — this one is the clean answer to "public catalogue endpoint without opening `rbac.public`". Each is a session with the parity table and the gate; none blocks a migration. |
| **#8 Schema expressivity** (low) | Partial unique indexes (`UNIQUE (code) WHERE form_year IS NULL`), composite primary keys (`PRIMARY KEY (form_id, field_id)`), `rate_limit` not in the schema grammar. | **Decided / documented.** Partial indexes are CLOSED by ADR-022 D2 (structured form reopens as SCHEMA-2); a composite PK is by design (the implicit `id` + a composite `unique` index expresses the constraint; the FK side is `foreign_keys`); `rate_limit` per resource is a defensible schema key — a small SCHEMA item if a second app asks. The migration note: these are the three things a DDL-for-DDL migration cannot express, so write them down in backend-spec §2 as the known translation table. |

**Ready (for the front as a whole):** Miguel decides whether "receive an
existing system" is a product path. #5 (`?fields=`) is DONE regardless (it
was the one that cost a client's first screen). If yes, the remaining order
by damage is #4 (documentation of `import`), #6c (`Route.AsRole`), #1 (a
`COPY`-class door only with a customer number), #6a/#6b, #8. If no, the
documentation halves (#1, #4, #8) are still worth doing — they cost a page
and they are what the next migrator reads first.

### OPS-35 — Mann-Whitney does not test the tail: a claim about the p99 needs a permutation test on Δp99, and `compare-groups` does not run one yet
- **Origin:** CENTINELA-C-S1 (2026-08-29), from the Centinela research report
  §Bloque 2 and decision A-54. The house's ABBA verdict is Mann-Whitney U on
  the pooled samples with the max(0.5 ms, 3 %) gate — correct for what it
  gates (the median: stochastic dominance), and BLIND to a change that moves
  only the tail. A p99 promise ("< 1 % of p99") is not measurable on this box
  at all (≈ 0.016 ms against a 0.5 ms EMD), and the overhead of the resource
  collector was therefore stated on allocs/op, CPU-seconds and RSS with the
  p99 as an upper bound (docs/BENCHMARKS.md §4c).
- **Shipped this session:** `tools/devhub/stats.PermutationQuantileDiff(a, b,
  p, resamples)` — a two-sided permutation test on the difference of the
  p-th percentile (deterministic RNG, +1 correction), pinned by a test with
  identical medians and a 2 % tail shift; the methodology note in
  docs/BENCHMARKS.md §7 and in the Centinela specs (C §6, B §6).
- **Impact:** medium for the measurement discipline: every "no_change"
  verdict so far is a MEDIAN verdict. Nothing published claims a p99 delta,
  so nothing is wrong today — the gap is that the protocol cannot yet SAY
  "and the tail did not move either".
- **Ready:** `POST /api/bench/compare-groups` (DevHub) reports, next to the
  Mann-Whitney verdict, `p99_delta_ms` + `p99_perm_p` (≥ 2000 resamples) and
  a bootstrap CI of the Δp99; `scripts/bench-protocol.sh`'s summary prints
  them; docs/BENCHMARKS.md §7 states the rule "a median verdict and a tail
  verdict are two verdicts". Runs on the DevHub's stored k6 samples — no new
  data collection.

### OPS-34 — Two builds of the SAME source measure ~9 % apart on the 105: the ABBA base must be built like the new binary
- **Origin:** MIGRACION-CONFIANZA-S1. The frozen ABBA on the PATCH protocol
  read +10–15 % for the new binary with base-vs-base identical (4.689 /
  4.685 ms). Attribution and a bisection showed the base commit REBUILT from
  a clean worktree measuring 5.101 ms — the new family's number — while the
  original base file (built in the main tree: buildinfo `v0.1.11+dirty` +
  vcs stamps, 24 MB of differing bytes vs the rebuild) kept measuring 4.64–
  4.69 across three arms. Same code, two layouts, ~9 % apart on this 1-vCPU
  box. The session's verdict was recovered by comparing same-session builds
  (guard ON 5.036 vs rebuilt base 5.101 → −1.3 %).
- **Impact:** medium for the measurement discipline: a base binary built
  earlier / elsewhere can turn a `no_change` into a false `regression` (or
  hide a real one), and the protocol's max(0.5 ms, 3 %) gate is below this
  artifact's size.
- **Ready:** `scripts/bench-protocol.sh` / the ABBA recipe document and
  enforce "build BASE and NEW in the same session, from the same kind of
  tree (both worktrees, or both the main tree), with the same version-stamp
  shape"; ideally the ABBA script builds both from commits itself. A note in
  docs/BENCHMARKS.md about the artifact size measured here.

### ENG-51 — Custom routes carry no `Server-Timing`

**Origin:** APP-PODER-S1. Every GENERATED read (list, get, subroute, embed,
aggregate) sets `Server-Timing` from the span tracker right before writing
its body; a consumer's custom route (`appximo.Route` handler, e.g. the
tiendita's `/api/catalogo`) ends with `ctx.JSON(...)` and sets nothing, so a
UI built on custom routes cannot show the engine's time. Low: the panel is
generic and reads generated routes; the header is a read-side courtesy.

**Ready:** `Ctx.JSON` (and the other terminal helpers) set the same header
from the tracker, or a documented `Ctx.ServerTiming()` a handler calls
before its own write — pinned by a library integration test.

### ENG-50 — JSON number fidelity beyond float64, in both directions (`json`/`jsonb`)

**Origin:** MOTOR-TIPO-JSON-S1 (ADR-028, alternative "preserve the client's
exact bytes", deferred). Every HTTP door decodes the request body into Go
values (`encoding/json` → float64), so a number inside a `json`/`jsonb`
document loses precision past ~2^53 (`12345678901234567890` → `…67000`) and
a decimal literal is re-rendered (`1.50` → `1.5`) before it reaches the
column; on the way OUT pgx decodes a `jsonb` document into Go values with the
same loss. A `json` field written as a JSON-text STRING keeps its numeric text
(compacted verbatim) — the one path that is exact today.

**Impact:** low for the known apps (money is `int64` minor units by doctrine;
ids are strings); real for a document store carrying big integers or exact
decimals. Documented in backend-spec §2 and SCHEMA_REFERENCE §3 as the ~2^53
limit.

**Ready:** an engine-wide decision — decode bodies with `UseNumber` (and
teach the type checks `json.Number`) or capture `json.RawMessage` for
json/jsonb fields on the way in, AND scan json/jsonb columns as
`json.RawMessage` on the way out (a pgx type-map change that also changes
`Ctx.Query`'s documented row types) — measured on the write and read
protocols. Not a `json`-only fix: half of it is worthless without the other.

### ENG-48 — The platform-admin login throttle has no knob either (same shape as ENG-47)

**Origin:** MOTOR-AUTORIZACION-S1 Part C, the sweep for limits "in the same
situation" as ENG-47. `platformadmin.loginThrottle` bounds `/admin/auth/login`
and the admin MFA verify at 5/min per admin email, hard-coded
(`newLoginThrottle(5, 5)` — `pkg/platformadmin/throttle.go`). Deliberately
NOT touched this session: a platform admin is a privileged credential no demo
shares, so today's default is the right default and loosening it has no
legitimate driver yet.

**Impact:** low. It would bite only an operator who scripts many admin logins
per minute (e.g. a CI job minting platform tokens in a loop — `X-Admin-Key`
covers that path today).

**Ready:** `APPXIMO_PLATFORM_LOGIN_ATTEMPTS_PER_MINUTE` / `_BURST` wired like
ENG-47's knob (same default, same boot warning above the default, same
refusal on a non-integer), or an ADR saying the admin throttle stays fixed
and why.

### RBAC-2 — A field allowlist DROPS a client's field silently — the "hidden attempt" contract

**Origin:** MOTOR-AUTORIZACION-S1 audit, finding 2 (generalized). For a role
with a `fields` allowlist, a write body carrying a field outside it is
dropped and the write answers 200/201 — on create (`EnforceCreateRBAC`) and
update (`CollectUpdate`'s `writable`). The IDENTITY column is now exempt
(explicit 403, ADR-027); every other non-allowlisted field keeps the
documented silent drop (AGENTS.md, SCHEMA_REFERENCE §RBAC: "dropped silently
— not an error").

**Impact:** not an escalation — the field is never written — but the
project's own rule ("an attempt is rejected, never swallowed": ADR-024, the
NIGHT-SWEEP class) says a client that believes it wrote `secret_flag` and got
200 has been lied to, and the log shows nothing. Changing it is a CONTRACT
change: a generic UI that PUTs a whole object back with fields the role may
only read would start failing.

**Ready:** decide in its own increment — (a) reject with 422
`rule: "forbidden_field"` naming the field (and make `/app` + the backoffice
guide send only writable fields), or (b) keep the drop and echo it
(`meta.dropped_fields` / a `Warning` header) so the attempt is visible; or an
ADR that keeps the silent drop with the reasoning. Measured with the
binary-diff gate either way.

### OPS-33 — An env value with spaces must be QUOTED, or the scripts that source it break

**Origin:** TIENDITA-VITRINA-S1, found hours before it would have bitten.
APP-VITRINA-S1 added `APPXIMO_APP_BANNER_TEXT=← Volver a La Tiendita` to
`/etc/appitools/appitools.env` **unquoted**. systemd's `EnvironmentFile=`
parses that fine, so the engine was correct and nothing looked wrong. But
`redate-demo.sh` — the `ExecStartPost=` of the nightly `demo-reset.service` —
does `. "$ENV_FILE"` in bash, where the line becomes `APPXIMO_APP_BANNER_TEXT=←`
followed by an attempt to RUN `Volver` → `command not found`, exit 127.

**Impact:** the nightly golden reset of the tiendita demo would have started
FAILING at its first run after that change (the restore itself ran; the
re-dating step did not, and the unit went to `failed`). The last successful run
was 2026-08-26 04:15; the next was due 2026-08-27 04:15. This session quoted
both env files (`.pre-quote` backups kept), restarted both services and ran the
whole unit end to end — `Result=success`, golden restored, dates re-anchored.

**Ready:** the class, not the instance. Either (a) `install.sh` and the
deploy docs write env values quoted and say why, or (b) the scripts that only
need `DATABASE_URL` stop sourcing the whole env file. A test that writes a
value with a space and asserts both consumers survive.

### COMMERCE-11 — The tiendita's six product photos weigh 1.55 MB (one of them 676 KB)

**Origin:** TIENDITA-VITRINA-S1's image audit. The photos are licence-verified,
brand-free and face-free (the A-21 filter holds), but they are served at their
ORIGINAL size — `rua-lan.jpg` is 676 KB, `jea-sli.jpg` 290 KB, `som-vue.jpg`
299 KB — for tiles that render ~260 px wide. Measured live: the SPA itself is
168 KB; a desktop first paint pulls 768–1 035 KB of photos on top, and the
number swings run to run with the lazy-loading scheduler.

**Impact:** the demo's weight is dominated by images nobody sees at full size.
Lazy loading already spares mobile (0 photos at `load`), so this is a desktop
and scroll cost, not a blocker.

**Why it was not fixed here:** replacing the photos means re-uploading to the
file store, re-pointing `productos.imagen_id`, and **regenerating the golden
dump** — and this session was explicitly told to leave the golden intact.

**Ready:** the six source JPEGs downscaled to ~1 000 px / ≤120 KB, re-seeded
through `seed.sh` (which is idempotent on photos), a NEW `golden-demo.dump`
regenerated by the documented procedure, and the before/after transfer
measured with `scripts/medir-carga.mjs`. Own photography (FOTO-PENDIENTE)
would close it better, and is Miguel's call.

### OPS-31 — Indexation steps that need Miguel's Google account (Search Console, GBP)

- **Origin:** FRENTE-COMERCIAL-S1 (2026-08-26), Part A.1. An external SEO
  critique found neither `appximo.com` nor `appximo.github.io/appximo` in
  any query, including hyper-specific ones; a WebSearch from the dev box on
  2026-08-26 confirmed it (the "appximo" SERP is owned by Appium/AppSumo/
  Apimo and a typo-squatter page that reports the domain as "expired one or
  more times before"; the Wayback Machine has **zero** captures of the
  domain, so that claim cannot be checked).
- **What the session could do, and did:** verified there is NO `noindex`,
  `nofollow` or `X-Robots-Tag` on either property (the suspect #1 was
  innocent); added `robots.txt` (Allow all + Sitemap), `sitemap.xml`,
  `<link rel="canonical">`, `og:url`, `meta robots` and JSON-LD
  (`ProfessionalService` with phone + areaServed Pereira/Dosquebradas on the
  landing; `Service` on conjuntos) to both properties; created the first
  crawlable category-intent page (`conjuntos.html`, ~720 words of prose).
- **What needs the account (exact steps, not attempted):**
  1. Google Search Console → Add property → **Domain** `appximo.com` → verify
     by the DNS TXT record Google prints (Cloudflare DNS → Records → TXT `@`).
     Then Add property → **URL prefix** `https://appximo.github.io/appximo/`
     → verify by HTML tag (add the `<meta name="google-site-verification">`
     to `site/index.html`, publish to gh-pages) or by the HTML file (commit
     it to gh-pages root).
  2. In each property: Sitemaps → submit `https://appximo.com/sitemap.xml`
     and `https://appximo.github.io/appximo/sitemap.xml`.
  3. URL Inspection → paste `https://appximo.com/`, `/conjuntos.html`,
     `/caso.html` and the site URL → **Request indexing** for each (one at
     a time; the quota is ~10/day).
  4. Pages report (Indexing → Pages) after 3–7 days: every URL must show
     "Indexed"; anything under "Discovered – currently not indexed" is the
     new-domain crawl budget, re-request weekly.
  5. Google Business Profile → business.google.com → create "Appximo"
     (category: *Software company* / *Custom software development*),
     service area Pereira + Dosquebradas, phone +57 311 517 5472, website
     appximo.com; verification is by postcard/phone/video as Google decides.
     This is the local-intent lever the critique ranks above everything
     else for "desarrollo de software a la medida Pereira".
  6. Bing Webmaster Tools → import from Search Console (one click; Bing
     feeds DuckDuckGo and ChatGPT search).
- **Ready when:** both properties show the sitemap "Success" and the three
  landing URLs "Indexed" in Search Console, and `site:appximo.com` returns
  them.

### OPS-32 — The subordinate secondary path has nowhere to deliver an email

- **Origin:** FRENTE-COMERCIAL-S1 A.3.3. The research prescribes a
  low-friction secondary CTA (PDF of the case in exchange for an email)
  visually subordinate to WhatsApp. The landing is static GitHub Pages: no
  endpoint receives a form, and the engine's demos are not the landing's
  origin. The session shipped the OTHER documented secondary — "mire primero
  un sistema funcionando, sin registrarse" (a text link under the hero CTA
  → `#pruebe`) — and no email capture.
- **Options (decision is Miguel's):** (a) a hosted form (Tally/Formspree
  free tier: ~5 min, an account in Miguel's name, the email lands in his
  inbox); (b) a `Route.Public` on one of the 58's engines
  (`POST /api/leads` with `Route.RateLimit`, CORS to appximo.com) — ~1 h,
  owned, but couples the landing to a demo box; (c) a `mailto:` link
  (zero infra, fewer conversions, no PDF). The PDF itself (the case in
  Spanish) does not exist yet either.
- **Ready when:** a visitor can leave an email and receives the case, and
  the address lands somewhere Miguel reads.

### DOC-3 — The batch/N+1 patterns are in backend-spec but not where an agent hits the problem

- **Origin:** atina evaluation, finding 2 (FIELD_FEEDBACK_RESPONSE §4). The
  builder's agent wrote a row-by-row recompute (~1,300 round trips), then
  rediscovered `= ANY($1)` + `unnest()` on its own — the exact §3.4b of
  backend-spec, which it did not open because it was not looking for a
  section called "batch patterns".
- **Disposition:** put a one-line pointer + the N+1 warning at the `Ctx`
  reference entries for `Query`, `Get`, `Insert` and `Update` (the place the
  agent IS when it writes the loop), and in the `UnsafeTx` callout. Docs
  only; the embedded spec changes → `spec_test`/build re-run.
- **Ready when:** the four `Ctx` entries carry the pointer and a fresh-agent
  run with a recompute task reaches §3.4b without being told.

### OPS-30 — A CLI token with an empty tenant is a cross-tenant wildcard on the data plane

- **Origin:** FRESH-AGENT-GAPS-S1 (2026-08-21), surfaced by the Part-A
  implicit-requirement class audit, **hand-verified live**.
- **What:** `pkg/auth/middleware.go` skips the tenant-match check when the
  token's `TenantID` is empty (`claims.TenantID != "" && claims.TenantID != tc.ID`).
  So `appximo token --role admin` **without `--tenant`** mints a token that
  authenticates against EVERY tenant, not one. Verified: an empty-tenant admin
  token answered `GET /api/tasks` 200 and `POST /api/tasks` 201 against a
  tenant it was never scoped to.
- **Severity, honestly:** minting the token needs the `JWT_SECRET`, and anyone
  with the secret can already sign a token for any named tenant — so this is
  **not** privilege escalation for an outsider. It is an operator footgun: the
  easy-to-forget flag produces an all-tenants key instead of a one-tenant key,
  and if that key leaks it is a wildcard rather than a single-tenant token.
- **Shipped this session (the CLI half):** `appximo token` now prints a loud
  WARNING when `--tenant` is empty, naming the wildcard behavior and the fix.
- **Deferred (the engine half), with reason:** the empty-`TenantID` skip is on
  the auth HOT PATH and has a legitimate neighbour (platform tokens carry
  `scope=platform` + no tenant and are handled by a separate `/admin` chain).
  Making the data plane reject an empty-tenant token needs a careful pass over
  every token issuer (worker service JWTs, platform tokens, MFA-pending
  tokens) to confirm none legitimately reaches `/api` with an empty tenant —
  a security change that earns its own session with the gate + bench, not a
  rushed one-liner here.
- **Ready when:** the data plane refuses a token whose tenant does not match
  the host (empty included), verified against every issuer, through the
  binary-diff gate + ABBA bench.

### ENG-45 — The implicit-requirement audit inventory (schema validates, breaks at runtime)

- **Origin:** FRESH-AGENT-GAPS-S1 (2026-08-21), Part A class audit — 27
  findings, 22 silent. SILENT-CORRUPTION-S1 (2026-08-21) re-audited the two
  worst FAMILIES with two fresh sweeps (literal-English-name-bound behavior:
  18 findings; map-iteration/boot-order non-determinism: 10 findings) and
  closed them — see its DONE section. The doctrine stands (C-DOCTRINA): each
  family is closed by a LOAD-TIME check (error, or a SCHEMA-5 warning when
  sometimes legal) or documented — **never** by the engine filling values in.
- **Priority criterion (SILENT-CORRUPTION-S1, written):** silent corruption >
  non-determinism > loud failure > friction. The list below is ordered by it.
- **CLOSED by SILENT-CORRUPTION-S1** (detail in its DONE section): the
  name-bound `auto` family (auto:"create"/"update" roles + `auto_requires_time`
  + `invalid_auto` errors + `auto_update_intent` warning + one refresh source
  `schema.AutoRefreshColumns` consumed by REST/batch/Ctx.Update); the relation
  subroute collision (`relation_subroute_collision` load error, single source
  `schema.RelationSubroute`); field/relation named `id`
  (`reserved_field_name`); custom-route writes now invalidate the response
  cache; validation-error order, file-policy 422 order, filter/aggregate/
  reject-list first-error — all deterministic; the GraphQL self-shadowed list
  query warns (`graphql_list_query_shadowed`); OpenAPI marks auto fields
  `readOnly` + `x-appximo-auto` and /app + both backoffice contract.js read it
  from the contract.
- **CLOSED by WRITE-ASYMMETRY-S1 (2026-08-21):** family 1 — create accepted a
  forged `id`/`auto` value with 201 (REST POST, batch create, Ctx.Insert) while
  update answered 422 `read_only` and GraphQL rejected structurally. Closed as
  a class with ONE source (`schema.GovernedFieldViolations` +
  `IsGovernedWriteField`) consumed by every door: PrepareCreate (REST/batch/
  Ctx.Insert), the GraphQL create resolver, CollectUpdate (REST PUT/PATCH,
  GraphQL update, batch update) and PrepareUpdate (Ctx.Update — which used to
  pass `{"id":…}` through as `SET id = …`, a PK rewrite; that id/auto half of
  family 3 is closed too). The import use case became DECLARABLE (doctrine
  C-DOCTRINA-3): a resource-level `"import": {"roles": […], "fields": […]}`
  permits exactly the enumerated roles to supply governed fields ON CREATE
  (never update), load-validated (`import_roles_required` /
  `import_unknown_role` / `import_unknown_field` / `import_fields_empty`),
  published as `x-appximo-import` (fields only — role names stay
  unpublished, the ENG-27 asymmetry), authored in Studio, rendered by
  `explain`, taught by the grammar, and honored hot from the deployed surface.
  The bench-fixture restore path is now the declared one (erp-demo `empleados`
  grants `rrhh-admin`; README documents it; proven live on nimbus). Update-side
  messages stay byte-compatible; anti-divergence tests pin every door
  (governed_divergence_test.go + governed_write_integration_test.go). Gate:
  126 SAME + 3 intentional DIFFs (the fix itself, corpus rows added); ABBA
  no_change on POST and PATCH protocols.
- **CLOSED by MOTOR-AUTORIZACION-S1 (2026-08-27):** family 1 — the row
  give-away on update. Audited first (docs/audits/AUTHZ_WRITE_AUDIT_S1.md: 61
  real requests × 3 binaries, every write door, every RBAC form; exploitable
  in v0.1.8 and v0.1.9), then closed as ONE policy (ADR-027,
  `codegen.EnforceUpdateRBAC` beside the create half in
  `pkg/codegen/rbac_write.go`): an identity-bound condition column is
  server-owned on update — another id / null → the same 403 as create; a
  PUT that omits it keeps the caller; own id re-sent is a no-op; judged on
  the body before the row lookup and before the allowlist; a literal
  condition stays a visibility filter. All-doors test
  `ownership_update_integration_test.go`; 11 gate rows. The audit ALSO closed
  the state-null 500 (a state field set to null / omitted on PUT → named
  422, `codegen.StateFieldNullViolations`) and re-ranked #3 below.
- **The families still OPEN, by damage:**
  1. ~~the row give-away~~ — **closed above.**
  2. **(silent corruption) `files`-resource shadowing:** the validator allows a
     schema resource named `files` but `migration.isEngineManagedTable`
     excludes the table from every diff — never provisioned, never converged —
     and a `file` field's FK assumes the engine's metadata table.
     **Disposition: close at load** (reject the shadow when any `file` field
     exists; provision the declared table otherwise) — or reject the shadow
     outright.
  3. **(loud failure) Ctx.Update remaining parity gap:** a null on a required
     field surfaces a raw 23502 as a 500 (CollectUpdate answers a clean 422
     `required`). The id/auto half of this family was closed by
     WRITE-ASYMMETRY-S1 (PrepareUpdate now runs the governed pass); the
     state-field-null half by MOTOR-AUTORIZACION-S1 (PrepareUpdate now runs
     `StateFieldNullViolations`). What remains is exactly the
     required-null: **Disposition: add the null-required check to
     PrepareUpdate — the CTX_PARITY fix pattern — 5 lines + the row in
     ownership_update_integration_test.go's door table; kept out of the
     security session on purpose (functional, no client can escalate with a
     500).**
  4. **(silent no-op) Declared-relation pieces don't line up:** `fk`, m2m
     `through` and `target_fk` are not checked to exist ✓ — fails at
     `?include=`/GraphQL time. **Disposition: SCHEMA-5 warning** (a column can
     be hot-added; the index-existence check honors the same escape).
  5. **(silent no-op → now warned) GraphQL naming:** the self-singular list
     shadow now WARNS (`graphql_list_query_shadowed`); the real fix (a
     collision-free get-by-id name, e.g. `<name>ById`) is a GraphQL contract
     break — **disposition: own increment, with a migration note**. Also still
     open: a required field WITH a default is `NonNull` in the create input
     where REST allows omission; Spanish plural mis-singularization
     (`clases`→`Clas`) is cosmetic. **Disposition: reconcile or document per
     case.**
  6. **(friction/cosmetic — EXCEPT the first, which the MOTOR-AUTORIZACION-S1
     audit re-reads as security-adjacent) Runtime-config assumptions**
     (documented today): `hmac_secret_env` unset signs with the EMPTY key — a
     receiver verifying `X-Appximo-Signature` accepts a payload anyone can
     sign, so the webhook's authenticity guarantee is silently void; a
     `wasm` hook naming an unloadable module; an `events` list with no
     consumer; `$external_client_id` never populated. **Disposition: the
     empty-key case becomes a boot-time WARNING naming the hook and the
     variable (or a load error when the env var is declared and empty) in
     the next engine increment; the rest stay candidate warnings.**
  7. **(cosmetic) OpenAPI:** auto fields in responses now carry `readOnly`;
     what remains is documenting the refresh semantics in the description
     text. **Disposition: documentation.**
- **Ready when:** every family above is either a load-time check with an
  actionable message or a documented deliberate-restart/limitation note — none
  left as a runtime-only surprise.


### OPS-29 — Releases lag the tags: v0.1.8's release run failed, and the Docker badge shows a SHA

- **Origin:** SHOWHN-MATERIAL-S1 (2026-08-19), pre-launch verification sweep.
- **What:** the tag `v0.1.8` exists (2026-08-17, commit 97480d1) but its
  Release workflow failed at the **"Create GitHub Release"** step (binaries
  built fine) — so the Releases page, the version-less download aliases and
  `appximo upgrade` all serve **v0.1.7**, while `go get @latest` and the tag
  list say **v0.1.8**. A launch-day visitor who checks tags sees the skew.
  Related cosmetic finding: the README's Docker badge renders the image tag
  `a7689d7` (a commit SHA) instead of a version, because Docker Hub carries
  no semver tags — only `latest` + per-commit SHAs.
- **Impact:** low functionally (every path works; they just disagree on
  "latest"), but it is exactly the kind of inconsistency an HN thread finds
  in minutes.
- **PRELAUNCH-TRUTH-S1 (2026-08-19) — cause narrowed, workflow hardened,
  four of five surfaces verified; the re-run remains the owner's:**
  - The failing step died in **~1 s** (18:32:28→29, per the public jobs API),
    BEFORE any asset upload (v0.1.7's same step took 10 s uploading), with
    release.yml unchanged across 7 straight successes. Two causes match that
    exact signature: a pre-existing release/**draft** for v0.1.8 (drafts are
    invisible anonymously but block `gh release create`) or a transient API
    error on the create call. The literal error line needs a logged-in look
    at the run — one click for the owner.
  - **release.yml hardened:** the create step is now idempotent (release
    exists in any state → upload `--clobber` + publish; else create with one
    30 s retry), so a re-run converges instead of failing on its own partial
    success. YAML validated; workflow-only change, zero engine code.
  - Surfaces verified separately: tags `v0.1.8` ✓ · `go get @latest` →
    v0.1.8 ✓ · version-less aliases → v0.1.7, checksum OK ✓ (they follow the
    release) · **`appximo upgrade` tested end to end from a real v0.1.6
    install** (detects v0.1.7, downloads via alias, checksum ok,
    self-replaces, exit 0) · Releases page → v0.1.7 ⚠ pending. Bonus: Docker
    was ALREADY coherent (`neodevtrix/appximo:v0.1.8` published 08-17) and
    the README badge now uses `sort=semver`, so it shows the version, not a
    SHA.
- **Ready when:** a release exists for the newest tag. The owner's exact
  path: (1) check https://github.com/appximo/appximo/releases logged-in for
  a stale v0.1.8 DRAFT and delete it if present (the re-run uses the OLD
  workflow, which dies against a draft); (2) "Re-run failed jobs" on run
  32055036588 (or `gh run rerun 32055036588 --failed`); (3) verify
  `releases/latest` redirects to v0.1.8. Alternative: the next tag
  (`git tag v0.1.9 && git push --tags`) flows through the hardened workflow
  — sessions do not tag.

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
- **LINKABLE-TRUTH-S1 (2026-08-19):** the DEAD LINKS are gone from every
  public surface — README (×2), the technical site, the case study — while
  the third-party build STORY stays everywhere (that is what crisblogs
  proves, and it survives without a URL). The landing's quantity claim now
  says only what survives a click ("4 construidos, 2 abiertos para probar").
  The app itself is unchanged and still broken for a visitor.
- **Ready when:** the two demo accounts log in from the buttons, or the
  buttons are removed from crisblogs' login, or the subdomain points at a
  replacement we operate. Needs: Miguel's call between (a)/(b)/(c).

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
- **Update (MIGRACION-CONFIANZA-S1, 2026-08-28):** the multi-app path WAS run for
  real — two apps (`vecingo`, `retotr`) side by side in an LXD Ubuntu 22.04
  container with native Postgres + Caddy + systemd, clean install / upgrade in
  place / foreign schema / port collision, all verified — so the "never run
  live" part now applies only to the 58 itself (its pre-OPS-10 inline Caddyfile
  migration). The container's Caddyfile was the OPS-10 layout from the start,
  so the inline-block migration remains unexercised.


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

### SCHEMA-8 — Omit a declared-heavy field from collections by default (`"list": "on_request"`) — PROPOSED, not built
- **Origin:** MOTOR-FIELDS-S1 (ADR-029). The migration report's alternative to
  `?fields=` was "exclude large fields from collections by default". Rejected
  as a flipped default: it is a contract break (every client reading
  `row.data` from a list gets `undefined` after an upgrade, silently — the
  ADR-024 class) and "heavy" is not a type the engine can decide. `?fields=`
  ships instead (opt-in per request, no schema change).
- **The declared form, if a second app asks:** a per-field key
  `"data": {"type": "json", "list": "on_request"}` — list and subroute reads
  omit the field unless `fields=` names it; the detail keeps it;
  `/openapi.json` publishes `x-appximo-list: "on_request"` on the property so
  a generic client (the `/app`) knows why a column is absent; `validate`
  warns when a `json`/`jsonb`/`text` field without it sits on a resource the
  author marks as large. Opt-in per schema: the author breaks their own
  clients on purpose, at a version of their choosing, and the contract says it.
- **Impact:** low today — `?fields=` covers the migrated case and the `/app`
  uses it; a client that does not send `fields=` keeps paying the document.
- **Ready:** the key parsed/validated at load (strict-key, json/jsonb/text
  only), honoured by list/subroute/GraphQL-list/admin-browse, published in
  the contract, pinned by the gate; measured `no_change` on the plain list.

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

### COMMERCE-10 — A 100% coupon produces a $0 order, accepted

- **Origin:** LINKABLE-TRUTH-S1, pinned while writing verify.sh section E
  (COMMERCE-8): a coupon with descuento_pct=100 yields base 0, IVA 0,
  total $0 — and the checkout accepts it, creating an order whose payment
  is for zero pesos. Defined behavior (the arithmetic closes), but a real
  store probably wants either a cap (<100) on coupon creation or a
  zero-total short-circuit that skips the gateway.
- **Impact:** Low — coupons are merchant-created (panel/dueño), not
  visitor-supplied; no data corruption, the math is exact.
- **Ready:** a decision — cap the pct at coupon creation, or define the
  $0-order flow (auto-paid? gateway skip?) — implemented with a suite case.

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

All three were **re-verified as still open on 2026-07-29**; the FRENTE-COMERCIAL-S1 rows were added 2026-08-26.

| Item | Why it needs him |
|---|---|
| **Open the "receive an existing system" front?** (MIGRACION-CONFIANZA-S1, MIG-FRONT) | Six findings from a real migration are registered with evidence and a reading each. Whether migrations are a product path — and therefore whether `?fields=`, a `COPY`-class import, `Route.AsRole` and the rest get built — is a product decision, not a sprint. The documentation halves (the batch endpoint, `import` for history, the DDL translation table) are done or cheap regardless. Also for him: ~~the 58 has NO swap~~ — done, `/swapfile 2G` is active (seen 2026-08-28 during MOTOR-FIELDS-S1). #5 (`?fields=`) is built. |
| **Does the migration report count as the FIFTH external evaluation?** (DOC-VITRINA-S1, A-25) | Under A-25 it now meets (2) — `docs/FIELD_FEEDBACK_RESPONSE.md` §5 answers the nine points in public, with the report's three wrong diagnoses counted in both directions — and (3). What is missing is (1): a WRITTEN confirmation that the migration ran without our direction (the report is addressed to us and its box is the external developer's who built atina/crisblogs/VecinGo). With that line, README/FAQ/site say "five independent field evaluations, one of them a real 23-table migration" and link §5; without it the material says what it says today: four evaluations + a migration report answered point by point. |
| **v0.1.10 as a SECURITY release** (MOTOR-AUTORIZACION-S1) | The row give-away (ENG-45 #1) is exploitable in EVERY published version by an ordinary account; the fix is on `main` (`6429a00`) and both demos run it. The tag is Miguel's; the release text AND the recommendation (yes, a GitHub Security Advisory, medium-high severity: privilege escalation between users of one tenant, no cross-tenant effect, rows cannot be stolen) are written in the internal repo `RELEASE_NOTE_v0.1.10.md`. Cutting the tag without the advisory leaves every v0.1.8/v0.1.9 deployment unwarned. |
| **Response-time promise on the landing** (FRENTE-COMERCIAL-S1 B.2) | The research's strongest lever (5 vs 30 min = 21× qualification, MIT/InsideSales). NOT published: no confirmed number. If Miguel can sustain e.g. "le respondemos en menos de 30 minutos en horario laboral", it is one line under the hero CTA (`.cta-trust`) on index.html and conjuntos.html. An auto-acknowledgement on WhatsApp Business (away message) is the zero-cost floor. |
| **Name + face next to the CTA** (FRENTE-COMERCIAL-S1 B.1) | The research (founder photo +34.7 %, personal trust in LATAM) says name + photo + city. Registered decision **A-32** (Miguel, 2026-08-24) says the proper name stays OUT of the CTA ("el equipo"), and no photo exists (FOTO-PENDIENTE since A-26). The session published city + phone with country code and kept "el equipo" — A-32 wins until Miguel re-decides. Reversing it: one sentence + one `.webp` ≤ 40 KB. |
| **The VecinGo testimonial** (FRENTE-COMERCIAL-S1 A.2.1) | Removed from index.html and caso.html. The disk holds no raw report, no name, and A-25's own chronology note ("a VPS already serving two apps" — the 58's exact state on 2026-08-09) makes the independence claim unverifiable from here; the critique also read the caption as self-attribution. It goes back ONLY with a named person's written confirmation (quote + how they want to be credited) — then it is worth ten times what it was. The technical docs (CASE_STUDY_VECINGO.md) are unchanged: they state their source. |
| **"Cientos de sistemas generados"** (A-29 → FRENTE-COMERCIAL-S1 A.2.2) | Removed. A-29 published it on Miguel's order with the written proviso "Miguel puede re-decidir con datos"; the session prompt set the condition ("si la evidencia real son pruebas y CI, sale") and A-29 itself records that the evidence IS the eval corpus + CI. Restoring it is one `<div class="stat">`; the recommendation is not to, until there are real deliveries to count. |
| **Cloudflare proxy on `api.appitools.com`** | Still dead-ends at the proxy (the 58 has no site for it). HOUSEKEEPING-S1's OPS-17 decision: `api.appximo.com` is deliberately NOT created (the bare-engine demo was retired; petfriendly IS the engine demo) — so the only action left is Miguel retiring the old `api.appitools.com` DNS entry. |
| ~~**Cut the first release tag**~~ | **RESOLVED (2026-08-05): v0.1.1 is out** — 4 platform binaries + checksums on Releases. Same day the working clone verified download+checksum+`version` and set `RELEASE_VERSION="v0.1.1"` in install.sh, enabling the documented no-`--binary` download path. |
| ~~**Rotate the 58's PostgreSQL password**~~ | **RESOLVED by PROD-JOURNEY-1B (2026-07-31):** the wipe (`--uninstall --purge`) dropped the role and database; the reinstall generated a fresh password (plus fresh JWT/admin secrets, rotated again on-box after the installer printed them to stdout — see OPS-7). The exposed credential no longer exists. |
| ~~**Publish the Go module**~~ | **RESOLVED in practice (2026-08-05):** the repo is public and v0.1.1 is tagged, so `go get github.com/appximo/appximo@v0.1.1` fetches from the public proxy — **verified live** in a scratch module. backend-spec §3.0 updated; the framework half of the product is reachable anywhere. DOC-2 fully closed. |
| ~~**Give `/root/commerce` a remote**~~ | **RESOLVED (2026-08-18):** Miguel created the private repo `miguel09acosta/latiendita`; SHOWCASE-TRUTH-S1 swept the full history for secrets (clean), documented the go.mod `replace`, and pushed. OPS-5 is DONE. |
| ~~**Pick the canonical repo URL**~~ (PHASE3-GUIDE-S1) | **RESOLVED by RENAME-AND-PUBLISH-PREP-S1 (2026-08-04):** everything is **`github.com/appximo/appximo`** — module path, imports, OpenAPI description, specs, badges, site. |
| ~~**Where `site/` lives**~~ (PHASE3-GUIDE-S1) | **RESOLVED by HOUSEKEEPING-S1 (2026-08-05):** GitHub Pages over the repo — https://appximo.github.io/appximo/ is LIVE (gh-pages root; doc links now absolute so they survive Pages). Moving to `appximo.com` later is a DNS + Pages-custom-domain change, nothing structural. |

---

## DONE in CENTINELA-C-S1 (2026-08-29) — Module C built: the engine observes its own resources and attributes the bottleneck; two Centinela spec promises corrected

| Item | What shipped | Verified by |
|---|---|---|
| **The two spec corrections (A-53, A-54)** | C §6 / B §6: the "< 1 % of p99 with Mann-Whitney" promise RETIRED (≈ 0.016 ms against a 0.5 ms EMD) and replaced by "≤ 1 % in allocs/op + CPU-seconds (benchstat, N ≥ 10, p < 0.05) and ≤ 1 % RSS post-GC; p99 an UPPER BOUND"; the methodological finding (Mann-Whitney tests dominance, not the tail → permutation test on Δp99 / bootstrap) registered for the whole protocol as OPS-35 with `stats.PermutationQuantileDiff` shipped. B §4b: the personal-data default is country + ASN with NO IP, 30–90-day retention, operator toggles (`ip_retention`, `treatment_policy_url` required for `full`), the Ley 1581/2012 art. 3.c/9/10 cited and the written caveat that it is not legal advice. Nothing of B built. | `tools/devhub/stats` test (identical medians, 2 % tail shift → permutation p < 0.01); decisions A-53/A-54 in the internal package; docs/BENCHMARKS §7 |
| **The collector** (`pkg/observability/resources*.go`, ADR-030) | Four layers on ONE goroutine: `runtime/metrics` (pre-allocated samples, per-tick histogram deltas), cgroup v2 (`memory.*`, `cpu.stat throttled_usec`, `cpu.max`, pids; `/proc/self` fallback; `cgroup_shared`; re-resolved EVERY tick), PSI (the cgroup's own first), `pgxpool.Stat` + the request's `query` span in a windowed HDR; Postgres server side only when local. Request path: 2 atomic adds + 1 HDR record, **0 allocs**; tick **1 alloc**; 900-tick ring; live 1 s while polled (`Touch` wakes the loop), background 10 s. Fixed footprint **1,118,992 B** (stated, not measured). Knobs `APPXIMO_SELFMON{,_INTERVAL,_LIVE_INTERVAL,_P99_MS}`; a bad value refuses to boot. `runtime.SetMutexProfileFraction(1e6)` when unset — `/sync/mutex/wait/total` accumulates only then (`runtime/sema.go:141`). | `TestResourceCollector_{ObserveAllocatesNothing,TickAllocatesNothing,FootprintBytes,RingIsBounded,RequestWindow,LiveMode,TouchWakesTheLoop,Run}`, `TestRuntimeReader_*`, `TestProcParsers`, `TestDBReader_*` |
| **The verdict** (`attribution.go`) | Eight values, fixed rules with written thresholds, ranked "the cause furthest from the operator's code wins", evidence signals + `also`, window summary. Three rules corrected by data: pool judged by wait TIME (not the instantaneous acquired/max), CPU wait must be MATERIAL (≥ max(2 ms, 5 % of p99)), throttling from 2 %. **Each of the eight provoked against a live engine and verified** — the harness in `evidencia/CENTINELA-C-S1/provocaciones/` (cgroup `cpu.max`/`memory.max`, netem on docker0, GOGC=2 over 47 KB documents, external busy loops, a `lockapp` consumer binary with a 30 ms mutex; k6 in a `cpu.weight=1` cgroup, `Cache-Control: no-cache`). | `TestAttribution_EightVerdicts` (15 cases), `TestAttribution_RankingAndAlso`, `TestSummarize`; the provocation logs (dominant verdict per run) |
| **The surfaces** | `GET /admin/resources[/snapshot]` (platform token / admin key; a tenant admin is 403 — the box is not a tenant's to see), `GET /debug/resources[/snapshot]`, 21 `appximo_selfmon_*` gauges on `/metrics`; `/admin` → **Resources**: live board, load-test window (verdict banner + tick timeline + 5 correlation charts), snapshot export/compare; the admin shell's first mobile layout. | `TestResourcesRoutes` (403/403/200/404), smoke JSON, Playwright desktop + 390×844 (0 px overflow, console clean, download verified); 3 corpus rows (auth only — live numbers are not a corpus row) |
| **The overhead, on the corrected method** (docs/BENCHMARKS §4c) | allocs/op **800 → 800, B/op 86.99 KiB → 86.99 KiB (±0 %)**; CPU-seconds ON vs OFF N=10+10: Δmedian −4.9 %, MWU p 0.68, CI [−14.7, +16.1] % — **not resolvable on this shared box, said**; RSS **+1.15 MiB background (+2.2 %), +2.3 MiB polled** — **over the 1 % budget**, cut from 2.2 MiB, the rest IS the ring; p99: Δ −2.5 ms across 60 k samples = host drift, **upper bound ≤ 2.5 ms**. | benchstat; `results.txt`; permtest |
| **Gates** | unit 44 ok · lint 0 · vet/gofmt 0 · full lane 44 ok · integration/e2e/resilience ok · binary-diff gate **166 = 163 SAME + 3 DIFF**, all intentional (the new auth rows: `/admin/resources` 404 → 403 without a key and with a tenant token, `/debug/resources` 404 → 401); four `list-fields-*` rows that flagged DIFF in the first runs were a CORPUS artifact — a small `per_page` with no `sort` pages by the random uuid of each scratch DB — and are deterministic now (`sort=title`) · ABBA read frozen, base and new built alike from worktrees (OPS-34): 0.631 / 0.675 / 0.636 / 0.634 ms → `no_change` ×4 (one arm drifted +7 % and came back) · binary +172,032 B by section (`.text` +68,608, `.rodata` +71,392 of which +30,135 admin assets, `.gopclntab` +32,376). | logs in `evidencia/CENTINELA-C-S1/` |
| **Deploy (the 58)** | Both apps: backups + golden dump untouched (md5 before/after), tiendita `commerce 95f5735-cent` + CLI `da1f36a-cent` via `deploy-update.sh --binary --cli`, vetapp `appximo da1f36a-cent`; rollback drilled both ways; demo mode through both entrances (live.mjs desktop + 390×844: zero writes leave the browser); verify-petfriendly 8/8; `/admin/resources` on both (systemd cgroup, cgroup PSI, local Postgres observable; `memory.peak` absent on kernel 5.15 → VmHWM). **The hard reading:** a k6 at 100 rps of the public catalogue (cache bypassed) against the LIVE tiendita → k6 p99 1.55 s and the app's own verdict **`pool_exhausted` (29/60 ticks, owner = the database): "10 of 10 acquired, 121 requests found no free connection per tick — the pool is undersized for this load"** — the tiendita's first wall at 100 uncached rps is `DB_MAX_CONNS=10`, which nobody had measured. | deploy log, live.mjs, k6-58.sh in `evidencia/CENTINELA-C-S1/` |

## DONE in DOC-VITRINA-S1 (2026-08-28, 5th session)

Site + docs, engine untouched. The thesis: the technical site is the first
thing an HN visitor opens and it showed a panel that no longer exists
(`app-tour.mp4` from 2026-08-17, captures with the "Appitools" brand from
2026-08-05) while six engine sessions had changed more than the site said.
Rule: zero claims without a request against the released **v0.1.13**
(`c655ce7`, downloaded from Releases, checksum verified).

| Item | What landed | Proof |
|---|---|---|
| **Audit first** (Part A) | 14 stale/false media + 12 stale text claims listed BEFORE any fix (`evidencia/DOC-VITRINA-S1/A-hallazgos.md`); "exact bytes preserved" confirmed at 0 live copies (only the audit, the ADR's rejected alternative and the backlog mention it — historically, correctly). | `verify-v0.1.13.log`: `?count=true` meta shape (page: `page/per_page/has_next/has_prev/total/total_pages`; cursor: `per_page/has_next`; cursor+count 400), `?fields=` (valid, unknown→400 listing the set, empty/double-comma→400, on aggregate→400, get-by-id, 100 rows 48 KB → 10 KB), `Server-Timing` (`query;dur=4.25, count;dur=2.90, app;dur=7.28`; `cache;desc="hit"` on the second GET), `/api/transaction` (2 creates 200, GET 405, 101→400, in `/openapi.json` with `x-appximo-transaction`), `jsonb` value (object→201 native; non-JSON string→422 `type`; JSON-text string accepted), governed fields (`created_at`→422 `read_only`), state null→422 `state`, illegal transition→422, `is_null`, GraphQL selection, the memory-guard and login knobs in `serve --help` |
| **The tour re-recorded** (Part B) | Playwright on the released v0.1.13 binary booted with `appximo up` on the demo's own `schema.json` (+ ONE `jsonb` field added by hand for the JSON editor, declared in docs/demo/README): login → home → list footer «Page 1 of 8 · 15 of 112 · query 2.8 ms» → next page → state filter → detail with parents/children → edit + JSON editor (invalid named before send, format, save) → columns + saved view → CSV → bulk transition of 15 rows via `/api/transaction` with progress → `/docs`, `/editor`, `/admin` (`engine v0.1.13`). **87.6 s, real time**, top bar «GRABACIÓN REAL · SIN ACELERAR · tiempo real HH:MM:SS.mmm», Spanish subtitles with an English line, bars padded OUTSIDE the frame so nothing overlaps (1280×960), H.264 crf 24 + faststart (`moov` at byte 36), 3.47 MB, no audio. Poster = frame 14.8 s. Frames verified visually at 18 points + OCR. Old tour + poster + GIF **archived** (`docs/demo/archive/`, gh-pages `assets/archive/`); no GIF for the new one (said). | `evidencia/DOC-VITRINA-S1/{tour.mjs,cues.json,subs.ass,seed.py,tour-poster.jpg}` |
| **Captures re-taken** | site: shot-app (footer visible), shot-docs / shot-studio / shot-admin (Appximo brand, v0.1.13), shot-tiendita (the current store, phone), shot-petfriendly + shot-https (the demo landing over HTTPS, Let's Encrypt checked with `curl -vI`); docs: quickstart `app-list` (starter todo-api on v0.1.13), `app-form` (the offers form: state chips + relation search), `live-https-demo`; README `app-record` (detail), `app-properties`, `app-offers`. Zero captures of the old panel or the old brand remain in any property; the landing was checked and NOT touched (its video was already on the redesigned panel). | thumbnails reviewed; `imgcheck.mjs` all 8 site images load after scroll |
| **Told where it is read** (Part C) | site: nav + §"What changed (v0.1.10 → v0.1.13)" (9 items) + "Known limits — said, not hidden" (7) + a `?fields=` row in the numbers table + captions; GUIDE §4 (`?fields=`, `Server-Timing`), §9 rewritten as of v0.1.13 (a "receiving an existing system" block; `is_null` no longer listed as missing; v0.1.1→v0.1.13; the first admin from `/admin` is no longer "terminal only"; OPS-11 line dropped — `--app` ran on real multi-app boxes); CAPABILITIES (Server-Timing, batches, breaker semantics, memory guard, installer verification, the migration limits); README (fields + Server-Timing in the box, the limits with links, the fifth report); QUICKSTART (v0.1.13, `latest` downloads with checksum, `upgrade`); docs/demo/README (the tour section). | requests above; `verify-site.mjs` |
| **The launch material** (Part D) | SHOWHN_MATERIAL addendum: the security FAQ updated to v0.1.13 (ownership on update), a new migration FAQ, the release/go-get rows to v0.1.13, OPS-29 closed by reality; **verdict on the count: stays at four** — see the Miguel table. `docs/FIELD_FEEDBACK_RESPONSE.md` §5 written (public, nine points, three corrections counted). | internal repo commit |
| **Verification** | 22 outbound links → 200, none behind a sign-in; desktop 1440×900 + mobile 390×844: 0 px overflow, 0 console errors, all images/assets 200; load before (live) 657 ms / 789 KB desktop · 440 ms / 527 KB mobile → after (local) 674 ms / 563 KB · 640 ms / 563 KB (the HTML grew 94.6 → 102.8 KB with the new section; the images shrank). Engine untouched (docs + media only); landing untouched; tags NOT cut. | `links.txt`, `site-changed.png`, `site-tour.png` |



Engine + `/app` + docs. The thesis: the migrated system's first screen
(`GET /api/declarations`, ~940 KB and a p99 of 3.8 s per page of 20 for a
list showing a NIT, a year and a status) is not a bandwidth problem — a
`json`/`text` value past ~2 KB lives in TOAST and `SELECT *` detoasts it per
row; a projection applied in Go after the read would save the bytes and none
of the seconds. So the projection reaches the `SELECT` list. Engine
`2902d4f` → `2013a95` + `c0de87b`; Miguel cuts the tag.

| Item | What landed | Proof |
|---|---|---|
| **`?fields=a,b,c`** (MIG-FRONT #5, ADR-029) | one parser (`query.ParseFields`) for every row-returning door: list (page + cursor), get-by-id, relation subroute (fields of the TARGET), the ROOT of `?include=` (the base subquery carries fields ∪ the FK/order columns the joins reference — `includeBuilder.baseRefs` — never `SELECT *`; embeds whole), admin browse, `ctx.Query` (`QueryOpts.Fields`). `id` always; unknown → 400 naming it + available set; hidden by the allowlist → OMITTED (the read contract, RBAC-2 — not the filter/sort value-oracle 403: the first cut was a 403 and the browser pass on a scoped role broke on it, because the contract is role-agnostic); empty/extra comma/repeated → named 400s; deployed surface as the universe; absent → `SELECT *` byte-identical. Published in `/openapi.json` on list/get/subroute with the available names. | `pkg/query/fields_test.go` (13); `pkg/integration/fields_test.go` pins for ONE request the SQL the engine emitted (pgx tracer), the TOAST blocks it reads with rows consumed (`pg_statio`, forced flush in the same tx, single-backend pool — `EXPLAIN ANALYZE` discards rows and detoasts nothing) and the bytes: 1.39 MB / 190 blocks / ~100 ms → 1.8 KB / 0 blocks / ~4 ms; gate 163 = 151 SAME + 12 DIFF (the contract + 11 new rows, each explained) |
| **GraphQL pushes its selection set into the SQL** | `listProjection`/`objectProjection` → `SelectOnly`; hidden fields dropped from the SQL and still `null`; any selection the walker cannot vouch for keeps `SELECT *`. The prompt's premise ("GraphQL already selects natively") was true on the wire and false on disk: it ran `SELECT *` and selected in Go. | integration subtest: `data { id nit }` → `SELECT "id", "estado", "nit"…`; hidden `data` not read |
| **The `/app` asks for what it paints** | every list/board/CSV/label request carries `fields=` (visible columns + state field + title candidates); `wholeRow` re-fetches by id before the form/detail; the teaching copy does the same. Two bugs of my own caught only by the browser: `titleFields` not imported (ReferenceError; `node --check` cannot see it) and the 403 above. | four schemas in browser (vet 11/11, fresco 12/12, conjunto 11/12 — the miss is the script expecting a chip literally «true» for a boolean filter; the API narrows 28 → filtered with the projection —, retotr admin + `contador` 0 px overflow / 0 console errors, footer «consulta 15 ms · 15 de 46.119») |
| **The measurement** (BENCHMARKS §4b) | rebuilt case: 46,119 rows × 52 KB (TOAST 1.8 GB, heap 7.5 MB). Page 1: 961,702 B / query 53 ms → 3,059 B / 1.2 ms; page 1000: 294 → 15 ms; last page 139 → 59 ms (the OFFSET stays). 10 pages: 1,300 TOAST blocks → 0. k6 10 rps × 60 s random pages: p50/p95/p99 174 ms / 2.25 s / 2.8 s (575 MB) → 20 ms / 81 ms / 175 ms (2.2 MB). The base-plain arm's 2.76 s was saturation order, attributed with an alternating 6 rps series (base 116/93 vs new 102/77 ms med). | `/tmp/mf/measure.sh`, `/tmp/mf/alt.sh` (the 105) |
| **Default omission of heavy fields — NOT built** | a contract break and "heavy" is not a type; proposed as a per-field declaration `"list": "on_request"` → **SCHEMA-8**. | ADR-029 §3 |
| **Gates + deploy** | unit ok · lint 0 · full lane 44 ok · gate above · ABBA read (benchblank, 100 rps × 30 s × 8 × 4 arms, both binaries built from worktrees with `build-engine.sh` — OPS-34): A 0.594 / B 0.612 / B2 0.816 / A2 0.821 ms p50 → A/B +2.9 %, A2/B2 +0.6 % `no_change` ×4; the base-vs-base +40 % between halves is host drift hitting both binaries alike (declared, not a direction) · 58: vetapp `c0de87b-fields`, tiendita `commerce 95f5735-fields` + CLI, backups + golden md5 `7dcffa84…` intact, verify-petfriendly 20/20, verify-vitrina 22/22 (its two purchases swept: ordenes 15 → 13), `?fields=` proven by HTTPS on both (tiendita productos 1,254 → 273 B; petfriendly appointments 842 → 167 B), rollback drilled both ways on both. **The 58 now has a 2 GB swapfile** (Miguel added it). | this file, 04 |

## DONE in MIGRACION-CONFIANZA-S1 (2026-08-28, 3rd session)

Engine + installer + docs. The thesis: every finding of a real migration
(Symfony 7.2 → v0.1.10, 23 tables, 46,119 rows, 1.2 GB) came from the
"receive a system that already exists" path the engine never exercised; this
session closes what costs SECURITY and TRUST and documents what is already
true, in damage order — and does NOT open the migration front (MIG-FRONT
above). Engine `bc69fe6` → this session's commits; Miguel cuts the tag.

| Item | What landed | Proof |
|---|---|---|
| **A · `install.sh` verifies what it installed** (the worst finding, the cheapest to fix) | Reproduced FIRST with real installs in an LXD container (Ubuntu 22.04, native Postgres + Caddy + systemd): a re-run over an existing `--app` by the "Update" line the summary itself prints (no `--schema`) KEEPS whatever schema is there — with VecinGo's under `/etc/retotr/`, `GET /api/asambleas → 200` on the retotr domain. (With `--binary` AND `--schema` both are replaced; the report's "kept the binary" did not reproduce — the verification covers it regardless of cause.) Now: the upgrade criterion is WRITTEN (kept: secrets/db/data/control port; always replaced: binary/unit/env layout/Caddy site/companions; schema: `--schema` replaces, else kept AND verified — byte-identical to, or same `name` as, a sibling `/etc/<other>/schema.json` → stops BEFORE any mutation, naming the app; the owner is the app whose schema `name` matches it); post-install `verify_installed`: binary sha256 == `--binary`, `/health` version locally AND through Caddy == the installed binary's `version`, schema on disk == `--schema`, companions executable, `appximo-cli tenant --help` answers — any mismatch = exit 1 with the service up. Also: the port preflight decides "ours" by the PID holding the port (the old "our unit is active" test let a re-run take a neighbour's port); companions resolve from `SELF_DIR` captured before any `cd` (the `cd /tmp` in the postgres step made a relative `$0` resolve to /tmp — the report's "no exec bit" was the wrong diagnosis; install(1) sets 0755 anyway); a stale `appximo-cli` symlink to a CONSUMER binary is removed and named; `--internal-tls`, `--scripts=DIR`; apt waits for the dpkg lock (unattended-upgrades on a fresh box killed re-runs); the Caddyfile is no longer backed up on every OPS-10 re-run. | Three real paths + two extras: clean install (vecingo, v0.1.9) `verified — installed == asked`; legitimate upgrade (vecingo → v0.1.10, no `--schema`, while retotr held a copy of its schema) EXIT 0 verified; foreign schema (retotr on v0.1.9 + vecingo's schema, re-run v0.1.10 without `--schema`) EXIT 1 with binary/env/unit/`/health` byte-identical before and after; with `--schema=retotr.json` EXIT 0, `/health` v0.1.10 locally and via Caddy; port collision (`--port=8094` held by vecingo) EXIT 1 "nothing was installed"; consumer stale symlink removed+named. Logs: `evidencia/MIGRACION-CONFIANZA-S1/inst/` (internal) |
| **D-bis · RAM + swap** | The installer reads MemTotal + SwapTotal; ≤ 2 GiB with NO swap → a loud warning with the swapfile recipe and the risk written (a bulk load makes the kernel OOM-kill the PostgreSQL every app shares — the migrator's own root-cause correction: 957 MiB, no swap, five apps down; the same load absorbed with 2 GB swap); repeated in the summary; never blocks. Documented in PRODUCTION.md §Prerequisites (+ two troubleshooting rows) and the lifecycle spec. | The container (1963 MiB, no swap) shows the warning on every run; `docs/PRODUCTION.md` |
| **B · The validator false positive** | Reproduced with the report's shape (`declarations.related_user_id` → `users`, `contador` scoped by it): runtime admin 6 · U1 5 · U2 1 · cross GET 404 · cross filter 0 — while `validate` said "matches NO rows, ever" and proposed a rename. Decision: KEEP the check (it caught a real zero-rows deployment, AUTHORING_JOURNEY 5-1), reword it as a QUESTION (both id spaces named, "the validator cannot tell which; you can — one request as a scoped role answers it"), put "keep the column exactly as it is" FIRST, never say rename; the other answer is a declaration (`references: "user_id"`). Same treatment for `identity_condition_on_implied_relation`. Swept the other name heuristics (`bare_condition_variable`, `auto_update_intent`, `missing_timestamps_convention`, `required_text_without_min_length`, `graphql_list_query_shadowed`): none asks for a rename of a DDL name (one asks to DECLARE, one to add). **Rule written** (warnings.go + AGENTS.md): the validator never pushes an author to change a name that comes from an existing DDL; parity with the source is a value. Rejected: removing the check (loses a real catch) and a new grammar key to declare identity (meta-schema + Studio + LLM grammar for a non-blocking warning). | `pkg/schema` `TestWarnings_IdentityConditionIsAQuestionNeverARename` (bans "rename"/"NO rows, ever"/"shows nothing", requires "CHECK"/"cannot tell"/"keep the column" first); the probe log `evidencia/…/probe.log` |
| **C · Canonicalization documented** | Verified with requests on `json` AND `jsonb`: keys re-sorted (recursively; array order kept), `0.0100…0859375` → `0.01` and `1.50` → `1.5` (same float64, no loss), `12345678901234567890` → `…67000` (ENG-50, the one loss, both directions, both types); the JSON-text STRING door on a `json` field keeps numeric text + key order (compacted) — the exact door, verified. Written in backend-spec §2 (with the parity recipe: canonicalize both sides — Python/Go/PostgreSQL one-liners), SCHEMA_REFERENCE §3, AGENTS.md, and an ADR-028 addendum with the table. "exact bytes preserved" swept: 0 in docs/README/site (the only mentions are historical — the audit and the ADR's rejected alternative). | `probe.log`; `grep -c 'exact bytes'` = 0 on backend-spec/README/site/SCHEMA_REFERENCE/GUIDE |
| **D · `/api/transaction` — the truth, and why it was "missing"** | Real requests against the PUBLISHED v0.1.9 and v0.1.10 binaries and HEAD: exists in all (admin 200, read-only role 403 naming op 0, per-resource role 200 on its own row, no token 401, GET 405, 101 ops → 400 "max 100", 100 ops → 200 ×100); since 2026-06 (`cf22c66`). **Why not found: `/openapi.json` — the document the engine calls the authority for EXISTENCE — did not list it in any version**; backend-spec mentioned it in one passing line. Now published (`x-appximo-transaction: true`, `TransactionRequest/Operation/Guard/Response/ErrorResponse`, `maxItems` = `DefaultMaxTxOps`) and backend-spec §2b "Writing from OUTSIDE the binary" carries the example, the measured cost (100 creates ≈ 50–70 ms; with 3 KB `json` docs each ≈ 100 ms; 100 single POSTs ≈ 1.06 s) and what does NOT exist (COPY, file importer, streaming). Reply for the external agent: internal `RESPUESTA_AGENTE_EXTERNO_MIGRACION.md`. | `TestOpenAPI_TransactionEndpointIsPublished`; gate DIFF `openapi-served-contract` (the ONE diff, this feature); `probe.log` |
| **D-ter · Backpressure — audit, then the minimal guard** | Audit (written in `pkg/resilience/memguard.go` and AGENTS.md): the engine had NO notion of host memory pressure — `GOMEMLIMIT` bounds its own heap only (never the problem: the memory that grows under a load is PostgreSQL's), the pool is a fixed 10 (`DB_MAX_CONNS`), the rate limiter counts requests, Route.Timeout bounds handlers, body/tx caps bound size — nothing host-derived. Guard: `APPXIMO_MEMORY_GUARD_MIN_MB` — while **`MemAvailable + SwapFree`** (never `MemAvailable` alone: on a Postgres box `shared_buffers` is Cached-not-reclaimable, tens of MiB at rest) is under the floor, data-plane WRITES (POST/PUT/PATCH/DELETE on `/api/*`, `/graphql`) answer `503` + `Retry-After: 5` + a body naming the measurement, the floor and the knob; reads/probes/auth flow. `/proc/meminfo` sampled ≤ 1/s by the first stale writer (CAS, no stampede); one atomic load on the hot path; unreadable meminfo never refuses. Default `max(32 MiB, 2 % of MemTotal)` — deliberately low; `0` disables; non-integer refuses to boot (ENG-47 rule). Wired after the tenant limiter in `app.go`. **Degradation, not capacity — said so in every doc.** | `pkg/resilience` 5 tests (available+swap, writes-only 503 with the named body, unreadable passes, ≤ 1 sample/interval, env contract); PRODUCTION.md env row + troubleshooting; ABBA below |
| **Gates** | unit ok · **full lane** (no `-short`): 43 ok + `pkg/extensions` `TestRunHook_WatchdogInterruptsInUnder100ms` red under load (the known watchdog flake — it ran while the gate, the LXD installs and three builds shared the one vCPU; green alone and green for the whole package re-run, untouched by this session) · lint 0 (after two cosmetic findings in the new file) · vet/gofmt · **gate 150 = 149 SAME + 1 DIFF** on the final binary (`openapi-served-contract`: the served contract now carries `/api/transaction` + its five components — the feature) · **ABBA — dirty first, then attributed, then clean.** PATCH protocol (erp_patch.js, nimbus, 100 rps × 30 s): A 4.689 / B 5.371 / B2 5.129 / A2 4.685 ms — the two NEW arms +10–15 % with base-vs-base identical: a signal, not noise. Attribution at the same hour (8 runs): new with the guard OFF (`APPXIMO_MEMORY_GUARD_MIN_MB=0`) 5.070, new with the guard ON 5.036, base 4.636 → the guard is exonerated (−0.7 %); microbench `Allow()` 70 ns, the whole middleware 433 ns, one `/proc/meminfo` read 30 µs ≤ 1/s. Bisection (5 runs each): the base commit REBUILT from a clean worktree 5.101, HEAD minus the guard wiring 5.099, minus the OpenAPI change 5.350, minus the warnings change 5.071 — **the same base source, rebuilt, measures like the new family**; the original base file differs from its rebuild in 24 MB of bytes (buildinfo: `v0.1.11+dirty` + vcs stamps in the main tree vs `(devel)` in a worktree → a different layout of identical code), so the +10 % was a build-artifact effect on this host, not the change. Apples-to-apples (same-session builds): guard ON 5.036 vs base 5.101 → −1.3 %, `no_change`. Read protocol with same-session builds: A 0.602 / B 0.637 / B2 0.643 / A2 0.621 ms, crossings +5.8 % / +3.5 % with same-binary drift 3 % / 1 % — `no_change` ×4, said as "not resolvable above the noise". New OPEN: OPS-34 (the ABBA base must be built in the same session and tree kind as the new binary). | `evidencia/MIGRACION-CONFIANZA-S1/{gate,abba-patch,abba-patch-attrib,abba-patch-bisect,abba-read}.log`; `benchmarks/history.tsv` `mc-patch-{A,B,B2,A2,C,D,E,base2,F,G,H}` / `mc-read-*` |
| **The 58** | Backups first (`backup.sh` → `appitools-20260828-081125.dump`, vetapp `vetapp-20260828-081125.pre-mc.dump`, binaries/CLI/env/schema `.pre-mc`, golden dump md5 `7dcffa84…` untouched). Deploy: tiendita `deploy-update.sh --binary --cli` → `commerce 95f5735-mc` + CLI; vetapp swap+restart. Both journals log `memory guard: writes answer 503 while MemAvailable+SwapFree < 39 MiB` (2 % of the 2 GB box — the floor, not a refusal). From outside: `/health` new on both, `/openapi.json` lists `/api/transaction` on both. **Rollback drilled both ways** (tiendita → `95f5735-app`, vetapp → `46fab38-app`, `openapi-tx=0`; forward again, `=1`). **A regression of my own, caught by the outside-in suite:** `verify-petfriendly` 18/20 — the embedded `/app` listed `transaction` as a resource and GET'd it (405 in the console); fixed in `pkg/backofficeui/web/contract.js` + the teaching copy (skip `x-appximo-transaction`, and any collection path without GET), committed `25d66f5`, rebuilt, redeployed: vetapp `25d66f5-mc`, tiendita `commerce 95f5735-mc` (engine 25d66f5). Suites after: `verify-petfriendly` 20/20, `verify-vitrina` 22/22; the two e2e purchases swept off the golden data (13/13/9). | `evidencia/MIGRACION-CONFIANZA-S1/58.log` |
| **Docs** | AGENTS.md (installer criterion + verification, memory guard, the validator rule, canonicalization), PRODUCTION.md (prereqs RAM/swap, flags, the upgrade table, verification, env row, troubleshooting), backend-spec §2 + §2b, SCHEMA_REFERENCE §3, ADR-028 addendum, lifecycle spec §6, backoffice-spec (the batch door is an action, not a resource), README (one line), `serve --help`; handoff 03 (A-50), 04, 00, 05, registro; the external reply `RESPUESTA_AGENTE_EXTERNO_MIGRACION.md`. Commits `cac3eba` (engine/installer/specs) + `25d66f5` (the /app fix) + the backlog commit. | |

**What did NOT enter, and why:** the six migration-front findings (MIG-FRONT
above — a product decision); a grammar key to declare an identity column
(rejected, see B); a host-derived pool size (registered under MIG-FRONT #2);
`install.sh --app` on the LIVE 58 (OPS-11 stays OPEN — this session exercised
the multi-app path for real, but in a container, not on the box with two
public demos). Environmental note for the 58: `swapon --show` is EMPTY on the
2 GB box — the new installer would warn there; adding a 2 GB swapfile is a
one-line ops action recommended to Miguel, not done by this session.

## DONE in APP-PODER-S1 (2026-08-28)

Engine + the embedded `/app`. The thesis: the panel is the vitrina every
visitor touches and it had contract left unused — a footer that said
«Página 1 · 15 en esta página» without saying of how many, a detail that was a
field list, an `x-appximo-json` tag with no editor. Six parts, in damage
order, all of them derived from `/openapi.json` (a condition per resource
would have failed the session). Engine `cdf4e85` → `46fab38`; Miguel cuts
the tag.

| Item | What landed | Proof |
|---|---|---|
| **A · Honest pagination** | Audit first: the engine DID return the total (`?count=true` → `meta.total`/`total_pages`, the exact `COUNT(*)` over the same filtered, RBAC-scoped set) and the panel already asked for it on page 1 — it just never showed it in the footer. Every list now pages by NUMBER (a cursor gives no «de N» and no «ir a»); footer «Página 1 de 2.414 · 15 de 36.201»; sizes 15/25/50/100/250 (15 stays the default, remembered per resource in the browser); first/prev/go-to/next/last. No estimated count: the exact one, and its cost on screen. | 36k-row ERP fixture (`nimbus.solicitudes`) in a real browser: page 1 with the count `query 10 ms`, sorted 27 ms, `search` ILIKE 15 ms + COUNT ~225 ms, page 2000 by OFFSET 343 ms — all visible |
| **A · The query time** | A small ENGINE change: every generated read publishes `Server-Timing` (`jwt`, `rbac`, `query`, `count` — its own stage now —, `app`; a response-cache HIT says `cache;desc="hit"`; the singleflight miss replays the producer's header, which used to be lost). Set by the handlers that own the response — no wrapper writer (the FILES-FIX-SENDFILE lesson). The footer shows «consulta 7,8 ms · respuesta 66 ms»: the engine's number + the round trip, independent of how many rows are painted. | `pkg/observability` unit test (Spans/ElapsedUS), `pkg/cache` unit test (replay on miss, `cache` on hit), `pkg/integration TestTracing_EndToEnd` (header on a served read; the HIT assertion conditional on `X-Cache`) |
| **B · Detail with relations both ways** | Audit: the schema's `relations`/`?include=` block is NOT in `/openapi.json`; what is: every FK (`x-appximo-relation`/`-references`) and the read subroute paths. So the detail resolves PARENTS through the published subroute (target RBAC enforced there; a lookup on the referenced column otherwise) and CHILDREN as every resource whose FK points here (a filtered list with count, peek, «Ver todos» → the list pre-filtered with a pinned chip; junction rows named by their other relations); the state machine as an ordered strip with the legal moves as buttons; files via signed URL/preview; each block fetches on its own and degrades to an inline notice; a legacy non-JSON text shows with a badge (no `::json` cast can run). Row click → detail (the pencil still opens the form); the detail has its own URL. | fresco `alquileres` (2 parents, 2 children, lifecycle), conjunto `pqrs` (files, 2 parents) desktop + 390×844; `see all` → pinned filter «Alquiler: ALQ-2026-207» |
| **C · JSON editor** | For `x-appximo-json: text|jsonb`: highlight layer (keys/strings/numbers/literals) under a transparent textarea, validation as you type with the parse error named, Formatear/Compactar, a foldable tree view, Tab indents; invalid JSON never leaves the browser (field painted) and the engine's 422 paints the same field. The two limits SAID: 1 MiB per request (≥ 900 KB warns, ≥ 1 MiB blocks: «responde 413») and ENG-50 (16+ digit integers / decimals with trailing zeros found on the RAW text before the parse loses them, named). What is saved re-opens natively. | `jsoned.mjs` 12/12 on fresco (jsonb) and tiendita (json): invalid flagged + blocked, `12345678901234567890, 1.50` named, format, tree, save → parity, 1.1 MB blocked |
| **D · Columns, views, URL** | Column picker over every listable field (structural order = default), saved views per resource in `localStorage` (columns, filters, sort, search, size) — zero engine state — and the current view in the hash (`#/res?cols=…&f.k=v&sort=…&per=…&page=…`; detail `#/res/id`). | `views.mjs` 8/8: a fresh page on the shared URL restores the columns; the detail URL opens the detail on a phone |
| **E · CSV + bulk** | CSV of the loaded page (visible columns + id, RFC 4180, BOM) or — saying first how many pages of 250 and the 10,000-row ceiling — everything filtered. Selection (a «Seleccionar página» button on phones) → bulk bar: change state (targets = union of legal moves; rows without one counted and skipped) or delete, via `/api/transaction` in batches ≤ 100 with a progress bar; a failed atomic batch is retried row by row so the failure is NAMED («cannot delete: still referenced by "alquileres"») while the rest goes through; failed rows stay selected; explicit confirmation listing how many and which. Demo: the overlay is applied row by row, ZERO requests leave the browser (verified by request capture), and a hand-made `/api/transaction` with the demo token is 403. | `bulk.mjs`: 50 selected → 45 deleted, 5 named 409; transition ×3; `demo-bulk.mjs` (390×844) 5/5 |
| **F · Relation search** | Past 100 rows (the `per_page` cap = the most a select can hold completely) the relation control becomes a debounced `?search=` box showing the target's title field; the list resolves labels the cached page lacks (≤ 40 lookups/page). | `relsearch.mjs` 6/6 with 121 clients |
| **Four schemas** | Generic browser e2e (driver adapted to detail-first rows) on the fixed SPA: vet 11/11 · conjunto 16/16 · fresco 12/12 · tiendita 15/15; report shots desktop + 390×844 for the four + the ERP, 0 px overflow, console clean. Found and fixed on the way: a popover with `display:flex` ignored the `hidden` attribute (painted over the headers, invisible to the eye at first, fatal to a click) → `[hidden]{display:none!important}`; an inline `style=` (CSP) → CSSOM only, and never re-serialize a styled node. | `/tmp/ap/logs/e2e-*.log`, `evidencia/APP-PODER-S1/shots` (internal) |
| **Gates** | unit 44 ok · full lane 43 ok + the tracing test (HIT assertion made conditional, green) · integration/e2e/resilience ok · lint 0 · vet/gofmt · **gate 150 SAME / 0 DIFF** (`/app` is not in the corpus; `Server-Timing` excluded from the header diff as timing noise — documented in the script — and pinned by the tracing test instead) · **binary +81,540 B**: SPA +75,761 B (app.js 59,915 → 112,570; style.css +12.8 KB; i18n +9.2 KB; contract +1.1 KB; gzip app.js 17.6 → 31.8 KB) + ~5.8 KB of Go. **ABBA frozen no_change ×8**: PATCH (8×4) clean-run medians A 4.472 / B 4.480 / B2 4.483 / A2 4.507 ms (≤ 0.8 % apart, 1–3 host stall runs per arm, declared); read (5×4) A 0.610 / B 0.639 / B2 0.649 / A2 0.597 — the cross deltas +4.8 % / +8.7 % with same-binary drift −2.1 % / +1.6 %: a ≤ 0.05 ms read-side cost (the header build) cannot be excluded; per protocol `no_change`, said as «not resolvable above the noise», not «zero». | `benchmarks/history.tsv` app-patch-*/app-read-*; `/tmp/ap/logs/{gate,full-lane,abba-*}.log` |
| **The 58** | Backups (`appitools-20260828-051040.dump`, vetapp `.pre-app.dump`, binaries/CLI/env/schema `.pre-app`, golden md5 `7dcffa84…` intact). Deploy: tiendita `deploy-update.sh --cli` → `commerce 95f5735-app` + CLI `46fab38-app`; vetapp swap+restart → `46fab38-app`. **From outside as a visitor** (demo account, desktop + 390×844): tiendita footer «Página 1 de 1 · 14 de 14 · consulta 2,6 ms · respuesta 133 ms», detail with 6 blocks, bulk delete simulated 14 → 0 with ZERO write requests, JSON editor on `tipos_producto`, 0 overflow, console clean; petfriendly footer/detail likewise. `verify-vitrina` 22/22 · `demo-check` 8/8 · `verify-petfriendly` 20/20. **Rollback drilled both ways** (tiendita → `95f5735-json` → `-app`, vetapp → `f45d80c-json` → `46fab38-app`). **Two slips, declared:** the live script first used the vet TEST user (not a demo role) on petfriendly, so two bulk deletes were really sent — both **403 by RBAC** (`veterinarian` may not delete `appointments`), 17 rows intact, the panel reported the failure; and my residue sweep used a `created_at ≥ today` window that also matched 3 golden orders re-dated by the nightly reset → restored from the golden dump with the drilled `restore.sh` (13/13/9, new binary, PID 467526). | `live.log`, `58-suites.log` (internal) |
| **Docs** | backoffice-spec §10 (limits) + §10b (the seven additions), AGENTS.md (`/app` paragraph + the `Server-Timing` contract line), frontend-spec (timing from the engine), the gate script comment; handoff 03 (A-49), 04, 00, registro. | |

**What did NOT enter, and why:** nothing of the six parts was deferred. Not
built on purpose: an estimated count (no evidence it is needed at real
sizes; the exact one is measured and shown), `relations` in OpenAPI to use
`?include=` (the FKs + subroutes suffice and it would couple the panel to
the embed cast), a cross-page persistent selection (selection is per loaded
page; other pages are said and skipped). Left as a row: ENG-51 (custom routes
without `Server-Timing`). Tooling note: `/tmp/vit/tools/e2e-app.mjs` expects
the OLD row-click-opens-form UX; the adapted driver is `/tmp/ap/e2e-app.mjs`.

## DONE in MOTOR-TIPO-JSON-S1 (2026-08-28)

Engine. The thesis: a type that accepts an escaped string and answers 500 to
the same content as an object is not rejecting, it is failing — two defects
(acceptance, failure form) judged separately, and a design decision nobody
had written. Audit before any fix
([docs/audits/JSON_TYPE_AUDIT_S1.md](audits/JSON_TYPE_AUDIT_S1.md)), decision
in [ADR-028](adr/ADR-028-json-field-is-a-json-value.md). Engine `5bc6dda` →
`f45d80c`; Miguel cuts the tag.

| Item | What landed | Proof |
|---|---|---|
| **A · The seven probes** | 35 real requests per binary (REST create/PATCH/PUT, batch, GraphQL, jsonb as the reference) against HEAD, v0.1.9 and v0.1.8: object/array/number/boolean on `json` → **500** everywhere (`failed to encode args[0]: unable to encode map[string]interface {} into text format for text (OID 25)` — captured only in the trace store, NOT in the log); a string, ANY string, → 201 stored verbatim (`"hola mundo"`); every read returned the escaped string (get, list, GraphQL `String`); batch 500 `failed_operation: 0`; GraphQL rejected an object structurally; identical in v0.1.8 → the defect is as old as the type (`b32c969`). Branch: the driver, not a type dispatch. One code path (`BuildInsertArgs` / the SET builder) — the demos' seeders all send strings, which is why nobody saw it. | `evidencia/MOTOR-TIPO-JSON-S1/probe-{v019,v018,base,new}.txt` (internal repo); the audit doc |
| **A · The collateral finding** | The query breaker (`pkg/db.TenantDB.exec`) counted EVERY error as a database failure: 40 object POSTs → 500 ×22 then 503 ×18; **six ghost-field 422s → every write of the process 503 for 8 s** (reads unaffected), renewable by any caller with `create`. All versions. | reproduced on HEAD and on the 58 before the deploy |
| **B · The decision (ADR-028)** | A `json` field holds a JSON VALUE: on write every door takes object/array/number/boolean, a **string is JSON text** (the jsonb/Postgres/pgx convention), a non-JSON string is a **422 `rule:"type"`** naming the field — on `jsonb` too (it was an anonymous 400 from 22P02); on read every HTTP surface returns the value natively (list/get/create/update, subroutes, `?include=` via `::json` in SQL, GraphQL as the `JSON` scalar — SDL `String`→`JSON` declared —, SSE, batch results, admin browse, webhook payload). Column stays TEXT (canonical compact JSON text; no migration). `Ctx.Query` keeps the text as a `string`. OpenAPI: type-less document property + `x-appximo-json: "text"|"jsonb"` so `/app` renders a JSON editor for both. Rejected alternatives and the migration note in the ADR; "exact bytes preserved" retracted from every doc. | `docs/adr/ADR-028-json-field-is-a-json-value.md`; backend-spec §2 (+ the row-types line) |
| **C · One function each way** | `schema.CoerceJSONFields` (pkg/schema/jsonvalue.go) called from PrepareCreate, PrepareUpdate, CollectUpdate, the GraphQL create, and re-run after a before-hook on REST/batch/GraphQL (idempotent); `schema.PromoteJSONText` at every REST read site with a per-resource precomputed column list (empty → zero cost), in the batch results, the admin browse and the GraphQL `JSON` scalar's Serialize; `alias.col::json` in `rowObject` for embeds. | unit tests `jsonvalue_test.go`, `TestWriteCores_CoerceJSONFields`; integration `json_field_integration_test.go` (5 tests, every door, parity, embeds, subroute, filter, a 36 KB realistic declaration, the breaker) — **fails on the pre-fix worktree on every door, passes on the fix** |
| **C · ENG-49 closed** | `resilience.NewQueryBreakerWith(name, isFailure)`; pkg/db passes `isUnavailableCause` — the SAME predicate that decides the 503 (timeouts, connection failures, class 08/53/57P0x). 40 ghost-field 422s → 40 × 422 and the next write 201, on HEAD and on the 58 after the deploy. | `TestNewQueryBreakerWith_StatementErrorsDoNotCount`, `TestJSONField_ClientErrorsNeverOpenTheBreaker` |
| **C · Volume** | A complete declaration (contribuyente, 120 renglones with soportes, totales): 36,534 B → POST 15 ms, GET 10 ms on the dev box, byte-equal round trip; `?include=` embed of a json row + a json child; subroute; `filter[eq]` over the canonical text. | the integration test's log |
| **C · Four schemas + 1-B** | vet 11/11 · conjunto 12/12 · fresco 11/11 · tiendita 15/15 (generic CRUD in a real browser on the fixed engine; the state/board coverage of APP-VITRINA-S1 was not re-driven — resources without a lifecycle were chosen) + json probes: fresco `equipos.especificaciones` (jsonb) object round-trip, tiendita `tipos_producto.atributos_def` (json) object round-trip and the seeded string rows now read as objects, `/app` renders the JSON editor for it and a PATCH of an edited object saves and re-opens natively. 1-B against commerce `95f5735-json` on a scratch DB: `verify.sh` 23/23 · `verify-webhook.sh` 26/26 · `e2e-1b.sh` 50/50 · `e2e-browser.mjs` 21/21. | `/tmp/mj/logs/{vit-e2e,1b-*}.log` (copied to the internal repo) |
| **D · Gates** | unit `-race -short` 0 · full lane (no `-short`) 44 ok · integration ok · e2e ok · resilience ok · lint 0 · vet/gofmt clean · **binary-diff gate 150 = 141 SAME + 9 DIFF** — the 8 new json corpus rows (object 500→201, array 500→201, string-as-text 201→201 with the OBJECT back, invalid string 201→422, jsonb invalid 400→422, PATCH object 500→200, PATCH `""` 200→422, GraphQL object structural-error→object) + `openapi-served-contract` (the `extra`/`attrs` properties: type-less + `x-appximo-json`). **ABBA frozen** (`appximo-base` = pre-fix HEAD, `appximo-new`): PATCH protocol (`erp_patch.js`, nimbus, 100 rps × 30 s × 8 × 4 arms) **`no_change` ×4** — with host stalls named: two whole runs at p50 ≈ 1.3–1.5 s (B2#2, A2#6) and p95 spikes on both binaries, sign-symmetric; clean-run medians A 4.27 / B 4.73 / B2 4.53 / A2 4.55 ms (A→B +11 %, A2→B2 −0.3 %, same-binary A→A2 +6 %: the cross deltas invert and the same-binary drift exceeds the B2/A2 delta → no binary effect resolvable above host noise). Read protocol (`sustained_2krps.js`, benchblank, 5 × 4 arms): **`no_change` ×4** (−0.3 %, +4.2 %, control −4.2 %, +0.1 %; p50 0.59–0.61 ms). | `benchmarks/history.tsv` json-patch-*/json-read-*; `abba-{patch,read}.log` |
| **E · The 58** | Backups first (`backup.sh` dump `appitools-20260828-012912.dump`, `pg_dump` of vetapp `vetapp-20260828-012924.pre-json.dump`, binaries/CLI/env/schema `.pre-json`, golden dump untouched md5 `7dcffa84…`). **The report's case reproduced LIVE before the deploy** on the tiendita as `dueno`: `POST /api/tipos_producto {"atributos_def": {...}}` → **500**, the GET an escaped `str`. Deploy: tiendita `deploy-update.sh --cli` → `commerce 95f5735-json` + CLI `appximo f45d80c-json` (PIDs 447329 → 461585); vetapp swap+restart → `appximo f45d80c-json` (445453 → 461771). **After: 201, GET returns the object, PATCH object 200, escaped string still 200, `"hola mundo"` 422 naming the field, the three seeded rows read as objects, 40 ghost 422s then a legal write 200, OpenAPI `x-appximo-json: text`.** Outside: tiendita `verify-vitrina` 22/22 (desktop + mobile, e2e purchase in both), `demo-check` 8/8 (both ways, zero writes, 403 ×4), petfriendly 20/20. **Rollback drilled both ways** (tiendita → `95f5735-authz` → `95f5735-json`, PIDs 462075 → 462202; vetapp → `6429a00-authz` → `f45d80c-json`, 462308 → 462392). Probe row deleted; the two e2e purchases + their clients swept — demo back on the golden counts (13/13/9). | `/tmp/mj/deploy/*.sh`, `58-verify*.log` |
| **F · Docs** | AGENTS.md (type table + the json/jsonb paragraph), backend-spec §2 + row types, SCHEMA_REFERENCE §3/§4.8, GUIDE, backoffice-spec §2 row, the LLM grammar (`pkg/aigen/prompt.go`), the audit, ADR-028; release-note draft extended (`RELEASE_NOTE_v0.1.10.md`, internal) with the migration note + the query for legacy non-JSON rows; the answer to the external agent (`RESPUESTA_AGENTE_EXTERNO_JSON.md`, internal — the five questions were not in the repo verbatim; they are answered as the report's five diagnostic questions, for Miguel to adjust before forwarding); handoff 03 (A-48) / 04 / 05 / 00 / registro. | |

**Found and NOT fixed (each with its row):** ENG-50 (number fidelity beyond
float64 — engine-wide, both directions). **Known and documented, not a row:**
a legacy `json` row holding non-JSON text (only possible from an engine before
ADR-028) reads back as a string on the Go surfaces but breaks the `::json`
cast of an `?include=` embed of that resource with a 400 until fixed — the
release note carries the query; the project's own apps have none (checked on
the 58: 2/2 rows valid JSON). `examples/backoffice-guide/web/contract.js` (the
teaching copy) has no JSON control at all — left as is (documented as "may
diverge in polish"); the embedded `/app` is the reference.

## DONE in MOTOR-AUTORIZACION-S1 (2026-08-27)

Engine and security. The thesis: an ownership column a client can rewrite is
privilege escalation between users, every app inherits it, and it is unlikely
to be alone — so the session AUDITED THE CLASS before touching a line
([docs/audits/AUTHZ_WRITE_AUDIT_S1.md](audits/AUTHZ_WRITE_AUDIT_S1.md)). ENG-47
is the mirror image: a defence without a valve. Engine `f9c6ba4` → `6429a00`;
Miguel cuts v0.1.10.

| Item | What landed | Proof |
|---|---|---|
| **A · The audit, before any fix** | One schema declaring every class (identity/PK, `auto` audit, ownership in the three RBAC forms, a literal condition, `tenant_id`/`created_by` as plain columns, state machine, `file`, `import`, allowlist), 61 real requests per binary through REST create/update (PATCH+PUT), GraphQL, `POST /api/transaction`, `Ctx.Update` (a custom route), files upload, signup and login — fired at HEAD, **v0.1.9** and **v0.1.8** (downloaded, checksum-verified), the stored effect read back as admin after every write. | `results-{base,v019,v018}.md` in the internal repo (`evidencia/MOTOR-AUTORIZACION-S1/`) |
| **A · Findings by damage** | (1) **The row give-away on update — in ALL THREE binaries**, five doors, three RBAC forms: `PATCH {"owner_id": B}` on an owned row → 200 and B reads it; `null` → orphan; a PUT omitting a nullable condition column → NULL. (2) The allowlist hid the same attempt silently (200, unchanged). (3) A state field set to null → **500** at every door (PUT on any lifecycle resource failed unless the client re-sent the state). (4) v0.1.8 only: forged `id`/`created_at`/`updated_at` stored with 201 (closed in v0.1.9). (5) ENG-47. NOT in the class, verified: `tenant_id` (tenancy is the schema, never a column), `created_by` unless it is the role's own condition column, FKs to identity, upload extra parts, the signup `role`. ENG-45 re-read: #1 this class (closed), #6's `hmac_secret_env`-empty-key is security-adjacent (re-worded), the rest functional. | the audit doc's table |
| **B · ONE policy, from the contract** | [ADR-027](adr/ADR-027-identity-column-server-owned-on-update.md): for a condition bound to `$user_id`/`$external_client_id` (`rbac.WhereCondition.Identity`, one predicate `rbac.IsIdentityVar`), the column is server-owned on UPDATE as on create — `codegen.EnforceUpdateRBAC` in `pkg/codegen/rbac_write.go` beside the create half; another id / null → the SAME 403 message; PUT-omitted → the caller's value; own id → no-op; judged on the client body BEFORE the row lookup (never an existence oracle) and BEFORE the allowlist (explicit, never dropped). Wired at REST PUT/PATCH, GraphQL `update…`, batch update, `Ctx.Update`. A literal condition stays a visibility filter (the moderator keeps approving — tested). Server path: an unscoped role or a handler on `UnsafeTx`. | `ownership_update_integration_test.go` (every door × form × case) — FAILS on the previous code at every door; `rbac_write_test.go` |
| **B · State field never null** | `codegen.StateFieldNullViolations` (one source: CollectUpdate + PrepareUpdate): PATCH null / PUT omission → 422 `rule: "state"` naming the field. | same test file; live on both demos (dueño PATCH `estado: null` → 422) |
| **B · The four schemas** | tienda (commerce, `dueno`, 17/17), veterinaria (13/13), conjunto (18/18), the FRESH alquiler audiovisual (19/19) in a real browser on the fixed engine — CRUD, transitions, board drag, relations, files, delete, mobile. Plus a scoped-role probe per schema (`veterinarian`/`tecnico`/`residente` — all `references: user_id` FKs): give-away 403 through PATCH and batch, own id 200, legal transition 200, state null 422, the unscoped admin transfers (200) and the row leaves the scoped principal (404). | `/tmp/vit/tools/e2e-app.mjs` logs; `scoped-probe.py` |
| **C · ENG-47 closed** | `APPXIMO_AUTH_LOGIN_ATTEMPTS_PER_MINUTE` / `_BURST` (`Config.AuthLoginAttemptsPerMinute/Burst`, mapped per app in the fleet), default **exactly 5/5**; above the default → boot WARNING naming the weakened guard; a non-integer → refuses to boot (never a silent fallback on a security knob). Documented with the risk in README, PRODUCTION, lifecycle-spec, backend-spec, `serve --help`. Proven three ways: default → 6th login 429 (unit+integration+real binary); raised → passes; invalid → boot error. **Applied on the tiendita demo only** (`APPXIMO_AUTH_LOGIN_ATTEMPTS_PER_MINUTE=60`, one commented line in its env, reversible): from outside 8 wrong logins as the public account → 8 × 401; petfriendly keeps the default → 6th = 429. Same knob-less shape found and NOT touched: the platform-admin throttle → ENG-48. | `loginlimit_*_test.go`; the 58 |
| **D · Gates** | unit `-race -short` 0 · full lane (no `-short`, Docker) 44 ok + the known `TestRunHook_WatchdogInterruptsInUnder100ms` timing flake under load (green alone, 0.105 s) · lint 0 · vet/gofmt clean · **binary-diff gate 142 = 133 SAME + 9 DIFF, every DIFF one of the 11 new `owner-*` rows**: state-null 500→422 (×2), tx give-away 200→403, GraphQL give-away data→errors, and — on the base side the tx row had already given the row away — put-other/patch-null/patch-give-away 404→403, put-omits-owner and reads-own 404→200 (the cascade of the hole itself; the two rows that must NOT change, own-id no-op 200 and hidden-row 404, are SAME) · **ABBA with frozen binaries on the PATCH protocol** (`erp_patch.js`, nimbus, rrhh-admin, 100 rps × 30 s × 8 runs × 4 arms on :8281): **`no_change` on all four crossings** — A/B −7.0 %, A2/B2 −8.0 %, base-vs-base +5.2 %, new-vs-new +4.1 % (the controls are the same size as the deltas: the 105's floor); p50 base 4.83/5.15 ms, new 4.20/4.57 ms · the 36 `solicitudes` rows the bench's setup() created were swept (36 201 → 36 201). | `benchmarks/history.tsv` `authz-*`; gate log |
| **D · 1-B suites** | Against the new commerce build (`95f5735-authz`, engine `6429a00`, only the module replace changed): `verify.sh` 23/23 · `verify-webhook.sh` 26/26 · `e2e-1b.sh` 50/50 · `e2e-browser.mjs` 21/21. | logs |
| **E · The 58** | Backups first: PIDs (440627/439236), `backup.sh` dump + `pg_dump` of vetapp (`vetapp-20260827-022111.pre-authz.dump`), binaries/envs/schemas `.pre-authz`, golden dump untouched (md5 `7dcffa84…`). **The attack reproduced LIVE before the deploy** on petfriendly (engine dec6614) with a minted `veterinarian` token on an admin-created probe appointment: `PATCH veterinarian_id=<another vet>` → **200, stored**; batch → 200 (null → 422 `required`, that schema declares the column required). Deploy: tiendita via `deploy-update.sh` (+`--cli`), vetapp swap+restart → `95f5735-authz` / `6429a00-authz` (PIDs 444524/444564). **After: 403 / 403, the row stays the caller's; legal transition 200.** Tiendita from outside: `cliente`/`demo`/`empleado` → 403 on PATCH/POST/tx/DELETE. Stranger walks (clean Chromium, desktop + 390×844): tiendita 14/14, petfriendly 14/14; demo mode both ways live 8/8 with tiendita row counts identical before/after (ordenes 13 · productos 9 · clientes 13 …). **Rollback drilled both ways** (→ `95f5735-vitrina4`/`dec6614`, smoke green → back to authz, PIDs 445409/445453), attack re-run on the redeployed engine: blocked. Probe rows deleted (0 left). | logs `attack58-*`, `verify58-*`, `democheck58` |
| **F · Docs** | AGENTS.md (RBAC: the update side + state-null), README (RBAC line + config table), GUIDE, SCHEMA_REFERENCE, backend-spec (§3.3 Ctx comment, §3.5 layer 2, §5 auth), lifecycle-spec, PRODUCTION (env table with the risk), MODEL_LAB, `serve --help`, the `Ctx.Update` doc comment, ADR-027, the audit; the v0.1.10 release note + advisory recommendation drafted in the internal repo (`RELEASE_NOTE_v0.1.10.md`) — Miguel decides. | |

**Found and NOT fixed (each with its row):** ENG-48 (admin throttle knob),
RBAC-2 (allowlist silent drop for non-identity fields — a documented contract
to re-decide), ENG-45 #3 (Ctx.Update required-null 500, functional), ENG-45 #6
(`hmac_secret_env` empty key — now worded as security-adjacent), ENG-41 met
in the field: petfriendly's production schema declares `veterinarian_id`
`required` so a veterinarian cannot CREATE an appointment via the API (422
before RBAC forces the column — the audit had to create the row as admin).
**A hygiene slip to know about:** a `source` of a token file with hyphenated
names printed six minted demo tokens into the session transcript (the 58's
`veterinarian`/`owner`/`cliente`/`demo`/`empleado`/`dueno`, exp 2026-08-28
02:21 UTC); they expire on their own, the transcript is private, and no
secret (JWT_SECRET, passwords) left the box.

## DONE in TIENDITA-VITRINA-S1 (2026-08-26, 6th session of the day)

The two public demos made to show what they SELL. `tiendita.appximo.com` gets
the hottest traffic the business has — whoever clicks "pruébela" is already
considering buying — and it was showing what the owner's CUSTOMERS would see:
the only link to the panel lived in the footer, in fine print, at **1 466 px of
scroll on desktop and 1 736 px on mobile**. A hardware-store owner left without
knowing there was a panel. No engine code: commerce was rebuilt against the
SAME engine commit the box runs (`dec6614`) through `-modfile` + a worktree, so
only the SPA changed (proved: `git diff 69ad3f1..HEAD` is empty outside
`web/` and `scripts/`).

| Item | What shipped | Proof |
|---|---|---|
| **A · Two explicit, persistent modes** | An ink bar under the WhatsApp return bar carries a segmented control — «Así lo ven sus clientes» / «Así lo ve usted» (mobile: «Sus clientes» / «Usted · el panel»). Sticky in BOTH modes, distinguished from the green bar by surface, shape and place. The mode IS the route, so a reload keeps it. | Live on both viewports; `verify-vitrina.mjs` 22/22 × 3 runs |
| **A · No login wall** | Tapping «Así lo ve usted» does NOT land on a form: it signs in with the public demo account (read-only server-side) and lands on the board. **0 px of scroll** to learn the panel exists; **0.4 s (desktop) / 0.5 s (mobile)** to see the offer; **1.0–2.3 s** to be inside the panel. | measured, medians of 5 / 2 live |
| **A · The panel says what it is** | A context strip — «Este es el panel del dueño — pedidos, inventario, clientes y las ventas del día» — above the untouched demo-mode notice. | screenshots; asserted in the suite |
| **A · petfriendly too** | Its portada carries the same control («Qué es esto» / «Así lo ve usted» → `/app`) on the same system, and the ENG-46 banner now words the return the same way (env only). The engine's `/app` cannot host a segmented control — the ENG-46 banner is its single seam; documented asymmetry, not a gap. | `verify-petfriendly.mjs` 20/20 |
| **B · The shop on the system** | Inter bundled (CSP `font-src 'self'`), paper/ink/white surfaces, one accent by variable, generous radii and soft shadows, `tabular-nums` on money, chips with a dot, shimmer skeletons, empty states with an action, inline errors, toasts. Store rhythm, not back-office: bigger art, more air, a two-column cart and checkout with a sticky summary. | screenshots desktop + 390×844 |
| **B · Photos that do not depend on photography** | The three products with no photo that passes the A-21 filter left the pastel gradient for a WOVEN PLATE of the brand palette — loom weave, a medallion, a cloth label with the initials — deterministic per SKU, **zero image bytes** (pure CSS gradients). Photo and plate share ONE frame, so the grid reads as a designed catalogue. The detail page shows the photo WHOLE on paper (`contain`), not cropped. | `01-vitrina`; `e2e-browser.mjs` B1c |
| **C · Motion, CSS only** | Named durations per element type: microfeedback/hover 100 ms, press 140, icon 200, card in 260 / out 170, sheet 340 / 230. `ease-out` in, `ease-in` out, `ease-in-out` on state change. Nothing loops except the skeleton shimmer, which exists only while content is pending. `prefers-reduced-motion` honoured. No new dependency. | `app.css` tokens; three motion states |
| **Demo mode, both ways** | Overlay create/edit/transition visible in session, reload resets, **ZERO** non-GET requests toward `/api`, hand-crafted POST/PATCH/DELETE/upload with the demo token → **403 ×4**, Postgres row counts identical. Run in production too. | `demo-check.mjs` 9/9 local, 8/8 live |
| **Two silent demo-mode gaps closed** | (1) A product created in the overlay 404'd on its own detail GET — "producto creado" followed by "no pudimos cargar el formulario". (2) A variant created in the overlay vanished on reload: the layer merged the product row but not its EMBEDDED array (`?include=variantes`). Both are the "accept and go on in silence" class. | `demo-check.mjs` rows 3–4 |
| **The public demo account's login quota** | Caught by the timing measurement: 1 of 5 runs landed on the login form. The engine throttles login per identity at 5/min with no knob → ENG-47. Bounded in the SPA: one retry at 1.6 s, then the merchant door WITH the reason written and a "Reintentar ahora". | quota exhausted on purpose: 4 × 429, honest door at 6.2 s, 0 page errors |
| **The nightly golden reset, repaired** | `demo-reset.service` would have FAILED at its next run (2026-08-27 04:15): an unquoted env value with spaces broke `redate-demo.sh`'s `. env`. Both env files quoted, both services restarted, the whole unit run end to end → `Result=success`. → OPS-33. | journal + `Result=success` |
| **Deploy + rollback** | `pg_dump` before every deploy (the golden dump untouched: 89 291 B, md5 `7dcffa84…`), `deploy-update.sh` (backup → atomic swap → health), rollback drilled BOTH ways on the tiendita (to `69ad3f1-vitrina` and back to `95f5735-vitrina4`) and on petfriendly (portada + banner env). PIDs 434687 → 439233+; the demo left on the golden (13 orders) and verified read-only afterwards. | `/health` versions at each step |
| **Suites** | `verify.sh` 23/23 · `verify-webhook.sh` 26/26 · `e2e-1b.sh` 50/50 · `e2e-browser.mjs` 21/21 (two selectors re-pointed on purpose: `.sold-out` → `.badge.out`, the add-to-cart banner → `.toast`). The 7 `e2e-1b` failures seen first were the missing seed env — **identical on the previous binary**, so not a regression. | logs |
| **Cost** | SPA payload 111 → **168 KB** (+57: Inter 38.7 KB + ~18 KB of CSS/JS); first paint +120 ms and first tile +150 ms in a paired A/B/B/A on the same box and DB (n=12 per arm). The page's real weight is the photos, not the SPA → COMMERCE-11. | `medir-carga.mjs`, paired local runs |

## DONE in APP-VITRINA-S1 (2026-08-26, 5th session of the day)

The embedded `/app` (the face every visitor touches: "Probar el panel", the
end of the idea→system video) rebuilt on the design system atina proved on
this engine — WITHOUT touching its generic nature. The engine cut-off line of
WRITE-ASYMMETRY-S1 was reopened by Miguel's decision for this one session;
ENG-45 stays the post-launch map, only the date moved. The data path did not
change (gate SAME on every data case; bench no_change on all four crossings (A vs B +0.7 %, A2 vs B2 −7.7 %, base-vs-base control +12.1 %, new-vs-new +2.8 % — the control is the largest delta, i.e. the 105’s noise floor; p50 base 0.649/0.673 ms, new 0.651/0.677 ms, 100 rps × 30 s × 8 runs per arm)).

| Item | What landed | Verified by |
|---|---|---|
| **A. The system, ported** | `pkg/backofficeui/web`: tokens on `:root` (ink, ONE accent + every derived colour via `color-mix`, surfaces, radii, shadows, easing, an 8-colour lifecycle palette BY POSITION), ink sidebar with monograms + counts + user block, page headers with eyebrow/`tracking-tight` titles, `tabular-nums` everywhere, tables with sticky uppercase headers/hover/sort arrows/keyset or offset pagination, positional state chips (terminal = hollow dot), enum chips, yes/no chips, money (`_centavos`) and dates formatted, relation columns RESOLVED to the target label, enum/state/boolean filters, list ⇄ BOARD (kanban derived from `x-appximo-transitions`; drag = legal transition, illegal columns dimmed, mobile tap-chips), home with stat tiles + dark hero, drawer/bottom-sheet forms with structural field order and chip-radio states, jsonb editor (type-less property), file widget with policy + signed-URL preview, two-step delete, toasts, shimmer skeletons, empty states with the action, painted 422s, inline-error + retry. Inter bundled (latin variable, 39 KB) — `font-src 'self'`; zero inline styles (`style-src 'self'`); zero CDN; hand-written static CSS; no build step; no new runtime dependency. Zero resource/state/domain names in the bundle. | 4 schemas in a real browser (below); `embed_test.go` pins font/CSP/self-containment/board/positional palette |
| **Theme hook** | The EXISTING seam (`Config.AppThemeCSS` / `APPXIMO_APP_THEME_CSS` → `/app/theme.css`) now re-brands the whole panel from ONE variable: `:root{--app-accent:#FF5A36}`. Proven with a coral theme on the fresh schema and deployed as the two demo brands (tiendita green `#1c9d5b`; petfriendly sage `#2f6b4f` on warm paper). A schema-level `branding` key was considered and rejected (the schema is the data contract). | screenshots `theme-coral-*`; the 58 |
| **B. Generic, proven generic** | Browser e2e (`e2e-app.mjs`, console-strict, desktop + 390×844): tienda (commerce schema, 14 resources) 17/17 · veterinaria (petfriendly's production schema) 17/17 · conjunto residencial (8 resources, 5 state machines, 3 file fields, a `references: user_id` FK — written from the VecinGo case study) 18/18 + 14/14 · a FRESH schema never seen (alquiler audiovisual: 6 resources, 3 state machines, an 18-column `equipos`, jsonb + gin, file fields, `references: user_id`) 19/19. Each run: home, sort, filter, 422 painted, create, search, edit, chip transition, board drag legal + illegal, file upload → attach, relation labels, delete, count restored, mobile sheet. Layout gate: 0 px overflow, 0 sub-24 px targets on every screen. | `/tmp/vit/tools/e2e-app.mjs` logs; shots in `/tmp/vit/shots/*` |
| **Demo mode, both ways** | `demo-check.mjs` on the tienda: demo role → overlay create/edit/board-move/delete visible in session, reload resets, **ZERO** non-GET requests toward `/api` (network listener), hand-crafted POST/PATCH/DELETE/upload with the demo token → **403 ×4**, Postgres row counts identical before/after. 9/9. Repeated from OUTSIDE on the 58 after the deploy (14/14 on both demos, three times each; tiendita Postgres row counts identical before/after every walk). | script + psql counts |
| **C. Gates** | unit `-race -short` 0 · full lane (no `-short`, Docker) 0 after one timing flake (`TestRunHook_WatchdogInterruptsInUnder100ms`: 100.47 ms under a fully loaded box — lint + gate + unit + full lane running together; green alone, the same flake previous sessions recorded) · lint 0 · vet/gofmt clean · **binary-diff gate 129 SAME + 2 DIFF, both intentional and DOCUMENTED IN THE CORPUS** (new rows `app-shell-served` — same status/CSP, new body — and `app-bundled-font` — 404 → 200 `font/woff2`) · **binary delta +104 KB explained byte by byte**: font +38,752 · style.css +26,845 · app.js +34,412 · i18n +4,687 · contract +3,509 · theme +571 · index +180 = +108,956 bytes of embedded sources → +106,496 bytes of binary · **ABBA with FROZEN binaries** (`5201c3f-base` vs `-vitrina`, blank fixture, 100 rps × 30 s × 8 runs × 4 arms on :8281): no_change on all four crossings (A vs B +0.7 %, A2 vs B2 −7.7 %, base-vs-base control +12.1 %, new-vs-new +2.8 % — the control is the largest delta, i.e. the 105’s noise floor; p50 base 0.649/0.673 ms, new 0.651/0.677 ms, 100 rps × 30 s × 8 runs per arm). | logs in `/tmp/vit/*.log` |
| **ENG-46 — closed** | `Config.AppBannerText/Href` (+ `APPXIMO_APP_BANNER_TEXT/HREF`) → `backofficeui.Options.Banner` → `/app/ui-config.json {banner:{text,href}}` → the SPA renders a sticky return bar as TEXT nodes + one validated link (http/https/mailto/tel/same-site; `javascript:`/`data:`/scheme-relative dropped to text-only). Login and panel alike. Pinned by `TestBannerSeam`. Deployed on petfriendly's `/app` (live on both: “← Volver a la portada de PetFriendly” and “← Volver a La Tiendita”, href `/`). | `embed_test.go`; the 58 |
| **Re-pointed suites (deliberately)** | `TestChromeBehaviorsPinned`: the mobile breakpoint pin 720 → **900 px** (the ink sidebar needs the room). `e2e-browser.mjs` (commerce 1-B) drives the SPA's own `/panel`, not `/app` — untouched, re-run green after the tiendita deploy (21/21 against the new commerce binary booted locally on :8281). | |
| **D. The 58** | Both demos deployed through the drilled protocol. **petfriendly (vetapp)**: pre-deploy backups `appitools.pre-vitrina` / `vetapp.env.pre-vitrina` / `schema.json.pre-vitrina`; binary swap eb4c659 → 7f33ede → 5bf4e0c → **dec6614** (PID 414742 → … → 434440); `APPXIMO_APP_THEME_CSS=/etc/vetapp/app-theme.css` (sage on warm paper) + `APPXIMO_APP_BANNER_TEXT/HREF` added to the env; rollback drill twice (back → font 404 + old ui-config, redo → 200 + banner). **tiendita (commerce 69ad3f1-vitrina, engine dec6614 in the module replace)**: pg_dump before each deploy (`appitools-20260826-2051…/2102…/2110….dump`, the golden dump untouched, 89,291 B), `deploy-update.sh` (backup → atomic swap → health → auto-rollback; rollback copies in `/opt/appitools/bin-rollback/`, pre-vitrina binary also at `/root/tiendita-bin-pre-vitrina`), env gains theme (the shop’s green) + `APPXIMO_APP_DEMO_ROLES=demo` + banner; rollback drill through deploy-update itself (f82db2d-frente back, 404 on the font, then the vitrina binary again, 200). PIDs 417240 → … → 434687. Verified FROM OUTSIDE as a stranger (clean Chromium, desktop + 390×844, console-strict): petfriendly 14/14 ×3 runs, tiendita 14/14 ×3 (the first tiendita run caught 3 probe 503s from the circuit breaker → fixed by batching the probe, redeployed, clean). | |
| **E. The video** | `idea-a-sistema.mp4` re-recorded on the redesigned panel — 58.0 s (54.4 s live + a 3.6 s held last frame for the closing line, the counter STOPS there), 2.74 MB, H.264 1280×800 + silent AAC, faststart, burned Spanish subtitles, ONE top-right box “GRABACIÓN REAL · SIN ACELERAR · tiempo real hh:mm:ss” (label + counter together, so nothing overlaps it — the previous session’s finding). Content: the command line as typed, `appximo new` (haiku, Spanish names asked for and obtained: bicicletas / clientes / ordenes_reparacion with a 6-state machine, VALID first try at 8.6 s, card at 9.8 s), then the browser: login with the printed credentials, home, create clienta → bicicleta → orden de reparación through the real forms, the BOARD, a drag recibida → diagnóstico, the chip changed in the list. Fictitious data. Frames verified visually. The previous video is ARCHIVED, not deleted: `assets/archivo/idea-a-sistema-2026-08-22.mp4` + poster + `NOTA.md` (Miguel’s decision). Landing: only the video, the poster and the caption duration (45 → 58 s) changed, through `tools/gen2.py` (the wa.me assert passed). | |
| **F. Docs** | `backoffice-spec` §2 (+ the type-less jsonb row) and §10b rewritten to the shipped panel (structure, derived screens, one-variable theming, positional palette, board, CSP self-containment); README `docs/img/demo/app-*.png` and the technical site's `shot-app.png` retaken on the new panel with the same real-estate demo (`docs/demo`); handoff (04/03 A-45/00/registro) + this section. | |

**Not ported, and why (the honest line):** atina's ScoreRing/matching, the
per-phase tabs with counts, treated photography of people, a drawn wordmark —
domain or raw material a schema does not declare. What the `/app` still lacks
that IS reachable generically: a record DETAIL page with its related rows
(`?include=` embeds from the `relations` block), column chooser + saved views,
bulk actions/CSV export (spec §10), a search-backed relation select past 100
rows, and a dashboard with aggregates (`/api/{r}/aggregate` is generic
already). Each is a backlog candidate, not a regression.

## DONE in HERO-Y-DIRECCION-S1 (2026-08-26, 4th session of the day)

Six direction corrections on the live landing plus the component hero
(internal decisions A-38..A-43). Engine untouched; the 58 untouched.

| Item | What landed | Verified by |
|---|---|---|
| Hero | ink + grain + a pre-blurred, never-legible texture (11 KB); a collage of BUILT components (paid order with golden-set data, notification card, the 58.2 % ring, an 18-module stat) with a one-pass staggered entrance; the registered triple eyebrow; the A-27 H1 complete on desktop (it used to be clipped); the sub says "trabaja solo: avisa, cobra y recuerda"; "lo ve funcionando antes de pagar" once per screen | captures desktop + 390×844; motion contracts 0 stuck ×3 modes; 132 KB of hero captures gone (page 270 → 164 KB) |
| Team voice (A-39, FINAL) | zero proper names on index/conjuntos/caso; team block with three true credentials; SOCIA-PENDIENTE retired; FOTO-PENDIENTE = team/work photo, never stock | `grep` = 0 on the three generated pages |
| Demos folded (A-40) | one-line strip `#pruebe` (tienda · agenda · conjunto); return bars untouched; no system counts in headings | nav + hero anchors resolve |
| VecinGo opens (A-41) | `demovecingo.appximo.com` audited from outside: valid TLS to 2026-11-17, demo mode without login, Spanish, fictitious-data banner, a radicar does not persist (silent no-op), 0 console errors → "Abrir el sistema ↗" on the card and on conjuntos | Playwright audit + screenshots |
| Curated captures | pf-panel (English column names), pf-thumb, atina-panel, tienda, tienda-hero removed; nothing published with English, raw tables or test-looking data | asset audit against the generated pages |
| Reach + contrast | "Desde Pereira, Colombia — para todo el país" everywhere; the real contrast bug (`.s-ink a` outranking `.btn-wa` → 1.24:1 light-green-on-green on four live buttons) fixed at the root; every button ≥ 7.76:1 | `contrast.cjs` (WCAG ratio per button over the effective background) |
| "Lo que no hacemos" (A-42) | rule: focus/principle limits only, never a capability we lack or seem to lack; two lines out, two in | rule recorded in 03 |
| Reorder (A-43) | VecinGo leads the cases as our case; atina is the centrepiece of "Para empresas con equipo técnico" (bars, video, open, own CTA — the 19th wa.me origin); external-developer attribution verbatim everywhere; hero stat 32 → 18 | wa.me set: 18 preserved + 1 new (generator assert) |

### Requires a decision from Miguel (added by HERO-Y-DIRECCION-S1)

| Item | Why it needs him |
|---|---|
| **VecinGo authorship in public material** | A-43 asks the landing to present VecinGo as "caso nuestro: lo construimos". The public technical material (docs/CASE_STUDY_VECINGO.md, FIELD_FEEDBACK_RESPONSE §3, the site) says an **independent developer** built it. Both cannot be true in public at once. The landing now says "hecha con nuestra tecnología… para un conjunto residencial" (true under either reading) and does NOT say "la construimos". If Miguel confirms authorship in writing, one line in `tools/gen2.py` changes AND the technical material must be corrected the same day; if the third-party account stands, the wording stays. |

## DONE in REDISENO-VISUAL-S2 (2026-08-26, 3rd session)

| Item | What was done | Verified by |
|---|---|---|
| **D1–D6 applied** | `assets/system.css` v2 with atina's compiled values (radii .9/1.25/1.75 rem, shadow-soft/lift, reveal .8 s/22 px, ink/35 + multiply layers, grain .18); Inter self-hosted (39 KB); ink `#0b1512` / white / zinc-50 alternating per section; green only as accent; serif + gold removed; ring (`.ring`), bars (`.bars`), initials (`.avatar`), chips, stat blocks; hero = 2×2 (mobile 1×3) grid of real captures incl. a live capture of petfriendly's demo panel; A-27 hero copy restored, IA chip out; team section names Miguel Acosta with `data-pendiente` markers. | live captures; request audit EXTERNAL=none; wa.me sets asserted identical (18 origins) |
| **Load** | live 5-run medians before→after: landing desktop 534→374 ms, mobile 369→451 ms, 303→270 KB; conjuntos 331/233 ms; caso 288/201 ms. | `perf.cjs` |
| **Contracts / rollback / demos** | 0 stuck × 3 pages × 3 modes, 0 loops; `git revert` reproduces the previous file byte for byte; e2e purchase with the bar on both viewports; petfriendly bar live. | `contracts.cjs`, `cmp`, `compra.cjs` |

## DONE in REDISENO-VISUAL-S1 (2026-08-26, 2nd session)

Visual execution only — content, CTAs (18 origins, set verified identical
before/after), claims and the wide hero (A-27) untouched; no engine code; the
58 untouched (bars re-verified, one more e2e purchase per viewport).

| Item | What was done | Verified by |
|---|---|---|
| **A. The system, applied** | `assets/system.css` + `system.js` shared by index/conjuntos/caso: token scales (accent 50→800, ink 950→500, radii, soft/lift/glow shadows), type scale 3xl→6xl with `text-wrap:balance`/`tracking-tight`, 11 px eyebrows, `tabular-nums`, semantic classes (`card`, `card-dark`, `card-accent`, `chip`, `input`, `skeleton`, `media`, `bframe`, `reveal`), nav transparent→solid, FAQ, mobile bar. Fonts bundled (Fraunces static 600 display 16 KB + Inter variable 39 KB, latin subsets via fonttools), `font-src 'self'` intact. | request audit: EXTERNAL=none on every page; the accent hex appears only in the token block |
| **B. Proof hierarchy** | Level 1 `#casos` on ink: atina + VecinGo as large pieces (wide capture, click-verifiable numbers, click-to-play video, the problem solved, "what it means for you"). Level 2 `#pruebe`: the two open demos as a subordinated invitation ("y además puede entrar a tocar"). `#conjuntos` = the one accent-background section (the segment door). **Out:** the "3 sistemas abiertos" stat (it counted demos) and the ERP/CRM pick. | live captures desktop + 390×844 |
| **C. Technical site** | Ink hero with the giant `appximo` wordmark in the indigo accent, capability marquee (loop), float on the atina loops, tilt on the demo cards, reveal per section, bundled Inter; the loops start only when visible (`preload=none` + posters), no drop-shadow/grain (measured +200 ms of main thread). | live 5-run medians; contracts |
| **Motion contracts** | no-JS / reduced-motion / motion-after-full-scroll = **0 stuck** on all three commercial pages; infinite loops **0** on commercial, **3** on the technical site (by design; 0 under reduced-motion). GSAP removed (−118 KB), choreography in CSS. | `contracts.cjs` |
| **Load** | Landing, local 5-run medians before→after: desktop 427→473 ms, mobile 343→324 ms; live after 458/335 ms, 303 KB. conjuntos 229/213 ms, caso 306/180 ms. Technical site: bytes 846→529 KB locally (loops lazy), medians in the session report. | `perf.cjs` |
| **Rollback** | `git revert` of the session's landing commits reproduces `fa64ba6` byte for byte; same for gh-pages. | `cmp` |

## DONE in FRENTE-COMERCIAL-S1 (2026-08-26)

A commercial-front session run on the 105 — **no engine code changed** (docs
only: an `UnsafeTx` callout in backend-spec, a Windows caveat in QUICKSTART,
the atina response + case study + design guide). Every item verified in a
real browser (desktop 1366×900 + mobile 390×844), before and after.

| Item | What was done | Verified by |
|---|---|---|
| **A.1 Indexation** | No `noindex`/`nofollow`/`X-Robots-Tag` anywhere (both properties, all pages). What was missing was everything else: `robots.txt` + `sitemap.xml` + canonical + `og:url` + `meta robots` + JSON-LD on appximo.com (3 URLs) and on the technical site. Wayback: zero captures of the domain. The Google-account steps are OPS-31. | curl 200 on both robots/sitemaps live; WebSearch reproduces the critique's finding |
| **A.2 Claims** | The third-party testimonial OUT of index.html and caso.html (unverifiable: no raw report, no name — see the decisions table); **"Cientos de sistemas generados" OUT** (evidence = corpus/CI per A-29's own text); **"1,58 ms" OUT of the hero** (stays in the technical section); "para una comunidad real" → "para un conjunto residencial"; caso.html's "sin conocernos y sin nuestra ayuda" → "documentada paso a paso". The stats band now: **3 sistemas abiertos** (each a link) · **3½ h / 18 módulos** (→ caso.html) · **10+ años** (A-29). atina's counts published ONLY as counted in its public `/openapi.json` (32 resources, 48 custom routes) and bundle (30+ screens); "39 pantallas", "2 marcas", "≈3 h", "un trimestre" NOT published loose. | `grep` on the live pages; the sweep table in the session report |
| **A.3.1 Return bar** | Live on **tiendita.appximo.com** (storefront + panel; sticky top, 38 px; sticky headers offset by `--volver-h`; rebuilt against the SAME engine commit the box runs, `eb4c659`, via `-modfile` + worktree — only the SPA changed) and on **petfriendly.appximo.com**'s portada (`/app` cannot take it → ENG-46). Deployed through `deploy-update.sh`; **rollback drilled**: pre-frente binary redeployed and verified (`f130182-linkable`), then the new one again (`f82db2d-frente`); petfriendly: old file + restart → 0 bars, new file + restart → 1. Backups: `/root/tiendita-bin-pre-frente`, `/opt/appitools/bin-rollback/appitools.20260826-000112`, `/opt/vetapp/web/index.html.pre-frente`. | Browser e2e: bar visible on home / product / cart / checkout / **order confirmation** (two real orders, ORD-20260826-9445 and -8633, email `prueba.barra@demo-appximo.test` — residue until the 04:15 UTC golden reset) / panel login / panel (demo aviso intact) / orders; 0 console errors |
| **A.3.2 Attribution** | 18 distinct `wa.me` texts, all first person, each naming its page and CTA (`… Los vi en appximo.com (inicio)`, `(appximo.com/conjuntos · caso)`, `(tiendita.appximo.com · barra)`…). | Every link probed: wa.me 302 → api.whatsapp.com 200 |
| **A.3.3 Secondary** | A subordinate text link under the hero CTA ("Abra un sistema funcionando, sin registrarse ↓" → `#pruebe`). PDF-by-email: no endpoint → OPS-32. | Live |
| **B** | City (Pereira y Dosquebradas) + `+57 311 517 5472` next to every primary CTA on both commercial pages; "el equipo" kept (A-32); response time NOT promised (unconfirmed → decisions table). | Live |
| **C** | New `#capacidad` section right after the video: atina as capacity proof **translated** ("su sistema no depende de una sola persona…"), immediately followed by the visitor-sized systems (`#sistemas`); the conjuntos pick now opens the segment page; footer links to both. Hero, 3 `?hero=`, tilt and count-up kept (A-27/A-31/A-33). Loops NOT covered by a registered decision removed from the commercial pages: capability marquee, floating chips, dashed-arrow animation. | Live, 3 heros, 0 stuck opacity |
| **D** | `conjuntos.html`: H1 = the administrator's pain; three trade pains (asamblea/quórum por coeficiente + Ley 675, cartera/mora, PQRS con radicado); VecinGo with video + two screens; "lo que no hace"; how it starts; ONE CTA with its own origins; links back to the house and to caso.html. Distinct search intent from the house (category: "software administración conjuntos / propiedad horizontal"; the house: "sistemas a la medida"; caso.html: the case itself). ~720 words of prose, FH 69.5. | Live 200 (also `/conjuntos`), 0 errors, mobile clean |
| **E** | Commercial pages: one accent (WA green only on CTAs), tabular-nums, soft shadows, sticky mobile CTA (A-34's deferred bar, both pages), real populated captures (atina portal + panel frames), microinteractions ≤ .35 s. Technical site: atina with two silent loops (allowed there), four-app status line. Identity untouched. | Screenshots in the session's scratch (`shots/after`) |
| **F** | `docs/CASE_STUDY_ATINA.md` (verified counts table first; the ≈3 h figure ONLY inside the phase table and labelled a self-report), `docs/DESIGN_GUIDE.md` (the mould, placeholder tokens, stack as recommendation with its argument, motion contracts as a gate; the fifth-spec option rejected with reasoning; the untested prompt NOT reproduced), the four stumbles crossed: #1 documented (UnsafeTx callout added), #2 documented (§3.4b; reachability → DOC-3), #3 working as designed (Route.RateLimit + frontend-spec trap 5), #4 not Appximo (QUICKSTART Windows caveat). atina.appximo.com verified from a clean browser: 200, portal usable without an account, 0 console errors. Media measured: `landing.gif` 6.7 MB → 447 KB mp4 / 290 KB webm; promo 9.7 MB → 1.6 MB 720p trimmed at 57.5 s (before the loose "≈3 h" card), click-to-play; the 2-min tour NOT published. | ffprobe + live sizes |
| **G (A-25)** | atina qualifies (external, no direction — confirmed in writing; findings answered point by point in FFR §4; the words say what the docs show) → **the count is FOUR** in README, SHOWHN_MATERIAL and the technical site, with the link (it opens today). Disclosed in the case study: a contractor's build for the maintainer's own use — external to the engine and its authors, not a stranger. | FFR §4 + case study |

## DONE in SILENT-CORRUPTION-S1 (2026-08-21)

The two ENG-45 families that could not wait for launch — both of the shape
"valid schema → validator approves → the engine corrupts or decides at random,
silently" — closed as CLASSES, with two fresh audit sweeps (18 name-bound
findings, 10 non-determinism findings) recorded in the session report.

- **The `auto` role is now DECLARED, not guessed from an English name.**
  `auto` accepts `"create"` (set once at insert, any name) and `"update"`
  (refreshed by the engine on every update, any name) alongside the legacy
  `true` (byte-compatible: create semantics + the literal-`updated_at` refresh
  magic). One refresh source — `schema.AutoRefreshColumns` — consumed by REST
  PUT/PATCH, the batch transaction AND `Ctx.Update` (which used to stamp
  nothing: the same row got a fresh timestamp through REST and a stale one
  through a handler). Load errors: `auto_requires_time` (the column is
  TIMESTAMPTZ regardless of declaration — any other type silently diverged),
  `invalid_auto` (closed value set). SCHEMA-5 warning `auto_update_intent`:
  legacy `true` on an update-intent name (`modificado_en`, `last_modified`…)
  names the frozen behavior and both fixes; create-intent domain names
  (`placed_at`, `creado_en` — the engine's own examples) stay silent. The 422
  `read_only` STAYS for every auto field (frozen+read-only is the correct pair
  for a creation timestamp; the dead cell existed only because update intent
  was inexpressible — now it is expressible). OpenAPI: auto fields carry
  `readOnly: true` + `x-appximo-auto` (the EFFECTIVE role), and `/app`, both
  backoffice `contract.js` copies and the BACKOFFICE spec now derive "engine-
  managed" from the CONTRACT, never from the two English names (before, a
  Spanish `modificado_en` rendered as an editable field the engine rejected on
  save). Studio: the auto checkbox became a 4-option select (a toggle would
  have silently degraded `"update"` → `true` — the same corruption class);
  `explain` (en/es) words the two roles distinctly. Grammar (`GrammarCore` →
  `spec` + the internal loop) teaches the roles; a LIVE Spanish `ai-generate`
  run produced `modificado_en: {"auto": "update"}` first try, and the legacy
  hand-written case warns at `validate`, `validate --json` (`warnings[]`) and
  engine boot. Verified live end-to-end by a DB-backed integration test over
  all three update paths (examples/model-lab/auto-roles.json).
- **The relation-subroute collision is a load error, not a per-boot coin
  flip.** `customer` + `customer_id` (both relation fields) used to collapse
  onto `GET /api/{r}/{id}/customer` with chi silently keeping whichever field
  Go's map iteration yielded last — a different relation, joined on a
  different column, under the OTHER target's RBAC, per restart; the OpenAPI
  deterministically documented one variant (right ~half the time) and
  `appximo generate` emitted two same-named handlers that did not compile.
  Now: ONE derivation `schema.RelationSubroute` (router + OpenAPI + generator
  all delegate; anti-divergence test pins the OpenAPI paths against it) and
  the load error `relation_subroute_collision` naming both fields, the path
  and the fix — proven stable across 10 repeated validations, and `serve`
  refuses to boot the colliding schema.
- **The rest of the non-determinism class, swept and closed:** `Validate()`
  returns its errors SORTED (the control-plane/admin deploy APIs used to
  reshuffle them per call, and one path embedded `errs[0]` — a different
  reason per identical retry); file-policy 422 violations sorted; the filter
  loop, the aggregate unknown-param loop and `RejectListParams` now name the
  sorted-first offender (the ENG-16 class — the same file already carried the
  fix for `order[…]` with the measured 174/26 coin flip); a field OR relation
  literally named `id` is the load error `reserved_field_name` (a relation
  named `id` REPLACED the row's id in every embed; a field named `id` was
  half-honored: migration skipped it, GraphQL let it overwrite `id: ID!`);
  the self-singular GraphQL list shadow (`menu`, `media`, `lineas_orden`)
  warns (`graphql_list_query_shadowed`). Verified already-clean: fleet
  duplicate domains (both manifest paths reject), GraphQL type/field builds
  (sorted), OpenAPI path assembly (sorted), migration DDL order (sorted).
- **A parity hole the session's own test exposed, closed:** a committed
  non-GET custom route now INVALIDATES the tenant's response cache (route.go
  commit seam) — before, a `Ctx.Update` through a custom route left cached
  GETs serving the pre-write record until TTL: fresh data written, stale data
  read, no error anywhere. (Predates this session's changes; found because
  the new integration test read back through a cached GET.)
- **Data audit (nothing touched):** tiendita and petfriendly (read-only, via
  /editor/current-schema) use only canonical `created_at`/`updated_at` —
  zero affected rows in production; dev tenant `nimbus` has
  `asignaciones.asignado_at` auto:true (create-intent name, correct create
  semantics — no corruption, no warning); the in-repo fixtures' 11
  domain-named auto fields are all create-intent and correct
  (`dispatched_at`, `placed_at`, `creada`, …).
- **Gates:** unit + full lane + `-tags integration` + lint 0 + gofmt/vet
  clean; binary-diff gate + ABBA bench against the FROZEN new binary — see
  the session report for the verdicts; 4 corpus rows + an auto field added to
  the gate schema (timestamps are normalized, so the field is diff-safe).

## DONE in FRESH-AGENT-GAPS-S1 (2026-08-21)

Four gaps that fresh agents hit building real apps from the public docs alone —
each closed as a CLASS, not an instance. Engine changes went through the
binary-diff gate (120 SAME + 1 explained DIFF — the intentional `?include=`
hint) and an ABBA write-path bench.

- **`created_at` (Part A) — implicit requirement, closed at LOAD not by magic.**
  A schema that omitted `created_at` on new resources validated and then
  crashed at request time with `column does not exist`. The decision
  (03_DECISIONES C-DOCTRINA, doctrine that will recur): the validator WARNS,
  the engine never fills it in — implicit magic breaks `declared == deployed`
  as badly as silent input tolerance breaks ADR-024. Shipped: SCHEMA-5 warning
  `missing_timestamps_convention`, convention-relative (fires only when other
  resources declare timestamps; a timestamp-less schema and pure junctions stay
  silent), naming the exact field to add. The class was then AUDITED (→ ENG-45,
  27 findings); one boot-panic member fixed here: a **GraphQL type-name
  collision** (`categoria` + `categorias` → `Categoria` → `validate ✓` then
  boot panic) is now the load error `graphql_type_collision`, computed from
  `schema.GraphQLTypeName` — the ONE naming source pkg/graphql now delegates
  to, pinned by a divergence test. And `?include=` on a relation-less resource
  now says a FK column alone does not enable it.
- **The CLI token + `Ctx.MintToken` (Part B).** The `--user-id`-less token
  footgun (a role scoped by `$user_id` matches zero rows, silently) recurred
  from the commerce field report (GAPS 1B-3) where it was noted but never
  surfaced at the CLI. Fixed: `appximo token` warns on a missing `--user-id`
  (schema-aware — it names the resources the chosen role row-scopes) and on an
  empty `--tenant` (the OPS-30 wildcard). The paired `Ctx` gap — CreateUser
  could make an identity but nothing could mint its session — closed with
  **`Ctx.MintToken(userID, role)`**: byte-shape identical to `/auth/login`
  (one `pkg/auth.GenerateToken` path), so a custom registration endpoint
  auto-logs-in like the engine's own signup. Integration-tested: minted token
  works on a generated route; empty userID and undeclared role refused, named.
- **`backend-spec` batch patterns (Part C).** A fresh agent wrote 3 queries ×
  400 rows = a two-minute request. Added §3.4b (the `= ANY($1)` read + the
  `unnest()` write, from a route that compiles and was verified live: a
  3-item reprice ran as 2 statements, a bad id → named 422, RBAC 403) and
  safety rule 6 (the N+1 warning) WHERE the loop is written, not in an
  appendix — plus the other three self-kills (unbounded reads, unindexed
  filters, network-in-loop) each pointing at a verified worked example.
- **`frontend-spec` §11 is now a PROCEDURE (Part D).** An agent shipped a UI
  broken on phones for weeks with every API test green. §11 is rewritten from
  advice into the definition of done: a copy-paste **mobile layout gate**
  (390×844, zero horizontal overflow, touch targets ≥24px with the WCAG-2.2
  inline-link exemption — verified against the reference storefront, and its
  strict draft caught real sub-24px controls), then console-strict e2e, then
  forced failure states. It is the single source; `backoffice-spec`'s
  checklist now references it instead of restating it.

## DONE in PRELAUNCH-TRUTH-S1 (2026-08-19)

The last claims that wouldn't survive a "show me", closed before launch:

- **The evaluator count is now exactly what the linkable material shows.**
  "Three outside developers" overstated it: our own public response doc
  describes the second evaluation as "an agent", and nothing linkable proves
  three distinct people. The claim is now **"three independent field
  evaluations from outside the project … one of them driven end to end by
  the evaluator's AI agent"** — rewritten in the README, the launch FAQ and
  the case study (whose "A different independent evaluator" line now says
  what the response doc supports). The counting rule is recorded as a
  decision (internal 03, A-25): evaluations, not persons; external infra +
  published-and-answered findings + words matching the documents; a broken
  or private app's findings still count but its URL is never linked.
- **No VPS price without a run behind it.** The "$7–16 VPS" (site meta,
  GUIDE ×2) and "$6 VPS" (QUICKSTART, the embedded LIFECYCLE spec) became
  "a cheap VPS" or the measured **$16/mo with its spec**; BENCHMARKS'
  "$6–16/mo" closing line became the RAM spec its own data backs ("a small
  1–2 GB VPS"). ADR-020's "$7–16" target range and its quote in the
  certification stay — dated design records, not launch material. The
  commercial landing never had a price figure (A-16 holds; the session
  premise didn't apply there).
- **Stale/unsourced countables swept:** the site's "current release v0.1.5"
  → "every release from v0.1.5 onward" (never goes stale); the site's
  crisblogs "25/25 browser checks" → 24/24, the number the published
  response doc actually states.
- **OPS-29 advanced** (cause narrowed via the public jobs API, release.yml
  made idempotent, `appximo upgrade` proven from a real v0.1.6 install,
  Docker badge semver-sorted) — see the OPS-29 entry; the re-run is the
  owner's.
- The T1-vs-T3 title trade-off written against the predicted top critique,
  and the full "show me" claim table — both in the internal
  `SHOWHN_MATERIAL.md` addendum. The T1 recommendation stands; the choice
  is the owner's.

## DONE in SHOWHN-MATERIAL-S1 (2026-08-19)

The Show HN launch material — five title candidates with a recommendation,
the author's first comment, a 12-answer objection FAQ — written in native
English and verified claim by claim; the material itself lives in the
internal handoff repo (`SHOWHN_MATERIAL.md`), the launch date is Miguel's.
Every public promise re-verified live first: both demos touched from a clean
browser (tiendita catalog/cart/checkout+coupon field; petfriendly demo login
executed against the live panel — future appointments visible, the nightly
re-anchor works), release v0.1.7 downloaded + checksum-verified + booted,
`go get` from a virgin module cache (51 s), the Docker quick start run
VERBATIM on a clean project (compose→healthy 7.9 s vs the claimed ~9 s; all
four copy-paste commands answer exactly as documented), CI/Docker/Security
workflows green in Actions, all three badges rendering. Fixes that fell out:

- **README `~22 MB pull` → `~41 MB`** (the image grew when the embedded UIs
  started shipping in the module; measured against the registry).
- **`.env.example`'s unquoted `SMTP_FROM`** broke the quick start's own
  `source .env` with two bash syntax errors — quoted (the engine's dotenv
  parser strips surrounding quotes; behavior identical).
- **The README's "demo data is public and writable" line** made precise:
  the store checkout writes (mock payments), the demo panels are read-only
  server-side, everything resets nightly.
- **The technical site's missing favicon** (its only console 404) — an
  inline SVG icon, applied to `site/index.html` and pushed to gh-pages.
- One translated-sounding phrase in the README ("said, not hidden" →
  "disclosed, not hidden"); the rest of the English audit passed clean.

Numbers ruled OUT of the material for lack of backing: the "$7 VPS" framing
(nothing was ever measured on a $7 box — everything says $16/mo with its
spec, or no price), and the historical NestJS comparison (already retired by
OPS-12). Filed: OPS-29 (the v0.1.8 release-run failure found during
verification).

## DONE in LINKABLE-TRUTH-S1 (2026-08-19)

- **COMMERCE-8 — the coupon reduces the taxable base.** ONE computation
  (handlers/checkout.go): descuento = subtotal×pct/100 (truncating), split
  per line by largest-remainder (shares sum EXACTLY), per-line IVA over the
  discounted base, total = subtotal − descuento + IVA + envío. orden_lineas
  gained descuento_centavos (additive migration). The AUDIT found 7 surfaces
  touching totals; after the fix exactly ONE computes — the rest read stored
  values: orden-publica now exposes descuento (the tracking page's numbers
  used to NOT add up with a coupon), the tracking page and panel detail show
  the Descuento row, the invoice/nota carry DiscountCents+ShippingCents and
  Validate refuses a header that does not close peso a peso, reconciliation
  already read stored line values. 5 cases pinned in verify.sh §E with
  explicit arithmetic; verified ON SCREEN in production: $220.000 − $22.000
  + $37.620 = **$235.620** (the engine used to charge $239.800). The product
  page's «IVA del 19% incluido» lie became «+ IVA del 19% en el checkout».
- **OPS-28 — petfriendly is touchable (verdict: VITRINA).** vetapp deployed
  the current engine (eb4c659; backups + rollback binaries kept), a minimal
  static portada at / (the README-linked root used to answer a raw 401 JSON)
  routing visitors to the demo panel (credentials published on the page —
  the DEMO-SHOWCASE /app demo mode: read-only `demo` role, per-session
  overlay, RBAC as the boundary) and developers to /docs. A plausible small
  clinic seeded through the API (3 vets with real auth users — the
  references:user_id FK pattern —, 8 owners, 11 pets, 17 citas across ALL
  states incl. FUTURE confirmed/requested ones, 6 vaccinations). Non-
  persistence verified BOTH ways: demo-token writes all 403; a full browser
  session emitted ZERO write requests and Postgres counts were identical.
  Two latent 422s fixed on the way (required + RBAC-forced veterinarian_id —
  the validator's own suggested fix).
- **The golden set no longer ages (the class, not the instance).**
  scripts/seed-historia.sh builds the 13-order history through the real flow
  and applies a designed age distribution (cerrada 30d · entregadas
  24/18/13/9/6 · enviadas 4/2 · preparando/cancelada 1 · pagada/pendientes
  hoy); scripts/redate-demo.sh runs after every nightly restore
  (ExecStartPost) and re-anchors ALL visible timestamps to the execution day
  — rewriting ORD-YYYYMMDD numbers and payment references coherently
  (supersedes A-18's "no backdating" caveat, which rejected backdating
  WITHOUT the coherence rewrite). Aging drill: −30d simulated, one run
  restored the exact distribution. vetapp got its own nightly re-anchor
  (vetapp-redate.timer, attended-anchor, birth_dates untouched).
- **The photo catalogue passed a brand/face filter it had never seen.**
  The Lacoste-logo polo left ENTIRELY (product + photo); the filter then
  caught 4 of the remaining 9 with hard evidence: Levi's patch focal
  (cha-den), UGMONK stamp (cam-bas), an identifiable portrait (ves-flo —
  CC0 covers copyright, not personality rights) and two faces + third-party
  fair signage (old rua-lan). The ruana got a vetted replacement (Ovecha
  Ragué Poncho, CC BY-SA 3.0); chaqueta/camiseta/vestido use the DESIGNED
  placeholder on purpose. Real brand names in attrs (Arturo Calle, Koaj,
  Levis, Studio F, Zenú) became plausible fictional ones — the class dies in
  data too. Licenses + /creditos-fotos.md carry the criterion and the
  removal log. COMMERCE-9: attribute labels humanized («Hecho a mano: sí»),
  category chips show the accented NAME and filter by slug.
- **Dead promises swept.** crisblogs is a story, not a link, in README (×2),
  the site and the case study; petfriendly links land on the new portada;
  the landing's trust-bar claim adjusted to the minimum that survives a
  click («4 sistemas construidos — dos abiertos para que usted los pruebe»,
  pushed live). The measured fact moved: **2 of 4 pass the full rubric**
  (tiendita re-audited outside-in post-changes: arithmetic closes on
  screen, 9-tile catalogue, demo panel, 0 console errors, load 119 ms).

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
