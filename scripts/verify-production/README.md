# `verify-production` — measure YOUR Appximo deploy

> Don't take our numbers. Run this against your own server and get your own.

This is a repeatable suite that measures the **whole production stack** — Caddy
(TLS) → the engine → PostgreSQL, under systemd, on your box — not the engine in
isolation. It answers, with evidence:

| Question | Script |
|---|---|
| How much RAM/CPU does the stack cost, idle and under load? | `footprint.sh` |
| How much load does it sustain, and where is the knee? | `load.sh` |
| What do TLS and the reverse proxy actually cost? | `load.sh --compare-tls` |
| Does it stay fast at 100K / 1M rows? | `seed.sh` + `load.sh` |
| REST or GraphQL for my frontend? | `load.sh` (both arms) |
| What happens when I kill it / it reboots / I deploy under load? | `chaos.sh` |
| All of the above, in one report | `run-all.sh` |

Everything is parameterizable, idempotent, re-runnable, and writes both JSON
(machine-readable, one file per measurement) and a Markdown report.

---

## Quick start

You need **two machines**: your server, and a load generator that is *not* your
server. A loader sharing the server's CPU competes with the thing it measures and
corrupts both sides; the suite detects co-location and stamps the results as such,
but the honest numbers come from a second box.

On the **load generator** (needs `k6`, `python3`, `curl`, and ssh to the server):

```bash
git clone <this repo> && cd appximo/scripts/verify-production

bash run-all.sh \
  --target=https://api.example.com \
  --server-ssh=root@YOUR.SERVER.IP
```

That runs the footprint, load, ladder, layer-cost, and REST-vs-GraphQL phases and
writes `verify-results/<timestamp>/report.md`.

Add data and chaos when you want the full picture (**`--with-chaos` kills
services and reboots the box — only on a machine you can afford to break**):

```bash
bash run-all.sh --target=https://api.example.com --server-ssh=root@IP \
  --seed=1000000 --with-chaos
```

Just checking the plumbing works? `--quick` (short runs, few repetitions — a
smoke test, explicitly *not* evidence).

---

## The two things that most often make a benchmark lie

The suite checks both and refuses to hide them.

**1. A CDN in front of your domain.** If `api.example.com` is proxied by
Cloudflare or Fastly, the name resolves to the *edge*: every request crosses the
internet twice and is terminated by the CDN's TLS. You would be measuring the
CDN. The suite detects this and tells you:

```
! a CDN (cloudflare) fronts api.example.com. These numbers therefore include the
  CDN's hop and TLS, NOT just your stack.
  For the product's own latency, re-run with --origin-ip=<your server's IP>.
```

With `--origin-ip` it pins the hostname to your origin (correct SNI, your own
certificate) and measures *your* stack. Both numbers are legitimate — the origin
number is the product's latency, the public number is what a user experiences
with that CDN in front — but they must never be confused. On our reference box
the difference was **2.8 ms vs 9.8 ms**: the CDN was 3.5× the entire product.

**2. The response cache.** The engine caches GET responses for 5 s, keyed by
tenant + request URI. Hammering one URL therefore measures the cache. So the
suite runs **both arms** and labels them:

- `cache on` — the production default. Models many clients loading the same
  page; the cache legitimately absorbs them.
- `cache bypassed` — every request URI is unique, so every request reaches
  PostgreSQL. **This is the floor**, and the number to quote when someone asks
  what the stack really sustains.

---

## Scripts

Each runs standalone; `run-all.sh` just sequences them.

### `footprint.sh` — runs ON the server

```bash
bash footprint.sh --label=idle
bash footprint.sh --label=under-load --watch=60 --interval=5   # reports the PEAK
```

Reports **PSS**, not RSS. RSS double-counts the shared pages every PostgreSQL
backend maps, which is how PostgreSQL ends up reported as several times its real
size; PSS divides shared pages among their sharers so the per-service numbers sum
correctly. It also splits **anonymous** memory (truly owned by the process) from
file-backed pages (the mapped binary — page cache the kernel reclaims under
pressure). For "will this fit in 1 GB", the anonymous number is the one that
decides. cgroup `memory.current` per unit is reported alongside as an independent
second source.

### `seed.sh` — runs ON the server

```bash
bash seed.sh --tenant=api --orders=1000000                # additive
bash seed.sh --tenant=api --orders=100000 --reset         # start clean
```

Writes directly via SQL (a million HTTP POSTs would take hours and would measure
the loader). Rows are shaped exactly like the engine's own, deterministic by
construction so two runs with the same `N` produce the same distribution, and it
`ANALYZE`s afterwards — without fresh statistics the planner may pick a
sequential scan and "slow at 1M rows" becomes an artifact of the seeding.

Expects a schema with `customers` / `orders` / `order_items`; the one used for
the reference numbers is [`schema/bench-schema.json`](schema/bench-schema.json).

### `load.sh` — runs ON the load generator

```bash
bash load.sh --target=https://api.example.com --token=$JWT --scenario=read --both-cache-arms
bash load.sh --target=... --token=... --ladder="100 250 500 1000 2000"
bash load.sh --target=... --token=... --compare-tls --engine-url=http://127.0.0.1:8090
```

Scenarios: `read` `write` `mix` (80/20) `heavy` `aggregate` `rest_include`
`graphql_nested`.

Methodology, inherited from the engine's own `scripts/bench-protocol.sh`:

- one **warmup** run, discarded (the pgx pool, PostgreSQL's buffer cache and
  Caddy's connections are all cold on the first pass)
- N measurement runs with cooldowns, so run-to-run variance is visible instead of
  hidden inside one long run — the report prints the between-run CV
- percentiles from the **pooled raw sample**, never the mean of per-run
  percentiles (the mean of per-run p99s is not the p99)
- an **open** workload model (constant arrival rate), so a slowing server cannot
  throttle its own offered load and hide its saturation
- a **pre-flight** request per scenario whose body is actually inspected, so a
  broken endpoint is never measured — this matters most for GraphQL, which
  answers HTTP 200 and puts errors in the body

`--compare-tls` is a paired **ABBA** A/B (public HTTPS vs the engine directly)
so anything that drifts over the session cancels out instead of being attributed
to the layer.

### `chaos.sh` — runs ON the load generator, breaks the server

```bash
bash chaos.sh --target=https://api.example.com --server-ssh=root@IP --case=engine-kill
bash chaos.sh --target=... --server-ssh=... --case=all
```

Cases: `engine-kill` `caddy-kill` `postgres-stop` `pool-exhaust`
`memory-pressure` `deploy-update` `reboot`.

It runs from the loader on purpose: the probe has to survive the server
rebooting, and "what does the user see" is only answerable from outside. Each
case starts a fixed-rate probe, lets it settle, injects the fault at a recorded
timestamp, and keeps probing through the failure — then reports time-to-first
error, outage duration, **requests actually lost**, and the status codes the user
saw. A chaos test that doesn't measure the hole it punched is theatre.

Recovery requires a *run* of consecutive successes, not one lucky 200, so a
flapping service is not scored as recovered.

### `stats.py` / `report.py`

Pure standard library — no numpy, no pip — because the suite has to run on a box
where you just installed Appximo.

`stats.py` does percentiles, distribution-free bootstrap CIs for the median
(deterministic seed, so re-analysing the same data gives the same CI), and
**Mann-Whitney U** for A/B comparisons — latency is right-skewed, so a t-test is
the wrong instrument. A change counts as real only if it is **both**
statistically significant (p < 0.05) **and** practically material (median moved
more than max(0.5 ms, 3%)); otherwise the verdict is `no_change`. That is the
same gate `make bench-protocol` uses.

---

## Reading the report

Every number carries its conditions — scenario, rate, cache arm, where load came
from, whether a CDN was bypassed. A latency without those is a rumour.

Watch for:

- **`run-to-run CV`** above ~15%: your measurement bench is noisy and no small
  delta from it should be believed.
- **`achieved rps` well below `rate`**: something could not deliver the offered
  load. That is sometimes the server saturating (a real knee) and sometimes your
  *loader* running out of CPU. The ladder says which.
- **`colocated_loader: true`**: the loader shared the server's CPU. Directional
  only.

## Reference numbers

Measured on a $16/mo 2 vCPU / 2 GB droplet: see
[docs/BENCHMARKS.md](../../docs/BENCHMARKS.md). Use them as a sanity check, not
a target — your hardware, network, schema and data are different, which is the
entire reason this suite exists.

## Pointing the verification at ANY instance (engine or consumer app)

This is the single declared interface (OPS-8, CONSUMER-PATH-S1). Every suite in
this repo takes the same idea: **name the target explicitly; nothing assumes
"the binary is the engine" or "the schema is the bench schema".**

| Knob | Used by | Meaning |
|---|---|---|
| `--target=URL` | `load.sh`, `chaos.sh`, `run-all.sh` | the public base URL under test (HTTP or HTTPS) |
| `--server-ssh=HOST` + `VP_SSH_OPTS` | `chaos.sh`, `run-all.sh` | where faults are injected; extra ssh options (e.g. a ControlPath) via the env var |
| `--path='/api/…?…'` | `load.sh` | the READ endpoint on a **consumer** app (the default `/api/orders` only exists on the bench schema; the pre-flight refuses a 404 instead of measuring it) |
| `VP_DB_PROBE_PATH='/api/…'` | `chaos.sh` | a **DB-touching** path on this deploy, so the database cases (postgres-stop, pool-exhaust) measure real user impact — on a consumer app `/healthz` proves nothing |
| `--token=JWT` | `load.sh`, `chaos.sh` | auth for the probed endpoints (mint with `appximo-cli token`) |
| `--origin-ip=IP` | `load.sh`, `chaos.sh` | pin the hostname past a CDN/proxy in front |
| `footprint.sh` | (runs ON the server) | needs `lib.sh` beside it; `--service=NAME` if the unit is not `appximo` |

The **acceptance smoke** (`scripts/acceptance-test.sh`, repo root) follows the
same rule with env vars: `BASE` / `ADMIN` (data/control plane), `APPXIMO_CLI`
(how to invoke the ops CLI), `SCHEMA_FILE` (the schema the app actually
serves), `ADMIN_ROLE` (the broad role that schema declares — a consumer app has
no `admin`), `PSQL_CMD` (optional physical checks), `TENANT_A`/`TENANT_B`
(smoke tenants — **created-by-the-run tenants are deleted at the end**, a suite
never leaves residue on a demo). Checks that require the quickstart contract
(`tasks` + `admin`/`viewer`) detect the served surface via `/openapi.json` and
report **INFO/SKIP with the reason** when it is absent: a FAIL always means
"broken", never "it is a different app".

The commerce regression suites (`/root/commerce/scripts/*`) use the same shape:
`BASE`, `HOST`, `TENANT`, `PSQL_CMD`, `TOKEN`.
