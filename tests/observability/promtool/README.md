# promtool SLO rule tests — not applicable in S37 (deviation)

`context-docs/TESTING_PLAN.md` lists `slo_test.yml` here *"si reglas SLO existen como
YAML"* (if SLO rules exist as YAML). **They do not.**

Appitools' SLOs are **not** implemented as Prometheus alerting/recording rules.
They live as a Go burn-rate engine:

- `pkg/observability/slo.go` — multi-window burn-rate engine (`SLOEngine`), wired in
  `cmd/appitools/cmd_serve.go` (`NewSLOEngine(...)`, `go sloEngine.Run(ctx)`).
- `pkg/observability/alerter.go` — Slack / Noop / Cooldown alerters.
- Covered by Go unit tests: `pkg/observability/slo_test.go`, `alerter_test.go`.

There is therefore no `*.rules.yml` for `promtool test rules` to exercise, and no
Prometheus rule-evaluation in the stack. The repo-wide search for
`alert:` / `expr:` / `for:` in YAML returns nothing (verified S37).

`promtool test rules` becomes relevant only if/when the burn-rate SLOs are *also*
exported as Prometheus YAML rules (e.g. for an external Alertmanager). Until then,
the SLO logic is validated by the Go tests above, and the runtime SLO state is
asserted end-to-end by the k6 gate (`tests/performance/`) and visible at
`/debug/tenant/{id}` (`.slo`).
