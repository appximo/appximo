# `capacity` — the capacity laboratory

Answers, with a method instead of an anecdote, the question a customer
actually asks: **how many users does this hold?**

It is laboratory tooling, not engine surface: nothing here runs in a served
binary and nothing in `pkg/` imports it. Zero dependencies outside the
standard library and this repo's own `tools/devhub/stats`.

```
go build -o capacity ./tools/capacity
```

## The one command

```bash
# 1. a ladder of load levels, open model, repeats per level
capacity sweep -url http://127.0.0.1:8181 -host acme.localhost -token "$TOK" \
  -admin-key "$ADMIN_KEY" -pg-pid "$(docker inspect -f '{{.State.Pid}}' pg)" \
  -name read -path '/api/products?per_page=20&cb={n}' -span 1000000 \
  -rates 25,50,100,150,200,300,400,500,700 -repeats 3 \
  -duration 40s -warmup 10s -rest 12s -out sweep.jsonl

# 2. fit the Universal Scalability Law and print the whole report
capacity fit -in sweep.jsonl -think 30s,5s,0s -bootstrap 2000 -json fit.json
```

`sweep` prints a line per run and writes one JSON object per run;
`fit` prints the markdown that goes into a report.

Also: `capacity run` (one level), `capacity soak` (the endurance run, one row
per slice), `capacity soakreport` (the endurance verdict, by slope),
`capacity abba` (the frozen A B B A verdict, median AND tail).

## What it does that a one-off `k6` does not

**Open model, and coordinated omission corrected by construction.** Requests
go out on a schedule fixed before the run; the scheduler never waits for the
server. Every request records TWO latencies — `service` from the actual send,
`response` from the SCHEDULED send. When the system keeps up they are equal;
when it stalls, the requests that "should" have gone out during the stall are
still counted, with the wait included. Their divergence is the evidence the
correction is real; their agreement at low load is the evidence the instrument
is not inventing it. Cross-checked against `k6 constant-arrival-rate`:
identical achieved rate, medians within 10 %, and the tail difference has a
named cause (k6 reports connection-pool wait separately as `http_req_blocked`;
this tool includes it, because the user waits for it too).

**Client patience, not an infinite queue.** Past the ceiling an open-model
generator accumulates an unbounded backlog. `-patience` gives up on a request
that has waited too long and COUNTS it as abandoned with its full latency —
the opposite of coordinated omission, and what keeps the generator from
OOM-ing the box it is measuring.

**CPU accounting, so the confound is measured instead of hidden.** On a
single-vCPU box the generator competes with the engine it measures. Every run
records the CPU-seconds of the engine (`/proc/<pid>/stat`), of PostgreSQL (its
cgroup `cpu.stat` — summing processes by name UNDERCOUNTS and goes negative
when backends exit), of the generator itself, and the box's idle and steal.
From that comes the second, generator-free estimate: the **service demand law**,
`X_max = C/D`, an upper bound that does not depend on how much CPU the
generator stole. The USL fit bounds the truth from below, the service demand
from above.

**The engine's own verdict beside every point.** With `-admin-key` each run
asks the Module C self-monitor what it thought of that window, bounded with
`?since=` to that run alone. The ladder therefore produces a saturation
SEQUENCE — what gives first, what gives next — and not only a number.

**A trust gate that can refuse to publish.** R² ≥ 0.90, worst between-repeat
CV ≤ 5 %, ≥ 6 levels, and at least one level past the fitted peak. Below any
of them the report says **NOT publishable as a ceiling** and names which
criterion failed. `N_max` printed under a bad fit is how a benchmark becomes a
lie.

## The model

    X(N) = γN / (1 + α(N−1) + βN(N−1))            Gunther, USL
    N_max = sqrt((1 − α)/β)                        the peak
    users = λ × (think time + response time)       Little
    R = S/(1 − ρ)                                  M/M/1, the latency knee

γ is single-client throughput, α CONTENTION (serialisation), β COHERENCY (the
N² cost of keeping shared state consistent — what makes throughput go DOWN
past the peak rather than merely flat). β = 0 degenerates to Amdahl: a ceiling
at γ/α, never retrograde, and the report says so instead of printing an
`N_max` that does not exist.

The fit is Levenberg–Marquardt on the throughput residuals, seeded from
Gunther's own linearisation `(γN/X − 1)/(N − 1) = α + βN`. Uncertainty is a
bootstrap at the level of the REPEATED MEASUREMENT: each replicate draws one
run per level and refits. `usl_test.go` recovers known parameters from
synthetic data — the only way to know a nonlinear fit is not just drawing a
plausible curve through noise.

Gunther's warning is printed with every report and is part of the contract:
the law is not a crystal ball, it cannot predict an intrinsic pathology or a
broken measurement, and where the data diverge from it that is a fact about
the system, to be said and not smoothed.

## Declaring the load profile is not optional

`1000 rps` is not `1000 users`. The report translates a throughput into
concurrent users only under a **named** profile, and prints the assumption in
the same row:

| profile | think time | users at 281 rps |
|---|---|---|
| browsing | 30 s | 8 428 |
| active use | 5 s | 1 405 |
| burst, no pause | 0 s | 1 |

Same engine, same second, three answers differing by four orders of
magnitude. A capacity number without its profile is not an answer.
