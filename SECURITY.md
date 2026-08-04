# Security Policy

## Reporting a vulnerability

**Please do not open a public issue for a security vulnerability.**

Report it privately, with a reproduction if you have one:

- **GitHub:** [private vulnerability reporting](https://github.com/appximo/appximo/security/advisories/new) (preferred)
- **Email:** miguel09acosta@gmail.com — subject starting with `[SECURITY]`

You should get an acknowledgment within **72 hours**. Please give us a
reasonable window to ship a fix before any public disclosure; we will credit
you in the release notes unless you prefer otherwise.

## How reports are handled

This is the practice the project already follows internally, now stated as
policy:

1. **Details stay out of writing until fixed.** An exploitable finding is never
   described in the issue tracker, the backlog, an audit document, or a commit
   message while it is open — only its existence and severity are registered
   (see `docs/BACKLOG.md` for an example of a redacted-by-design entry). The
   reproduction goes to the maintainer directly.
2. **The fix ships with proof.** A security fix lands with a regression test
   and, when it touches the data path, a row in the binary-diff gate corpus
   (`scripts/binary-diff/corpus.jsonl`) so the behavior is pinned from then on.
3. **Disclosure after the fix.** Once released, the finding is documented
   honestly — this project publishes its audits (`docs/audits/`), including the
   ones that found real defects.

## Scope

- The engine (`appximo serve`), its generated REST/GraphQL surface, auth
  (`/auth/*`, JWT, RBAC), the file store, the control plane, the admin API,
  fleet mode, and the worker/outbox path.
- The published Docker image and the production installer
  (`scripts/install.sh`).

Out of scope: the maintainer's demo deployments (do not scan or test against
domains you do not own — the CI security workflow itself refuses to point ZAP
at production), and dependencies with an upstream fix already available
(update instead; CI runs govulncheck and blocks known-vulnerable builds).

## Supported versions

Pre-1.0: only the latest release (and `main`) receives security fixes.

## Hardening notes for operators

- Never expose the control plane (`:9090`) to the internet.
- `JWT_SECRET` must be long and random (≥ 32 chars).
- `/metrics` and `/debug/*` are admin-gated; keep `ADMIN_KEY` strong.
- The production checklist lives in `docs/PRODUCTION.md` §Security checklist.
