# Benchmark data

[`data/s46-pub-runs.csv`](data/s46-pub-runs.csv) is the raw per-run
export behind every number in the public benchmark — all 109 runs,
including the schedule-invalid ones (label-marked, never deleted).

[`data/deploy-overhead-runs.csv`](data/deploy-overhead-runs.csv) is the
raw export behind the per-layer deployment-overhead table in
[docs/DEPLOY.md](../docs/DEPLOY.md#measured-overhead-of-each-layer) —
30 runs (10 per configuration: native / Docker bridge / native+Caddy),
all included; the two runs where the loader missed its k6 schedule
(`dropped_iterations > 0`) are identifiable by their inflated tails and
were excluded from nothing — the verdicts use pooled Mann-Whitney plus
median-of-run-medians, both robust to them.

- **Methodology, statistics, limitations**:
  [context-docs/BENCHMARK_PUBLIC.md](../context-docs/BENCHMARK_PUBLIC.md)
- **Reproduce it**: [`benchmark-lab/`](../benchmark-lab/) has the k6
  scripts, the NestJS baseline, and the seed; the protocol driver is
  `make bench-protocol` (warmup + N runs + statistical verdict).
