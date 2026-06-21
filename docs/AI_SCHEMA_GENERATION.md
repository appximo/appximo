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
with a transform back to the map form) is the array-IR below.

### Array-IR — deep structure constrained by construction (AI-F2-S2)

The envelope's limit is that the strict subset cannot express an **arbitrary-keyed
map**. The fix is to change the *representation*: rewrite every arbitrary-keyed map
as an **array of objects with an explicit name key** (`pkg/aigen/ir.go`):

```
MAP  (engine consumes): "resources": { "empleados": { "fields": { "email": {"type":"string"} } } }
IR   (model generates):  "resources": [ { "name":"empleados", "fields":[ {"name":"email","type":"string"} ] } ]
```

An array of objects with a **fixed item schema** IS inside the strict subset, so
`IROutputSchema()` constrains the structure **in depth**: every item is
`additionalProperties:false` with all keys in `required` (optionals emulated as
nullable), `type` is the 9-value enum, `on_delete`/`format`/relation-kind/… are
enums — so a wrong type or an unknown deep key is **impossible by decode time**, not
merely correctable. Every arbitrary-keyed map becomes an array: `resources`,
`fields`, `relations`, `hooks`, `rbac.roles`, a role's `permissions`, and a state
machine's `transitions` (keyed `from`→`to`). `TestIROutputSchemaIsStrictSubset`
walks the whole IR schema and asserts it uses **no** disallowed keyword and that
every object is closed with a complete `required` — the property the map-form
meta-schema could never satisfy.

Three pieces make it a working mode:

- **`MapToIR` / `IRToMap`** — total, deterministic transforms (arrays sorted by the
  explicit key). The round-trip is **identity**: `map→IR→map == map` (and
  `IR→map→IR == IR`) over **every** corpus gold and repo example schema
  (`TestIRRoundTripIdentity`, property-based) — the IR loses and distorts nothing.
- **Error-path translation** — the validator runs on the map form and emits map
  paths (`resources.empleados.fields.email.type`); `TranslateMapPathToIR` rewrites
  them to the IR the model produced (`resources[0].fields[1].type`) by resolving
  each named segment to its array index, so the loop's correction round speaks the
  model's own space.
- **The loop** (`Options.ArrayIR`, `appitools ai-generate --array-ir`) — generate IR
  under `IROutputSchema` → `IRToMap` → validate (the engine's validator, unchanged)
  → translate remaining errors to IR paths → correct. The same graceful fallback as
  the envelope (a rejected structured request or an empty result drops to plain).

The deep structural error class can no longer occur in IR generation; only the
semantic, cross-reference class remains for the validator + loop. **Measured** as a
harness arm below — confirmed in *simulation* (0 deep structural errors at attempt 1).

> ⚠ **Live reality (AI-F2-S3, measured).** Against the **real** Anthropic API the IR
> schema is **rejected** — the strict-outputs subset has a hard limit of **16
> union-typed (nullable) parameters**, and the Appitools field grammar has ~17
> optional keys, so `IROutputSchema` exceeds it ("too many parameters with union
> types"). The envelope (`OutputSchema`) is *also* rejected, for a different reason:
> the subset requires `additionalProperties:false` on **every** object, which cannot
> express the envelope's deliberately-open `resources`/`rbac`. So **neither
> constrained-decoding mode engages on the real API** — both fall back to plain
> generation. See the live measurement section. (Two genuine schema bugs were fixed
> en route — a double-applied null from a shared sub-schema, and a nullable enum
> combined with a type-array — but the 16-union ceiling is structural, not a bug.)

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

### The measurement instrument (AI-F2-S1) — `appitools ai-eval` + `pkg/aigen/eval`

Before any *new* generation technique, the research demands the **instrument** to
judge it: "without this, no later decision is defensible." The loop's real
convergence rate on **this** domain (typed schemas with FKs/RBAC/state machines) is
published nowhere — every figure in the literature is extrapolated from
text-to-SQL/code-gen. `pkg/aigen/eval` measures it.

Three parts:

1. **A stratified gold test set** (`pkg/aigen/eval/corpus/`, embedded): NL
   description + a hand-written, validate-clean **gold schema**, across three
   complexity tiers — `simple` (2-3 resources), `media` (relations + basic RBAC),
   `compleja` (FKs, both RBAC forms, state machines). A test asserts **every gold
   validates**. It is a **seed** (24 cases now); the research wants **~120-160 per
   stratum** for full power, and the harness grows to that without changing. A
   too-small corpus yields wide intervals and inconclusive tests — that is correct,
   and the instrument says so.

2. **A generic paired ablation harness.** Each case runs under every **condition**
   (`plain` vs the AI-F1-S1 `structured` envelope vs the AI-F2-S2 `array-IR` deep
   decoding) → a **paired** binary outcome (first-try semantic success), empirical
   iterations-to-valid, and the structural/semantic error split. A future technique
   (constraint-aware, RAG) plugs in as a **new condition** — the harness, outcomes,
   and statistics are unchanged. Deterministic: a built-in **simulated** model makes
   the whole run reproducible with no API key (`ai-eval`); `--live` measures a real
   model. The simulator's structural-fault model is **depth-faithful** so the three
   arms genuinely differ: `plain` can emit a shallow ENVELOPE fault AND a DEEP one,
   `structured` removes only the envelope (deep still leaks), `array-IR` removes both
   (only the semantic class remains) — exactly the AI-F2-S2 hypothesis, made
   measurable.

3. **Statistics with paper-grade rigor** (`stats.go`, pure Go):
   - `p_sem` per condition/stratum with a **Wilson** score interval (not Wald —
     Wald under-covers near 0/1).
   - **E[iterations] measured empirically** (mean + median). The geometric `1/p_sem`
     is reported **only** as a labeled "independent retries" bound — the
     validator-guided loop is *not* i.i.d. (each attempt conditions on the previous
     feedback), so the memoryless formula does not apply.
   - **McNemar's** paired test (Dietterich 1998 — the test with acceptable Type I
     error for run-once algorithms; never a paired t-test or a proportion
     difference): **exact binomial** when discordants `b+c < 25`, Edwards
     continuity-corrected χ² otherwise. For >2 conditions, **Cochran's Q** omnibus +
     pairwise McNemar with **Holm-Bonferroni** FWER control.
   - **Honest about power:** an underpowered comparison (`< 25` discordants) is
     flagged INCONCLUSIVE, never sold as significant.

```
appitools ai-eval            # simulated, deterministic — runs all 3 arms
appitools ai-eval --json     # machine-readable analysis
appitools ai-eval --live --model claude-haiku-4-5   # measure a real model (temp 0)
```

The simulated 3-arm run (n=24 seed) shows the expected ordering: structural errors
at attempt 1 are **plain 0.38 → structured 0.12 → array-IR 0.00** (deep p_struct→1)
and first-try `p_sem` rises **plain 0.54 → structured 0.71 → array-IR 0.75**. The
McNemar comparisons are flagged **UNDERPOWERED / INCONCLUSIVE** (7–10 discordants ≪
25) and Cochran's Q is not significant — exactly right at this corpus size, and the
instrument says so loudly. **The conclusion (array-IR vs envelope) needs the corpus
scaled to ~120-160/stratum**; the arm is now wired into the gate, ready to conclude.

This is the gate every future technique passes: measured against the baseline with
McNemar on **this** domain, or discarded.

### The first real measurement (AI-F2-S3) — corpus scaled + `--live` verdict

The corpus was scaled **24 → 120** (40/stratum, a 5× lift; every gold validates and
passes the `map→IR→map` identity round-trip), the client got **retry-with-backoff**
(honoring `Retry-After`) + **temperature 0** for reproducibility, and `ai-eval`
gained `--sample N` (stratified subsample to bound a cost-/rate-limited live run):

```
appitools ai-eval --live --model claude-haiku-4-5            # full 120-case 3-arm run
appitools ai-eval --live --model claude-haiku-4-5 --sample 10  # 10/stratum bounded run
```

**Measured live (Haiku, temp 0, 30-case stratified subsample, 90 generations,
total API cost ≈ $0.52):**

| stratum | first-try valid | convergence | E[iterations] | cost/schema |
|---|---|---|---|---|
| simple | 100% (10/10) | 10/10 | 1.00 | ~$0.003 |
| media | 80% (8/10) | 10/10 | 1.20 | ~$0.007 |
| compleja | 90% (9/10) | 10/10 | 1.10 | ~$0.008 |
| **all** | **90% (27/30)** | **30/30** | **1.10** | **~$0.006** |

**Two findings, both honest and load-bearing:**

1. **The democratization thesis holds — strongly.** The *cheap* model (Haiku) reaches
   **90% first-try** valid schemas and **100% convergence** within the 3-iteration
   budget, at **~$0.006/schema** and **~1.1 iterations**, across simple → complex
   (FKs, both RBAC forms, state machines). "El barato alcanza" is confirmed on the
   real domain, not extrapolated.

2. **The constrained-decoding foundation does NOT engage on the real API — a
   data-over-expectation result.** Both structured-decoding arms **fell back to plain
   in 0/30 cases** (the instrument now prints this `⚠ … engaged in only 0/30 …`): the
   envelope can't express open `resources`/`rbac` (every object needs
   `additionalProperties:false`), and the array-IR exceeds the subset's **16-union
   limit**. So `plain`, `structured`, and `array-IR` were the *same* generation; their
   tiny p_sem differences (0.900 / 0.933 / 0.900) are temp-0 API nondeterminism, not
   treatment effects, and every McNemar is INCONCLUSIVE (0–1 discordants). **What
   actually delivers the 90% is the validator-guided plain loop** (the AI-F0
   contract), not the AI-F1/AI-F2 decoder constraints.

**Verdict on the structural foundation:** *not validated on real data as designed.*
The structured-outputs path is dead weight on the current API for this grammar (it
silently falls back). The honest options, to decide with data: **(a)** accept that the
plain validator-guided loop is already enough on this domain (90%/100% at ~$0.006) and
treat constrained decoding as not worth its complexity here; or **(b)** redesign the
IR to fit the real subset (≤16 unions, no open objects) — e.g. a reduced *core* IR for
generation-time keys only, or staged generation — and re-measure. The instrument and
the 120-case corpus are ready to score whichever path is tried next; the full
120-case `--live` run (rate-limited on a low-OTPM tier; the subsample is the bounded
proxy) is the standing next data point.

## Notes

- `appitools validate` (no `--json`) stays human-readable and is the semantic
  authority; `--json` adds the structural layer and the machine format. The engine's
  validation **rules** are identical to either — only the presentation differs.
- The Go API mirrors the CLI: `schema.ValidateReport(raw []byte) ValidationReport`
  (unified), `schema.ValidateAgainstMetaSchema(raw) []ValidationError` (structural),
  `schema.Validate(*APISchema) []ValidationError` (semantic).
- The complete grammar each rule enforces is in
  [SCHEMA_REFERENCE.md](SCHEMA_REFERENCE.md).
