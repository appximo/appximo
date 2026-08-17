# The 47-second demo — how it was recorded, and how to reproduce it

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

**Measured duration of the recorded run: 46.7 s** end to end (the `.cast`
file's own timestamps are the measurement — timing data is embedded in the
recording format and cannot be edited without leaving traces).

## Honesty notes (read before accusing us of stagecraft)

- The command line is echoed with a cosmetic typing effect, then **executed
  for real**. Output and timing are the run's own. Nothing was cut.
- The GIF (`appximo-new.gif`) is rendered from the cast with
  `--idle-time-limit 2 --speed 1.25` (long silences capped at 2 s, 25% faster
  playback) — the untouched cast with real timing is
  [`appximo-new.cast`](appximo-new.cast); play it with
  `asciinema play appximo-new.cast`.
- `--port 8501 --control-port 9501` appeared in the recorded command because
  the recording box reserves the default ports; you can omit both.
- The `postgres:16` Docker image was already pulled on the recording box (as
  it will be on most dev machines). First-ever run adds the pull time.
- The AI step's cost and iteration count print on screen in the run itself.
  Generation quality varies per run: the recorded take validated on iteration
  1; a take may need 2–3 (the loop self-corrects; measured convergence data:
  [docs/AI_SCHEMA_GENERATION.md](../AI_SCHEMA_GENERATION.md)).

## The browser tour (`app-tour.mp4` / `app-tour.gif`)

The tour walks the SAME instance the cast produced — `/app` (the back-office
generated at runtime from `/openapi.json`), `/docs` (Swagger), `/editor`
(Appximo Studio's ERD of the generated schema), `/admin` (observability) — with
data seeded through the public API by [`seed.sh`](seed.sh) (plain `curl`s,
including two real state-machine transitions: an offer accepted, a property
draft→published→reserved). The tour is a *tour*, not a timing claim; the only
timing claim in this demo is the cast's.

## Reproduce it

```bash
# 1. install appximo (any of the documented paths), have Docker running
# 2. empty directory, ANTHROPIC_API_KEY exported
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
