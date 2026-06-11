# Benchmark data

[`data/s46-pub-runs.csv`](data/s46-pub-runs.csv) is the raw per-run
export behind every number in the public benchmark — all 109 runs,
including the schedule-invalid ones (label-marked, never deleted).

- **Methodology, statistics, limitations**:
  [context-docs/BENCHMARK_PUBLIC.md](../context-docs/BENCHMARK_PUBLIC.md)
- **Reproduce it**: [`benchmark-lab/`](../benchmark-lab/) has the k6
  scripts, the NestJS baseline, and the seed; the protocol driver is
  `make bench-protocol` (warmup + N runs + statistical verdict).
