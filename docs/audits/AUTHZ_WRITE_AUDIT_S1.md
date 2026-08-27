# Authorization write audit — every field the client must not write, every write door

**Session:** MOTOR-AUTORIZACION-S1 (2026-08-27), Part A — written BEFORE any fix.
**Trigger:** ENG-45 family 1 (the WRITE-ASYMMETRY-S1 matrix surfaced that an
owner-scoped role can give its row away on UPDATE) + ENG-47.
**Method:** no conclusion from code. One audit schema declaring every class
of field (the raw per-request results live in the maintainer's internal
repo, `evidencia/MOTOR-AUTORIZACION-S1/`), 61 real
requests per binary through every write door, the stored effect read back as
the admin after each write, fired at THREE binaries: HEAD `f9c6ba4`, the
published **v0.1.9** and **v0.1.8** (checksum-verified downloads).

## The doors

| Door | Path into the engine |
|---|---|
| REST create | `POST /api/{r}` → PrepareCreate → before_create hook → EnforceCreateRBAC → RunInsert |
| REST update | `PUT/PATCH /api/{r}/{id}` → ValidateWrite → CollectUpdate(allowlist) → existence check (row condition) → before_update hook → RunUpdate(row condition + transition guard) |
| GraphQL | `create<X>` (same create core) · `update<X>` (CollectUpdate + RunUpdate) |
| Batch | `POST /api/transaction` create/update ops (same cores) |
| Library | `Ctx.Insert` / `Ctx.Update` (custom Go handlers; PrepareCreate/PrepareUpdate + EnforceCreateRBAC on insert) |
| Files | `POST /api/files` (multipart; identity from Host+JWT only) + the `file` field attach |
| Auth | `POST /auth/signup` (client `role` ignored) |

Read-only surfaces (relation subroutes, `/admin/tenants/{id}/data`, SSE,
aggregates) take no body and are out of the class.

## The classes, and what the binary actually does

Legend: ✅ rejected explicitly · ❌ **accepted (hole)** · ⚠ silent · — n/a.

| Class | Field(s) probed | Door | HEAD | v0.1.9 | v0.1.8 |
|---|---|---|---|---|---|
| identity / PK | `id` on create | REST · batch · Ctx.Insert | ✅ 422 read_only | ✅ | ❌ **201, stored** |
| identity / PK | `id` on update | REST · batch · Ctx.Update · GraphQL (structural) | ✅ 422 | ✅ | ✅ |
| audit | `created_at`, `updated_at` (`auto`) on create | REST · batch · Ctx.Insert | ✅ 422 read_only | ✅ | ❌ **201, stored `1999-01-01`** |
| audit | `created_at`, `updated_at` on update | all | ✅ 422 | ✅ | ✅ |
| **ownership** | condition column = **another principal** on create | REST · batch · GraphQL · Ctx.Insert | ✅ 403 | ✅ | ✅ |
| **ownership** | condition column = **another principal** on UPDATE of an owned row | **REST PATCH · REST PUT · GraphQL update · batch update · Ctx.Update** | ❌ **200 — the row now belongs to B, A can no longer read it** | ❌ | ❌ |
| **ownership** | condition column = **null** on update | REST PATCH · batch · Ctx.Update | ❌ **200 — the row belongs to nobody** | ❌ | ❌ |
| **ownership** | PUT that OMITS a nullable condition column | REST PUT | ❌ **200, `owner_id` written NULL (self-orphan)** | ❌ | ❌ |
| **ownership** | condition column = other, role with a field allowlist that excludes it | REST PATCH · batch · GraphQL · Ctx.Update | ⚠ **200, silently dropped** (unchanged) — the attempt is hidden | ⚠ | ⚠ |
| ownership, all three RBAC forms | per-resource `permissions` (`owner_id`), role-global `conditions` (`created_by`), `condition_actions` (`author_id`) | every update door | ❌ identical in the three forms | ❌ | ❌ |
| literal condition (visibility filter) | moderator scoped `status = pending` approves a ticket | REST PATCH | ✅ 200 — **legit**, the row leaves the scope on purpose | ✅ | ✅ |
| BOLA baseline | update / steal another principal's row | all | ✅ 404 (hidden row, no oracle) | ✅ | ✅ |
| tenant | a declared column named `tenant_id` | create/update | plain field (201/200) — **tenancy is Host → schema, not a column**; nothing to govern | same | same |
| tenant | Host of another tenant with a valid token | any | ✅ 401 | ✅ | ✅ |
| tenant | file id used from another tenant's Host | GET /api/files | ✅ 401 | ✅ | ✅ |
| state | create in a non-initial state | REST · batch · GraphQL · Ctx.Insert | ✅ 422 | ✅ | ✅ |
| state | undeclared transition / move out of a terminal state | REST · batch · GraphQL · Ctx.Update | ✅ 422 (SQL guard, race-safe) | ✅ | ✅ |
| state | state field set to **null** (PATCH null; PUT omitting it) | REST · batch · Ctx.Update | ❌ **500 "internal error"** (the SQL guard receives a non-string) | ❌ 500 | ❌ 500 |
| file | attach a nonexistent / other-tenant file id | REST · batch · GraphQL | ✅ 422 file_not_found | ✅ | ✅ |
| files upload | extra multipart parts `tenant_id`, `original_name=../../etc/passwd` | POST /api/files | ✅ ignored (ADR-024 exception); identity from Host+JWT; name sanitized | ✅ | ✅ |
| declared exception | `created_at` on create by an `import`-granted role | REST | ✅ 201 stored verbatim (the server-side path) | ✅ | — (no `import` in v0.1.8) |
| unknown field | `ghost` | create/update | ✅ 422 unknown_field | ✅ | ✅ |
| auth | `role: "admin"` in the signup body | POST /auth/signup | ✅ ignored, configured role wins | ✅ | ✅ |
| auth | 7 logins, one identity | POST /auth/login | 5 × 401 then **429** (the ENG-47 baseline: 5/min, no knob) | same | same |
| `x-appximo-*` | `x-appximo-auto` (readOnly) → governed above; `x-appximo-relation`/`-file`/`-initial`/`-transitions` describe writable fields with their own checks (FK/policy/guard) | — | no derived field the contract marks that the engine lets the client write | | |

GraphQL `update<X>` with `{owner_id: null}` sent as a variable answers
`400 empty input`: graphql-go drops null variables (ENG-22), so the null
give-away is unreachable through GraphQL by accident, not by design.

## Findings, by real damage

1. **Privilege escalation between users of one tenant — the ownership give-away
   on update (ENG-45 #1), in every published version.** Any role with an
   identity-bound row condition (`$user_id` / `$external_client_id`) and the
   `update` action can `PATCH {"<cond>": "<other user id>"}` on a row it owns
   and hand it to another principal, or `null` it out of every principal's
   scope; a nullable condition column is NULLed by a PUT that omits it. Five
   doors, three RBAC forms. Create closes the same hole with a 403
   (`EnforceCreateRBAC`); update has no counterpart — the condition lives only
   in the WHERE. **Exploitable in v0.1.8 and v0.1.9** (reproduced against the
   downloaded binaries). Damage: an app whose users own rows (orders,
   documents, tickets — every owner-scoped schema the AI generator emits)
   inherits it; the attacker needs an ordinary account with update on the
   resource. It cannot STEAL a row (the WHERE excludes rows the caller does
   not own → 404) — it can GIVE one away or discard it, which is enough to
   plant data under another user's identity, hide one's own trail, or
   destroy access to one's own records.
2. **Same seam, silent variant.** A role whose field allowlist excludes the
   condition column gets the reassignment DROPPED with 200 — the attempt is
   invisible to the client and the audit trail alike. Safe by accident,
   wrong by the project's rule (an attempt is rejected, never swallowed).
3. **A state-machine field set to null is a 500** (PATCH `null`, PUT omitting
   the field, batch, Ctx.Update). Loud failure with the wrong status, and a
   PUT on any resource with a lifecycle 500s unless the client re-sends the
   state. Not an escalation; the row is unchanged.
4. **v0.1.8 only:** forged `id` / `created_at` / `updated_at` accepted on
   create with 201 and stored (the WRITE-ASYMMETRY-S1 hole, closed in v0.1.9).
   Damage: audit-trail forgery by any create-permitted role; client-chosen
   primary keys.
5. **ENG-47 (not a hole, a defence without a valve):** the login limiter is
   5/min per (tenant, email), hard-coded; a public demo sharing one identity
   is throttled by its own visitors. The admin login throttle
   (`platformadmin.loginThrottle`, 5/min) is the same shape, also knob-less.

Not in the class, verified: `tenant_id` (a column so named is a plain field —
isolation is the schema, never a column), `created_by` unless it is the
role's own condition column (then it is finding 1), FK columns that point at
identity (`x-appximo-references: user_id` — the SCHEMA-5 warning covers the
id-space mismatch; the write is a plain FK write), the files upload's extra
parts, the signup `role`.

## ENG-45 re-read with a security eye

| ENG-45 entry | Class |
|---|---|
| 1 · owner-scoped role gives its row away on update | **this class — authz asymmetry, closed by this session** |
| 2 · `files` resource shadowing (never provisioned) | functional silent corruption (migration), not authorization |
| 3 · Ctx.Update null on a required field → raw 23502 → 500 | loud failure, functional — the same shape as finding 3 above (a null reaching SQL); adjacent, still open |
| 4 · declared-relation pieces not checked to exist | functional (silent no-op at include time) |
| 5 · GraphQL naming shadow / NonNull-with-default | functional contract |
| 6 · runtime-config assumptions (`hmac_secret_env` unset → empty HMAC key) | **security-adjacent, other class**: a webhook signed with the empty key is verifiable by anyone — a boot-time warning is the right increment, not this session's |
| 7 · OpenAPI description text | cosmetic |

## Disposition (decided here, built in Part B)

One policy, derived from the schema's RBAC block (the contract): **for a role
whose row condition is identity-bound, the condition column is server-owned
on UPDATE exactly as it already is on CREATE.** A body that supplies it with
anything but the caller's own id (another id, null) is `403 field "<col>"
must match the authenticated principal` — regardless of the field allowlist,
explicitly, never dropped; a full-replacement PUT that omits it keeps the
caller's value; re-sending the caller's own id is a no-op. Symmetric across
REST PUT/PATCH, GraphQL, batch and `Ctx.Update` from ONE implementation.
A literal condition stays a visibility filter (the moderator keeps
approving). The server-side paths stay open: an unscoped role (admin, a
`condition_actions` list that leaves update unconditional) reassigns freely,
and a custom handler that must transfer ownership runs as such a role or on
its own SQL (`UnsafeTx`) — declared, greppable, never the client's call.
Finding 3 is closed in the same pass (a state field can never be null: named
422). Findings 2 and 4 fall out of it / are already closed. ENG-47 gets its
valve in Part C.
