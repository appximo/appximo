# Certification report — 2026-08-01

> **Follow-up status (SILENT-FAILURE-S1, same day).** Four of the five findings this
> report opened are **CLOSED**, and the fifth is superseded by a wider policy:
>
> | Finding | Status |
> |---|---|
> | **ENG-14** (`is_null` silently ignored) | **CLOSED** — and generalized: the audit found the same shape across ten input surfaces, so the fix is a policy ([ADR-024](adr/ADR-024-unrecognized-input.md)) plus [an audit](audits/UNRECOGNIZED_INPUT_AUDIT.md), not a regex patch. Two more instances were found and fixed: a misspelled `dry_run` turned a PREVIEW into a real migration, and `appitools serve <path>` served a different app than the one named. |
> | **SEC-1** (CSP lost on cached responses) | **CLOSED** — security headers now survive the cache, by allowlist (replaying every header would replay `X-Trace-Id` too). |
> | **SEC-2** (`'unsafe-inline'` on script-src) | **CLOSED** — hash-based CSP, verified in a real browser with a control arm: legacy → injected script executed; hardened → the shell still boots and the injected script is blocked. |
> | **SEC-3** (`Route.Public` untested live) | **CLOSED** — all three branches exercised through a booted App over real HTTP. |
> | **OPS-3** (`golangci-lint` not in CI) | **CLOSED** — `.golangci.yml` with every exclusion justified; `make lint` 62 findings → **0**; gate wired into CI. |
>
> **One NEW finding of the same class was escalated privately** and is tracked as
> **SEC-5** by ID only — it is an exploitable information-disclosure vector, so per
> the session rule the reproduction went to the maintainer directly and is not
> written in the repository.
>
> The claims this session's fixes changed (the "silently ignored" sort/filter
> behavior in AGENTS/CAPABILITIES/SCHEMA_REFERENCE) are corrected in those files.

> **Second follow-up (INPUT-CLASS-CLOSE-S1, same day).** The seven items the
> adversarial review of SILENT-FAILURE-S1's own fix opened (ENG-25…ENG-31) are
> **CLOSED** — see [BACKLOG §DONE in INPUT-CLASS-CLOSE-S1](BACKLOG.md) and
> [UNRECOGNIZED_INPUT_AUDIT §INPUT-CLASS-CLOSE-S1](audits/UNRECOGNIZED_INPUT_AUDIT.md).
> Claims in THIS report that changed under it:
>
> - **The GraphQL "2000 total selections" cap now holds.** At certification time
>   it was advertised but bypassable ~46× through fragment spreads (found after
>   this report, tracked as ENG-28); the analyzer now charges a fragment's cost
>   at every spread site, re-verified by test and by the binary-diff gate.
> - **A wrongly-typed filter value is a named 400** (`filter[amount][gt]: "abc"
>   is not a valid int64 value`), no longer the anonymous `invalid request` —
>   with a live-Postgres conformance test guaranteeing the check is never
>   stricter than the database. `time` values remain the documented exception.
> - **Update's 422 changed shape** to the same `validation_failed` + `fields[]`
>   contract create uses (REST, GraphQL and batch identically), so the OpenAPI
>   `ValidationErrorResponse` model is now true for every write verb.
> - **New rejections (contract changes):** empty `page`/`per_page`/`sort`/
>   `order`, and `?order=` without `?sort=`, are named 400s; `?filter[id][eq]=`
>   is new, working surface.
> - **Verification infrastructure:** every claim above was checked by the new
>   **binary-diff gate** (`scripts/binary-diff-gate.sh`, 63 paired requests,
>   base-vs-new, stable diff set fully explained) — the technique that found
>   what `make test` green did not, now a repo gate.


**Session:** CERTIFY-S1 · **Commit under test:** `be876f4` · **Method:** everything
below was executed against a **running engine** built from that commit. No figure was
copied from a previous document.

**Why this exists.** Five sessions in a row touched the write path, the migration
executor, the validator, the auth middleware, the static CSP, the installer and
tenant routing. The project's strongest claims — the flagship benchmark, the OWASP
review, and a README that says its claims are "audited against the running engine" —
were all measured *before* that work, and nobody had re-verified them. This
repository has already produced three claims that expired by accident, so claim
drift here is a demonstrated phenomenon, not a hypothesis.

**The rule this session applied:** a statement without a measurement from today is an
expired statement. Every figure that survives below carries its date, its condition,
and how to reproduce it.

---

## 0. Verdict at a glance

| Area | Result |
|---|---|
| Flagship engine benchmark (2000 rps) | ✅ **Reproduced** — 0 errors, p50 1.600 ms, **under the limiter configuration the doc declares** |
| The same benchmark on engine **defaults** | ⚠️ **~50 % of requests are `429`** — the default per-tenant limit is 1000 rps. Behaving as designed; the headline needs the caveat |
| NestJS comparative claim (~4.8× / ~2.7×) | ⚠️ **NOT re-verified** — harness intact, original conditions gone (§2.3) |
| Multi-tenant isolation | ✅ **10/10 live probes clean** across REST, GraphQL, search, filters, aggregate, by-id (BOLA), SSE, files |
| RBAC (deny-by-default, allowlist, row conditions, mass-assignment) | ✅ **14/14 live probes clean** |
| Original OWASP findings (9, session 16) | ✅ **all 9 still fixed**, verified live |
| New security findings | **1 low-severity defect** (CSP dropped on cached responses) — no exploit; backlog **SEC-1** |
| Correctness findings | **1 silent-failure defect** (`is_null` filter ignored) — backlog **ENG-14** |
| Documentation claims | **34 verified · 6 corrected · 4 not verifiable** |
| `make test` / integration lane | ✅ green |
| `go vet` / `gofmt` | ✅ clean |
| `golangci-lint` | ⚠️ **62 issues, exit 1** — and it does **not** run in CI (OPS-3) |
| `govulncheck` | ⚠️ **not verifiable on this box** (OOM-killed); the CI gate stands |

**Publishable today?** Yes, with two edits and one removal — see §5.

---

## 1. The measurement environment

| Role | Machine | Note |
|---|---|---|
| **SUT** | the 58 — DigitalOcean droplet, **2 vCPU / 1963 MiB**, Ubuntu 22.04 | the *same hardware* as the original benchmark |
| Stack | Caddy (static binary) · **PostgreSQL 18.4 native, uncapped** · engine `GOMAXPROCS=2 GOGC=200 GOMEMLIMIT=1GiB` | |
| **Loader** | the 105 — 1 vCPU shared, same region, k6 **v0.55.0** | never co-located with the SUT |
| Network | public DO network, RTT floor **min 1.343 ms** (ping ×10) | original: ~1.2 ms |
| Dataset | `guides`: 100 k rows (25 k per status) → later 1 M rows / 223 MB | same shape as the original |

**Three conditions differ from the original run, declared rather than hidden:**

1. The 58 now **serves two production apps** (the tienda and petfriendly). Both were
   idle throughout (load average 0.00 before the runs), but they are resident.
2. PostgreSQL is **native 18.4 and uncapped**. The original ran PG 16 in Docker
   **CPU-capped to 0.5 vCPU** — a declared condition of the published figures. This
   favours today's numbers on the cache-bypassed arm.
3. The bench engine ran in **its own process, port and database** (`:8095`,
   `certbench`), reachable only from the loader's IP through a temporary `ufw` rule.
   Neither live app's configuration was touched at any point, and the whole
   environment was removed afterwards.

Reproduce — build the engine, seed a `guides` resource, then:

```bash
TARGET_URL=http://SUT:8095 TENANT_ID=bench10 BENCH_TOKEN=<jwt> \
ENDPOINT='/api/guides?filter[status]=pending&sort=created_at&order=desc&per_page=20' \
K6_SCRIPT=benchmark-lab/k6-pub.js \
  bash scripts/bench-protocol.sh 10 my-label 2000 30s
```

Every run this session appended a row to **`benchmarks/history.tsv`** (§6).

---

## 2. The flagship benchmark, re-measured

### 2.1 Results

Median of the per-run medians, bootstrap CI95 (seed 42):

| Arm | Runs | Requests | p50 | CI95 | p95 | Errors | CV between runs |
|---|---:|---:|---:|---|---:|---:|---:|
| **2000 rps** (limiter 3000/300, as the published §2.2 declares) | 10 | **597,461** | **1.600 ms** | [1.568, 1.665] | 61.2 ms | **0** | 3.9 % |
| 2000 rps on engine **defaults** (limiter 1000/100) | 10 | 296,050 | 1.634 ms | [1.565, 1.747] | 91.1 ms | **~50 % `429`** | 35.6 % |
| 500 rps | 3 | 45,003 | **1.532 ms** | [1.516, 1.543] | 2.09 ms | 0 | 0.9 % |
| 500 rps, **cache bypassed** | 10 | 149,920 | **2.436 ms** | [2.378, 2.495] | 4.03 ms | 0 | 5.0 % |
| 1000 rps | 3 | 88,590 | 1.474 ms | [1.437, 1.838] | 3.96 ms | 0 | 14.0 % |
| 250 rps | 3 | 22,503 | 1.758 ms | [1.732, 2.250] | 6.59 ms | 0 | 15.3 % |

### 2.2 Against the published numbers

| Published (2026-06-10) | Measured today | Verdict |
|---|---|---|
| 2000 rps: p50 **1.58 ms** CI95 [1.52, 1.62] | **1.600 ms** CI95 [1.568, 1.665] | ✅ **confirmed** (CIs overlap) |
| **0 errors in 600,118 requests** | **0 errors in 597,461 requests** | ✅ **confirmed** |
| p95 63.0 ms CI95 [58.7, 68.3] | 61.2 ms | ✅ confirmed |
| between-run CV 4.5 % | 3.9 % | ✅ confirmed |
| 500 rps: p50 **1.528 ms** CI95 [1.499, 1.547] | **1.532 ms** CI95 [1.516, 1.543] | ✅ **confirmed** |
| 500 rps cache OFF: **2.756 ms** [2.647, 2.859] | **2.436 ms** [2.378, 2.495] | ✏️ **better than published** — today's PostgreSQL is uncapped; the original carried the 0.5-vCPU headwind it declared |
| "did not saturate at ≤2000 rps" | p95 61 ms, 0 errors, schedule kept → not saturated | ✅ confirmed |

**The most important caveat, and it is new information:** the 2000 rps headline is
only reachable with `RATE_LIMIT_RPS=3000 RATE_LIMIT_BURST=300`. On **engine defaults
(1000 rps / 100 burst per tenant)** a 2000 rps single-tenant load gets **~50 %
`429 Too Many Requests`** — measured and broken down by status code: **10,080 ×
`200`, 9,921 × `429`, 0 × 5xx**. The engine is doing exactly what it is configured to
do; the limiter is per-tenant and the load is single-tenant. The published benchmark
*does* declare the raised limiter in its §2.2, but the README headline does not, and
a reader reproducing it on defaults will not see 2000 rps. **README corrected** (§4).

### 2.3 The NestJS comparison — NOT re-verified

The harness is **intact and recoverable**: `benchmark-lab/nestjs-baseline/` still has
`dist/main.js`, `node_modules` including `@prisma`, `ecosystem.config.js` and the
Prisma schema; a source-only copy also survives in
`/root/archives-58/nestjs-bench-58-20260731.tgz`.

It was **not re-run**, because the published measurement's conditions no longer exist
and a run under different conditions would not be a re-verification:

| Published condition | Today |
|---|---|
| Node **v22.22.3** on the SUT | **no Node at all** on the 58 (wiped and reinstalled natively) |
| pm2 7.0.1, cluster × 2 | not installed |
| PostgreSQL **CPU-capped to 0.5 vCPU** (Docker) — declared, identical for both arms | **no Docker**; PG is native and uncapped |
| A dedicated SUT | the 58 now serves two production apps |

Installing Node + pm2 + Docker on a box that serves two live assets, in order to
benchmark a competitor baseline, is not a trade this session was willing to make.

**Status: the comparative claim (~4.8× with cache, ~2.7× bypassed, NestJS saturating
between 500 and 750 rps) is NOT re-verified. Last valid measurement: 2026-06-10.**
It is marked as such in the README. Restoring it needs a *dedicated* SUT with both
stacks installed and PostgreSQL constrained identically — tracked as **OPS-12**.

### 2.4 Production-stack figures (`docs/BENCHMARKS.md`)

| Published | Measured today | Condition / verdict |
|---|---|---|
| production layers cost **+0.97 ms** p50 (proxy +0.71, TLS +0.26) | **+1.22 ms** — engine direct `1.829 ms` → through Caddy+TLS `3.052 ms` (petfriendly) and `3.061 ms` (tienda) | ✏️ slightly higher; the two live apps agree to 0.01 ms, a good internal consistency check |
| 1 M rows, filtered+sorted page: **4.44 ms** p50 (full stack) | **2.994 ms** engine-direct at 100 rps; + the 1.22 ms layer cost ⇒ **~4.2 ms** full-stack equivalent | ✅ consistent |
| **500 rps sustained, knee at 750** (full stack, 1 M rows, cache bypassed) | engine-direct: 500 rps → **2.348 ms** clean; **750 rps also clean** (2.494 ms; 2 of 3 runs kept schedule) | ✏️ **the knee is a full-stack limit, not an engine limit.** Not re-measured through Caddy+TLS — that needs a Caddy site on the live box (declined, OPS-11) |
| footprint: whole stack **37.5 / 109 / 153.5 MiB** anon | not reproducible — the box runs two extra apps and a different dataset shape | ⚠️ **not verifiable under the original condition.** What *was* measured, bench engine alone with 1 M rows: **PSS 66.4 MiB / anon 33.7 MiB idle**; **PSS 58.2 / anon 25.4 MiB under 200 rps cache-bypassed load** |
| engine "~24 MB RSS idle" (README) | **21.0 MiB** RSS idle (small dataset); 33.8 MiB for the dev engine on the ERP fixture | ✅ confirmed as an order of magnitude |

### 2.5 The ADR-020 range verdict

*"Apt for mid-scale B2B, tens-to-hundreds of tenants, vertical scaling, $7–16/mo; not
for consumer scale; no HA or clustering."*

**Still true.** Nothing measured today moves the boundary: the engine sustains
2000 rps on 2 vCPU when the limiter allows it, the knee sits in the stack rather than
the engine, and there is still no clustering story — scale is vertical. One
refinement worth writing down: the **default** per-tenant rate limit (1000 rps) is
the first ceiling a single-tenant workload meets, well before CPU.

---

## 3. Security posture

### 3.1 The original OWASP review, re-run

Scope reconstructed from `context-docs/SESSION_LOG.md` session 16 (2026-05-30, nine
findings) plus SEC-AUDIT-V1/V2. Every item re-tested **live** against a running
engine with `APPITOOLS_ENV=production` and two tenants holding distinguishable data.

| # | Original finding | Re-test today | Result |
|---|---|---|---|
| 1 | JWT bypass on `/graphql` | unauthenticated POST `/graphql` | ✅ `401` |
| 2 | Tenant hopping via JWT | alpha's token against beta's Host | ✅ `401` |
| 3 | Field stripping missing in GraphQL | `viewer` (allowlist id/title/status) queries `secret` | ✅ value absent from REST **and** GraphQL |
| 4 | SQL injection in `Condition.Field` | unknown filter field; condition fields | ✅ `400`; a bad condition field is now a **load-time** error |
| 5 | OFFSET DoS (`page` unbounded) | `?page=999999` | ✅ clamped to `page: 10000`, empty page (`MaxPage` intact) |
| 6 | Security headers missing | header dump on `/api/*` | ✅ `X-Content-Type-Options`, `X-Frame-Options`, `Strict-Transport-Security`, `Referrer-Policy` — ⚠️ **but CSP is missing on cached responses, see SEC-1** |
| 7 | GraphQL introspection in production | `{__schema{types{name}}}` | ✅ rejected |
| 8 | Unbounded `/graphql` body | 2 MB document | ✅ `413` |
| 9 | ILIKE wildcard bleed | `?search=%` | ✅ 0 rows (escaped, not match-all) |

Plus the JWT hardening the same audit established, all live: no token → `401`,
garbage token → `401`, **`alg=none` forgery** → `401`, wrong-secret signature →
`401`, expired token → `401`.

Additional surfaces, all live:

- **GraphQL alias amplification:** 60 root selections → rejected (cap 50). ✅
- **Admin-gated surfaces:** `/metrics`, `/debug/traces` → `401` unauthenticated,
  `200` with the admin key; `/admin/*` → `403` with no key *and* with a wrong key;
  **`/debug/pprof` is not mounted at all in production (`404`)** — stronger than the
  documented `401`. ✅
- **Health probes** `/healthz`, `/readyz` public by design. ✅
- **Control plane:** `POST /tenants` → `401` with no key and with a wrong key. From
  the public internet, ports 9090/9098/9195/9196 on the live box are **filtered**
  (ufw allows only 22/80/443). ✅ The process binds `0.0.0.0`; the firewall is the
  control, which is exactly what `AGENTS.md` describes.
- **`UnsafeTx` as a complete audit:** ✅ still true. Nine occurrences repo-wide and
  **none in engine request-handling code** — two in `ctx.go` (the interface and its
  implementation), the rest in `examples/`. `grep UnsafeTx` remains a complete audit
  of RBAC-bypass sites.

### 3.2 Multi-tenant isolation and RBAC — probed live, not read

Two tenants (`alpha`, `beta`), each holding a uniquely-named row and a marker string.
Every probe used alpha's credentials and searched the response for beta's marker.

| Surface | Result |
|---|---|
| REST list / search / filter | ✅ no beta data |
| GraphQL list | ✅ no beta data |
| Aggregate (`count`, `sum`) | ✅ alpha's sum = 100 (beta's 999 absent) |
| Direct id (BOLA): GET / PATCH / DELETE beta's row id | ✅ `404` on all three — **not `403`**, so existence never leaks |
| SSE with a foreign-tenant token | ✅ `401` |
| Files upload without a token | ✅ `401` |

RBAC, same engine:

| Property | Result |
|---|---|
| Deny by default (`nobody` role → `notes`) | ✅ `403`; its own granted resource → `200` |
| Read-only role cannot create / delete | ✅ `403` / `403` |
| Field allowlist on REST | ✅ `secret` absent |
| Field allowlist on GraphQL | ✅ the requested `secret` is not returned |
| Row condition (`owner_id = $user_id`) scopes lists | ✅ owner1 sees only its own row — not owner2's, not unowned rows |
| Row condition scopes **aggregates** | ✅ owner1 `count:1` vs admin `count:3` |
| Create forces the condition field | ✅ `owner_id` set to the caller |
| **Mass-assignment block** — creating a row attributed to another principal | ✅ `403` |
| Cross-principal PATCH | ✅ `404` (no existence leak) |
| A row condition with an **empty** `$user_id` | ✅ fails **closed** (`invalid request`), never matches all rows |

### 3.3 New finding — SEC-1 (low severity, no exploit)

**Cacheable `GET /api/*` responses are served without the `Content-Security-Policy`
header that `StrictCSP` sets.**

Evidence:

```
GET /api/notes                             → no Content-Security-Policy
GET /api/notes  (Cache-Control: no-cache)  → Content-Security-Policy: default-src 'none'; frame-ancestors 'none'
```

Mechanism (read after measuring): `pkg/cache/response_cache.go` stores only
`Content-Type` and `Etag` (`buildItem`) and replays only those (`writeItem`).
`SecurityHeaders` runs *outside* the cache, so its four headers survive; `StrictCSP`
runs *inside* the cached group, so its header is lost.

**Why this is not an escalation:** the affected responses are `application/json`
served with `X-Content-Type-Options: nosniff`, `X-Frame-Options: DENY` and HSTS all
still present. CSP on a JSON response is defence-in-depth, not the control that stops
an attack, and no exploit was demonstrated. It is nevertheless a real deviation from
the documented posture. Tracked as **SEC-1**.

### 3.4 The `'unsafe-inline'` in the default static CSP

`DefaultStaticCSP` — served for every `Config.Static` mount that does not override it:

```
default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline';
script-src 'self' 'unsafe-inline'; connect-src 'self'; font-src 'self';
base-uri 'self'; form-action 'self'; frame-ancestors 'none'
```

`'unsafe-inline'` is on **`script-src`**, not only on styles. Stated precisely:

**What is lost.** The main defence CSP offers against cross-site scripting. If an
attacker can inject markup into the served HTML, an inline `<script>` **will
execute** — the policy no longer stops it. That is the primary protection this
directive exists to provide, and with `'unsafe-inline'` present it is off.

**What still holds, and it is not nothing:** no **external** script sources
(`script-src 'self'`), no **external** `connect-src` — so the classic exfiltration
step of shipping stolen data to an attacker-controlled host is blocked — no framing
(`frame-ancestors 'none'`), no foreign `form-action`, and `base-uri 'self'`.

**Why it is there.** SvelteKit's `adapter-static` shell boots hydration from an
inline `<script>` (as do Next export and Astro islands). The first strict draft of
this default blanked a real SvelteKit app in every browser — invisible to `curl`,
caught only by a browser suite. The default has to run the mainstream bundlers'
output. It is permissive **by necessity, not by design**, and an app whose bundle
emits no inline scripts should tighten it per mount:

```go
StaticMount{CSP: strings.Replace(appitools.DefaultStaticCSP,
    "script-src 'self' 'unsafe-inline'", "script-src 'self'", 1)}
```

**The principled fix** is a hash-based policy: at boot, compute the sha256 of each
inline `<script>`/`<style>` block in the mount's `index.html` and emit `'sha256-…'`
instead of `'unsafe-inline'`. This keeps the mainstream bundlers working *and*
restores the XSS protection. Tracked as **SEC-2**.

### 3.5 Toolchain

| Check | Result |
|---|---|
| `go vet ./...` | ✅ clean |
| `gofmt -l` | ✅ clean |
| `make test` (unit, `-race -short`) | ✅ green |
| integration lane (`-tags integration ./pkg/... .`) | ✅ green |
| `golangci-lint run ./...` | ⚠️ **62 issues, exit 1** (50 `errcheck`, 12 `staticcheck`). **No workflow references it** — OPS-3 confirmed, now with a number |
| `govulncheck ./...` | ⚠️ **not verifiable here** — OOM-killed (exit 137) three times on the 105 (1 vCPU / 2 GB, shared), even scoped to `./cmd/appitools/...`. CI runs it as a blocking gate (`.github/workflows/ci.yml:112`), and AGENTS.md records a real incident where it blocked a release, so the gate demonstrably works — it simply cannot be exercised on this box |

---

## 4. Documentation claims — the audit

**34 verified · 6 corrected · 4 not verifiable.** Only the non-"✅ as written"
verdicts are listed; the verified set is the body of §2 and §3, plus the functional
surface below.

**Verified live, in one batch** (all ✅): the nine documented filter operators
(`eq`/`partial`/`start` on strings, `gt`/`gte`/`lt`/`lte` on numbers,
`after`/`before` on time); an unknown filter field → `400`; a `422` listing **every**
invalid field at once; an unknown field on write → `422 unknown_field` (not a `500`,
not silently dropped); `?count=true` adding `meta.total` and the plain list carrying
**no** total (off by default); `/openapi.json`, `/openapi.yaml` and `/docs` served
unauthenticated; `POST /api/transaction` running cross-resource ops atomically, the
failing op naming its **index**, the whole batch rolling back with no partial state,
and 101 ops → `400` (cap 100); the 1 MB body cap → `413`; the SSE stream reachable
with a valid token; strict-key rejection of an unknown top-level schema key.

### ✏️ Corrected

| # | Where | Claimed | Truth today | Fix |
|---|---|---|---|---|
| C-1 | `README.md` | "one **~60 MB** static Go binary" | a plain `go build` is **84.7 MB**; the **release** build (`scripts/build-engine.sh`: `CGO_ENABLED=0 -trimpath -ldflags="-s -w"`) is **63.7 MB** | README says ~64 MB and names the build that produces it |
| C-2 | `README.md` | "2,000 req/s sustained … 0 errors" | true **only** with `RATE_LIMIT_RPS=3000`; on defaults ~50 % `429` | caveat added |
| C-3 | `README.md` | "~4.8× faster … ~2.7× bypassed" (present tense) | not re-verified since 2026-06-10 | re-dated and marked |
| C-4 | `AGENTS.md`, `README.md` | "Filter ops `neq`, `in`, `like`, `is_null` → 400" | `neq`/`in`/`nin`/`like`/`ilike` **do** → 400. **`is_null` returns `200` and is silently ignored** | doc corrected to the measured behavior; the defect is ENG-14 |
| C-5 | `context-docs/BENCHMARK_PUBLIC.md` | reproduction uses `appitools token --tenant 10` | tenant id `10` is **no longer registrable** (ENG-11 tightened the rule to `^[a-z][a-z0-9]{1,29}$` — it must start with a letter) | recipe updated |
| C-6 | `docs/BENCHMARKS.md` §3 | production layers cost "+0.97 ms" | **+1.22 ms** measured 2026-08-01 | both figures kept, each with its date |

### ⚠️ Not verifiable (with the reason)

| # | Claim | Why not |
|---|---|---|
| N-1 | whole-stack footprint 37.5 / 109 / 153.5 MiB | the reference box now runs two extra apps; the original condition cannot be recreated without taking them down |
| N-2 | full-stack knee at 750 rps | needs a Caddy site for the bench app on the live box; declined (OPS-11 risk) |
| N-3 | the NestJS comparative figures | §2.3 |
| N-4 | `govulncheck` clean | §3.5 — OOM on this hardware |

### The "honest list of what's missing" (`docs/CAPABILITIES.md`)

Re-read against the field reports (PARTS ONE–FIVE) and the backlog. It was **missing
five real limits** discovered in the journeys; all five are now listed:

- no zero-downtime binary upgrade (ENG-2) — a deploy costs ~0.5 s of `502`s;
- **no restore command in the engine** — `backup.sh` exists; restore is a documented
  `pg_restore` procedure (ENG-3);
- `install.sh --app` has never run on a real multi-app box (OPS-11);
- **the Go module is not published**, so the framework half is unreachable off this
  machine (DOC-2's remaining half);
- the platform super-admin can only be created from a terminal.

---

## 5. Fit to publish

If the public page and the public repo shipped tomorrow, **with today's evidence**:

**Can be said, backed by measurements dated 2026-08-01:**

- 2,000 req/s sustained on 2 vCPU with **0 errors in 597,461 requests**, p50 1.60 ms
  — stating the limiter setting it requires.
- p50 **1.53 ms at 500 rps**; **2.44 ms with the response cache fully bypassed**,
  every request reaching PostgreSQL, with JWT + RBAC + multi-tenancy + validation +
  rate limiting all active.
- A filtered, sorted, paginated page over **1 M rows in ~3 ms** engine-side
  (~4.2 ms through Caddy + TLS), not degrading as the table grows.
- Production HTTPS (reverse proxy + TLS) costs **~1.2 ms** at the median.
- **Multi-tenant isolation and RBAC hold under direct attack** — 24 live probes
  across every read and write surface, including BOLA by direct id and
  mass-assignment on create.
- The nine findings of the original OWASP review are **all still fixed**, verified
  live.

**Must NOT be said without new work:**

- **Any comparison to NestJS.** Not re-verified; the conditions are gone. Either
  re-run it on a dedicated box (OPS-12) or drop the claim.
- **"~60 MB binary."** It is ~64 MB release / ~85 MB plain build.
- **"0 errors at 2000 rps"** without naming the limiter configuration.
- **Whole-stack footprint numbers** as if measured today.
- **"golangci-lint clean"** — it is not, and it does not run in CI.

**Should be said, and currently is not:** the default per-tenant rate limit is
1000 rps. It is the first ceiling most single-tenant load tests will hit, and it is
not a defect — it is a safe default that the benchmark deliberately raises.

---

## 6. The measurement infrastructure (Part 0)

The canonical baseline tenant had been deleted **twice** by routine cleanups, cutting
the historical series both times. A documented "do not delete this" rule already
existed and did not survive contact with reality, so the fix is idempotence rather
than another rule:

- **`scripts/bench-protocol.sh` now self-heals its fixture.** If the bench tenant is
  missing it is recreated **from the schema the target engine actually serves**
  (`GET /editor/current-schema`), so the fixture is correct by construction for
  whatever app is under test. It only ever *creates* a missing tenant — never
  migrates, never deletes, never touches an existing one. `BENCH_NO_AUTOFIX=1`
  disables it. **Proven:** the tenant was deleted and the next run recreated it and
  measured with no intervention.
- **`benchmarks/history.tsv`** is a new append-only log. Every protocol run writes
  one row: date, commit, label, target, tenant, endpoint, script, rate, duration,
  runs, p50/p95 medians, between-run CV, error rate, and a free-text condition note.
  This session's runs are its first entries — the series now exists.

`OPS-9` closes with this. What it asked for beyond the fixture (deriving defaults
from the served schema) was already in place and worked: with the tenant missing, the
pre-flight failed *actionably*, naming the endpoint, tenant, role and overrides.

---

## 7. What this session did NOT certify

Stated plainly, so nobody mistakes silence for a pass:

1. **The NestJS comparison** (§2.3).
2. **Full-stack figures through Caddy+TLS for the API path** — only the
   trivial-endpoint layer cost was measured (§2.4).
3. **Whole-stack footprint** under the original condition (N-1).
4. **`govulncheck`** (N-4).
5. **`Route.Public` optional authentication** — the surface that moved most recently.
   The pure `serve` binary registers no custom routes, so a live test needs a
   consumer binary; the two on the 58 are production assets and exercising their
   public routes means writing to them. Covered by `route_public_test.go` at the unit
   level only. Tracked as **SEC-3**.
6. **`api-cert.sh`** — bound to the ERP demo collection and needs `newman`, which is
   not installed on this box. The generic layer of `acceptance-test.sh` was exercised
   instead.
7. **Chaos / resilience figures** (`BENCHMARKS.md` §6) — they kill services and
   reboot the box; the 58 serves two live apps.

---

## 8. Reproducing this report

```bash
# 1. Engine benchmark (needs a SUT and a separate loader)
bash scripts/bench-protocol.sh 10 <label> 2000 30s      # env in §1

# 2. Security battery — a local engine with two tenants and four roles
go build -o appitools ./cmd/appitools
DATABASE_URL=… JWT_SECRET=… ADMIN_KEY=… APPITOOLS_ENV=production \
  ./appitools serve --schema <sec-schema>.json --port 8081
#    the probes themselves are inline in §3.1–3.2

# 3. Toolchain
go vet ./... && gofmt -l . && make test
go test -tags integration ./pkg/... .
golangci-lint run ./...        # 62 issues today
govulncheck ./...              # needs > 2 GB RAM

# 4. Production stack, from a machine that is NOT the server
bash scripts/verify-production/run-all.sh --target=https://… --server-ssh=root@…
```

Findings opened by this session: **SEC-1**, **SEC-2**, **SEC-3**, **ENG-14**,
**OPS-12** — all in [docs/BACKLOG.md](BACKLOG.md), with the evidence above.

---

## Addendum — post-certification contract changes (THIRD-PARTY-READY-S1, 2026-08-02)

This report certifies the engine as of 2026-08-01. The following session
changed three certified-adjacent contracts, each measured with the session's
own gates (binary-diff 64/65 SAME with the single diff being the intended
change below; write-path ABBA `no_change`, Δp50 +0.055 ms, MWU p=0.096, the
base-vs-base control moving 3× more than the effect):

- **`/openapi.json` now also lists an app's REGISTERED custom routes**
  (ENG-33): method/path/auth-mode/`x-public`/`x-required-role`/
  `x-byte-serving`, summary from the new optional `Route.Description`, and the
  `info.description` now names the agent trilogy. A pure `serve` binary's
  document is byte-identical except for that description line (the gate's one
  diff). The 401-before-routing probe semantics are unchanged and now
  documented as deliberate.
- **Custom GET routes answer HEAD** (ENG-32) — was a 401/404-shaped nothing.
  Generated routes unchanged (pinned by a corpus row, SAME on both binaries).
- **File fields can declare `accept`/`max_bytes`** (FILES-1, attach-time 422
  `file_policy`) and **`Ctx.ServeFile` accepts a cache policy** (FILES-2).
  Additive: schemas and handlers that declare neither behave byte-identically
  (the whole 63-case data-path corpus is SAME).

No previously certified performance or security claim is invalidated; the
flagship numbers were not re-measured (nothing on the read path changed — the
gate shows every read-path corpus case SAME).
