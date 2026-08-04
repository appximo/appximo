# ADR-023 — The deployable-binary contract (consumer apps on the official production path)

**Status:** Accepted (CONSUMER-PATH-S1, 2026-07-31)
**Origin:** PROD-JOURNEY-1B walked the full production lifecycle with a CONSUMER
binary (the commerce app) and found the official tooling implicitly assumed "the
binary is the engine" (commerce `docs/GAPS.md` 3-1..3-5). The consumer had to
*impersonate* the engine to be installable. This ADR names the contract, so any
consumer app deploys without tribal knowledge.

## Context

`install.sh`, `deploy-update.sh` and the systemd unit the installer writes were
built for `appximo serve`. Concretely they assumed:

1. **Identity:** `install.sh` validated the binary with
   `<bin> version | grep -qi appximo` — an unexplained grep; a consumer binary
   without a `version` subcommand was refused with "not an appximo binary".
2. **Invocation:** the unit runs `<bin> serve --schema <path> --port <n>`. Go's
   `flag` package stops parsing at the first non-flag argument, so a consumer
   main using plain `flag.Parse()` **silently discarded every flag** and booted
   with defaults — the canonical silent-failure mode.
3. **Version traceability:** nothing told a consumer to inject a build version;
   `/health` reported `"dev"` and a rollback decision was guesswork (OPS-4).
4. **Ops tooling:** `admin create`, `tenant`, `migrate`, `token`, `validate`
   live in the ENGINE's CLI. A consumer binary serves; it cannot operate its own
   database. The journey had to improvise scp'ing the engine CLI.
5. **Control plane:** the installer's summary hardcoded :9090; a consumer picks
   its own port (commerce: 9099) and the doc lied.

## Decision

**A deployable binary is any binary that honors this contract:**

| Obligation | Exact form |
|---|---|
| **Identity** | `<bin> version` exits 0 and prints one line: `<name> <version> (commit <sha>)…`. The installer/deploy check is "does `version` run and print something" — **no name grep**: the contract is behavioral, not branding. |
| **Serve** | `<bin> serve --schema <path> --port <n> [--control-port <n>]` starts the server. A leading `serve` word is accepted; **any other bare word, and any leftover argument after the flags, aborts loudly (exit ≠ 0) with an actionable message** — never a silent boot with defaults. |
| **Config** | `DATABASE_URL`, `JWT_SECRET`, `ADMIN_KEY` from the environment (the unit's `EnvironmentFile=/etc/appximo/appximo.env`), plus any `APPXIMO_*` the engine documents. |
| **Health** | `/healthz` (liveness) and `/health` (`{"status","version"}`) on the data port; the version equals the `version` subcommand's. `/readyz` flips 503 while draining. |
| **Version** | Injected at build time (`-ldflags -X main.version=…`) and passed to `Config.Version`, so `/health`, the deploy smoke check and the rollback decision see a real SHA. |

The library makes compliance one call: **`appximo.ParseServeArgs(name,
version, revision, defaults)`** implements version/serve/fail-loud parsing, and
`scripts/build-consumer.sh` is the canonical consumer build (SPA + static build
+ ldflags). A consumer main is ~4 lines of contract code.

**The ops CLI ships as a companion, explicitly.** In production a consumer
deploy is **two artifacts**: the app binary (serves) and the engine CLI
(operates: tenants, migrations, tokens, super-admin). `install.sh --cli=PATH`
installs it at `/opt/appximo/bin/appximo-cli`; when the installed binary IS
the engine, the installer symlinks `appximo-cli` to it so ONE documented path
(`appximo-cli …`) works on every box. The "one binary" claim in
README/CAPABILITIES/PRODUCTION is reconciled to this truth: one binary **serves
everything**; a second, optional binary **operates** it.

**The control-plane port is detected, not assumed.** After start, the installer
reads the service's actual listening sockets (`ss -ltnp` on the unit's PID) and
prints the real control port in the summary and the register-tenant hint.

**Secrets never go to stdout.** The installer prints WHERE they live
(`/etc/appximo/appximo.env`, 0600); `--show-secrets` opts into printing
values for a human on a private terminal.

## Options considered

- **Keep the engine-CLI shape as an implicit contract, document it** — rejected:
  documentation alone leaves the silent flag-parsing trap armed, and every
  consumer re-implements version/serve by hand (commerce did; the next app
  would too, differently).
- **Embed the full ops CLI into every consumer binary** (export the cobra tree
  from the library) — rejected for v1: it drags cobra + every ops dependency
  into every consumer build, blurs the security story (a serving binary that
  can also drop tenants), and the companion-CLI model matches operational
  reality (humans/scripts operate; the service serves). Reconsider if a
  single-artifact story becomes a product requirement.
- **A `describe`/`ports` subcommand for control-port discovery** — rejected:
  runtime detection from the live listener is strictly more truthful than
  self-declaration, and adds zero API surface.
- **systemd socket activation / a pidfile protocol** — out of scope; nothing in
  the current path needs it.

## Consequences

- Any consumer app using `ParseServeArgs` + `build-consumer.sh` installs,
  deploys, health-checks and rolls back through the untouched official path.
- The engine binary itself already satisfies the contract (its cobra CLI
  provides `version`/`serve`), so nothing changes for pure-engine installs.
- A binary that does NOT satisfy the contract is refused at install time with
  the contract named in the error — the failure moved from "silent wrong boot"
  to "loud, actionable, at the door".
