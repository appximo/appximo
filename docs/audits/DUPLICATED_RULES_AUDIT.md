# Duplicated-rules audit — where the same rule lives twice and can disagree

**Session:** FIELD-FEEDBACK-S1 (2026-08-07), Part B.
**Trigger:** the first third-party field evaluation (FEEDBACK.md) named its own
cross-cutting pattern: *almost every structural finding is duplicated logic that
diverged* — two tenant-id validators (T1), three RBAC judges in disagreement
(ST1/ST2), the boot file vs the tenant record (B7), filter metadata vs the real
columns (M1). This audit maps the class the way MIGRATION_HONESTY_AUDIT mapped
"the migrator grades its own work" and UNRECOGNIZED_INPUT_AUDIT mapped "accepts
and continues silently": every place a rule is implemented more than once, with
its divergence state and the mechanism that keeps it honest.

The rule the audit applies: **a rule may be *mirrored* for UX, but only one
implementation may hold a verdict** — and every mirror either derives from the
authority mechanically or is pinned to it by a test that fails the build on
divergence. A mirror that can silently disagree with the surface holding the
button is a defect even while the two happen to agree.

---

## The class-level guarantee (what made the instances cheap)

The verdict that gates an action must come from the authority itself, not from a
copy. Two structural moves close most of the class:

1. **Studio's Deploy gate now asks the engine** (`POST /editor/validate` →
   `schema.ValidateReport`, the same `schema.Validate` the CLI, the boot, the
   control plane and the `/admin` deploy APIs run). The client-side mirror
   (`editor.validate()`) remains as live-hint UX and blocks only as a fallback
   when the engine is unreachable. A future rule added to the Go validator is
   therefore enforced at the gate on the day it ships, with no JS change.
2. **Mirrors that must exist client-side are pinned by tests** that read the UI
   source and compare the literal against the Go authority
   (`pkg/controlplane/tenant_rule_pin_test.go`), the same discipline
   `spec_test.go` applies to the grammar docs.

---

## Findings

### 1. ST1 — the virtual `files` resource: three judges, one wrong — **FIXED**

The rule "the built-in `files` store is grantable in RBAC unless shadowed by a
real resource" existed in the Go validator (`pkg/schema/validator.go`,
`validateRBAC`), in the runtime mount (`app.go`), and in the OpenAPI generator
(`pkg/codegen/openapi_generator.go`) — and was **missing** in Studio's
client-side mirror (`editor.svelte.ts rbacIssues()`), the only judge wired to
the Deploy button. Verdict matrix before the fix: CLI ✅ · engine boot ✅
(grant enforced live) · Studio ❌ `permission over unknown resource "files"`.
The wrong judge pushed users to *delete* grants that live uploads depend on.

The evaluator's suspicion that "the same message exists inside appximo.exe,
suggesting a server-side path with the same gap" is resolved: the string exists
only in the embedded JS bundle — no Go path ever rejected the grant
(`grep "permission over unknown resource"` over Go: zero hits).

Fixed: the gate defers to the engine (class guarantee #1); the local mirror
learned `isVirtualResource()` (accept + the `files_grant_actions_only` rule);
`TestValidateRouteFilesGrant` pins the authority's verdict in both RBAC forms.

### 2. ST2 — Studio could not *represent* the grant and dropped it silently — **FIXED**

Four Studio paths filtered resource names through `getEntityByName` and so
silently discarded a legal `files` grant on load/edit/convert:
`roleResourceNames`, `addPermission`, `convertToPerResource`, and RbacModal's
picker/checkbox grid (`toggleRes` rebuilt `resources` from `entityNames`,
erasing `files` on any unrelated click). All five now go through
`rbacResourceNames` / `isVirtualResource`; the grant renders labeled
"built-in file store", actions-only (conditions/fields hidden — the engine
rejects them there).

### 3. T1 — the tenant-id rule: right regex, lying message — **FIXED**

The creation authority is `controlplane.tenantIDRe`
(`^[a-z][a-z0-9]{1,29}$`, ENG-11: schema alphabet ∩ DNS-label alphabet). Both
SPA mirrors carried the correct regex — but Studio's deploy modal carried a
hand-written *message* claiming "digits or `_`" were legal, the exact opposite
of the API's verdict (the evaluator's underscore id passed the form and died on
the server). The message now comes from `tenantIdIssue()` (the one per-cause
explainer already in the same module), and
`TestTenantIDRuleSingleSource` pins both UI regex literals to the authority and
rejects any resurrection of the stale wording.

Documented exception, not a divergence: `pkg/platformadmin.tenantIDRe` is
deliberately looser on the LOOKUP/DELETE side so legacy hyphenated tenants
remain deletable; it is a defence-in-depth guard before `Sanitize()`, never a
creation rule (comment at its declaration says so).

### 4. M1 — filter metadata vs the deployed schema — **FIXED this session (Part C)**

The write path resolves a resource from the tenant's DEPLOYED schema (ENG-12,
`pkg/codegen/deployed.go`); the read path's filter/sort validation still used
the boot-compiled resource — the same rule ("what fields exist") computed from
two sources, diverging after every hot migration. See Part C of the session
report; the fix reuses the ENG-12 seam rather than writing a second one.

### 5. B7 — boot schema file vs tenant record — **DOCUMENTED, residual filed**

Two sources of truth by design (the boot file compiles the served surface; the
tenant record drives per-tenant migration). The divergence the evaluator hit
(a stale record almost reverting live RBAC via a Studio deploy) loses most of
its teeth with B1 fixed (no second process with a mutilated schema copy), and
`migrate` has persisted+notified since CONSUMER-PATH-S1 — the operator rule is
"deploy through migrate/Studio, don't hand-edit only the boot file". What
remains — the engine warning when a tenant's stored schema hash diverges from
the boot schema — is filed as **ENG-36** in docs/BACKLOG.md.

---

## Mirrors that remain, and what keeps each honest

| Rule | Authority | Mirrors | Kept honest by |
|---|---|---|---|
| Virtual `files` grantable | `pkg/schema/validator.go` | Studio `isVirtualResource` · `app.go` mount · openapi generator | gate defers to authority; `TestValidateRouteFilesGrant` |
| RBAC action vocabulary | `validator.go validRBACActions` | meta-schema enums · Studio `RBAC_ACTIONS` · runtime `pkg/rbac` | gate defers; meta-schema exercised by the same `ValidateReport` the gate calls |
| Tenant id | `controlplane.tenantIDRe` | Studio `TENANT_ID_RE` · admin panel `TENANT_ID_RE` | `TestTenantIDRuleSingleSource` (regex + wording pin) |
| Identifier regex `^[a-z][a-z0-9_]*$` | `pkg/schema/validator.go` | meta-schema `$defs/ident` · Studio `IDENT_RE` · codegen/query/db guards | meta-schema + semantic validator run together in `ValidateReport` (a mismatch surfaces as contradictory errors in one report); Go guards are defence-in-depth *behind* the validator, never a differing user verdict |
| Field type / format / hook-event / relation-kind vocabularies | `pkg/schema/validator.go` + `keys.go` | meta-schema enums · Studio `schema.ts` constants | gate defers to authority; Studio constants are UX pickers whose invalid output the gate now catches |
| Reserved names (`transaction`, `auth_*`) | `validator.go` | meta-schema · Studio literals · `pkg/migration/runner.go` | gate defers; migration guard is behind the validator |
| Grammar docs vs engine | `pkg/aigen GrammarCore` | `docs/SCHEMA_SPEC_LLM.md` etc. | `spec_test.go` divergence pin (pre-existing model) |

The remaining mirrors are **pickers and hints** — they shape what the UI offers,
but since this session none of them holds a verdict a user must obey. The next
rule added to the engine: add it to the Go validator only; Studio's gate
inherits it. Add a Studio hint when UX wants live feedback, and if the hint
encodes the rule's *content* (not just its vocabulary), pin it.

## Not findings (checked, behavior is honest)

- **`pkg/rbac` runtime lookup is name-agnostic** (map lookup, no resource
  universe) — it cannot disagree with the validator about which names are
  legal; it only enforces what the schema declared.
- **`validateRouteGrants` (custom-route segments)** is boot/deploy-time
  validation against *registered* routes — a different input (the running
  binary), not a duplicated rule.
- **The meta-schema cannot cross-reference** (JSON Schema has no "key must name
  a declared resource"), so its permissiveness on permission keys is a layering
  fact, not a divergence: the semantic validator behind it holds the verdict.
