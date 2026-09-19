# ADR-035 — The common question costs nothing: a deterministic parser over the schema answers first, a plan cache second, the model last — behind a daily spend cap the owner is told about

**Status:** accepted (VOZ-SIN-IA-S1, 2026-09-19)
**Drivers:** ADR-033 built (VOZ-PREGUNTAS-S1) put the first paid model call in
the product: ≈ US$ 0.003 per question, one second of latency, and a per-tenant
cap of 30 model calls per minute — a ceiling of ~US$ 1 300 a day if a token
ever ran in a loop, absurd for a shop that asks ten questions a day. And most
of those questions have a fixed shape («cuántas órdenes hay hoy»): a counting
verb, a resource the schema declares, a period. The schema is finite and the
plan grammar is closed; that is solvable with rules, in microseconds, for free.

## Decisions

### 1. The cap comes first — it protects the wallet from minute one

`pkg/askspend`: a ledger per tenant and day, persisted (`public.ask_spend`) so a
restart never forgets the morning's spend, with three knobs read FAIL-FAST
(`APPXIMO_ASK_PER_MINUTE`, `APPXIMO_ASK_DAILY_USD`, `APPXIMO_ASK_ALERT_PCT`;
a non-number, a negative, a 0 per-minute or a percent outside 0..100 refuses
to boot naming the variable).

- **Per minute: 6 model calls per tenant** (was 30). A voice question takes
  several seconds to dictate and a second to answer; six a minute is a human
  at full speed, thirty was a script. It counts MODEL calls only: parser and
  cache answers are ordinary reads, bounded by the tenant rate limiter like
  any GET.
- **Per day: US$ 0.50 of model spend per tenant** (≈ 150 model questions at the
  measured cost, ten times a heavy human day). `0` disables it explicitly.
- **At the cap the MODEL is off, nothing else is.** The parser, the plan
  cache and the three fixed commands keep answering; a question that needs
  the model gets «Hoy ya se gastó el techo diario del modelo…» until the day
  changes in the declared timezone. Degradation, never a dead channel.
- **The owner is told, once:** at 80 % of the daily cap a warning through
  the same alerter every other alert uses (Telegram in Spanish with «Qué
  hacer», Slack), and once more when the cap is reached; each fires at most
  once per tenant per day (the ledger row remembers) — the alerter's own
  cooldown is not enough, since it keys on (tenant, level).
- **The spend is visible:** `appximo_ask_spend_usd{tenant,window=day|month}`,
  `appximo_ask_questions{tenant,source}` and the cache counters on
  `/metrics`; `GET /admin/ask` (platform token or admin key) with today, the
  month and the last 30 days per tenant, the caps and the cache hit rate;
  and every reply carries a `spend` block. Nobody has to open the
  provider's console.
- **Worst case, bounded:** a runaway token spends the daily cap plus one
  question (US$ 0.503 at the default), then reads «capped» until tomorrow.
  Before: 30/min × 1 440 min × US$ 0.003 ≈ US$ 1 300.

### 2. The deterministic parser answers only when it is SURE

`pkg/ask/parser.go`. Everything it understands beyond Spanish function words
and the closed operation/period vocabulary comes from the tenant's schema —
no word of any app is wired in. "Sure" is checked in code, all of it:

1. exactly ONE resource of the ROLE's vocabulary is named (schema name,
   singular/plural, underscores as spaces; never a synonym the schema does
   not carry — «pedidos» for `ordenes` goes to the model);
2. exactly ONE operation (count / list / sum / avg / group by), or a bare
   noun phrase, which lists;
3. EVERY word is consumed by a recognized piece — a stopword, the operation,
   the resource, a declared enum/state value of that resource (whole,
   accent-insensitive, plural tolerated, `pendiente_pago` ≡ «pendiente de
   pago»), a period phrase, a group-by field, or a proper name introduced by
   a preposition. One leftover word («vendimos», «vigentes», «nuevos»,
   «ignora») and the question goes to the model;
4. each enum value maps to exactly one field of the resource;
5. a proper name maps to exactly one place — the single relation whose
   target has a name-like label, else the resource's own name-like field;
   two candidates (an óptica's `citas` with `pacientes` AND `optometras`) →
   the model;
6. a sum/average names a numeric field or the resource has one obvious
   amount (`total_*` beats `subtotal_*` and `descuento_*`);
7. the plan passes the SAME validation as a model plan, then the same name
   resolution (the matcher of ADR-033 §4, reused, not reimplemented) and
   the same executor with the role's row condition and field allowlist.

A write verb («borrá», «cancelá», «agendá»…) anywhere is refused
deterministically — no key, no call. The parser never guesses to save a
call: a wrong plan costs more than three tenths of a cent.

**Measured on the corpus of real questions** (the twelve owner questions and
the provocations of VOZ-PREGUNTAS-S1 plus the shapes an owner uses by voice;
49 questions, `parser_test.go`): **33 of 49 (67 %) answered without a
model**; every one of the 49 gives the SAME kind of answer as before (the
parser's plan is the model's plan for those shapes, executed by the same
code). The unit test fails if coverage drops under 55 %.

### 3. The plan cache remembers plans, never data

`pkg/ask/cache.go`: key = tenant + role + the normalized question (case,
accents, punctuation, spaces); value = the plan the model produced, cached
BEFORE names resolve (a new row with that name is found tomorrow) and only
for executable kinds (`unclear` is re-asked; `write` never reaches the
model). A period is a TOKEN the engine resolves at execution («today» is
tomorrow's today) and the only time literal, `"now"`, is resolved the same
way, so no cached plan is bound to a date — verified by test and by the
live pass (a new row appears in the count served from the cache). Bounded:
2 000 entries, 24 h, least-recently-used out; scoped by tenant AND role, so
a plan never crosses either. Hit rate on `/metrics` and `/admin/ask`.

### 4. VOZ-8 (the prompt cache) is CLOSED, not built

With the parser taking the common shapes, the model sees only the rare
question; padding the vocabulary to Anthropic's cacheable minimum would make
every model call 2× longer in input to save a fraction of a call that now
happens a few times a day. The saving is in not calling, not in calling
cheaper. Reconsider only if a tenant's model share stays above half its
questions for a month (`appximo_ask_questions{source="model"}` tells).

## Consequences

The cost structure inverts: the common question is free and instant, the
model is reserved for the rare one, and the wallet has a floor the owner
chose and is told about. The price is a second parser to keep honest — its
"sure" rule is the whole contract, and the corpus test pins it.
