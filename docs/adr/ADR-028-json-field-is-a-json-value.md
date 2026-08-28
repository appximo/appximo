# ADR-028 — A `json` field holds a JSON VALUE, on every door, in both directions

**Status:** accepted (MOTOR-TIPO-JSON-S1, 2026-08-27)
**Drivers:** an external field report on v0.1.9 — `POST {"data": {"nit":"900"}}`
on a `json` field is a **500**, the escaped-string form is a 201, and the GET
returns the escaped string — blocking the migration of a production system
whose 1.2 GB live in one `json` column of nested objects. Audited before any
fix in [docs/audits/JSON_TYPE_AUDIT_S1.md](../audits/JSON_TYPE_AUDIT_S1.md):
identical in v0.1.8, v0.1.9 and HEAD; the defect is as old as the type.

## Context

`"type": "json"` maps to a TEXT column (deliberately — LIBRARY-GAPS-S1 kept
every pre-existing column untouched when `jsonb` arrived). The write path's
type check says "any JSON value is acceptable for a json/jsonb column" and
hands the decoded Go value to pgx. pgx encodes a Go `string` into TEXT and
nothing else, so an object, an array, a number or a boolean fails to bind and
the handler masks the driver error as **500 "internal error"** — not logged
(captured only in the trace store), and counted by the query circuit breaker
as a database failure. A string, ANY string, is stored verbatim with no
validation (`"hola mundo"` → 201) and comes back over HTTP as an escaped JSON
string, in every read surface (REST, embeds, GraphQL where the field is
`String`, SSE, the back-office, webhooks).

`backend-spec`, SCHEMA_REFERENCE and the LLM grammar say only "stored as
TEXT — exact bytes preserved". Nobody had written what the type ACCEPTS or
what a read RETURNS. This is a design decision that had never been taken,
and the 500 is the symptom.

Two defects, judged separately:

1. **The failure form.** A value the engine cannot bind is client input; the
   answer is a named 422 or acceptance — never a 500. Non-negotiable
   regardless of the acceptance decision.
2. **Acceptance.** What is a `json` field?

## Decision

**A `json` field holds a JSON value.** It is not "a text column that happens
to be named json". Concretely:

1. **On write, every door accepts every JSON value** — object, array, number,
   boolean, and a string. A **string is taken as JSON TEXT** (the document's
   source: `"{\"nit\":\"900\"}"` is the object `{"nit":"900"}`; `"123"` is the
   number 123), which is exactly the convention `jsonb` already had on this
   engine and the one Postgres itself and pgx use for `'…'::jsonb`. A string
   that is not valid JSON (`"hola mundo"`, `""`, `"[1,"`) is a **422
   `rule: "type"`** naming the field and saying a string is read as JSON text.
   `null` stays NULL, governed by `required` as before. The doors: REST
   POST/PUT/PATCH, GraphQL `create…`/`update…`, `/api/transaction`,
   `Ctx.Insert`/`Ctx.Update` — ONE function (`schema.CoerceJSONFields`)
   called from the shared write cores, so no door can diverge.
2. **On read, every HTTP surface returns the value natively** — REST list,
   get, create/update responses, relation subroutes, `?include=` embeds
   (`::json` in the SQL), GraphQL (the field becomes the `JSON` scalar — the
   SDL changes, see Consequences), SSE, batch results, the admin data browse,
   after-hook webhook payloads. A stored text that is not valid JSON (written
   by an engine before this ADR) is returned as a JSON string on the Go
   surfaces — lenient, never a 500 — and is a documented condition for the SQL
   embed path (below).
3. **The column stays TEXT** — no physical migration, no churn on any
   existing tenant (the LIBRARY-GAPS-S1 decision stands) — and it holds
   **canonical compact JSON text**: an object/array/number/boolean is encoded
   by Go (keys sorted, numbers as float64 — the HTTP path's documented ~2^53
   limit, the same fidelity `jsonb` has on the read path today), a JSON-text
   string is compacted (its numeric text and key order are kept). "Exact bytes
   preserved" is RETRACTED from the docs: it was never true for an object
   (500) and the read path re-encodes anyway.
4. **The library read stays a `string`.** `Ctx.Query` returns the stored text
   as a Go `string` (documented row type, unchanged) — a handler that wants
   the document unmarshals it. The library is the precise path; changing its
   row type would break consumer handlers for no gain. `Ctx.Insert`/`Update`
   ACCEPT the same values every other door does.
5. **The contract says so.** `/openapi.json` publishes a `json` property as a
   TYPE-LESS property (an arbitrary JSON document), like `jsonb`, tagged
   `x-appximo-json: "text"` (`jsonb` gets `"jsonb"`), so a generic tool — the
   embedded `/app` back-office first — renders a JSON editor instead of a text
   input that would show `[object Object]`.
6. **GraphQL:** a `json` field is the `JSON` scalar, like `jsonb`. The
   "stays `String` so every SDL is byte-unchanged" rationale of LIBRARY-GAPS-S1
   is retracted: a `String` field structurally cannot accept an object, which
   is the report's defect at the GraphQL door; parity across doors is the
   point of this ADR.
7. **`jsonb` inherits the write-side validation:** a non-JSON string is the
   same named 422 (it was an anonymous 400 `invalid request` from Postgres
   22P02). Same function, same message, one vocabulary for the two document
   types. Everything else about `jsonb` is unchanged.

### Companion decision — the query breaker counts only unavailability (ENG-49)

The audit found that `pkg/db.TenantDB.exec` runs every statement through the
circuit breaker with gobreaker's default "every error is a failure": a unique
violation, an unknown column, a class-22 value, an encode error — all client
input, none a database outage — all counted. Six `422`s in a row opened the
breaker and every write of the process (every tenant of the app) answered
503 for 8 s; any caller with `create` on any resource can do it, renewably.
**Decision:** the breaker's `IsSuccessful` is the SAME predicate that already
decides the 503 (`isUnavailableCause`): only "the database could not serve
this" counts — timeouts, connection failures, class 08/53/57P0x. A statement
the database REJECTED proves the database is up.

## Alternatives rejected

- **Reject objects explicitly (422) and keep the string contract.** Turns the
  500 into an honest error but leaves the type useless for what its name
  promises, and the migration blocked. The name `json` is the contract.
- **Accept objects, keep strings as strings (a JSON string value).**
  Unambiguous (`"123"` stays the string "123") and round-trips perfectly —
  but it would silently change the meaning of every existing write:
  `"{\"nit\":\"900\"}"` (today: the object, read back escaped) would become
  a string containing JSON, forever, and existing rows written as raw text
  would read back as objects while new string writes would not. Two
  representations of "an object" in one column. Rejected: the jsonb/Postgres
  convention exists, is what today's string writers meant, and is consistent
  with the sibling type.
- **Change the physical column to Postgres `json`.** The "right" storage —
  pgx binds and decodes it natively, Postgres preserves the text — but it is
  an `ALTER COLUMN TYPE` over every existing tenant column with data (fails on
  any non-JSON row, gated as a type change), and the read path would still
  decode into Go values (same float64 fidelity). Churn for no client-visible
  gain. Reconsider if a consumer needs Postgres-side JSON operators on the
  type; today that consumer declares `jsonb`.
- **Preserve the client's exact bytes** (a second decode of the body into
  `json.RawMessage` for resources with json fields). Would keep numeric text
  beyond float64 and key order on the way in — and lose both on the way out,
  where the engine decodes every jsonb/json value into Go before serializing.
  Rejected for now: complexity for a fidelity the read path does not deliver.
  Reconsider together with a read-side `RawMessage` scan (an engine-wide
  precision decision, not a `json`-only one) — recorded as ENG-50.

## Consequences (declared, with the migration note)

- **Reads change for existing rows** written as JSON text: a client that
  did `JSON.parse(row.data)` on the string now receives the object and must
  drop the parse. This is the change the report asked for; it is a break for
  a client that depended on the escaped form.
- **Writes of non-JSON strings change:** `"hola mundo"` / `""` were 201, now
  422. A `json` field that was being used as free text must become `string`/
  `text` (a metadata-only migration: the column is TEXT either way — rename
  the type in the schema, redeploy, no data moves). Rows already holding
  non-JSON text: find them with
  `SELECT id FROM <table> WHERE <col> IS NOT NULL AND NOT (<col> ~ '^\s*[\[{"tfn0-9-]')`
  (a coarse filter; the release note carries the exact query); they read back
  as strings on the Go surfaces and make `?include=` embeds of that resource
  fail with 400 until fixed (the `::json` cast). Known, documented.
- **GraphQL SDL:** `json` fields change from `String` to `JSON` (inputs and
  outputs). Introspection-driven clients pick it up; a hand-written query that
  sent a string keeps working (the scalar accepts a string as JSON text).
- **OpenAPI:** the property loses `"type": "string"` and gains
  `x-appximo-json`. Generic form generators must treat it as a document.
- **Filters:** `?filter[<json>][eq]=` keeps comparing the stored TEXT, now
  canonical — equality against the compact Go encoding. Unchanged intent,
  documented as fragile (it always was); use `jsonb` + `@>` for real queries.
- Hot path: a resource with no json/jsonb field pays one precomputed-flag
  check per write and nothing on read (measured `no_change`, ABBA on the write
  protocol); a json write pays one `json.Marshal`/`json.Compact` of the value.

## Addendum (MIGRACION-CONFIANZA-S1, 2026-08-28) — canonicalization, verified with requests

A real migration (Symfony 7.2, 23 tables, 46,119 rows, 1.2 GB in one `json`
column) measured what "canonical compact JSON text" means in practice and
reported it as undocumented. Verified on HEAD with real requests, on `json`
and `jsonb` alike:

| Sent | Stored / returned | Class |
|---|---|---|
| `{"zeta":1,"alpha":{"y":2,"x":1}}` | `{"alpha":{"x":1,"y":2},"zeta":1}` | keys sorted, recursively (array order kept) — **no loss** |
| `0.01000000000000000020816681711721685132943093776702880859375` | `0.01` | shortest round-trip rendering of the SAME float64 — **no loss** |
| `1.50` | `1.5` | a trailing zero is not a value — **no loss** |
| `12345678901234567890` | `12345678901234567000` | beyond 2^53 — **LOSS** (ENG-50, both directions, both types) |
| `"{\"zeta\": 1, \"dec\": 1.50, \"big\": 12345678901234567890}"` (a JSON-text STRING on a `json` field) | `{"zeta":1,"dec":1.50,"big":12345678901234567890}` | compacted, numeric text and key order kept, emitted verbatim on read — **the exact door** |

Consequence, now written where a migrator looks (backend-spec §2 and §2b):
**byte identity is not reachable through the API** for a value written as a
value; a parity check must canonicalize both sides (parse → sort keys →
compare values), and a document that needs exact numeric text takes the
JSON-text-string door on a `json` (TEXT) field. "Exact bytes preserved" was
retracted in this ADR and survives in no doc, README or site copy (swept
2026-08-28; the only remaining mentions are historical, in the audit and in
this ADR's own rejected-alternative bullet).

