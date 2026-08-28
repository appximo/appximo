# `json` type audit — what the write and read doors do with a JSON value

**Session:** MOTOR-TIPO-JSON-S1 (2026-08-27), Part A — written BEFORE any fix.
**Trigger:** an external field report on v0.1.9, blocking the migration of a
real production system (46,119 rows, 1.2 GB, 99 % of it nested objects in one
`json` column):

```
POST /api/declarations  {"data": {"nit": "900"}}        → 500
POST /api/declarations  {"data": "{\"nit\":\"900\"}"}   → 201
```

**Method:** no conclusion from code. One audit schema
(`declarations.data` json, `forms.metadata` json+required, `docs.doc` jsonb as
the reference), 35 real requests per binary through REST create/PATCH/PUT,
the batch transaction and GraphQL, the stored value read back after every
write, fired at THREE binaries: HEAD `5bc6dda`, the published **v0.1.9** and
**v0.1.8** (checksum-verified downloads). The raw per-request results live in
the maintainer's internal repo, `evidencia/MOTOR-TIPO-JSON-S1/`.

## The seven probes

| # | Probe | Body | v0.1.9 | v0.1.8 | HEAD |
|---|---|---|---|---|---|
| 1 | **The report's 500** | `POST {"data": {"nit":"900"}}` | **500** `internal error` | 500 | 500 |
| 1 | The operator log | — | the access log line says `status=500`; **no error line at all**. The cause is captured ONLY in the trace store (`slow_traces.err_msg`): `query: failed to encode args[0]: unable to encode map[string]interface {}{"nit":"900"} into text format for text (OID 25): cannot find encode plan` | same | same |
| 2 | The string that works | `POST {"data": "{\"nit\":\"900\"}"}` | 201, response `"data":"{\"nit\":\"900\"}"` | 201 | 201 |
| 2 | **Read parity** | `GET /api/declarations/{id}` and the list | **`"data":"{\"nit\":\"900\"}"` — an escaped string, not the object** | escaped | escaped |
| 3 | Array at the root | `POST {"data": [1,2,3]}` | **500** (`unable to encode []interface {}{1, 2, 3} into text format for text`) | 500 | 500 |
| 3 | Deep nested object | a 3-level declaration with arrays of objects | **500** | 500 | 500 |
| 3 | Number / boolean | `{"data": 42}` / `{"data": true}` | **500 / 500** (`unable to encode 42 into text format`) | 500 | 500 |
| 4 | PATCH object / array | `PATCH {"data": {...}}` | **500** | 500 | 500 |
| 4 | PATCH escaped string | `PATCH {"data": "{\"nit\":\"902\"}"}` | 200, stored | 200 | 200 |
| 4 | PUT object / string | full replacement | **500** / 200 | same | same |
| 4 | Empty string | `{"data": ""}` (POST and PATCH) | **201 / 200 — stored `""`** (not JSON) | same | same |
| 4 | `null` | `{"data": null}` | 201 / 200 → NULL; `required` json + null → 422 `required` | same | same |
| 4 | Non-JSON string | `{"data": "hola mundo"}` | **201 / 200 — stored verbatim, read back as `"hola mundo"`**: the column is a TEXT column with zero validation | same | same |
| 4 | `required` json + object | `POST /api/forms {"metadata": {"k":1}}` | **500** | 500 | 500 |
| 5 | Batch transaction | `{"op":"create", "data":{"data":{...}}}` | **500** `failed_operation: 0`; the string form 200 | same | same |
| 5 | GraphQL | `createDeclaration(input:{data:{nit:"907"}})` | structurally rejected: `Expected type "String", found {nit: "907"}` — the field is `String` in the SDL, so an object can never be sent; the string form works and reads back as a string | same | same |
| 5 | Library (`Ctx.Insert` / `Ctx.Update`) | the same map | code-identical binding (`BuildInsertArgs` / the SET builder hand the Go map to pgx) → the same encode failure. Proven by the Part-C test (`TestJSONField_LibraryParity`) which fails against the pre-fix code | | |
| 5 | The project's own seeders | commerce `seed.sh`, the four vitrina seeders | ALL go through HTTP and send the **escaped string** (commerce: `"atributos_def":"$3"` with `$3` a JSON text). That is why the demos never hit it: nobody ever POSTed an object into a `json` field | | |
| 7 | Since when | — | **identical in v0.1.8, v0.1.9 and HEAD**; the `json` type has been TEXT + no validation since the first scaffold (`b32c969`, 2026-05-27). The defect is as old as the type | | |

Reference — the `jsonb` type on the same binaries: object → 201 and the GET returns
the object; array → 201; number/bool → 201; a string is taken as JSON TEXT (`"{\"k\":1}"`
→ stored `{"k":1}`, read back as the object); a non-JSON string (`"hola"`, `""`,
`"[1,"`) → **400 `invalid request`** (Postgres 22P02 through the class-22
ladder — anonymous, no field name).

## 6. What `backend-spec` says (verbatim)

> **Documents go in `jsonb`, not `json`.** `jsonb` is a real Postgres jsonb
> column: containment (`@>`) works and an `indexes` entry can declare
> `"method": "gin"` […]. `json` is stored as TEXT — exact bytes preserved,
> but nothing you can query or index.

and, in the row-types paragraph:

> `jsonb` as a decoded **`map[string]any`**; `json` as `string`

**It is silent on what a `json` field ACCEPTS on write and on what a read
returns over HTTP.** "Exact bytes preserved" is the only sentence about the
value, and it is false for an object (500) and misleading for a string (the
bytes come back wrapped in an escaped JSON string). The same silence in
SCHEMA_REFERENCE §3 ("JSON stored as text", "the bytes you sent come back
unchanged") and in the LLM grammar. This is the design decision that was never
taken — ADR-028 takes it.

## The diagnosis

- **Branch: the driver, not a type dispatch.** Object, array, number and
  boolean fail identically (`cannot find encode plan` for each Go type into
  `text`); only a Go `string` has a pgx encode plan for a TEXT column. The
  engine's create/update type check explicitly says "any JSON value is
  acceptable for a json/jsonb column" and hands the decoded value to pgx
  unchanged — correct for `jsonb` (pgx encodes a map as jsonb natively),
  wrong for `json` (a TEXT column).
- **Two defects, separable:** (1) acceptance — the type accepts a string and
  nothing else, undocumented; (2) the failure form — a client value the engine
  cannot bind is a **500**, uncaptured in the log, and (see below) counted
  against the database.
- **No read parity, no workaround for a migration.** The string form is not a
  workaround: the caller must double-encode on write and every consumer
  (REST, GraphQL, SSE, embeds, the back-office, webhooks) receives a string
  and must parse it. For a system migrating 46k rows with N consumers, that
  is a change in every consumer, not a workaround.
- **One code path.** Every door binds the same map through the same two
  builders (`BuildInsertArgs`, the SET builder) — there is no seeder path that
  serializes differently; the demos survived because their seeders send
  strings.

## 8. The collateral finding — the query breaker counts client input as a database failure

While pacing the probes: after a burst of 500s every WRITE in the process
answered **503 `service unavailable`** for ~8 s, and a batch `503 database
unavailable`. Reproduced deliberately on HEAD:

```
40 × POST {"data": {"nit":"900"}}   → 500 ×22, then 503 ×18; writes stay 503 for 8 s
40 × POST {"nit":"1","ghost":1}     → 422 ×6, then 503 ×34   ← a plain unknown-field 422
```

`pkg/db.TenantDB.exec` runs every statement through `resilience.NewQueryBreaker`
(trips at ≥10 requests with ≥60 % failures, 8 s open), and gobreaker counts
EVERY non-nil error as a failure — a unique violation, an unknown column, a
bad `file` reference, a class-22 value, an encode error. None of those means
the database is down; all of them are produced by client input. So any
authenticated caller with `create` on any resource can open the breaker with a
handful of 422s and take every write of the process (every tenant of the app)
down for 8 s, renewably. Reads are unaffected (a different code path).
Present in v0.1.8, v0.1.9 and HEAD. Fixed in this session as **ENG-49**
(the breaker counts only unavailability-class errors — the same predicate that
already decides the 503).
