# Case study — VecinGo: a neighborhood-association platform, built by a third party in an afternoon

> **TL;DR.** An independent developer — no access to this repository, no
> contact with us during the build — took Appximo v0.1.6 from `go get` to a
> **production deployment with HTTPS** of a real neighborhood-association
> platform: **18 resources, 8 state machines, 3 roles, 13 custom Go handlers,
> a 13-screen SPA embedded in the binary, weighted voting with quorum, PQRS
> case tracking, multi-tenant** — onto a VPS that was **already serving two
> unrelated production apps** — in **~3–3.5 hours of effective work**.
>
> Their verdict, verbatim: *"as a consumer, I would do it again."*
>
> This document is our write-up of their report. The build is theirs; the
> four engine defects they found are ours, and every one is closed in code
> with a pinned test — the closures are part of the story, not a footnote.

## Why this domain is hard

A neighborhood-association (HOA-style) platform is not a TODO app wearing a
costume. Assembly decisions are decided by **weighted votes** — each unit's
vote counts by its ownership coefficient — and are only valid if a **quorum**
of coefficients attends. PQRS complaints move through a legally meaningful
lifecycle. Residents may see their own building's data and nothing else. In a
consulting shop this is weeks of backend work before the first screen exists:
the data model is wide (18 resources), the authorization is row-scoped per
role, and the state machines are load-bearing (a posted assembly decision must
be immutable).

## What was built

| Dimension | Count |
|---|---|
| Schema resources (tables, CRUD+GraphQL+OpenAPI each) | 18 |
| Declared state machines | 8 |
| Roles (admin / directive / resident), row-scoped | 3 |
| Custom Go handlers (`appximo.Route` + `Ctx`) | 13 |
| SPA screens, embedded in the one binary (`go:embed`) | 13 |
| Effective build time, idea → HTTPS production | **~3–3.5 h** |
| Apps already live on the target VPS (untouched) | 2 |

The weighted-quorum voting and the PQRS flow are custom Go handlers over the
engine's `Ctx` API; everything else — the CRUD for 18 resources, validation,
auth, RBAC row scoping, migrations, the admin panel, the API docs — is the
declarative 90%.

## Where the time went

From the evaluator's report: the declarative layer — 18 resources, 8 state
machines, the 3-role matrix — cost **minutes** (the schema validated on the
**second** iteration, using `appximo validate --json` as the oracle). The
custom 10% — the 13 Go handlers with the domain logic that actually differs
from every other app — took roughly **70% of the total time**. Their reading,
which we'll take: *"that is exactly where the cost should be."*

Two details from that phase worth repeating:

- The validator emitted **8 warnings** (`required_field_is_rbac_forced`) on
  the way to a valid schema. All 8 were **real future bugs**: each one a
  column that was both `required` and RBAC-forced, which would have made
  every resident-side create fail with a 422 in production. The warnings
  named the fix; none of the eight shipped.
- The 13-route × 3-role authorization matrix (`routes` grants, validated at
  boot against the registered routes) came out **correct on the first try**.

On the documentation, verbatim: *"the best engine→agent interface I have
consumed: I did not invent a single endpoint."* (The five printable specs —
`appximo specs` — were the only documentation used.)

## The frictions — and what happened to them

A case study without frictions is an advertisement. There were four, and one
more we found ourselves while answering them. Every one is closed in code with
a regression test; the full finding-by-finding response is
[FIELD_FEEDBACK_RESPONSE.md](FIELD_FEEDBACK_RESPONSE.md), and the class audit
it triggered is [audits/CTX_PARITY_AUDIT.md](audits/CTX_PARITY_AUDIT.md).

1. **`ctx.Insert` did not apply schema `default`s.** Rows written by a custom
   handler landed with a NULL status, and the next state-machine transition
   failed with `invalid transition from ""` — an error at the wrong moment,
   pointing at the wrong place. **Closed** by making
   `codegen.PrepareCreate/PrepareUpdate` the *single* body-preparation source
   both write paths call — not by patching the second implementation.
2. **Numbers were read/write asymmetric.** The engine returns `int64`s, but
   handing that same `int64` back to `ctx.Insert` failed validation (only
   `float64` was accepted — true of JSON, false of Go). **Closed**: one
   shared definition of "a number" (`schema.AsFloat64`/`IsIntegral`); your
   exact `int64` reaches the database.
3. **`appximo up` reported failure over work that had succeeded.** Against a
   remote database (~119 ms RTT × 18 resources), a fixed client deadline
   expired *while the DDL completed* — twice. **Closed**: a timeout is no
   longer a verdict; `up` asks the control plane what actually landed, and
   the deadline is sized from the schema and the measured RTT.
4. **The installer disturbed the neighbours.** Deploying onto a VPS with two
   live apps, `install.sh` restarted the shared PostgreSQL even when its
   tuning hadn't changed (~3 s blackout), and a failed run left an orphan
   env file. **Closed**: identical config → no restart (verified: neighbour's
   postgres PID unchanged, a 400-sample probe with zero non-200s), and every
   port is preflighted before a single file is written.

**The fifth finding is the method's argument.** The report named two
`Ctx` divergences; instead of patching those two, we audited the whole
Ctx-versus-generated-path class — 17 behaviors — and found **five**, one of
them security-relevant and *unreported*: `ctx.Insert` skipped create-time
RBAC, so an owner-scoped role could create rows attributed to another
principal — 201 through a custom route, 403 through `/api`. Nobody hit it in
the field. It is closed and pinned by the same parity test suite
(`ctx_parity_integration_test.go`) that now runs every payload through both
write paths and demands identical rows. The audit table is public and fully
closed: of its 17 behaviors, 5 were already identical, 8 were divergences —
now fixed — and 4 are differences documented as deliberate, with the
reasoning.

## What we can and cannot show

VecinGo runs in **private production for a real community**, so this write-up
has no screenshots of their data — the numbers, the timeline and the quotes
come from the evaluator's written report, and the engine-side claims are
verifiable in this repository (commits, tests, the audit). If you want to see
a third-party build you can *click on*, the second evaluation's app is public:

### The short second case: crisblogs

A different independent evaluator — working **only** from the distributed
binary and `appximo specs`, no repository access — built a complete blog
(login, reader/editor roles, cover-image uploads, a publish state machine,
24/24 checks in a mobile-viewport browser) and deployed it publicly:
**crisblogs**. Their sharpest
line — *"the cliff appears when someone wants their own frontend"* — became
that release's centerpiece: anonymous public reads (`rbac.public`) and
first-class static serving (`--static`), both closed in code the same week.
Same method, public artifact.

## Method note

Field reports here follow a fixed path: reproduce → audit the **class**, not
the instance → close in code from a single source → pin with a regression
test → publish the finding-by-finding response, including what we got wrong.
Corrected claims stay visible in the docs as corrections — that is the point
of them.
