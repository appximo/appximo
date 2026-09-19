# ADR-036 — Knowing where the money goes: who answered each question and why, a bounded history that keeps the shape and not the person, `gasto` on the same card, and a per-user cap that composes

**Status:** accepted (VOZ-TRAZABILIDAD-S1, 2026-09-19)
**Drivers:** ADR-035 cut the worst case from ≈ US$ 1 300 a day to US$ 0.50 and
the owner had no way to see it: `/admin/ask` gave a day's total, nothing said
which phrase cost what, who answered it, or why the parser passed. Without
that, "the parser answers 67 %" is a lab number (VOZ-9) and the phrases that
go to the model — the ones that say what the parser should learn — are lost.

## Decisions

### 1. Every reply says who answered; the voice never reads the cost

The JSON always carries `source`, `cost_usd`, `fallback` (the parser's
reason code, e.g. `unknown word: vendidas`) and `fallback_es`. With
`APPXIMO_ASK_TRACE=on` (default OFF — for whoever administers, a shop owner
does not care) the reply's TEXT ends with one small line: `⚙︎ parser · 2 ms ·
US$ 0` / `⚙︎ caché · 3 ms · US$ 0` / `⚙︎ modelo · 1,1 s · US$ 0,0029 · al
modelo porque: palabra fuera del schema «vendidas»`. **Never in `speech`.**
Thought as daily use, not as a demo: a voice that appends "tres centavos"
to every answer is muted in two days, and the thing worth knowing — why the
parser passed — is read, not heard.

### 2. The history keeps the shape, not the person

`public.ask_history`, one row per question: when, tenant, role, user id (the
JWT subject — an id), the question text under a policy, source, kind,
resource, cost, latency, model time, the plan, the parser's reason, cache
hit. Written OFF the answer path (a buffered channel, one writer goroutine;
a full buffer drops rows and logs it — the answer is never delayed).
Retention `APPXIMO_ASK_HISTORY_DAYS` (30; `0` = off), pruned hourly and at
boot — never an unbounded log.

**No IP is stored, ever (A-53).** **The text follows
`APPXIMO_ASK_HISTORY_TEXT`:** `redacted` by default — the proper names the
plan identified (its `match` filters) become `[nombre]`, so «las órdenes de
Ana Gómez» is kept as «las órdenes de [nombre]»: the shape the parser needs
to learn from, without the customer. `full` keeps names; `none` keeps the
plan only. The same discipline as request bodies (A-53/A-61): personal data
is opt-in. The honest limit: a name the engine could NOT identify (a
question that went unclear) stays as typed under `redacted` — a tenant that
cannot accept that sets `none`.

### 3. The three lists that matter, and VOZ-9's real number

`GET /admin/ask?tenant=<id>&days=<n>`: `top_cost` (the phrases that cost
the most), `top_repeated`, `model_fallbacks` (each with the parser's reason)
and `share` (the real parser / cache / model split). The lab corpus stops
being the measure of the parser; the tenant's own questions are.

### 4. `gasto` — the same card, admin-grade only

A fourth Telegram command beside `resumen`, `estado`, `ayuda` (none of them
touched): today and this month, the cap and what is left, who answered how
many, the phrases that cost the most — the digest's census card
(`summary.Render` with a subtitle and rows), picture + text, no new
renderer. Served by `GET /api/ask/spend[?format=png]` and authorized by the
RBAC that already exists: `policy.Allows(role, sentinel, "read")` with a
sentinel no schema can declare — true only for a wildcard-resource,
admin-grade role, the same inherited test the tenant observability routes
use. A listed or row-scoped role is 403: the spend of a platform is the
administrator's business, not a clerk's. The bot asks as its configured
role, so `gasto` works where that role is admin-grade and says so elsewhere.

### 5. A cap per user that composes

`APPXIMO_ASK_DAILY_USD_PER_USER` (default `0` = off): per (tenant, user
id, day), persisted beside the tenant rows. Whichever cap is reached first
wins. At the user cap only that user degrades to parser/cache/fixed
commands («ya usaste tu cupo diario del modelo»); the rest of the tenant
goes on; the administrator gets one alert per user per day. Off by default
because a single-owner app must not meet a second, silent ceiling.

### 6. The «no entendí» cached 24 h is now cached 1 h

Temperature 0 is deterministic in intent, not a guarantee of identical
output; an hour still stops a loop from re-billing the same nonsense every
second, and no phrase the model could answer is denied a whole day.

### 7. `deploy-app.sh --env-add=KEY[,KEY…]` (OPS-56)

Values come from the operator's own environment, travel over ssh stdin,
land 0600 after the pre-deploy env copy, and are verified present. Never on
a command line, never in a log — the same rule as the model key.

## Consequences

The question path is now accountable phrase by phrase, with the cost where
it is read and not where it is heard; the history is bounded by days and by
privacy policy; the administrator can see the tenant's spend from the same
chat, and nobody else can. The price is one more table and one more
goroutine — both off the answer path.
