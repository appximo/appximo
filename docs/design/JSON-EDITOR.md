# JSON-AUDIT-V1 — Feasibility map: the Appitools-aware JSON editor + the external-agent schema spec

Audit date: 2026-07-09. Every claim verified against the source (symbols named
per finding) and, where it matters, against a running engine + Studio (live
import probe, `validate --json` probe). This is the construction map for two
complementary pieces:

1. an **assisted JSON editor** inside Studio — paste/edit the raw schema with
   live syntax + structural + SEMANTIC validation against the engine's real
   rules, autocompletion, and errors located at their line;
2. the **rules pack for an external agent** (Claude Code / Opus) so schemas can
   be generated outside and brought in.

The one-line conclusion: **the intelligence already exists on the server; the
editor is a thin client over it.** Three of the four hard pieces (semantic
validator with path-addressed errors, formal JSON Schema, LLM grammar) are
built and tested — what is missing is one HTTP route, one CodeMirror view, and
one packaging command.

---

## Verdicts — the five questions

### §1 Does Studio edit JSON today? **NO — import/export only, and import is blind.**

- Studio is 100% graphical (canvas + panels). The only JSON surfaces are the
  Toolbar's **Import** (a textarea → `JSON.parse` → `editor.loadSchema`) and
  **Export** (read-only view + Copy + Download) — `Toolbar.svelte`
  (`tryImport`, `exportJSON`, `download`, `copyJSON`). There is no editable
  JSON view anywhere.
- **Import validates NOTHING beyond JSON syntax.** `tryImport` catches only the
  `JSON.parse` error; it does not run `editor.validate()`, the strict-key
  check, or anything semantic.
- **Live probe (erp engine + Studio):** importing a schema with an invented
  resource key (`bloque_inventado`) and an invalid field type (`"number"`)
  showed **no error**; the unknown key was **silently dropped** (round-trip
  loss — `transform.ts schemaToModel` copies only the keys it models:
  fields/relations/indexes/foreign_keys/hooks/events/renamed_from/rbac/
  workflows), and the invalid type **survived into the export** — Studio can
  export a schema the engine rejects, and the user finds out at deploy.
  This is the exact gap the assisted editor closes.

### §2 The validator and its frontend mirror — **the mirror is broad but panel-shaped; the reusable "validate the whole document with locations" function already exists SERVER-SIDE.**

- **The engine's semantic inventory** (`pkg/schema/validator.go`, 1259 lines,
  `Validate(s) []ValidationError`): resource/field naming + reserved names
  (`auth_*`, `transaction`), unknown types, relation targets exist, the whole
  file-field constraint block, `on_delete`/`on_update` validity + nullability,
  `references` (unique + type-compatible via `pgKindForAPIType`),
  `renamed_from` (both levels), empty enum, field rules
  (`validateFieldRules`: pattern RE2 ≤200, lengths, min/max),
  `validateDefault`, `validateStateMachine`, `events` values, `relations`
  (all 3 kinds), `indexes`, composite `foreign_keys`, hooks (after ⇒ webhook
  only, per-type required keys), and RBAC both forms (`validateRBAC`).
  **Crucially, every error already carries a dot-path** (`Field:
  "resources.tasks.fields.prio.type"`) plus `Rule/Got/Expected/Fix`
  (AI-F0-S2).
- **The frontend mirror** (built by UI-F2-\*/UI-F4-\*): `fieldRules.ts`
  (fields/defaults/state machines/file — faithful), `relationIssuesFor`,
  `dataModelIssues` (references, composite FKs, pending renames),
  `hookIssues`, `rbacIssues` (both forms), names/dupes/reserved — all
  aggregated by `editor.validate()` (the deploy preflight). Gaps vs the
  engine: **no strict-key check** (unknown keys are dropped before the mirror
  ever sees them), no `events`-value check, and an imported unknown TYPE is
  only partially flagged. Output is **human strings, not paths** — panel-
  shaped, keyed to the model, not to a JSON document.
- **The reusable whole-document function EXISTS: `schema.ValidateReport(raw
  []byte)`** (`pkg/schema/report.go`) — the AI-F0-S2 unified report: runs the
  meta-schema (structural: unknown keys, closed sets, name patterns, required
  keys) AND `Validate` (semantic cross-references), merges, dedups, sorts, and
  returns `StructuredError{Path, Rule, Message, Expected, Got, Fix, Source}`.
  Probed live: an invented key → `path: "resources.tasks", rule: unknown_key,
  got: bloque_inventado, fix: remove…`; `type: "number"` → `invalid_enum_value`
  with the full expected set. **Today it is reachable only via the CLI
  (`appitools validate --json`) and `pkg/aigen` — there is NO HTTP route.**
- **Design consequence:** do NOT chase full mirror parity in TypeScript. The
  JSON editor's semantic layer should be a **debounced call to the engine's own
  `ValidateReport`** (Studio is served same-origin by the engine). The mirror
  stays what it is — instant per-panel advice.

### §3 Can a formal JSON Schema be generated? **It already EXISTS — hand-written with parity enforced by tests, which beats generation here.**

- `pkg/schema/appitools.schema.json` (Draft 2020-12, 310 lines, AI-F0-S1),
  embedded (`metaschema.go`), used by `validate-schema`, printed by
  `appitools meta-schema`. It covers the whole grammar: closed type set
  (including `file`), name `patternProperties`, `additionalProperties: false`
  everywhere (strict keys), both RBAC forms, relations/indexes/FKs/hooks/
  state machines/events.
- **Sync is enforced, not hoped for:** `metaschema_test.go` makes every repo
  schema pass and known-invalid cases fail; the FILES-LINK-S1 session updated
  it when `file` landed (verified: `"file"` is in the type enum). Generating
  it from the Go structs would express LESS (no conditional subschemas, no
  pattern keys) — reflection sees shapes, not rules. **Verdict: keep the
  hand-written file + parity tests; it is already the maintained contract.**
- What JSON Schema expresses (→ free in the editor): structure, required
  keys, closed sets, name patterns, unknown keys. What it cannot (→ layer 3):
  cross-references (relation targets exist, references unique + type-match,
  condition fields exist on the resource, renamed_from vs declared names,
  state-machine/enum coherence, after-hook ⇒ webhook). The two layers already
  agree because `ValidateReport` runs both.

### §4 Monaco vs CodeMirror in the static editor — **CodeMirror 6.**

- Studio today: **one 469 KB JS bundle + 77 KB CSS**, two npm deps (dagre,
  xyflow), go:embed, zero Node in prod. Monaco is a multi-megabyte,
  multi-file distribution with web workers (its JSON-schema smarts live in a
  worker) — embedding works but multiplies the bundle ~×6–10 and adds worker
  wiring to the Vite/go:embed pipeline, for features we get elsewhere.
- **CodeMirror 6** is modular ESM (core + `@codemirror/lang-json` + lint +
  autocomplete in the low hundreds of KB, no workers, MIT), themes via CSS
  variables (fits the 636736d sober tokens, light/dark), and
  **`codemirror-json-schema`** provides JSON-Schema-driven completion, hover
  docs and structural diagnostics directly from our meta-schema. Exact bundle
  delta to be measured at build time; the order-of-magnitude gap vs Monaco is
  not in question.
- **The three-layer architecture:**
  1. **Syntax** — CM6 `lang-json` (parse errors as you type).
  2. **Structure** — `codemirror-json-schema` fed with the SAME
     `appitools.schema.json` (autocomplete of keys/enums, hover, unknown-key
     and closed-set diagnostics, offline/instant). Source it from the engine
     (`GET` the meta-schema) or bundle it at build — same binary, same commit;
     serving it via HTTP keeps one runtime source and costs one fetch.
  3. **Semantics** — debounced (~500 ms) `POST` of the buffer to a new thin
     route returning `ValidateReport`; map each `StructuredError.Path`
     (dot-path) to a buffer position by walking CM's syntax tree (the JSON AST
     is already in the editor — resolve `resources.tasks.fields.prio.type`
     key-by-key; array indexes are numeric segments), render as lint
     diagnostics with `Fix` as the tooltip's action hint.

### §5 The external-agent spec — **90% exists; the missing 10% is packaging.**

- `docs/SCHEMA_REFERENCE.md` (2300 lines) is the complete human grammar —
  too long as a system prompt, right as the reference an agent can open.
- **The distilled LLM grammar already exists**: `pkg/aigen/prompt.go`
  (~100 lines) — closed sets, strict-keys warning, naming, the canonical
  few-shot, "output only JSON". It is exactly the "system prompt that turns
  any agent into an Appitools schema generator" — but it is **unexported Go**.
- **The correction oracle already exists and is agent-runnable**:
  `appitools validate --json` (the same `ValidateReport`). An external Claude
  Code session with the repo (or just the binary) can run the SAME
  validator-guided loop `ai-generate` uses — generate → validate → fix from
  path/fix hints — with Miguel's own subscription as the model.
- `AGENTS.md` (integration half) + `llms.txt` already target agents.
- **Gap:** one exportable artifact. Plan: `appitools spec` prints the
  distilled grammar (move the prompt text to an embedded file shared by
  `pkg/aigen` and the command — single source), and a short
  `docs/SCHEMA_SPEC_LLM.md` wrapper documenting the loop for external agents:
  *paste the spec → generate → `appitools validate --json` → fix → repeat →
  paste into Studio's JSON editor*.

---

## Construction design (next sessions)

**S1 — the validation surface (engine, thin): ✅ BUILT (JSON-EDITOR-S1).**
- `POST /editor/validate` — body = raw schema JSON (1 MiB cap, 413 over it),
  response = `ValidateReport`. Stateless, no data access, JWT-skipped like the
  rest of `/editor` (same class as `/openapi.json`; the tenant rate limiter
  still applies). `GET /editor/meta-schema` — serves `schema.MetaSchemaJSON()`
  (Cache-Control 1 h). Implemented in `pkg/editorui/validate.go` + registered
  in `Register` (literal routes win over the asset wildcard in chi); tested in
  `validate_test.go` (good schema → valid, the probe schema → unknown_key +
  invalid type at their paths, body cap → 413, meta-schema == embedded bytes).
  Zero hot-path impact (new routes).
- `appitools spec` + the shared embedded grammar file (S3 — pending).

**S2 — the JSON view in Studio (the editor): ✅ BUILT (JSON-EDITOR-S2).**
- The "Code" view (`CodeView.svelte`, Canvas | Code toolbar toggle): CM6 with
  the three layers. Open = current canvas serialized (`editor.toJSON()`);
  Apply = parse → `ValidateReport` gate (errors block, located, jump-to-first)
  → `loadSchema` (renamed_from lifts into the baseline — renames preserved).
  Paste-from-agent lands here. Import/Export modals kept (redundant but cheap).
- Path→position mapping via the CM syntax tree walk (`pathToRange.ts`, no
  extra parser dep; handles both `foreign_keys[0]` and `.0.` index dialects;
  `unknown_key` errors refine to the offending property node).
- **As-built deviation from §4's sketch:** layer 2 (meta-schema client-side) is
  COMPLETION + HOVER only; buffer DIAGNOSTICS are unified in the debounced
  layer 3 (`ValidateReport` carries the structural errors too). Rationale:
  one authority (no duplicate markers for the same fault), and the client
  library validates an older draft (Draft04 walker) while the meta-schema uses
  Draft-2020-12 conditionals (if/then, dependentRequired) that only the Go
  side evaluates — structural marks still land within the 400 ms debounce.
  Also: the library's markdown-it+shiki hover renderer is aliased to a tiny
  escape stub at build time (multi-MB chain for plain-sentence tooltips).
- Bundle: the Code view is a LAZY chunk — canvas bundle 472 KB (was 469 KB),
  CodeView chunk 654 KB (207 KB gzip) loaded on first toggle. No workers.
- Playwright-verified live (20/20): the broken probe schema shows every error
  at its line with its fix (silent-drop killed: `bloque_inventado` and
  `"type": "number"` are located errors now, not silent discards); Apply is
  gated while invalid and loads the canvas when fixed; renamed_from survives
  Apply → re-export; syntax layer instant; theme computed light/dark.

**S3 — the agent pack:** `docs/SCHEMA_SPEC_LLM.md` + `appitools spec` +
README/AGENTS pointers. The golden external loop documented end-to-end.

**Explicitly reused (nothing semantic is rebuilt):** `ValidateReport` +
meta-schema + `Validate` dot-paths (server), `fieldRules.ts` mirror (panels,
unchanged), `prompt.go` grammar (distilled spec), Studio auth/deploy flows
downstream of Apply.

**Order:** S1 → S2 → S3. S1 is prerequisite-free; S2 depends on S1; S3 is
independent and cheap.
