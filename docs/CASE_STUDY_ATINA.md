# Case study — atina: a multi-client recruiting SaaS, built by an external developer from the public docs alone

> **TL;DR.** A developer with no access to this repository and **no direction
> from us** took Appximo from `appximo version` to a **production deployment
> with HTTPS** of a multi-client recruiting platform — **32 schema resources,
> 48 custom Go routes, a 30+-screen Svelte 5 SPA embedded in the binary,
> four roles with public reads declared in the schema, a matching engine, a
> kanban with per-phase communications, consent-by-link, scheduled jobs and
> a mail worker** — and it is open today at
> [atina.appximo.com](https://atina.appximo.com). The builder's own report
> says the whole thing took **≈3 hours of wall-clock work in one session**;
> that figure is reproduced below ONLY with its phase-by-phase breakdown,
> and marked for what it is — the builder's self-report, not our measurement.
>
> The four frictions they hit are listed at the end with our answer to each
> ([FIELD_FEEDBACK_RESPONSE.md §4](FIELD_FEEDBACK_RESPONSE.md#response-to-the-fourth-field-evaluation-atina--frente-comercial-s1-2026-08-26)).
> Three of the four were **not engine defects** — and that is the interesting
> part of this report: the documentation had already said what to do, and
> the builder's agent found out the hard way anyway.

## What was verified by us, and how

The numbers a reader can check are the ones we publish. On 2026-08-26 we
fetched the platform's **public, unauthenticated** `/openapi.json`
(1.1 MB) and its SPA bundle from a clean box:

| Claim in the builder's report | What we counted | Source |
|---|---|---|
| 32 resources | **32** generated `/api/{resource}` segments (the built-in `files` store excluded) | `/openapi.json` paths |
| 46 custom endpoints (45 in their video) | **48** `x-appximo-custom-route: true` operations (method × path) | `/openapi.json` paths |
| 39 screens | **34 route patterns / 30 lazy-loaded page chunks** in the public bundle — screens with tabs inflate the builder's count; we publish "30+" | `assets/index-*.js` |
| Public reads declared in the schema | **59** operations flagged `x-public` | `/openapi.json` |
| "Two brands from one build" | not verifiable from outside (we don't know the second domain) — **not published** | — |
| In production with HTTPS | `https://atina.appximo.com/health` → `{"status":"ok","version":"v1.2.0"}`; the portal renders its job listings without an account; 0 console errors at 1366×900 and 390×844 | browser run |
| Built without our direction | **Confirmed in writing by the maintainer** (the builder is a contractor who worked from the published documentation; we had no contact during the build) | maintainer statement |

Everything else below is **the builder's report**, reproduced with that label.

## What was built (builder's report)

**atina** is a multi-client SaaS for candidate acquisition, matching and
selection-process management, specified from a 27-page functional document.

- **The data engine** — 32 resources (clients, consultants, companies,
  opportunities with requirements, screening questions, professionals with
  experience/education/languages, applications with **two state machines**,
  search profiles, consents, communications, notifications, updates and
  trilingual catalogs) with a four-role RBAC and public reads declared in
  the schema. REST + GraphQL + OpenAPI + admin panel + back-office CRUD +
  visual editor, **without writing a line of code for any of it.**
- **The business logic the schema does not express** — 48 custom endpoints
  in Go, in the same binary: the matching engine (five weighted dimensions,
  partial equivalences, automatic recalculation when an offer, profile or
  company changes), the kanban with per-phase communication rules, three
  closing types, the consent-by-link flow without login, CV extraction with
  AI, CSV exports, per-company interest analytics, six scheduled automations
  and a mail worker with per-client templates in three languages.
- **A complete frontend** — public white-label portal, candidate area,
  client/consultant back-office and master console. A Svelte 5 SPA served by
  the same binary, which also serves **two brands from one build** by access
  domain.
- **Realistic demo data** seeded through the API: 2 consultancies, 8
  companies, 24 professionals, 11 processes, 48 applications, hires,
  consents.
- **Production**: a shared VPS already running four other Appximo apps,
  native Postgres, systemd, Caddy with Let's Encrypt, backups, the worker as
  a second service.
- **Marketing material** generated against the real app: a 60 s subtitled
  video, a 2-minute full tour and GIFs.

## Where the time went (builder's report — wall-clock, one session)

The total is only meaningful next to its parts. It is the builder's own
timing of an agent-driven build; we did not observe it and cannot verify it.

| Phase | Duration | What happened |
|---|---|---|
| Check the install and resolve the version | 2 min | `appximo version` + the GitHub redirect. Already current: a no-op. |
| **From the functional document to a running API** | **~15 min** | Read `appximo spec`, write the 32-resource schema, three passes of `appximo validate --json` (every error names the path and the fix), `appximo up`, checklist with real requests. |
| Custom Go backend (matching, kanban, consent, worker) | ~55 min | 14 files, ~2,500 lines. `appximo backend-spec` describes the whole `Ctx`: nothing to guess. |
| **Production with HTTPS** | **~6 min** | `install.sh --app=…` onto a box that already had other apps: user, database, systemd unit, Caddy site and certificate in one pass. Registering the tenant: one `curl`. |
| Frontend (50 files) + seeder | ~60 min | Svelte 5 + Tailwind 4; the `appximo frontend-spec` contract avoided every round-trip with the API. |
| Browser verification, fixes, redeploy | ~20 min | Playwright: 39 screens × 4 roles × 2 sizes, zero errors. `deploy-update.sh` swaps the binary with automatic rollback. |
| Second brand on a second domain | ~15 min | One block of CSS variables, a brand map by hostname, a Caddy site and a new tenant. The same binary serves both. |
| **Total** | **≈ 3 h** | From "which version do I have?" to a production demo with data. |

Read that table for its *shape*, not its total: the declarative layer and
production were minutes; the custom logic and the frontend — the parts that
are specific to this product — were the two hours. That is the same 90/10
split the [VecinGo case](CASE_STUDY_VECINGO.md) reported, from a different
builder on a different domain.

## What cost the least, and why (builder's report)

- **The whole API.** A validated JSON and one command. Semantic validation
  caught things that would have been production bugs: a `required` field
  that the RBAC was going to force (the candidate's `POST`s would have
  failed 422 "forever"), the missing `minLength` on a required text, the
  `files` grant every role that uploads needs.
- **Auth, users, JWT, roles, files, signed URLs, pagination, filters,
  aggregations, SSE.** All present. The frontend only consumed.
- **Production.** Idempotent multi-app installer; updater with health check
  and rollback; backup in one script.
- **Debugging.** Every engine error names the problem and the way out: "this
  binary registers no custom routes, so the grant is inert",
  "`profesional_id` is required AND is the role's condition column".
- **Adding a domain with another brand.** The tenant is the first DNS label;
  registering `atina` and giving it a Caddy site was all the engine asked.

## What cost the most — the four frictions (builder's report), each with our answer

Each of these is answered point by point in
[FIELD_FEEDBACK_RESPONSE.md §4](FIELD_FEEDBACK_RESPONSE.md#response-to-the-fourth-field-evaluation-atina--frente-comercial-s1-2026-08-26).
The one-line version:

1. **"My own SQL, not the engine."** The only real bugs came from skipping
   the declarative layer with hand-written `INSERT`s: schema defaults do not
   apply outside the generated path, and a `uuid` read into a Go `any` is
   not a `string`. — *Not an engine defect; the rule was already in
   `backend-spec` ("`ctx.Insert`/`ctx.Update` first; raw SQL only for what
   the schema cannot express"). We made the consequence explicit in the
   `UnsafeTx` callout.*
2. **Matching performance with the database across the internet.**
   Row-by-row recalculation cost ~1,300 round trips; loading in bulk and
   batching `INSERT … ON CONFLICT` brought it to four queries. — *Not an
   engine defect; the batch/`unnest()` patterns were already in `backend-spec`
   §3.4b, with the N+1 warning. The report is the best evidence yet that a
   pattern section must be reachable from the place the agent is when it
   hits the problem.*
3. **Being a good citizen of the public rate limit.** An anonymous visitor
   fired twelve reads on opening the portal and hit the public-route limit;
   one custom `/api/catalogos` route with its own budget and a one-hour
   cache solved it. — *Working as designed and already documented
   (`Route.RateLimit`, frontend-spec trap 5). The builder's solution is the
   documented one.*
4. **Seeding accents from Git Bash on Windows.** `curl` sent bytes in the
   system code page; a Go seeder and a small repair script fixed it. — *Not
   Appximo. Recorded as a Windows caveat in QUICKSTART, next to the other
   known Windows traps.*

## Why the result was possible (builder's report)

1. **The schema is the application.** Types, relations, state machines,
   permissions, public reads and indexes live in one file the engine
   validates with judgement and deploys hot.
2. **Custom logic runs inside the engine**, with the tenant transaction and
   the RBAC already resolved. Closing a process or a consent flow are plain
   Go functions with `Ctx`. One binary.
3. **The contracts are written for agents.** `spec`, `backend-spec`,
   `frontend-spec`, `backoffice-spec`, `quickstart`: no API surface ever had
   to be invented.
4. **The same schema serves the whole journey.** Prototype with `appximo
   up`, then compiled into the own binary, then `migrate` for each change.
5. **Operating is one command each time.** Install, update with rollback,
   back up, migrate, list tenants — with messages that say what to do next.

## What this evaluation is, and is not

By the criterion in our public material ("external evaluation" = ran outside
our infrastructure without our direction + findings published and answered
point by point + the words say exactly what the material shows), atina is the
**fourth external evaluation** — and the strongest, because it is the only
one whose app a reader can **open and use today**. It is also the first
built by a contractor for the maintainer's own use, which is disclosed here
so that "external" is read correctly: external to the engine and to its
authors, not a stranger who found the project on the internet.

Its report is a self-report of an agent-driven build; the counts we could
verify from outside are in the first table, and nothing else in this
document is presented as measured by us.
