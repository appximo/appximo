# gh-pages — the technical site (appximo.github.io/appximo)

One self-contained static page: `index.html` + `assets/`. **No build step, no
runtime dependencies** — styles inline, Inter bundled (`assets/inter-latin-var.woff2`),
images and videos local. Serve the directory from anything and it works
identically. `dev/bench/` is written by the Benchmark GitHub Action (do not edit
by hand).

## Provenance of every claim

Each number on the page comes from
[docs/CERTIFICATION_2026-08-01.md](https://github.com/appximo/appximo/blob/main/docs/CERTIFICATION_2026-08-01.md)
or [docs/BENCHMARKS.md](https://github.com/appximo/appximo/blob/main/docs/BENCHMARKS.md)
**with its condition attached** — the flagship benchmark names the limiter
configuration it requires, the `?fields=` figures name the dataset and the
box, the footprint figures name their dataset and date, and the page makes
**no comparison against other frameworks** (certification §2.3 / OPS-12). Edit a
number only together with the measurement that produced it.

## The media (re-taken 2026-08-28 against the released v0.1.13 binary)

- `assets/app-tour.mp4` + `tour-poster.jpg` — the browser tour: 87.6 s, real
  time (the top bar carries the recording's own clock), 1280×960 H.264 +
  faststart, Spanish subtitles burned with an English line under each, 3.5 MB.
  How it was recorded and what it shows, step by step:
  [docs/demo/README.md](https://github.com/appximo/appximo/blob/main/docs/demo/README.md).
  The previous tour (2026-08-17, the pre-redesign panel) is **archived, not
  deleted**: `assets/archive/app-tour-2026-08-17.mp4` + its poster.
- `assets/shot-app.png`, `shot-docs.png`, `shot-studio.png`, `shot-admin.png` —
  `/app`, `/docs`, `/editor`, `/admin` of the SAME app the tour walks
  (`docs/demo/schema.json`, booted with `appximo up` on v0.1.13; `/admin`
  shows `engine v0.1.13`). Chrome is English because the page is.
- `assets/shot-tiendita.png` (390-px phone viewport) and `shot-petfriendly.png`
  / `shot-https.png` — the two live demos as they answer today over HTTPS
  (`petfriendly.appximo.com`: Let's Encrypt, checked with `curl -vI`).
- `assets/shot-atina.webp`, `atina-*.mp4/webm`, `poster-atina-landing.webp` —
  atina, the third-party build (2026-08-25).
- `assets/shot-crisblogs.png` — the third party's blog login (unchanged; not ours).
- `assets/appximo-new.cast` / `.mp4` — the 47-second `appximo new` recording;
  the cast is played by asciinema-player, untouched.

## Verified

Rendered with Playwright/Chromium on 2026-08-28: mobile 390×844 and desktop
1440×900 — no horizontal scroll, all images load, zero console errors, every
outbound link answers 200 without a sign-in.
