# ADR-038 — The owner's words are declared, never wired: `aliases` in the schema; a write PLAN is cached and re-prepared; a sentence that is not a question is never billed

**Status:** accepted (VOZ-AHORRO-S2, 2026-09-20)
**Drivers:** the first days of real use (VOZ-9: `gasto` and `public.ask_history`
on the 58) showed three leaks, each avoidable and none a missing capability:
(1) the same write order dictated three times cost three model calls
(`[create cupones: …]` ×3 = US$ 0.011); (2) «Si pero mejor el viernes» —
an answer to a confirmation that had just been cancelled — bought a
«no entendí» from the model (US$ 0.004) and sat an hour in the cache; (3)
the deterministic parser settled 40 % of the real questions against 67 % of
the lab corpus, and the card said why: the owner's words are not the
schema's («pedidos» is not `ordenes`, «ventas» is nothing, and on an English
schema every Spanish word fails). This ADR makes what exists cost what it
should; it adds no capability.

## Decisions

### 1. Synonyms are DECLARED in the schema — `aliases` — and validated unique at load

A resource carries `"aliases": ["pedidos", "ventas"]`; an enum field carries
`"aliases": {"pendiente_pago": ["sin pagar", "pendientes"]}`. Both live
INSIDE the thing they name (like `renamed_from` and `state_machine.pending`),
because an alias is a property of the resource or the value, not a
top-level dictionary an author maintains apart from it; Studio preserves
them like it preserves `summary` (resource-level in the entity's extras,
field-level inside the field definition).

**The engine wires no domain word.** Every word the parser recognizes
beyond Spanish function words and the closed operation/period vocabulary
comes from the tenant's schema — now including its aliases. If the schema
does not declare «pedidos», the parser does not know «pedidos», and that is
the correct behavior for a generic engine: the alternative — a Spanish
synonym table in Go — is a product decision about ONE app's domain smuggled
into every app.

**Every alias must mean exactly ONE thing in the whole schema**, enforced
at load with the SAME derivation of forms the parser matches
(`schema.NameForms`, `schema.ValueForms` — one source, so "unique at load"
and "recognized at runtime" are the same predicate):

- a resource alias that is a declared resource's own name or plural →
  `alias_is_resource_name`; on two resources → `alias_ambiguous`; also a
  declared value or value alias anywhere → `alias_is_value` (a word cannot
  mean a resource and a state at once);
- a value alias whose key is not in `enum` → `alias_unknown_value`; on a
  field with no enum → `alias_needs_enum`; a declared value's own form →
  `alias_is_value`; a word that already means another value of the SAME
  resource → `alias_duplicate`; a resource name → `alias_is_resource_name`;
- **a value alias MAY repeat across resources** («sin pagar» on orders and
  on invoices is a state of each): the parser scopes it by the resource the
  sentence names, and settles a sentence that names no resource only when
  the value exists in exactly one place.

The validator caught the session's own first draft twice (a plural the
forms already derive, a resource alias equal to a state) — the rule works.

**The parser uses them like the schema name**, for questions and for the
one write shape it settles (a state transition: «cancelá el pedido
ORD-1003»), with the same "sure" rule: ambiguity falls to the model. The
model's vocabulary lists them (`ordenes (also called: pedidos, ventas)`,
`pendiente_pago (also: sin pagar)`), so the rare question that still needs
the model maps the owner's words from the schema. The reply always speaks
the schema's word: an alias is understood, never spoken back.

**Generic Spanish the real history asked for**, added beside the aliases
because they are language, not domain: «los últimos N» (a list, newest
first, bounded), a name after «llamado / que se llama», a name after the
resource a relation points at («las órdenes del cliente Ana Gómez» — two
resources are one question when the second is the first's relation target
and introduces a name), a Capitalized run that is not the first word
(dictation capitalizes proper names), a token with digits matched against
the resource's identifier field («ORD-1003»), gender agreement on values
(«pedidos cancelados» → `cancelada`), and a value that exists in exactly one
place naming its resource («cuántos perros hay» → `pets.species = dog`).

**Measured on the 58's real questions** (every question with text in
`ask_history` and the sessions' live logs — 28 questions, both apps), the
new parser against each app's schema WITHOUT and WITH its aliases:
tiendita 67 % → 78 %, petfriendly 20 % → 70 %, **total 50 % → 75 %**. The
journal-era questions (no text kept) are classified by their recorded
reason: «no resource named» ×5 and «unknown word: usuarios/pendientes/
últimos/Carlos» and «two resources: clientes, ordenes» ×2 are the shapes
this ADR settles; «two periods» and «vendimos» are not (§Limits).

### 2. A WRITE plan is cached — never the result — and re-prepared before every confirmation

ADR-037 §7 said write plans are not cached ("a cached create is a footgun
for no saving worth having"). The real history retracts the second half:
the same order dictated three times cost three calls. The first half stays
true and is the design: **the cache holds the plan the model produced — the
resource, the field names, the values as SAID (time tokens, `{"match":…}`
names) — and nothing that came from the database.** A cache hit re-runs
the whole preparation against the database of the moment: names are
matched again (a person added since is found; one deleted is offered for
creation), the update's row is looked up again (gone → «no encuentro»;
relabelled → the confirmation shows the row as it is now; already in the
target state → «ya está así»), time tokens are resolved on the day it runs
(«mañana» cached today is tomorrow's tomorrow), required fields are checked
again, and a FRESH pending with a new id is created. **Nothing a cached
plan says in the confirmation is older than the moment it is asked.** The
result is never cached: two confirmed «anotá pagar la luz» are two rows.

Scope: **tenant + role + USER** (+ the vocabulary fingerprint). A read plan
is knowledge about the schema, the same for everyone in the role; a write
plan is an order, and an order is personal — one user's dictated sentence
never seeds another user's pending. The model's `write` refusal (it gave
up) is cached like `unclear`, for an hour.

Measured on Miguel's own case: three repetitions of the coupon order cost
US$ 0.0115 before and US$ 0.0038 after (one call, two cache hits at US$ 0,
each with a fresh, current confirmation).

### 3. A sentence that is not a question is never billed — with the line written

The parser is now also sure of what is NOT a data question, and answers it
at zero cost: a stray answer to a confirmation that no longer exists
(«sí pero mejor el viernes» — cancelled, explained, not re-planned), a
greeting, a request for help («qué puedo preguntar» — answered from the
vocabulary), a bare proper name («Ana Gómez» — «¿qué querés saber de…?»).

**The line:** the verdict is given ONLY when the sentence contains NOTHING
the grammar could execute — no operation word (count/list/sum/avg/«últimos»),
no schema word (resource, alias, value, field), no period, no write verb.
Anything executable keeps the model reachable even when the rest is noise,
because the model's own knowledge may still map it («cuántos pedidos hay» on
a schema that declares no alias for pedidos). Discarding a legitimate
question — converting it into a free but WRONG «no entendí» — is worse than
three cents; the test corpus pins both sides (`TestAliases_PreDiscardIsNarrow`).
The residual risk, named: a sentence that starts with a yes/no word and
contains nothing executable («si tenemos ventas» on a schema without the
alias «ventas») is discarded; with the alias declared it is a question.

**Useful vs wasted.** `gasto`, `GET /api/ask/spend` and `GET /admin/ask`
split the model spend: useful (a model call that produced a plan the engine
ran or confirmed — including a pick or a not-found, where the plan was right
and the data was not there) and wasted (a paid `unclear`, `write_refused`,
`unavailable`, `invalid`). The phrases that cost the most are marked
«✗ no sirvió».

**The «no entendí» hour, re-examined and kept.** The reason to shorten it
— a synonym declared AFTER the refusal leaving the owner with the old error
— is closed structurally: the plan cache key carries the vocabulary's
FINGERPRINT (resources, aliases, fields, values, value aliases), so a
schema change makes every plan translated against the old vocabulary
unreachable, restart or not. And the sentences that were never questions no
longer enter the cache. What remains under the hour is a genuine model
verdict on a genuine question, and an hour is the right memory for one.

### 4. The trace on a screen that is not Telegram

The reply gains `display`: the text as plain text (line breaks kept, no
HTML) INCLUDING the `⚙︎` trace when the app has it on. `speech` still never
carries it (A-77). A Siri shortcut speaks `speech` and shows `display`.

## Limits (written now)

- «vendimos», «vigentes», «la más cara», «¿y ayer?», a question in English
  on a Spanish app — still the model's (VOZ-7 territory: an alias names a
  THING, not a verb, a comparison or a ranking).
- An alias is authored in Studio's Code view; the entity panel has no
  aliases field yet (preserved, not edited visually).
- Aliases are compiled with the boot schema (the vocabulary is boot-static,
  like RBAC): declaring them is a schema change — deploy + restart, and a
  rollback restores the schema FIRST (OPS-55: the previous binary rejects
  the key).

## Consequences

The cost structure of the voice channel now follows use: the common word is
free because the schema says what it means, the repeated order is free after
the first, and the non-question is free by rule — with the money that IS
spent visible as useful or wasted. The price is one more block to author
(Studio preserves it; `explain` reads it back) and one more thing the
validator must keep honest, which it does from the same forms the parser
matches.
