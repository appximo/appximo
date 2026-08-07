# ADR-026 — Declarative public reads: the `rbac.public` block

**Status:** accepted (PUBLIC-SURFACE-S1, 2026-08-07)
**Drivers:** second field evaluation — a blog built end-to-end on the
distributed binary could not serve a single anonymous page: `/openapi.json`
declared no public route, every generated CRUD path demanded a token, and the
three most common demo apps (blog, catalogue, landing) therefore required
writing Go (`Route.Public`). "Que un blog se vea sin login no puede exigir
escribir Go."

## Decision

A schema may declare an **anonymous read surface** as a sibling of
`rbac.roles`:

```json
"rbac": {
  "roles": { "admin": { "resources": "*", "actions": ["*"] } },
  "public": {
    "articulos": {
      "actions": ["read"],
      "conditions": { "field": "estado", "op": "eq", "val": "publicado" },
      "fields": ["id", "titulo", "cuerpo", "estado", "portada"]
    },
    "files": { "actions": ["read"] }
  }
}
```

An unauthenticated request (no `Authorization` header) on an app whose schema
declares the block proceeds carrying synthetic claims
`{Role: "$public", TenantID: <request tenant>}` and is evaluated by the **one
existing RBAC evaluator** — the block compiles into the reserved role
`rbac.PublicRoleName` (`"$public"`) at policy unmarshal
([pkg/rbac/policy.go](../../pkg/rbac/policy.go)). Every surface inherits it
with zero per-surface code: REST list/get, relation subroutes, `?include=`
embeds, GraphQL queries, the aggregate endpoint, SSE, and the file store.

## Options considered

1. **A reserved role name inside `rbac.roles` (e.g. `"public"`)** — rejected:
   an existing schema may already declare a role named `public` for
   *authenticated* users; upgrading the engine would silently turn it
   anonymous. A meaning-changing upgrade is exactly the class this project
   refuses.
2. **`Route.Public`-style per-route flags in the schema** — rejected: routes
   are generated, not declared, so the schema has nothing to hang a flag on;
   and per-route flags don't compose with row conditions/field allowlists.
3. **A separate top-level `public` block with its own evaluator** — rejected:
   a second RBAC implementation is the "duplicated logic that diverges" class
   (DUPLICATED_RULES_AUDIT); divergence here would be a security hole.
4. **`rbac.public` compiled into the existing evaluator as a reserved role**
   — chosen. New key ⇒ provably backward-safe (strict-key validation has
   always rejected unknown keys, so no existing schema carries it); one
   evaluator ⇒ one set of guarantees.

## Security posture (the hard requirements, each pinned by a test)

- **Deny-by-default intact.** Only resources in the block, only `read`. A
  resource absent from the block answers 403 to anonymous callers; a schema
  without the block behaves byte-identically to before (tokenless = 401 at the
  JWT stage) — pinned by `TestPublicSurface_NoBlockMeansNoChange`.
- **Read-only, enforced at load.** `actions` must be exactly `["read"]` — an
  anonymous write is a spam/mass-creation abuse surface that must not be
  openable by a schema typo. If a future increment wants anonymous writes
  (e.g. a contact form), it must bring its own abuse analysis (CAPTCHA/rate
  design) — reconsider then, in a new ADR.
- **The field allowlist binds NAMING, not just returning** (the SEC-5 lesson,
  closed **generally** in this session, for every role): `?filter[hidden]=`,
  `?sort=hidden`, `?order[hidden]=` are 403 (`query.ErrForbiddenField`, the
  same contract the aggregate already had), and the `?search=` sweep only
  touches role-readable text columns. Before this, any allowlisted role could
  use a hidden column as a match/no-match value oracle.
- **No identity variables.** A public condition's `val` must be a literal;
  `$user_id`/`$external_client_id` are load errors (an anonymous request has
  no identity — the rule would match zero rows forever, the SCHEMA-5 class,
  but here it is *always* wrong, so it is an error, not a warning).
- **No silent downgrade** (the ENG-6 rule): a present-but-invalid/expired/
  foreign-tenant Bearer stays 401 — anonymity is the absence of credentials,
  never the failure of them.
- **Aggressive anonymous throttle.** Anonymous requests pass the same
  per-(tenant, IP) limiter `Route.Public` uses (`APPXIMO_PUBLIC_ROUTE_RPS`,
  default 5 rps / burst 10) before RBAC or SQL runs.
- **Response cache: anonymous requests neither read nor write it.** The HIT
  path requires a validated token, and the store path requires a cacheable
  validated role — an anonymous response is never shared with an
  authenticated caller or vice versa. (Public traffic scales via HTTP-level
  caching upstream if ever needed.)
- **Reserved name.** `rbac.roles` may not declare `"$public"`; the constant
  is pinned identical across `pkg/schema` and `pkg/rbac` by
  `TestPublicRoleNamePinned`.

## Consequences

- `/openapi.json` marks the read operations of publicly-readable resources
  with `security: []` + `x-public: true` (subroutes only when the *target* is
  public too), so a generic tool discovers the anonymous surface.
- **Probe semantics change only for apps that opt in:** with a public block
  declared, a tokenless request reaches RBAC, so an undeclared resource
  answers 403 (not 401). Recorded in the gate corpus (`no-auth` row).
- The public-images pattern (frontend-spec §7.5) is reachable without Go:
  grant `"files": {"actions": ["read"]}` publicly and mint signed URLs
  anonymously (`GET /api/files/{id}/url` → the HMAC-token URL); ids are
  unguessable UUIDs, and the grant means "anyone with an id may read that
  file" — say so in the app's own terms before using it.
- Studio's RBAC editor does not author the block yet (BACKLOG UI-2); the
  JSON view does.
