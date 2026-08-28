# The demo — running app at 0:22, how it was recorded, and how to reproduce it

The hero demo shows **one sentence → a running, multi-tenant app** using
`appximo new`. Everything below exists so a skeptical reader can verify the
recording is honest and repeat it on their own machine.

## What the recording shows

```
appximo new "a real-estate listings portal: properties with price, neighborhood,
bedrooms and a lifecycle (draft, published, reserved, sold); agents; clients;
scheduled visits; purchase offers with amount and status" --name homeboard --yes
```

One command, from an empty directory:

1. the AI generation (claude-haiku-4-5) writes `schema.json`, self-correcting
   against `appximo validate --json` — in the recorded run it was **valid on
   the first try** (1 iteration, ~$0.03 of API cost, the stats print on screen);
2. the validator's **warnings** print — legal-but-probably-wrong RBAC rules,
   each with its fix. They are part of the product, not noise: every one of
   them is a bug that would otherwise fail *silently* in production;
3. `appximo up` starts Postgres (Docker), writes the secrets, registers the
   tenant with the schema, creates the first admin, boots the server, and
   **fires one real request through the full auth chain** before declaring
   success (`✓ verified: GET /api/agents answered 200`);
4. the card prints the URLs (`/app`, `/docs`, `/admin`, `/editor`), the
   admin credentials, and a dev token;
5. Ctrl+C — the graceful shutdown is part of the take.

**The timing, from the cast's own timestamps** (embedded in the recording
format — they cannot be edited without leaving traces):

| Moment | At |
|---|---|
| the AI-generated schema validates — **first try**, stats on screen | **0:17** |
| the app is **running**: one request verified through the full auth chain, card with URLs + credentials + dev token | **0:22** |
| end of take (graceful `Ctrl+C`, drain) | **0:47** |

So: **22 s to a running, verified app**; the remaining 25 s are the card sitting
on screen and the shutdown. Both numbers are in the recording; neither is
rounded in our favour.

**Watch it with controls** — pause exactly at 0:17 and 0:22, rewind, change
speed, or select and copy the text:

- in the browser: **<https://appximo.github.io/appximo/#demo>** (the same
  `.cast` this repo ships, played by asciinema-player)
- in your terminal: `asciinema play docs/demo/appximo-new.cast`

The GIF in the README is a convenience rendering of that cast — it plays at
**real speed** (no speed-up) with idle gaps capped at 3 s, and, being a GIF, it
cannot be paused. That is exactly why the player link sits next to it.

## Honesty notes (read before accusing us of stagecraft)

- The command line is echoed with a cosmetic typing effect, then **executed
  for real**. Output and timing are the run's own. Nothing was cut.
- The GIF (`appximo-new.gif`) is rendered from the cast with
  `agg --speed 1.0 --idle-time-limit 3 --fps-cap 12 --last-frame-duration 6`:
  **real speed**, silences capped at 3 s, and the final card held 6 s so it is
  readable without scrubbing. The untouched cast is
  [`appximo-new.cast`](appximo-new.cast).
- `--port 8501 --control-port 9501` appeared in the recorded command because
  the recording box reserves the default ports; you can omit both.
- The `postgres:16` Docker image was already pulled on the recording box (as
  it will be on most dev machines). First-ever run adds the pull time.
- The AI step's cost and iteration count print on screen in the run itself.
  Generation quality varies per run: the recorded take validated on iteration
  1; a take may need 2–3 (the loop self-corrects; measured convergence data:
  [docs/AI_SCHEMA_GENERATION.md](../AI_SCHEMA_GENERATION.md)).

## The browser tour (`app-tour.mp4`) — re-recorded 2026-08-28 on v0.1.13

The tour walks an instance of the SAME app the cast builds — the committed
[`schema.json`](schema.json) (5 resources, 2 state machines, 5 foreign keys),
booted with `appximo up` on the released **v0.1.13** binary — through what the
generated `/app` does today, then `/docs`, `/editor` and `/admin`. **87.6 s,
1280×960, H.264 + faststart, 3.5 MB, no audio.**

What it shows, in order (the burned subtitles are Spanish with an English
line under each; the top bar carries the real clock):

| At | What |
|---|---|
| 0:02 | the login `appximo up` printed the credentials for |
| 0:07 | home: one tile per resource, counts from the API |
| 0:13 | the list footer: **page 1 of 8 · 15 of 112 · the engine's own query time** (`?count=true` + the `Server-Timing` header every generated read carries) |
| 0:19 | next page; a filter by state — the total follows the filter |
| 0:27 | a row's **detail**: fields, the lifecycle strip with its legal moves, the parent (agent) and the children (offers, visits) resolved through the published foreign keys |
| 0:36 | edit → the **JSON editor** on a `jsonb` field: an invalid document is named before it is sent; format; save |
| 0:48 | **columns** picker and a **saved view** (browser storage + the URL hash) |
| 0:56 | **CSV**: this page, or everything filtered — it says first how many pages of 250 it will request |
| 1:02 | a **bulk transition** of the page's 15 reserved rows → published, through `/api/transaction` in atomic batches, with progress |
| 1:15 | `/docs` (the generated OpenAPI), `/editor` (the ERD in Studio), `/admin` (tenants, users, data, observability — the overview shows `engine v0.1.13`) |

Honesty notes, same rules as the cast:

- **Real time, no speed-up.** The top bar says «GRABACIÓN REAL · SIN ACELERAR»
  and carries the recording's own clock (`tiempo real 00:01:06.000`); the
  Swagger page's brief loading spinner at 1:15 is the real CDN load, kept.
- **One change to the generated schema, declared:** a `jsonb` field
  (`properties.amenities`) was added by hand so the JSON editor has something
  to edit. The diff against the AI-generated file is that one line; nothing
  else was touched.
- **Data is fictitious**, seeded through the public API (`/api/transaction`
  batches — the same door a migration uses; a re-run of the original
  [`seed.sh`](seed.sh) gives the 8-property version).
- **The bulk step writes for real** (it is not demo mode): 15 rows moved from
  `reserved` to `published`, one atomic batch, "15 applied".
- Chrome is English (the `/app` chrome follows the browser language; the
  schema's vocabulary is never translated).
- The previous tour (2026-08-17, the pre-redesign panel) is kept, not deleted:
  [`archive/app-tour-2026-08-17.mp4`](archive/app-tour-2026-08-17.mp4) and its
  GIF. No GIF is rendered for the new tour — 88 s of GIF would weigh more than
  the site; the MP4 plays inline everywhere.

The tour is a *tour*, not a timing claim; the only timing claim in this demo
is the cast's.

## Reproduce it

```bash
# 1. install appximo (any of the documented paths), have Docker running
# 2. empty directory, ANTHROPIC_API_KEY exported
#    (schema.json here is what this exact command generated on the recorded
#     take's re-run — 5 resources, 2 state machines, 5 foreign keys — plus the
#     one jsonb field the tour adds by hand, see above)
mkdir demo && cd demo
appximo new "a real-estate listings portal: properties with price, neighborhood, \
bedrooms and a lifecycle (draft, published, reserved, sold); agents; clients; \
scheduled visits; purchase offers with amount and status" --name homeboard --yes
# 3. open the printed /app URL, sign in with the printed credentials
# 4. optional: seed the same data the tour shows
TOKEN=<the dev token from the card> bash seed.sh
```

No API key? `appximo new` prints a ready-to-paste prompt for your own coding
agent instead, plus the `appximo up` command to run when it's done — same
result, your agent's tokens.

Time it yourself: `asciinema rec my-run.cast`, run the command, compare.
