# `lab` — the ephemeral capacity laboratory

CAPACIDAD-USL-S1 measured the engine's ceiling with the load generator ON THE
SAME BOX as the engine and PostgreSQL: the instrument competed for CPU with
what it measured, ate a measured 13–16 % of the only core, and the 420 rps
bistability could be the app, the generator, or both. This tool exists to make
that confound structurally impossible: a **disposable, isolated, reproducible**
laboratory on DigitalOcean, driven from the orchestrator box in one command.

It is laboratory tooling, not engine surface: nothing in `pkg/` imports it,
and no served binary contains it. Standard library only.

```
go build -o lab ./tools/lab
```

## The leash comes first

DO's granular scopes are per resource TYPE, not per resource — a token with
`droplet:delete` can delete every droplet in the account. The protection is
this wrapper, and it is TESTED (`guard_test.go`), not promised:

1. Droplets are created only with the **`applab-` name prefix AND the
   `applab` tag** — both, always.
2. Destroy requires **prefix AND tag AND no protected IP** (the production
   boxes are refused by address even if mislabeled). A refusal happens
   **before any network call** — the tests pin zero HTTP requests. The
   protected addresses are infrastructure, not source: the operator keeps
   them in `/root/.applab-protected` (one IP per line, optional label after a
   space) or `APPLAB_PROTECTED_IPS`; an empty belt is warned loudly (the
   name+tag guard needs no config and always applies).
3. A **hard cap of 4** simultaneous lab droplets. A broken loop cannot open
   twenty.
4. **`lab reap`** destroys anything `applab-*` older than `-max-age`
   (default 6 h) and prints the remaining listing as proof. It runs at every
   session close, no exceptions — a forgotten droplet bills silently.
5. The token lives in `/root/.do-lab-token` (mode 600, created by the
   operator; custom scopes `droplet:create/read/delete`, `ssh_key:read`,
   `vpc:read`), is read at runtime, and is **never printed, logged or
   committed**.
6. **Dry-run is the default.** Every mutating command prints its plan and
   touches nothing until `-apply` is passed.

## The topology (`lab up -apply`)

| box | size | why |
|---|---|---|
| `applab-gen` | `c-4` (4 **dedicated** vCPU) | the instrument may not have noisy-neighbour jitter, and must be bigger than the target — a generator that saturates first measures itself. Every run records the generator box's CPU; a run where it exceeds **70 % busy is INVALID** and says so. |
| `applab-target-basic` | `s-2vcpu-2gb` (Basic, **shared** vCPU) | what a customer actually buys — this box yields the honest number you tell a customer. |
| `applab-target-dedic` | `c-2` (2 dedicated vCPU) | low variance — the box for detecting regressions between versions; under 21 % steal a regression and a noisy neighbour are indistinguishable. |

All three share one region and its default **private VPC** (without the VPC
you measure the internet); `up` measures and records the private link's base
RTT. Targets are provisioned with **`scripts/install.sh`** — the exact customer
path — so every lab run also exercises the installer; an installer failure is
reported as a finding. After install, the per-tenant limiter's THRESHOLD is
raised out of the way (`RATE_LIMIT_RPS=1000000` in the env file — the
middleware stays in the chain; CAPACIDAD-USL-S1's 8-IP control showed raising
the number changes nothing else), the `lab` tenant is registered, the
deterministic dataset is seeded (`dataset/README.md`, sizes `small`/`large`),
and a tenant token is minted. `up` then tries to snapshot the provisioned
target so the next `up` is fast; if the scoped token cannot take snapshots it
says so and every `up` reinstalls from scratch (which re-exercises the
installer — not wasted work).

## The commands

```bash
lab up     -apply [-dataset large]        # topology + install.sh + seed + smoke
lab sweep  -apply [-target both]          # the authoritative ladder (25→700 rps,
                                          #   ×3/×4) + the 420×5 bistability probe;
                                          #   detached on the generator, SSH-hiccup-proof
lab soak   -apply -hours 8 [-rate 265]    # the endurance run
lab report -in results.jsonl \
           [-baseline old-sweep.jsonl]    # conditions + bistability verdict +
                                          #   point-by-point overlay vs the baseline +
                                          #   the capacity tool's USL fit + real cost
lab down   -apply                         # destroy EVERYTHING applab; verdict comes
                                          #   from a final re-listing, retries per
                                          #   droplet, idempotent — re-run to finish
lab reap   -apply [-max-age 6h]           # the backstop; ALWAYS at session close
lab status                                # API truth + local state
```

The sweep workload is byte-identical to the CAPACIDAD-USL-S1 baseline
(`?fields=` + filter + sort + cache buster on `productos`), so
`lab report -baseline` compares the curves point by point — that comparison,
not the new number, is the deliverable that says how much of the previous
measurement was the instrument.

State (droplet ids, private IPs, the minted lab secrets) lives in
`~/.applab/state.json`, mode 0600, outside every repo. `down` and `reap`
never trust it — they list by tag from the API.

## Cost (DO list prices, 2026-08)

c-4 $0.125/h + c-2 $0.0625/h + s-2vcpu-2gb $0.02679/h ≈ **$0.21/h for the
full topology**. A typical sweep session (~2.5 h up-to-down) ≈ **$0.55**; an
8 h soak ≈ **$1.90**. `lab report` prints the actual figure from the actual
uptimes.

## What it refuses to do

Touch the 105 or the 58 (protected by IP), destroy anything without the full
lab identity, exceed the cap, print the token, or publish a run in which the
generator was saturated.
