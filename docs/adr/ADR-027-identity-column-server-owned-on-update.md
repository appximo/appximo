# ADR-027 — The identity column is server-owned on UPDATE, not only on create

**Status:** accepted (MOTOR-AUTORIZACION-S1, 2026-08-27)
**Drivers:** ENG-45 family 1 — the WRITE-ASYMMETRY-S1 matrix showed that an
owner-scoped role could hand its row to another principal with a plain
`PATCH {"owner_id": "<other>"}` (200), through every write door, in every RBAC
form; verified against the published v0.1.8 and v0.1.9
([docs/audits/AUTHZ_WRITE_AUDIT_S1.md](../audits/AUTHZ_WRITE_AUDIT_S1.md)).
Every app built on the engine inherits it.

## Context

A row condition (`"conditions": {"field": "owner_id", "op": "eq", "val":
"$user_id"}`) has always been enforced in two places: the WHERE of every
read/update/delete (BOLA: a row the caller does not own is invisible, 404),
and — since FASE3-SEC — on CREATE, where `EnforceCreateRBAC` FORCES the
condition column to the caller and rejects a body naming another principal
(403 "must match the authenticated principal").

Update had only the WHERE. The WHERE guarantees the caller may touch the row
it is updating; it says nothing about the VALUE the body writes into the
condition column. So the caller could reassign the row (another id), detach it
(`null`), or — with a full-replacement PUT omitting a nullable condition column
— NULL it by accident. Same rule, two answers by verb: the class the project
had just closed for `id`/`auto` fields (WRITE-ASYMMETRY-S1), on a different
seam.

The question the backlog left open was whether ownership transfer BY THE OWNER
is legitimate. It is a real operation (hand a ticket to a colleague, transfer a
listing) — but it is the SERVER's decision, expressed by a role that is not
scoped by that column, never the client's body passed through a scoped role.
An owner-scoped role is, by declaration, "a principal who may act on rows that
are theirs"; a rule that lets it change what "theirs" means is not a scope.

## Decision

**For a row condition bound to the caller's identity (`$user_id` /
`$external_client_id`), the condition column is server-owned on every write.**

- **Create** (unchanged): forced to the caller; another value → 403.
- **Update** (new, `codegen.EnforceUpdateRBAC`, the same file as the create
  half — `pkg/codegen/rbac_write.go`): a body that supplies the column with
  anything but the caller's own id — another principal, `null` — is the
  SAME 403 with the SAME message. A full-replacement PUT that omits the
  column keeps the caller's value (never NULL). Re-sending the caller's own
  id is a no-op, so full-object saves keep working.
- **One implementation, every door:** REST PUT/PATCH, GraphQL `update…`,
  `POST /api/transaction`, `Ctx.Update`. A door that stops calling it fails
  the all-doors integration test (`ownership_update_integration_test.go`).
- **Judged on the client body, before the row lookup and before the field
  allowlist.** Body-only, so it can never become an existence oracle (another
  principal's row stays 404 whether or not the body names the column); before
  the allowlist, so an allowlisted role gets an explicit 403 instead of a 200
  that silently dropped the field (an attempt is rejected, never swallowed —
  the project's standing rule).
- **A LITERAL condition is deliberately NOT bound.** `status = "pending"` is a
  visibility filter, not ownership: a moderator scoped to pending tickets
  approving one moves it out of scope, and that is the workflow. Binding it
  would break a legitimate pattern to close no hole (there is no principal to
  give the row to). `rbac.WhereCondition.Identity` carries the distinction from
  the evaluator; `rbac.IsIdentityVar` is the single predicate.
- **The server-side path stays open and is the documented way to transfer:**
  an unscoped role (admin; a `condition_actions` list that leaves `update`
  unconditional — "read all, write own" is unaffected because there the
  condition DOES apply to update), or a custom handler running as such a role
  / on `UnsafeTx`. A `before_update` hook (the app owner's server-side code)
  runs after the check and may still reassign deliberately.

Closed in the same pass, same shape (a value reaching SQL that the contract
forbids): a state-machine field set to `null` (PATCH null, PUT omitting it)
was a 500 at every door; it is a named 422 (`rule: "state"`) from one source
(`codegen.StateFieldNullViolations`).

## Consequences

- A client that used to reassign ownership through a scoped role now gets
  403. That client was exploiting the hole, whether it knew or not; the
  server-side path is one role grant away.
- A PUT by a scoped role on a resource with a nullable condition column no
  longer orphans the row; a PUT on a resource with a lifecycle must carry the
  state field (422 says so) instead of failing with a 500.
- Hot path: one nil check for every role without an identity condition
  (measured `no_change`, ABBA with frozen binaries on the PATCH protocol).
- Published contract: `docs/SCHEMA_REFERENCE.md`, `AGENTS.md`, `GUIDE.md`,
  `backend-spec` §3.3/§3.5 say the update side now; the gate corpus pins
  eleven rows (`owner-*`).

## Alternatives considered

- **Silently drop the column on update** (the allowlist's contract). Rejected:
  it hides the attempt — the client believes it wrote, the log shows nothing.
- **Allow and document ("the owner may transfer").** Rejected: a scoped role
  cannot be allowed to redefine its own scope; transfer belongs to a role that
  is not scoped by that column.
- **Bind literal conditions too.** Rejected: breaks the moderator/approval
  pattern for no security gain.
- **Publish the rule in `/openapi.json` (`x-appximo-identity`).** Rejected:
  RBAC grants are per role and deliberately unpublished (the ENG-27
  anti-enumeration asymmetry); the 403 message is the contract a client sees.
