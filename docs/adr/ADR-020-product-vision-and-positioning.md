# ADR-020: Product vision, positioning, and the low-token-cost thesis

**Status:** Accepted.

This ADR records the **product vision and market positioning** as an
architectural decision — not just an operational plan. Where ADR-016
(extensibility) and ADR-019 (relations) decide *how the engine is built*, this
one decides *what the engine is for, who it is for, and which constraints every
future layer must respect*. The operational sequencing lives in
[`ESTADO_Y_PLAN_MAESTRO.md`](../../ESTADO_Y_PLAN_MAESTRO.md) (the 7-phase plan);
the *why* lives here.

---

## Context and problem

Appitools grew bottom-up: a declarative multi-tenant engine, then an
extensibility model (ADR-016), an outbox + worker tier, a file store, and
declarative relations (ADR-019). Each layer was measured and shipped, but the
**commercial north** — the answer to "what is this *against*, and where does it
win?" — had never been written down as a binding decision. Without it, every
new feature risks being justified by "a backend could use this" rather than
"our product needs this," and the engine drifts toward a generic backend that
competes everywhere and wins nowhere.

A market-research pass (PocketBase and Supabase as the primary comparables, with
data and primary sources) closed two questions:

1. **Where Appitools competes.** It is *not* in "simple backend for one app"
   (PocketBase wins that on packaging) and *not* in "massive SaaS with thousands
   of tenants" (the Postgres catalog degrades there). It competes in
   **mid-scale, multi-tenant productive B2B with native observability** — a
   lane where the specific *combination* of properties Appitools already has is
   not matched by either comparable on cheap hardware.

2. **A design thesis about token cost.** The more declarative and validated the
   schema is, the *fewer tokens* a model needs to operate it, and therefore the
   *cheaper* the model that can drive it. This is not a marketing line — it is a
   constraint that should shape the schema, the error messages, and the docs, so
   that an eventual AI layer runs on inexpensive models by construction.

Both findings are durable enough, and broad enough in their consequences, to be
recorded as an architectural decision: they constrain the roadmap (auth must be
multi-tenant-aware; the scale ceiling is a product range, not a bug) and the
internals (errors and schema must be LLM-legible; no new layer may degrade p50).

---

## Decision

### 1 — Commercial north: mid-scale, multi-tenant productive B2B

Appitools is the engine for **mid-scale productive B2B backends**: a business
group with N companies, a firm running its ERP/HR, a vertical with tens-to-low-
hundreds of customer-tenants. It is explicitly **not**:

- a **massive SaaS** of thousands of tenants on one cluster (the Postgres
  catalog bites there — see §2), nor
- the **simple backend of a single app** — PocketBase wins that on packaging
  (zero dependencies, embedded SQLite, one process to start). Fighting there is
  losing on packaging.

The front end is **out of scope** (v0 / Lovable solve it). Appitools is the data
+ logic + observability engine behind the app, not the app.

### 2 — Multi-tenancy is an offered *plus* with a declared ceiling

Multi-tenancy is a **product we offer**, not a need we are forced to satisfy at
any cost. Its healthy range is **tens to low-hundreds of tenants per instance**.
The Postgres catalog degrades materially past **~1 000–2 000 schemas** on a
single cluster (Andres Freund; PlanetScale) — the same ceiling ADR-019 already
relies on to reject per-request `information_schema` introspection. That ceiling
is **the product range, not a defect**.

A customer who needs total isolation, or more scale than one instance gives, gets
**another instance / port / container** — the CGO-free single binary is cheap to
multiply. We do not bend the architecture (or the catalog) to chase
thousand-tenant density we deliberately don't target.

### 3 — Three measured, defensible differentiators

The defensible position is the **combination** — none of the comparables has all
three together on a $7–16/mo VPS:

1. **Physical schema-per-tenant isolation.** Each tenant is its own Postgres
   schema, not co-mingled rows. This is directly sellable to a B2B buyer who
   asks "is my data *physically* isolated?". Supabase's RLS is **logical**
   isolation — a policy that can have bugs; ours is structural.
2. **N tenants in ONE CGO-free Go process.** Marginal cost per tenant ≈ $0 on a
   $7–16 VPS. PocketBase does multi-tenancy as **N instances** (N processes =
   RAM ×N, ops ×N).
3. **Native in-binary observability.** Prometheus + trace explorer + per-tenant
   debug + SLO burn-rate, no sidecar. It substitutes ~$46/host/mo of Datadog APM
   + Sentry; an equivalent self-hosted OTel stack needs 2–8 GB and **does not fit**
   in the 1–2 GB VPS that is the target.

**Why PocketBase doesn't compete in this lane:** SQLite serialises writes — one
writer at a time (sqlite.org; confirmed by PocketBase's own author) — and its
multi-tenancy is "N instances," expensive to operate. It shines on the simple
single-tenant app, not the B2B group.

**Why Supabase plays at a disadvantage here:** its Auth **does not support
multi-tenancy** (the `email` is globally UNIQUE), and its rich observability
exists **only inside its cloud**. Self-hosted loses exactly the two axes where we
are strong.

### 4 — The low-token-cost thesis (a design constraint, not a slogan)

**Principle:** the clearer and more declarative the schema, the fewer tokens an
LLM needs to reason about it → the cheaper the model that can operate it (down to
Haiku). Token cost is a function of the decision space: a validated JSON over a
finite vocabulary is reasoned about in few tokens.

**Evidence:** Lovable / v0 generate **code** — an unbounded, Turing-complete
space — which yields 40–50 % failure rates and **$15–70 fix-loops** per
iteration. Editing an **already-validated JSON** is the opposite: bounded,
verifiable, reversible. A cheap model chooses among **finite operations** on the
schema (add a field, an index, a relation, a role) instead of writing logic that
then has to be debugged.

**Decision:** every choice about the schema, the error messages, and the
documentation is optimised so a low-cost model understands it in few tokens.
"What consumes the least is Appitools" is a **design objective**, not a tagline.
This is why the engine already returns multi-field 422s and strict-key errors
that *list the valid keys* — those are the seeds of an LLM-legible surface.

### 5 — The AI layer and its technical prerequisites

The long-term product is an **AI agent over a visual schema editor**, optimised
for cheap models, that **operates the validated schema** rather than generating
code. It is gated on three engine prerequisites, which therefore become roadmap
commitments (FASE 5 in the master plan):

- **Self-documenting schema** — the JSON explains itself (intent, descriptions,
  relations) in few tokens.
- **LLM-legible errors** — build on the existing 422-multi-field / strict-key
  base so a cheap model corrects with minimal tokens.
- **Atomic, reversible schema operations with a diff** — apply/undo a schema
  change as a *verifiable transaction*, not a "regenerate everything." This is
  what makes it cheap *and safe* for an AI to drive.

---

## Consequences

- **The scale ceiling (low-hundreds of tenants) is the product range, not a
  bug.** Docs, positioning, and the roadmap state it as a deliberate boundary;
  the "another instance/container" answer is the supported path past it. This is
  consistent with ADR-019's rejection of per-request catalog introspection.
- **Auth must be multi-tenant-aware by design** (FASE 1). It is the structural
  advantage over Supabase (whose Auth is single-tenant by its UNIQUE-email
  model). Identity and tenant isolation are designed together, not bolted on.
- **No new layer may degrade p50.** Every measured property (1.58 ms p50,
  isolation, in-binary observability) is part of the value proposition. The
  standard pipeline (`bench-protocol.sh`, Mann-Whitney, 10 runs, CV < 5 %,
  threshold `max(0.5 ms, 3 % × median_A)`) gates each change — this is a
  non-negotiable constraint, the same one ADR-016 and ADR-019 already enforce.
- **The schema design is optimised for cheap AI models** from now on: error
  messages, key vocabulary, and docs are judged partly by how few tokens a model
  needs to operate them. This bends decisions in FASES 5–6 and constrains how new
  schema surface is added today (strict keys, finite vocabularies, clear errors).
- **Observability stays in-binary.** Adding an OTLP *export* (FASE 3) is fine;
  adopting a multi-GB OTel/sidecar stack is not — it would break differentiator
  #3 and the $7–16 VPS target.

---

## Alternatives discarded

| Alternative | Reason discarded |
|---|---|
| Compete with **PocketBase** in "simple backend for one app" | Lost on packaging — zero-dependency embedded SQLite, one process to start. Our edge (physical multi-tenant isolation, in-binary observability) is invisible to the single-tenant app and adds operational weight it doesn't want. |
| Target a **massive SaaS** of thousands of tenants on one cluster | The Postgres catalog degrades past ~1 000–2 000 schemas/cluster (Andres Freund; PlanetScale) — the exact workload that would break the schema-per-tenant model. The honest ceiling is low-hundreds; beyond it the answer is more instances, not more schemas. |
| Logical (RLS-style) tenant isolation instead of physical schema-per-tenant | Trades the most sellable B2B property ("physically isolated") for a policy layer that can have bugs. It would erase differentiator #1 and put us on Supabase's terms. |
| Adopt a heavy OTel/Datadog-style observability stack for richer telemetry | Needs 2–8 GB and a sidecar fleet — it doesn't fit the 1–2 GB VPS target and kills differentiator #3. An optional OTLP *export* (no SDK weight on the hot path) is the bounded version we keep. |
| Build the AI layer by **generating code** (Lovable/v0 model) | Unbounded, Turing-complete output → 40–50 % failure, $15–70 fix-loops. Operating a validated JSON over finite operations is bounded, verifiable, reversible, and runs on cheap models — the whole point of the low-token thesis. |
| Make the front end a first-class concern | Out of scope by design: v0/Lovable solve it. Appitools is the data + logic + observability engine behind the app. |

---

## Relationship to other ADRs

- **ADR-016 (extensibility):** the library + two-class model is *how* FASE 7's
  "ecosystem of connected Go engines" stays a single-binary story. This ADR is
  *why* that ecosystem is built only when a real customer asks.
- **ADR-019 (declarative relations):** its rejection of per-request catalog
  introspection (catalog degrades past ~1 000–2 000 schemas) is the same ceiling
  that defines the product range here — relations and positioning agree on the
  scale boundary.

The current single-node SSE boundary (the engine's pub/sub is in-process) is a
*current* limit, not a permanent one; breaking it (HA multi-node) is FASE 7,
gated on real customer demand.
