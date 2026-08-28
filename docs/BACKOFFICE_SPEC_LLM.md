# Appximo — the BACK-OFFICE contract (a CRUD admin UI generated from /openapi.json)

This is the fourth build-side document (`appximo backoffice-spec`), the
companion of `spec` (the schema), `backend-spec` (custom Go) and
`frontend-spec` (the API contract a UI consumes). It teaches ONE pattern,
distilled from a real, verified implementation (a 7-resource back-office in
~500 total lines, built during the first third-party field evaluation and
adopted here): **a complete admin CRUD UI — tables, forms, validation,
permissions, sort, search, pagination — derived at runtime from
`/openapi.json`, with zero resource-specific screens.** If the schema changes
and redeploys, the UI adapts by itself.

Why this pattern earns its own document:

- It is the same move as API Platform's Admin (React-Admin reading the
  contract), but cheaper and more complete here, because the contract Appximo
  publishes is unusually rich and its errors are written to be displayed.
- It is the **sovereign back-office**: it lives inside YOUR binary
  (`Config.Static`), with your theme, governed by your schema's RBAC — while
  the stock `/admin` is the platform operator's panel, this is your APP's.
- Since the `x-appximo-*` contract extensions shipped, the generated UI needs
  **no hardcoded domain knowledge at all** — the exception maps the first
  implementation carried (FK target columns, state machines, "which uuid is a
  file") are dead. Everything below reads from the contract.

Runnable proof: `examples/backoffice-guide/` — a no-build vanilla SPA
implementing every section of this document against a real engine.

**You may not need to build it at all:** the engine SERVES this exact pattern
at **`/app`** on every binary (the embedded generic back-office — sign in as a
tenant user and every schema resource gets its screens). Build your own — this
document — when you want it inside YOUR SPA, with your theme, your overrides
and your custom actions; `/app` is the zero-effort baseline it grows from.

---

## 1. The idea in one line

```
schema.json ──(engine)──▶ /openapi.json ──(runtime fetch)──▶ tables + forms + menus
```

The UI knows nothing about "members" or "bookings". It knows how to read a
contract. Write the reader once; every resource — including ones added after
you shipped — gets its screens for free.

## 2. What the contract publishes (all of it — the blind spots are closed)

Fetch `/openapi.json` (unauthenticated). Everything a generic UI needs:

| From the contract | Becomes |
|---|---|
| `components.schemas.<Pascal>` → `properties` with `type`, `format`, `enum`, `maxLength`, `minimum`, `maximum`, `readOnly` | the right control per field, with native browser limits |
| `required` on `<Pascal>Input` | asterisks + create-form validation |
| **`default` on a property** | a `required` field WITH a `default` is satisfiable by OMISSION (the engine fills it on create) — do NOT add the native `required` attribute to it; a required field WITHOUT one gets `required`, so the browser blocks the fully-empty submit with a pointed message instead of the server's generic `400 empty body` |
| published methods per path (`get/post` on the collection, `get/patch/put/delete` on the item) | which buttons exist per resource |
| `x-appximo-relation` on a property | this field is a FK and WHICH resource it points to |
| **`x-appximo-references`** on a property | which COLUMN of the target the FK stores — `"id"` normally, `"user_id"` in the `$user_id` RBAC pattern. **A relation selector must send `row[references]`, never blindly `row.id`** — sending id where user_id is expected violates the FK, and the framework's own recommended pattern creates exactly those FKs |
| **`x-appximo-file: true`** (+ `x-appximo-accept`, `x-appximo-max-bytes`) | this uuid is an uploaded-file reference, not a FK: render the upload→attach widget (frontend-spec §7) with the declared policy shown to the user before they pick a file |
| **`x-appximo-initial` / `x-appximo-transitions`** on a status property | the state machine: create-forms offer only initial states; edit-forms offer `[current, ...transitions[current]]`; a terminal state (empty list) renders read-only |
| **`x-appximo-import`** on a resource's component schema | the resource accepts its listed governed fields (`id` / auto timestamps) on CREATE from import-granted roles — a data-import capability, NOT a form concern: keep those fields read-only in generated forms (`readOnly: true` stays the truth for every caller outside the grant; the granted role list is deliberately not in the contract). Surface it, if at all, as a specialized "import" affordance |
| **`x-appximo-virtual-resources`** at the document root | engine-provided resources that are not in the schema (the `files` store, with its RBAC action vocabulary) — list them separately, never as CRUD resources |
| **`x-appximo-transaction: true`** on `POST /api/transaction` | the atomic batch door (up to 100 create/update/delete ops, all-or-nothing) — an ACTION, not a resource: never list it in the navigation, never `GET` it (405). The general rule that covers it: a collection path with no `get` operation is not browsable. Use it for bulk actions (§10b) |
| a property with **no `type`** (described as "an arbitrary JSON document", tagged `x-appximo-json: "jsonb"` or `"text"`) | a `jsonb` OR a `json` column (ADR-028: both are JSON VALUES on every door — a `json` field used to be published as a plain `string` and came back escaped): render a JSON editor (a validated textarea is enough), never a text input; send the parsed value, receive it natively |
| `x-appximo-custom-route: true` on an operation | a hand-written Go route: exclude from the generated CRUD, surface as an action/link if you want it |
| the RBAC, by answering 403 | which resources this ROLE sees — probed, never configured (§5) |

## 3. The contract reader (~150 lines, zero dependencies)

The reference shape (`examples/backoffice-guide/web/contract.js` is the
complete version):

```js
export async function loadContract(fetchJSON) {
  const doc = await fetchJSON('/openapi.json');
  const paths = doc.paths ?? {};
  const virtual = new Set(Object.keys(doc['x-appximo-virtual-resources'] ?? {}));

  // Resources = collection paths that are generated CRUD (not custom routes,
  // not virtual-store operations).
  const names = Object.keys(paths)
    .filter((p) => /^\/api\/[a-z_]+$/.test(p))
    .map((p) => p.slice(5))
    .filter((n) => !virtual.has(n))
    .filter((n) => !Object.values(paths['/api/' + n] ?? {})
      .some((op) => op['x-appximo-custom-route']))
    .sort();

  const resources = names.map((name) => {
    const Pascal = name.replace(/(^|_)([a-z])/g, (_, __, c) => c.toUpperCase());
    const read = doc.components?.schemas?.[Pascal];
    const input = doc.components?.schemas?.[Pascal + 'Input'];
    const colMethods = Object.keys(paths['/api/' + name] ?? {});
    const itemMethods = Object.keys(paths[`/api/${name}/{id}`] ?? {});
    const required = new Set(input?.required ?? read?.required ?? []);

    const fields = Object.entries(read?.properties ?? {}).map(([key, p]) => ({
      key,
      type: p.type, format: p.format ?? null, enum: p.enum ?? null,
      maxLength: p.maxLength ?? null, minimum: p.minimum ?? null, maximum: p.maximum ?? null,
      readOnly: !!p.readOnly || key === 'id',
      required: required.has(key),
      relation: p['x-appximo-relation'] ?? null,
      references: p['x-appximo-references'] ?? 'id',   // the FE5 fix, from the contract
      file: p['x-appximo-file'] === true,
      accept: p['x-appximo-accept'] ?? null,
      maxBytes: p['x-appximo-max-bytes'] ?? null,
      transitions: p['x-appximo-transitions'] ?? null, // state machine, from the contract
      initialStates: p['x-appximo-initial'] ?? null,
      auto: p['x-appximo-auto'] !== undefined,     // engine-managed, from the contract — never guessed from field names
    }));

    return {
      name, fields,
      title: name[0].toUpperCase() + name.slice(1).replace(/_/g, ' '),
      canCreate: colMethods.includes('post'),
      canEdit: itemMethods.includes('patch'),
      canDelete: itemMethods.includes('delete'),
    };
  });

  return { resources, byName: Object.fromEntries(resources.map((r) => [r.name, r])) };
}
```

Note what is ABSENT: no resource-name list, no FK exception map, no mirrored
state machine, no "these uuids are files" list. If you find yourself writing
one of those, you are reimplementing something the contract already says.

## 4. Field → control mapping

```js
export function controlFor(f) {
  if (f.transitions) return 'state';        // constrained select, §6 rule 5
  if (f.enum) return 'select';
  if (f.file) return 'file';                // upload→attach widget, §8
  if (f.relation) return 'relation';        // select over the target resource, §7
  if (f.type === 'boolean') return 'checkbox';
  if (f.format === 'date-time') return 'datetime-local';
  if (f.format === 'email') return 'email';
  if (f.type === 'integer' || f.type === 'number') return 'number';
  if (f.maxLength && f.maxLength > 200) return 'textarea';
  return 'text';
}
```

Wire `maxlength`, `min`, `max` from the contract onto the inputs — the
browser's native validation and the server's then tell the same story.

## 5. Permissions by probing: the 403 answers for you

Do NOT encode a role matrix. Ask, once per resource, on opening the index:

```js
try {
  const r = await api(`/api/${res.name}?per_page=1&count=true`);
  state[res.name] = { total: r.meta?.total ?? 0 };
} catch (e) {
  state[res.name] = e.status === 403 ? { denied: true } : { error: true };
}
```

Deny-by-default does the rest: a receptionist role sees its 4 of 7 resources,
an admin all 7, with zero configuration. Render denied resources dimmed, not
hidden — the user understands they exist and the role doesn't reach them.
(Cost: one request per resource on the index — irrelevant for a back-office;
consider probing lazily past ~50 resources.)

## 6. The generic form — the five rules that matter

One component serves every resource. The non-obvious rules, each learned
against the live engine:

1. **On CREATE, OMIT empty fields.** `required` means "present and non-null":
   sending `""` PASSES it and creates a blank record; omitting the key is
   what triggers the correct 422. This is the single most common generated-
   form bug. Pair it with the NATIVE layer: give the `required` attribute to
   contract-required fields **without a `default`** (see §2) — the browser
   then blocks the fully-empty submit itself (an all-omitted create would
   otherwise be the engine's generic `400 empty body`), while a partial
   submit still exercises the engine's painted 422.
2. **On EDIT, PATCH partial** — only what changed; the server validates only
   what you send. Numbers go as JSON numbers: update rejects `"7"` even
   though create tolerates it (documented create-path leniency).
3. **Explicit `null` clears** a nullable field (remove a FK, detach a photo).
4. **Paint the whole 422 in one pass.** The body carries EVERY failing field
   (`{field, rule, message}`): mark them all on their inputs, scroll to the
   first. A 409 (duplicate, referenced delete) NEVER discards the user's
   work — banner + form intact; the message already names the column/table.
5. **State fields offer only legal moves.** From the contract:
   create → `initialStates`; edit → `[current, ...transitions[current]]`;
   `transitions[current].length === 0` → render the value read-only (terminal).
   Still handle the 422 `invalid transition` — two operators can race, and the
   loser reloads the row.

Submit skeleton:

```js
const body = {};
for (const f of fields) {
  const v = clean(values[f.key], f);          // number coercion, ISO dates, '' → null
  if (!editing && (v === null || v === '')) continue;   // rule 1
  body[f.key] = v;
}
try {
  const r = editing
    ? await api(`/api/${res.name}/${row.id}`, { method: 'PATCH', body })
    : await api(`/api/${res.name}`,           { method: 'POST',  body });
  onSaved(r);
} catch (e) {
  if (e.status === 422 && e.fields?.length) paintFields(e);        // rule 4
  else if (e.status === 409) banner(e.message);                    // work intact
  else screenStateFor(e);                                          // frontend-spec §5
}
```

## 7. Relation selectors — send the referenced COLUMN

The one place generic tools used to break. The recipe:

```js
const target = await api(`/api/${f.relation}?per_page=100`);
options = target.data.map((row) => ({
  value: row[f.references],                    // ← x-appximo-references, NOT row.id blindly
  label: row.nombre ?? row.name ?? row.title ?? row.email ?? row[f.references],
}));
```

With `references === "user_id"` (the `$user_id` RBAC pattern) the selector
sends the target's `user_id` — the value the FK actually stores. Past ~100
target rows, switch the select to a search input backed by `?search=`.

## 8. Lists, files, and the rest (delegating to frontend-spec)

- **Columns:** first N non-object fields, preferring
  `name/title/code/status/email`-ish keys; overrides (§9) fix the rest.
- **Sort:** ONE field (`?sort=x&order=asc`) — `sort=a,b` is a named 400, and
  that 400 is a bug in YOUR UI, not user error. **Search:** `?search=` sweeps
  the text fields. **Pagination:** keyset (`?after=` + `meta.has_next`).
- **Delete:** two-click confirm; show the 409 verbatim — `still referenced by
  "clases" record(s)` is already end-user language.
- **Files:** `f.file` fields render the upload→attach→display flow exactly as
  frontend-spec §7 specifies (multipart upload → take `file_id` → set it as
  the field value → display via short-lived signed URL — an `<img>` cannot
  send an Authorization header). Show `f.accept`/`f.maxBytes` next to the
  picker; the server enforces them at attach time with a 422 `file_policy`.
- Every screen keeps the six mandatory screen states and the error→state
  mapping of frontend-spec §5/§6 — this document adds the generation layer,
  not a different error contract.

## 9. Growing past the generated 95% — the overrides registry

Personalizing a resource must NOT eject it from the generated system. One
registry, consulted at every decision point:

```js
export const OVERRIDES = {
  miembros: {
    columns: ['codigo_socio', 'nombre', 'documento', 'activo'],
    labels:  { codigo_socio: 'Carnet' },
    cells:   { foto: (row) => SignedAvatar(row) },     // custom cell renderer
    widgets: { foto: PhotoEditor },                    // custom form widget
    actions: [{ label: 'Export CSV', run: exportMembers }],
  },
};
```

The generic screens read `OVERRIDES[res.name] ?? {}`; an overridden resource
still inherits forms, error painting and permission probing. The visual theme
is your SPA's own tokens — nothing here dictates a look.

## 10. Honest limits

- `json`/`jsonb` fields: the embedded `/app` ships a real JSON editor
  (§10b); in YOUR SPA a validated textarea is the floor, a structured editor
  is resource-specific work (an override widget).
- Bulk actions and CSV export: the embedded `/app` has them (§10b, through
  `/api/transaction` in batches of 100 and a client-side CSV); in your SPA
  they are natural §9 extensions — copy the batching and the partial-failure
  reporting, not just the buttons.
- The permission probe costs one request per resource at index open.
- A brand-NEW resource appears in `/openapi.json` only after the engine
  restarts with a schema that includes it (the deploy flow tells you when) —
  the UI adapts on next load, but not before the contract does.
- The back-office inherits your app's session (frontend-spec §2: login →
  Bearer token). Building it as part of your SPA means the RBAC role of the
  logged-in user shapes it automatically — that is the point.

## 10b. The embedded /app: the design system, theme, language, demo mode

Everything above is the PATTERN (build the back-office inside your own SPA).
The engine also SHIPS the pattern as the embedded `/app` panel. Since
APP-VITRINA-S1 that panel runs on a real design system — the one a third
party proved on this same engine (ink sidebar, ONE accent, white/zinc
surfaces, Inter bundled, `tracking-tight` titles, `tabular-nums` figures,
soft shadows, ≤ 300 ms motion) — while staying 100% generic: nothing in the
bundle names a resource, a state or a domain. What the panel derives, beyond
the sections above, all of it from the contract:

- **Home**: one stat tile per resource (the §5 probe's `meta.total`), denied
  resources dimmed with a lock, a dark hero with the app title, the resource
  and record counts and the role.
- **List**: the first five columns by a naming convention (name/title/code/
  number/email-ish keys first, then lifecycle, enums, relations, numbers,
  dates, switches; long free text last), **relation columns RESOLVED to the
  target row's label** (one `?per_page=100` fetch per target, cached for the
  session — the §7 recipe, read side), money rendered as money when the key
  ends in `_centavos`/`_cents` (the int64-minor-unit convention), dates in
  the UI locale, booleans as yes/no chips, jsonb as `{n}`.
- **Lifecycle chips coloured BY POSITION**: the states of a state machine are
  ordered structurally (initial states first, then breadth-first along
  `x-appximo-transitions`) and the n-th state takes the n-th colour of an
  8-colour palette (`--app-s1…s8`); a terminal state shows a hollow dot. No
  colour is ever tied to a state NAME. Plain enums are neutral outline chips.
- **Filters**: one compact select per enum / state / boolean field (max 4) →
  `?filter[field][eq]=`; search → `?search=`; sort by header → `?sort=` —
  and because a cursor and a sort are mutually exclusive on the engine, a
  SORTED list pages by `?page=` while the unsorted list stays keyset
  (`?after=`); `count=true` is sent only when no cursor is in play.
- **Board**: a resource with a state machine gets a List ⇄ Board toggle. The
  board's columns are the ordered states; a card drag is a transition —
  columns the current state cannot reach are dimmed and refuse the drop,
  a legal drop is a `PATCH {status: to}` (rule 5 on the read side; the 422
  of a lost race reloads). On phones the cards expose their legal moves as
  tap chips. It loads `?per_page=100` in one request and says so when more
  exist.
- **Form**: a right-side drawer (a bottom sheet on phones) with the fields
  ordered structurally (title field, other required fields, text, relations,
  enums, numbers, dates, switches, the lifecycle, files, JSON); the five
  rules of §6 unchanged; the state field as chip-radios (create → initial
  states; edit → current + legal moves; terminal → locked); a jsonb column —
  published as a TYPE-LESS property, "an arbitrary JSON document" — as a
  monospace JSON textarea validated before submit; the file widget with the
  policy, the upload state, and an image preview through the signed URL;
  delete behind a two-step confirm; toasts for created / saved / deleted /
  moved; a 409 or a non-field error stays in a banner with the work intact.
- **Designed states**: shimmer skeletons while loading (the only looping
  animation, gone with the data), an empty state with the create action, an
  inline error with retry, 422s painted per field with a scroll to the first.

APP-PODER-S1 added what the contract already allowed — still nothing per
resource, all of it derived:

- **Honest pagination**: every list pages by number (`?page=` + `per_page`),
  never by cursor — a cursor gives no page number, no "of N" and no "go to".
  The footer says «Página 3 de 47 · 15 de 703»: the total is the engine's
  exact `COUNT(*)` over the same filtered, RBAC-scoped set (`?count=true`),
  requested once per query and kept across pages. Page size 15 by default
  (unchanged), selector 15 / 25 / 50 / 100 / 250 remembered per resource in
  the browser; first / previous / go-to / next / last. **250 is the ceiling on
  purpose**: painting half a million rows measures the browser, not the
  engine; for volume the tool is a load test against the API.
- **The query time, visible**: the engine publishes its stage durations on
  every generated read as a standard `Server-Timing` header
  (`query;dur=17.86, app;dur=31.00`, or `cache;desc="hit"` when the response
  cache answered and no query ran). The footer shows «consulta 7,8 ms ·
  respuesta 66 ms» — the engine's number and the round trip, independent of
  how many rows are painted. On the 36k-row ERP fixture: page 1 with the
  count 18 ms, page 2000 (OFFSET) 343 ms — the cost is on screen, not hidden.
- **Detail with relations both ways**: a row click opens a detail (the pencil
  still opens the form): the fields, the state machine as an ordered strip
  with the current state and its legal moves as buttons, the files with a
  signed download/preview, every PARENT resolved through the published
  subroute (`/api/{res}/{id}/{segment}`, the target's RBAC enforced there)
  or a lookup on `x-appximo-references`, and every CHILD: each resource whose
  FK (`x-appximo-relation`) points here, as a filtered list with its count and
  «Ver todos» → the list pre-filtered (a pinned chip). The schema's
  `relations`/`?include=` block is NOT in the contract, so it is not used —
  which also means a legacy non-JSON text in a `json` column (pre-ADR-028)
  cannot break the read: it shows as text with a badge. Each block fetches
  on its own and degrades to an inline notice. The detail has its own URL
  (`#/{res}/{id}`).
- **JSON editor** for `x-appximo-json: text|jsonb`: a highlighted editor
  (keys, strings, numbers, literals), validation as you type with the parse
  error named, Formatear / Compactar, a foldable tree view, Tab indents; an
  invalid document never leaves the browser (painted on the field), a 422 from
  the engine paints the same field. Two limits SAID in the interface: the
  engine's 1 MiB per request (≥ 900 KB warns, ≥ 1 MiB blocks with the message
  — a bigger document would be a 413) and ENG-50 (an integer of 16+ digits or
  a decimal with trailing zeros passes through float64 both ways — detected
  on the raw text before the parse loses it, named with the offending
  numbers). What is saved re-opens natively.
- **Columns, saved views, shareable URL**: a column picker over every
  listable field (the structural order is the default), views saved per
  resource in `localStorage` (columns, filters, sort, search, page size) —
  zero engine state — and the CURRENT view in the URL hash
  (`#/res?cols=…&f.estado=…&sort=…&per=…&page=…`), so a view is one link.
- **CSV + bulk**: «CSV» exports exactly the loaded page (visible columns +
  id, RFC 4180, UTF-8 BOM) or — saying first how many pages of 250 it will
  ask for and that the browser ceiling is 10,000 rows — everything filtered.
  A selection column (a «Seleccionar página» button on phones) opens a bulk
  bar: change state (targets = the union of legal transitions of the
  selected rows; rows without a legal move are counted and skipped) or delete,
  through `/api/transaction` in batches of at most 100 ops with a progress
  bar. A batch is atomic, so a failed batch is retried row by row to NAME
  exactly which rows failed and why (e.g. «still referenced by "alquileres"»)
  while the rest goes through; the failed rows stay selected. Delete asks an
  explicit confirmation listing how many and which. In demo mode nothing
  leaves the browser (the overlay is applied row by row; a hand-made
  `/api/transaction` with the demo token is a 403 anyway).
- **Only the columns it paints (MOTOR-FIELDS-S1)**: every list, board, CSV
  and label request carries `?fields=` with the visible columns plus the
  state field (chips, moves, the board) and the title candidates (row labels)
  — `id` always comes back. The engine pushes the list into the SQL `SELECT`,
  so a `json`/`text` document (never a list column) is not read for the
  page: on rows with a ~50 KB document, a page of 20 went from 1.4 MB /
  ~100 ms of query to 1.8 KB / ~4 ms, visible in the footer's «consulta».
  A row that must be WHOLE — the edit form, the detail — is re-fetched by id
  first (`wholeRow`), so a projected list row never reaches a screen that
  would save or show missing fields.
- **Relation search past 100 rows**: the relation select is complete only up
  to the API's `per_page` cap; past it the control becomes a search box
  (`?search=` on the target, debounced 250 ms) showing the target's title
  field, and the list resolves the labels the cached page lacks (bounded to
  40 lookups per page).

Three product knobs a consumer controls without rebuilding anything:

- **Your colors** (`Config.AppThemeCSS`, or `APPXIMO_APP_THEME_CSS=<file>` on
  the stock binary): the engine serves your CSS at `/app/theme.css`, linked
  after the panel's own. **One line is a brand**:
  `:root { --app-accent: #FF5A36; }` — every accent-derived colour (hover,
  soft chip background, focus ring, glow, the active dot on the ink sidebar)
  is a `color-mix()` of `--app-accent`. The full token list is the head of
  `style.css`; the ones worth knowing: `--app-accent` / `--app-on-accent`,
  `--app-nav-bg` (the sidebar, ink by default), `--app-bg` / `--app-surface`
  / `--app-border` / `--app-text` / `--app-muted`, the radii, `--app-font`
  (Inter ships bundled — the CSP is `font-src 'self'`, so a CDN font would
  silently fall back), and `--app-s1…s8` (+`-bg`), the lifecycle palette by
  position. Dark mode: redefine the same tokens under
  `@media (prefers-color-scheme: dark) { :root:not([data-theme="light"]) {…} }`
  and `:root[data-theme="dark"] {…}`, or leave them out to keep the default
  dark palette.
- **Language**: the panel chrome is Spanish/English (browser-derived — an `es*`
  browser sees Spanish; anything else English — with a persisted in-app
  toggle). Resource names, field keys, enum values and engine error messages
  are YOUR schema's vocabulary and are shown verbatim, never translated
  (labels are letter-case only: `fecha_de_entrega` → "Fecha de entrega",
  a relation `cliente_id` → "Cliente", `precio_centavos` → "Precio").
- **Demo mode** (`Config.AppDemoRoles` / `APPXIMO_APP_DEMO_ROLES=demo`): for
  the listed roles, `/app` SIMULATES writes in a per-session in-memory overlay
  — the visitor creates/edits/deletes/moves cards, sees their changes merged
  into every list, board and relation select for the rest of the session, and
  a reload resets everything. No write ever leaves the browser. Pair it with
  a role whose RBAC is READ-ONLY: the overlay is visitor coherence, the
  deny-by-default policy is the security boundary (a hand-crafted request
  with that role's token is still a 403). A discreet fixed notice says
  "you're trying things out — changes are not saved". The role list is
  published at `/app/ui-config.json` (role names are not secrets). This is
  how a public showcase stays touchable without being vandalizable.

The panel is mobile-first (≤ 900 px: the sidebar becomes a drawer, tables
become stacked labelled cards, the board scrolls horizontally with snap, the
form is a full-height sheet), theme-aware (`prefers-color-scheme` + a
persisted auto/light/dark toggle), and self-contained under the CSP
(`style-src 'self'`: no inline styles, no CDN, the font embedded — pinned by
`pkg/backofficeui/embed_test.go`).

## 11. Checklist an agent can verify

1. `/openapi.json` fetched once, cached; reader produces N resources with
   fields — zero hardcoded resource names anywhere in the bundle.
2. A relation field on a FK with `references: user_id` saves without a
   FK-violation 409 (the selector sent the target's user_id).
3. A file field shows the accept/size policy and completes
   upload→attach→signed-URL display.
4. A state field on create offers only initial states; on edit only legal
   moves; a terminal row's state is read-only.
5. Creating with an empty optional field omits the key (no blank-record bug);
   a 422 paints every named field at once; a 409 leaves the form intact.
6. A role with partial grants sees denied resources dimmed after the 403
   probe — with no role matrix in the code.
7. **The visual-verification procedure of `frontend-spec` §11 has been run and
   passed** — the mobile layout gate (390×844, zero horizontal overflow,
   touchable controls), the console-strict browser e2e, and the forced failure
   states. That section is the single source of the rule: a back-office with
   every API check green and no browser pass is NOT done (a real delivery
   shipped a 753 px document on a 390 px screen exactly this way).
