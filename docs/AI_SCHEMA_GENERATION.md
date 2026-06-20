# Generating Appitools schemas with an LLM — the validation feedback loop

This is the contract between an AI (or any tool) and the Appitools validator. It lets
a model generate a schema, get **machine-readable, actionable feedback**, and
self-correct until the schema is valid — no human in the loop.

The two pieces that make this deterministic:

1. **The formal meta-schema** (`pkg/schema/appitools.schema.json`, Draft 2020-12) —
   the structural grammar. See [SCHEMA_REFERENCE.md §12](SCHEMA_REFERENCE.md#12-machine-validation--the-formal-json-schema-meta-schema).
2. **The unified validation report** (this document) — one structured result that
   merges the structural (meta-schema) and semantic (Go) validators.

## The loop

```
generate schema  →  appitools validate --json schema.json  →  read report
      ↑                                                            │
      └────────────── apply each error's "fix" ←──────────────────┘
   (repeat until { "valid": true })
```

Each round, the model:
1. runs `appitools validate --json <file>` (exit 0 = valid, 1 = invalid);
2. for every entry in `errors[]`, edits the schema at `path` per `fix` (using
   `expected` to choose a valid value);
3. re-validates. The validators are pure and deterministic — the same schema always
   yields the same report.

## The report format

```json
{
  "valid": false,
  "errors": [
    {
      "path": "resources.users.fields.email.type",
      "rule": "invalid_enum_value",
      "message": "value must be one of 'string', 'text', 'int', 'int64', 'float64', 'bool', 'uuid', 'time', 'json'",
      "expected": ["string","text","int","int64","float64","bool","uuid","time","json"],
      "got": "varchar",
      "source": "metaschema",
      "fix": "use one of the allowed values listed in expected"
    },
    {
      "path": "resources.posts.fields.author_id.references",
      "rule": "reference_not_unique",
      "message": "references \"email\" must be the target's id or a UNIQUE column of \"users\" ...",
      "got": "email",
      "source": "semantic",
      "fix": "add \"unique\": true to users.email, or set references to a column that is already unique (or 'id')"
    }
  ]
}
```

Per-error fields:

| field | meaning |
|---|---|
| `path` | dotted location of the offending value (`resources.<r>.fields.<f>.<key>`, `rbac.roles.<role>.…`). Edit here. |
| `rule` | machine-readable category (stable; switch on this). |
| `message` | human-readable explanation (also lists options inline). |
| `expected` | the allowed values, when the rule is a closed set — pick one of these. |
| `got` | the offending value you supplied. |
| `fix` | a concrete instruction to correct it. |
| `source` | `metaschema` (structural) or `semantic` (cross-reference) — see below. |

`errors` is always an array (`[]` when valid). A `path` is reported by **one** source
at most: a structural error suppresses the semantic error at the same path (they are
the same problem), so there are no duplicates to reconcile.

## Two sources, two layers

- **`metaschema` (structural).** Type/enum/pattern/required/unknown-key/RBAC-form
  errors — everything JSON Schema can express. These are *shape* problems: a bad
  `type`, an unknown key, a name that isn't `^[a-z][a-z0-9_]*$`, an `op` other than
  `eq`, a `js`/`wasm` after-hook, mixing the two RBAC forms.
- **`semantic` (cross-reference).** Problems that need to look across the document,
  which the meta-schema cannot see: a `relation`/FK `target` or `references` column
  that doesn't **exist** or isn't **unique** on the target; a condition/allowlist
  field that doesn't exist on the resource; a `state_machine` state not in the
  field's `enum`; a `default` whose type doesn't match the field; a `renamed_from`
  that still names a declared field.

Fix the structural errors first if both appear — once the shape is right, re-validate
to surface any remaining semantic ones.

## Rule categories

Structural (`source: "metaschema"`): `invalid_enum_value`, `invalid_value`,
`unknown_key`, `missing_required`, `pattern_mismatch`, `form_mismatch`,
`length_out_of_bounds`, `structural_error`.

Semantic (`source: "semantic"`): `invalid_type`, `unknown_relation_target`,
`reference_not_unique`, `reference_type_mismatch`, `unknown_field`,
`unknown_resource`, `unknown_action`, `missing_condition_field`,
`condition_action_not_granted`, `invalid_condition_op`, `invalid_on_delete`,
`invalid_on_update`, `invalid_default`, `invalid_state_machine`, `invalid_relation`,
`invalid_foreign_key`, `invalid_index`, `invalid_renamed_from`,
`invalid_resource_name`, `rbac_form_conflict`, `invalid_events`, and the catch-all
`schema_error`.

(The set may grow; treat an unknown `rule` as "read `message`/`fix` and correct at
`path`".)

## How to apply a fix (examples)

- `rule: invalid_enum_value` / `invalid_value` → replace `got` at `path` with one of
  `expected`.
- `rule: unknown_relation_target` / `unknown_resource` → `expected` lists the real
  resources; pick the intended one (or add the missing resource).
- `rule: unknown_field` → `expected` lists the resource's columns; pick one.
- `rule: reference_not_unique` → either add `"unique": true` to the target column or
  point `references` at `id`.
- `rule: unknown_key` → delete the key named in `got` (it's a typo or not part of the
  grammar; the meta-schema lists the valid keys for that level).
- `rule: rbac_form_conflict` → a role is **either** role-global
  (`resources`/`actions`/`conditions`/`fields`) **or** per-resource (`permissions`),
  never both.

## The generation loop (AI-F0-S3) — `pkg/aigen` + `appitools ai-generate`

The sections above are the *contract* (validate → actionable errors → correct). The
`aigen` package and the `ai-generate` CLI are that contract **closed into a working
loop**: a natural-language app description goes in, a VALID schema comes out, with
the model self-correcting from the report — no human in the loop.

```
appitools ai-generate "un CRM para una óptica: clientes, citas, ventas"
```

What it does each round (`aigen.Generate`):

1. Sends the description + a **compact grammar** system prompt (`pkg/aigen/prompt.go`
   — the closed sets, the strict-key rule, and the canonical example, condensed from
   this reference so the model needs few tokens) to the model.
2. Extracts the JSON (strips ``` fences / prose), runs `schema.ValidateReport` — the
   **same** validators the engine uses, no shell-out.
3. If invalid, appends the machine-readable `errors[]` (the format above) to the
   conversation with a "correct these" preamble and loops — so the model corrects
   *in context*, not blind.
4. Stops at the first valid schema, or after `--max-iterations` (default 5), reporting
   the remaining errors if it never converged.

**The model is reached over raw `/v1/messages`** (`pkg/aigen/client.go`) — no SDK
dependency, CGO-free, key from `ANTHROPIC_API_KEY` (never hardcoded; absent → a clear
message, not an obscure failure). The `ModelClient` interface is the seam: tests inject
a deterministic stub, so the loop is *proven* (generate → invalid → correct → valid)
with no network and no key.

**Economic instrumentation — the point.** Every run reports the iterations it took to
converge, the cumulative input/output tokens, and the **approximate USD cost** on the
model's published price. The default model is the **cheap** one (`claude-haiku-4-5`,
$1/$5 per MTok) on purpose: the thesis is that a cheap model is enough, and this is the
number that confirms or challenges it. `--model` switches tiers for comparison;
`--json` emits the full result (schema + metrics) for tooling.

```
── AI schema generation ─────────────────────────
  resultado:   ✓ VÁLIDO (tras corrección)
  modelo:      claude-haiku-4-5
  iteraciones: 2
  tokens:      2400 in / 700 out (3100 total)
  costo aprox: $0.00590 USD
─────────────────────────────────────────────────
```

The loop closes the democratization argument: **the AI produces bounded, verifiable
JSON (tractable + cheap), the engine guarantees the hard part (correctness, RBAC, SQL),
and the loop converges without a human.** The end-to-end proof — generate → validate →
correct → provision a real tenant → CRUD works — for four app archetypes (óptica CRM,
task board, e-commerce, social) is `scripts/aigen-e2e.sh` (golden schemas in
`examples/aigen/`); with `ANTHROPIC_API_KEY` set it runs the live loop and records the
economics, without it it proves the provision→CRUD half against the committed schemas.

### Constrained decoding (AI-F1-S1) — structure at decode time, loop for semantics

Research (peer-reviewed) refined the loop: **constrained decoding** guarantees
structural validity *by construction*, so the loop should not spend rounds fixing
structure. The decomposition is `p = p_struct × p_sem`; fixing `p_struct = 1` at the
decoder removes the structural-error class entirely, leaving the loop for the
semantic, cross-reference class — which the trusted external validator makes
correctable (Huang et al., ICLR 2024: self-correction without an external oracle
does not work). Two mechanisms, two layers — mirroring this document's
`metaschema`/`semantic` split:

- **Structure → Anthropic structured outputs** (`output_config.format`, strict
  grammar). `aigen.OutputSchema()` is the JSON Schema the decoder is constrained to.
- **Semantics → the correction loop**, driven by `schema.ValidateReport` (the engine's
  own validator, kept as the final full check too — defense in depth).

**The honest guarantee boundary.** The Appitools schema is an *arbitrary-keyed map*
(resources keyed by resource name, fields by field name, roles by role name). The
strict-outputs subset is narrow — every object must be `additionalProperties:false`,
and it offers no `patternProperties` / `propertyNames` / `additionalProperties`-as-
schema — so that map shape **cannot** be expressed in it. Structured outputs therefore
constrains the **envelope**, not the deep structure:

| Guaranteed by the decoder (envelope) | Still validated + corrected by the loop |
|---|---|
| well-formed JSON (no fences / prose / truncation) | field `type` in the closed set |
| `$schema` = the v1 const, `version` = `"1"` | strict field/resource keys (unknown-key) |
| `name` present (string); `resources` present | enums, `min`/`max`, `pattern`, `format` |
| no **unknown top-level key** | relations/FKs to existing targets, RBAC fields, state machines, defaults — all the cross-reference semantics |

So constrained decoding removes the *JSON-wellformedness + envelope* class of
structural errors; the deep-structure + semantic classes remain the validator's job.
The model keeps emitting the **canonical map form**, so the validator's error paths
(`resources.X.fields.Y…`) stay coherent for in-context correction. Pushing
`p_struct → 1` on the deep structure (an array-IR the subset *can* fully constrain,
with a transform back to the map form) is the documented next increment.

**Defense in depth / graceful fallback.** A structured request that the live subset
rejects, or that returns an **empty `resources`** map (which the engine would otherwise
accept — a silent-worsening trap), drops to plain generation automatically; a
**safety refusal** (`stop_reason: "refusal"`) is surfaced as a refusal, not mis-handled
as a schema error. The default iteration budget dropped **5 → 3** (no structural rounds
to absorb). The **system prompt is sent as a prompt-cache block** (`cache_control`), so
correction rounds re-read it at ≈0.1× — the cost line reports the cache split (caveat:
the prompt must exceed the model's cache minimum, e.g. 4096 tokens on Haiku, to actually
cache). The **model is a pure parameter** (`--model`); the `ModelClient` seam is also
where a future cheap→expensive **cascade** wrapper plugs in, unknown to the loop.

`--no-structured` forces plain generation for comparison. Verified end-to-end against a
mock (`ANTHROPIC_BASE_URL`): the request carries `output_config.format` with the
const-pinned envelope and the cached system block; round 1 reports **0 structural / 1
semantic** errors (the structural class is gone) and converges on round 2.

## Notes

- `appitools validate` (no `--json`) stays human-readable and is the semantic
  authority; `--json` adds the structural layer and the machine format. The engine's
  validation **rules** are identical to either — only the presentation differs.
- The Go API mirrors the CLI: `schema.ValidateReport(raw []byte) ValidationReport`
  (unified), `schema.ValidateAgainstMetaSchema(raw) []ValidationError` (structural),
  `schema.Validate(*APISchema) []ValidationError` (semantic).
- The complete grammar each rule enforces is in
  [SCHEMA_REFERENCE.md](SCHEMA_REFERENCE.md).
