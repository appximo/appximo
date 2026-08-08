# site/ — the official page (PHASE3-GUIDE-S1)

One self-contained static page: `index.html` + `assets/` (real screenshots, no
mockups). **No build step, no runtime dependencies** — every style is inline,
fonts are the system stack, images are local files. Open `index.html` in a
browser and it works; serve the directory from anything (Caddy `file_server`,
GitHub Pages, `Config.Static`) and it works identically.

## Provenance of every claim

Each number on the page comes from
[docs/CERTIFICATION_2026-08-01.md](../docs/CERTIFICATION_2026-08-01.md) (or the
dated field reports it references) **with its condition attached** — the
flagship benchmark names the limiter configuration it requires, the footprint
figures name their dataset and date, and the page makes **no comparison against
other frameworks** (the old NestJS figures were deliberately not re-verified —
certification §2.3 / OPS-12). Edit numbers only together with a new
certification pass.

The screenshots are real captures (2026-08-02): the live shop at
`tiendita.appximo.com` (mobile viewport), petfriendly's generated `/docs`,
and Studio / Swagger / the admin panel served by a scratch engine running the
`examples/model-lab/ecommerce.json` schema.

## Placeholders that wait on Miguel (dashed amber markers on the page)

| Marker | Decision it waits on |
|---|---|
| `REPO URL — pending publication` (hero button + footer) | Making the repository public and choosing the canonical URL (README badges say `appximo/appximo`; the Go module path says `appximo/appximo` — pick one). |
| `no release tag yet` (footer) | Cutting the first tag (`git tag v0.1.0`) — turns into a Releases link. |
| `appximo.com — pending decision` (footer) | Where this page lives. The relative `../docs/...` links assume it is served from the repo (e.g. GitHub Pages over the whole repo, or any host with `docs/` alongside `site/`); if it gets its own domain, point those links at the public repo URLs instead. |

## Verified

Rendered with Playwright/Chromium on 2026-08-02: mobile 390×844 and desktop
1440×900 — no horizontal scroll, all images load, zero console errors.
