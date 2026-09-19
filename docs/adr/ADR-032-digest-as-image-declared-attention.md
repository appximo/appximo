# ADR-032 — The daily digest is read at a glance: a server-rendered image over the same counts, a declared `summary.resources` filter, and a `state_machine.pending` vocabulary the engine never guesses beyond structure

**Status:** accepted (VOZ-VISUAL-S1, 2026-09-18)
**Drivers:** A-70 / A-72 (the voice plan; step 1's frontiers — generic,
deterministic, RBAC as the hard boundary, read-only — are inherited here
unchanged), VOZ-2 (the digest called a product's `activo` "pendiente de
alguien"), and one field observation: the tiendita's five-resource digest
reads fine on a phone; VecinGo's eighteen would not — eighteen lines to find
the three that matter is technically correct and humanly useless.

## Context

`GET /api/summary` (ADR-031's cron pattern + VOZ-ESCALON1-S1) composes a
text digest from facts the engine already counts: created today, updated
today, and rows in non-terminal state-machine states. Three things it could
not do: (1) tell what needs a human from what is merely alive — every
non-terminal state was "pending of someone"; (2) choose which resources
matter in a wide app; (3) be read in three seconds. The three are the same
complaint — the digest did not distinguish what matters — and they are solved
together because the third depends on the first two: without a notion of
"waiting for action" there is nothing to put on top of a picture.

## Decisions

### 1. Rendered on the server, deterministically, from the same Report as the text

`pkg/summary/render.go` paints the digest as a PNG from the SAME `Report`
the text is composed from — one set of counts, two renderings. No language
model draws or words anything (a model that "draws the summary" can invent a
figure and nobody would know); the layout is code, the numbers are the
engine's, the same input yields the same bytes (pinned by test).

**Pure Go, no browser, no cgo.** `image/png` + `image/draw` (stdlib) +
`golang.org/x/image/font` (the `opentype`/`sfnt` rasterizer and the `vector`
package) + the Go fonts (`gofont/goregular`, `gofont/gobold` — Latin coverage
with accents and ñ). Emoji are not in the fonts, so the traffic light is a
drawn disc. A headless browser was refused on weight and on determinism.

**The binary grows 860,160 bytes** (stripped, `build-engine.sh` flags:
66,543,800 → 67,403,960, +1.29 %), by section:

| section | delta | what it is |
|---|---|---|
| `.noptrdata` | +302,336 | the two TTFs as byte slices: Go-Bold 151,748 + Go-Regular 148,672 = 300,420 |
| `.text` | +229,120 | x/image `sfnt`+`vector`+`opentype`+`font` ≈ 79 KB of symbols, stdlib `image/png`+`image/draw`+`compress/zlib` ≈ 68 KB (newly linked), `pkg/summary` render ≈ 27 KB, the rest inlining and runtime growth |
| `.gopclntab` | +137,528 | line tables for the new code |
| `.data` | +96,960 | rasterizer tables |
| `.rodata` | +92,160 | strings and type data |
| `.typelink`/`.itablink` | +1,184 | |

The fonts are a third of it and are the only part that could be cut (a
subset TTF with Latin-1 only would save ~150 KB); not worth a second font
pipeline for a 1.3 % delta on a 67 MB binary.

**Cost, measured.** Render alone (`BenchmarkRender_*`, 1 vCPU dev box): 5
resources 42.7 ms, 20 resources 64.0 ms (≈ 9.5 MB and ≈ 300 allocs per
render — a 800×2400 scratch canvas cropped to the used height; the PNG
encode at `BestSpeed` dominates). Live, end to end through the endpoint
(counts + compose + render + encode) on the dev box while the full test lane
ran beside it: 50–210 ms wall. The digest's 5 s budget is 25–100× away.
**Off the hot path:** the image is produced only when a caller asks
(`?format=png` — the Telegram receiver and the
scheduled consumer — never on a CRUD request; the plain-text call is
byte-identical to before except for the new vocabulary (binary-diff gate:
171 SAME on every CRUD case, 4 DIFF all on `/api/summary`).

### 2. Hierarchy, not a table

The picture has ONE job: in three seconds, is there anything to attend? So:
a traffic light + a headline ("13 esperan acción · 2 sin avanzar") in 40 px
bold on an 800 px canvas (≈ 18 CSS px on a 390 px phone); then a red band
with one row per resource that has declared-waiting rows — the count in
72 px, the resource, the per-state detail; then an amber band for the
inferred tier; then "HOY" (what moved, one line each, folded past six);
then "SIN NOVEDAD HOY" small and grey (flow-only resources, folded past
four). An empty day is a green card that says "Sin movimiento hoy" — never a
canvas of zeros. Twenty resources fold to ~1,700 px (one phone screen and a
bit); the text carries the full detail.

### 3. Never image-only: picture + text, and the words are the contract

Telegram delivers `sendPhoto` with the digest text as the caption when it
fits the 1024-character cap; otherwise the photo carries the first line and
the full text follows as a second message (`telegram.SendPhotoWithText`). If
the API refuses the photo for good (a 4xx) the text is sent instead and the
event is logged (`PhotoFallbackError`) — content reaches the phone, the
picture is the enhancement. A transport error or a 429 is returned as-is so
the outbox retries the whole delivery (verified live: Telegram unreachable →
`pending`, attempts climbing with the reason in `last_error`; reachable
again → delivered with `image:true`, row `sent`). An engine without the image
door (an older binary behind a newer worker) delivers text — never silence.

### 4. `summary.resources` — declared membership and order; the default is EVERYTHING, ranked

A top-level `"summary": { "resources": ["pqrs", "incidentes", …] }` says
which resources the digest reports and in what order. Load-validated: every
name must be a declared resource (`summary_unknown_resource`, listing the
declared ones), no duplicates, no empty list (dead config); strict-keyed like
every level. Studio round-trips it; `explain` reads it back.

**The default, argued from a twenty-resource app, is ALL readable resources,
ordered by what needs a human** — declared attention, then inferred, then
what moved today, then flow, then silence (`summary.Order`) — never "only the
ones with a state machine" and never a hard cap. Reasons: (a) hiding a
resource by an engine heuristic is the silent kind of wrong — an owner who
never declared a filter must never discover that the digest chose for them;
(b) the ranking already puts the four that matter on top and the image folds
the tail, so the eighteen-line problem is solved by hierarchy, not by
omission; (c) a state machine is the strongest signal of "something waits"
but not the only one (a resource with no lifecycle that got nine rows today
is news). An owner who wants fewer names them — and pays for fewer queries
(the handler asks only about the declared resources).

### 5. VOZ-2: `state_machine.pending` — declared is honest, inferred is humble, terminal is never pending

The engine cannot know from structure whether `activo` is somebody's to-do:
a product's `activo` and an order's `pendiente_pago` are both non-terminal.
So the vocabulary is a MIX of a declaration and a structural inference, with
the inference labelled:

- **Declared** `"pending": ["pagada", "preparando"]` — exactly these states
  mean "a row here waits for someone": the digest's top band, "esperan
  acción", the red light. Load-validated: each must be a known state and must
  NOT be terminal (a row that can never move again is finished, not waiting —
  `state_machine_pending_terminal`), no duplicates.
- **Declared empty** `"pending": []` — "nothing in this lifecycle waits for
  anyone" (an explicit "I looked"): every non-terminal state is a neutral
  count. `nil` vs `[]` survives JSON round-trips (custom marshal).
- **Absent** — the engine INFERS, and says so: the non-terminal INITIAL
  states (a row is born there and must be moved by someone) are reported as
  "sin avanzar (recién creados, nadie los movió)" — the amber light, never
  "esperan acción"; the other non-terminal states are neutral "en curso:
  activo 9" counts, never "pendientes de alguien". **Terminal states are
  never counted as anything.**

Why not declare-only: a schema written before this session (every one in
the field) would lose its attention tier entirely, and the inferred initial
state is a defensible structural fact, not a guess about words. Why not
infer-only: the tiendita proved the engine cannot tell `activo` from
`pendiente_pago` — only the author can, and "9 activos" said neutrally beats
"9 pendientes" said wrongly. The rule the validator keeps everywhere applies:
the engine says what it knows (structure) and never what it doesn't
(intent), and the text tells the reader which is which.

## Consequences

- `/api/summary` JSON gains `level` (`red|amber|green`), `headline`,
  `attention_total`; `text` changes vocabulary. `?format=png` and
  answers the image with `X-Summary-Level` (an `Accept: image/png` door was
  removed the next day: it shared the URL with the JSON and the URL-keyed
  response cache served a cached JSON to it — two representations, two URLs).
  The census
  view (`estado`) honors the same filter and order.
- Every schema keeps validating (both keys optional). The `spec` grammar
  teaches both; the meta-schema accepts both; `explain` reads both back.
- The image is a phone artifact: 800 px wide, 2× a 390 px screen; do not
  widen it for desktops — the digest's reader holds a phone.
- Not done, deliberately: a tenant timezone for "today" (the cron workflow
  declares its own; the digest labels the engine's day), a per-role filter,
  and `estado` as a picture from the bot (the endpoint renders the census
  card on `?view=census&format=png`, the receiver still sends it as text —
  a list of totals is already a glance).
