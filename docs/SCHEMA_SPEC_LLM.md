# Generate Appximo schemas with YOUR OWN agent

Appximo has two ways to turn a natural-language app description into a valid
schema. Both run the **same validator-guided loop** against the **same grammar
source** — they differ only in *whose* model does the generating:

| Front | Who generates | Cost | For whom |
|---|---|---|---|
| **Built-in**: `appximo ai-generate "<description>"` | the engine calls Anthropic (default `claude-haiku-4-5`) | ~$0.006 per schema (measured: ~90% first-try, 100% convergence) | users without their own AI tooling |
| **External** (this doc): `appximo spec` + your agent | **your** Claude Code / Cursor / Opus — the subscription you already pay | zero product API cost | anyone with an agent |

The grammar both fronts use is one shared source (`pkg/aigen` `GrammarCore`,
pinned by test) — your agent generates against exactly the rules the internal
loop uses, and the same oracle judges both.

## The flow (4 steps)

### 1. Get the grammar

```bash
appximo spec > appximo-spec.md
```

Prints the distilled schema grammar for an LLM: the closed type set, strict-key
rule, naming, relations (all 3 kinds), the `file` field, `jsonb` + gin indexes,
state machines, hooks, events, both RBAC forms (plus `routes` grants for custom
endpoints), full FK coverage — plus **two worked examples that are
validated against the engine in CI** (the spec can never teach a shape the
validator rejects) and the correction-loop instructions.

### 2. Give it to your agent, with your app description

Paste `appximo-spec.md` into the agent's context (or drop it in the repo the
agent works in). Then ask, for example:

> Using the Appximo grammar above, generate a schema for an optical-store
> app: patients, appointments (with a lifecycle: scheduled → attended /
> cancelled), prescriptions that carry an uploaded file, products and
> suppliers, and roles for admin, optometrist (may only update their own
> appointments) and salesperson (read-only on products). Output only the JSON,
> saved to optica.json.

A Claude Code session with this repo needs even less: `AGENTS.md` already
teaches it the integration surface, and it can run the oracle itself.

### 3. The correction loop — validate until green

```bash
appximo validate --json optica.json
```

Output: `{ "valid": true|false, "errors": [ { "path", "rule", "message",
"expected", "got", "fix" } ] }` — one machine-readable entry per problem, each
with the dot-path to the offending spot and a concrete fix. Exit code 1 while
invalid, 0 when valid.

Your agent edits the schema at each `path` following `fix`, re-runs the
command, and repeats until `"valid": true`. This is **the exact loop the
internal `ai-generate` runs** (generate → `schema.ValidateReport` → correct);
an agent that can run shell commands closes it autonomously in one or two
passes.

Example of what the oracle reports (a `"type": "number"` typo):

```json
{
  "path": "resources.productos.fields.precio.type",
  "rule": "invalid_enum_value",
  "expected": ["string", "text", "int", "int64", "float64", "bool", "uuid", "time", "json", "jsonb", "file"],
  "fix": "use one of the allowed values listed in expected"
}
```

### 4. Bring it in: paste → Apply → deploy

Open Appximo Studio (`/editor`) → **Code** view → paste the schema. The same
validator runs live there (three layers: syntax, meta-schema autocompletion/
hover, and the engine's semantic report with every error on its line), and
**Apply is gated** — an invalid document never reaches the canvas. From there
the normal Studio flow deploys it: dry-run migration preview, destructive-
approval gate, one-click engine restart when a new resource needs it.

No Studio? `appximo serve --schema optica.json` boots the API directly.

## Role of each document

- **`appximo spec`** — the distilled grammar for an LLM (this flow). Compact,
  closed sets, worked examples, the loop.
- **[docs/SCHEMA_REFERENCE.md](SCHEMA_REFERENCE.md)** — the complete
  human-grade reference (every key, every behavior, every error). Point your
  agent at it when a corner case needs depth; too long as a system prompt.
- **[AGENTS.md](../AGENTS.md)** — for an agent working *inside this repo* or
  integrating a user's project end-to-end (running the engine, calling the API).
