# Design guide — the craft, not the brand

> **What this is.** The three build documents (`spec`, `backend-spec`,
> `frontend-spec`) teach an agent to build a correct app on Appximo. None of
> them teaches it to build one that *looks* trustworthy. This guide does. It
> is the **mould** distilled from the strongest third-party frontend built on
> the engine so far — [atina](CASE_STUDY_ATINA.md), a recruiting SaaS whose
> design system its builder wrote up — with the brand-specific parts removed:
> tokens are placeholders, the stack is a recommendation with its argument,
> and every rule comes with the reason a reader can disagree with.
>
> **What this is not.** It is not a printable `appximo design-spec`. That
> was considered and rejected (reasoning below): the engine's specs are
> contracts the engine keeps; this is taste over a toolchain that will age.
> It is also not a tested prompt: atina's own "paste at the start of the
> session" prompt is deliberately NOT reproduced here, because we have not
> run it, and an untested prompt is a claim.

## 1. The stack, with the argument (a recommendation, not a contract)

| Piece | Recommendation | Why — and when to deviate |
|---|---|---|
| Framework | Svelte 5 (runes) + Vite, **pure SPA, `ssr = false`** | It is what `frontend-spec` recommends and what the engine's one-binary model needs: `go:embed` + `Config.Static{SPA:true}`. SSR would need a second process. Any static-output framework works; the criterion is *what a cheap model writes correctly first try*, and small, explicit frameworks win that. |
| CSS | Tailwind 4 with tokens in `@theme`, ~10 semantic classes in `@layer components` | Tokens become CSS variables, so a second brand is a `:root[data-brand]` block. Plain CSS variables + a few utility classes reach the same place with zero build; pick by team, not by dogma. |
| Type | ONE variable font, bundled (`@fontsource-variable/*`) | The engine's static CSP is `font-src 'self'` (see `pkg/backofficeui/embed.go`, `pkg/adminui/embed.go`): Google Fonts at runtime will not load. A system stack is the zero-byte alternative and is what the project's own commercial pages use. |
| Icons | one thin-stroke set (`lucide-*`), 18–20 px in nav, 14–16 px inline | Consistency beats variety; one set, two sizes. |
| Images | local, ≤ 190 KB, ≤ 1400 px, treated (cover + opacity + tint) | `img-src 'self' data: blob:` — nothing external. Treated photos read as one system; raw photos read as stock. |
| Motion | native transitions + a few keyframes + three small actions (`reveal`, `countUp`, `tilt`) | No animation library. See §4 for the contracts motion must satisfy. |
| Router | 60 lines of your own (`history` + `/a/:id` patterns) | It must skip the engine's prefixes (`/api`, `/app`, `/admin`, `/docs`, `/editor`, `/graphql`, `/openapi`) so a click there reaches the engine, not the SPA. |
| Verification | Playwright: every screen × every role × 1366 and 390 px, failing on `console.error`, `pageerror`, HTTP ≥ 400 | "curl does not see a blank page." The engine's `frontend-spec` §11 makes this mandatory; the design pass is the same script looking at the PNGs. |

Engine facts the design has to respect (verified against the source, not the
write-up): **one origin** (the SPA is served by the engine — no parallel
server, no CORS); **strict CSP** on static mounts (`DefaultStaticCSP`: no CDN
scripts, no runtime Google Fonts); **tenant = Host** (a brand-by-hostname map
is natural because the tenant already is); **files in three steps** (`POST
/api/files` → attach the id in the record → display through a signed URL,
because `<img>` cannot send a header — `frontend-spec` §7).

## 2. Tokens — the shape, with placeholder values

Copy the shape. Replace every value. Never write the accent's hex in a
component: everything goes through `var(--color-brand)` and `color-mix()`, so
a second brand is ten lines.

```css
@theme {
  --font-sans: "<Your Variable Font>", ui-sans-serif, system-ui, sans-serif;

  /* ONE saturated accent + an ink + a neutral ramp. That is the whole palette. */
  --color-brand: <accent>;            /* CTA, active indicator, focus ring, one section bg at most */
  --color-brand-50 … --color-brand-800: <the accent's ramp>;
  --color-ink: <near-black>;          /* hero, sidebar, key-data cards */
  --color-ink-950 … --color-ink-500: <the ink ramp>;
  /* neutrals: your framework's gray ramp (zinc/slate); do not invent a fourth colour */

  --radius-xl: .9rem; --radius-2xl: 1.25rem; --radius-3xl: 1.75rem;
  --shadow-soft: 0 1px 2px rgb(0 0 0 / .04), 0 12px 32px -12px rgb(0 0 0 / .14);
  --shadow-lift: 0 2px 4px rgb(0 0 0 / .05), 0 24px 48px -16px rgb(0 0 0 / .22);
  --shadow-glow: 0 0 0 1px color-mix(in srgb, var(--color-brand) 35%, transparent),
                 0 12px 40px -8px color-mix(in srgb, var(--color-brand) 45%, transparent);
}
```

Semantic classes worth having (≈10, no more): `.card`, `.card-dark`,
`.label`, `.input`, `.chip` (+ `-brand`, `-dark`), `.reveal`, `.skeleton`,
`.btn-glow`, `.container-x`. Everything else is utilities.

## 3. The principles (each with its reason)

1. **One accent, three surfaces.** Accent only for the primary CTA, the
   active indicator, high-emphasis rings and details. Surfaces: white
   (cards), the lightest neutral (page), ink (hero, sidebar, key-data cards).
   The accent as a *large background* at most once per page. *Why:* the
   accent's job is to be found (von Restorff); a second accent halves it.
2. **Type carries the hierarchy.** Titles `font-bold tracking-tight`
   (3xl→5xl), eyebrows `11px uppercase tracking-[0.18em]`, numbers
   `tabular-nums`. One family. *Why:* one family with weight + tracking
   contrast reads as designed; two families read as undecided.
3. **Generous radii, thin borders, soft shadows.** `rounded-2xl` cards,
   `rounded-xl` inputs/buttons, a `/80` neutral border + `shadow-soft`; hover
   = `-translate-y-0.5` + `shadow-lift`. *Why:* low visual complexity is the
   strongest 50 ms predictor of perceived quality (Reinecke et al., CHI 2013).
4. **Photos treated, never raw.** Cover + opacity 60–70 % + an ink or warm
   tint layer + a gradient to ink (+ grain if you must). *Why:* untreated
   photos of people read as stock, and stock measurably hurts.
5. **Motion with a purpose, 200–800 ms, `cubic-bezier(.22,1,.36,1)`.**
   `reveal` on sections, `countUp` on KPIs, `tilt` (≤ 6°) on feature cards,
   modal `fly y:24` 240 ms. Loops only where the page is *about* motion; a
   commercial page for a sceptical buyer should have none (see §4).
6. **Density by context.** Public: air (`py-24`). Back-office: `py-6/8`,
   `p-5` cards, `divide-y` lists, `11px uppercase` table heads. Mobile:
   bottom tab bar; sidebar becomes a drawer; modals become bottom sheets.
7. **Every state is designed.** Skeleton while loading, `Empty` with an
   action, inline error, the 422 rendered field by field (the engine gives
   you every failing field at once — show them all, scroll to the first),
   badges with a dot and a semantic tone. *Why:* the empty and error states
   are where a demo is actually judged.
8. **Data is explained, not just shown.** Number + context: a ring plus a
   breakdown in bars, percentages beside bars, chips that say what a value
   means. Own SVG and CSS bars; no chart library for a first version.

## 4. Motion contracts (the part that is a gate, not taste)

These came out of the project's own landing work, where a variant lost a
judged comparison because its reveal left sections blank — "a broken page
reads as a scam". Every animated page must satisfy all three:

- **(a) No JS → the page is fully visible.** Nothing starts at `opacity: 0`
  in the static HTML; the JS *adds* the hidden state and then removes it.
- **(b) `prefers-reduced-motion: reduce` → still and visible.**
- **(c) With motion on, after a full scroll, zero elements remain at
  `opacity: 0`.** (The verification script counts them; the count must be
  0.)

Two rules for pages that *sell to non-technical buyers* (the evidence is in
the project's research notes, summarised: complexity hurts in the first
500 ms; animation that blocks content reads as "noise, no substance"):
microinteractions under 0.3 s; no marquee, no floating loops, no giant
wordmark collage. Keep those for a developer audience, which rewards them.

## 5. Components a first version needs (and no more)

`Button` (variants, `loading`, `href` → `<a>`), `Field`/`Select`, `Modal`
(bottom sheet on mobile), `Badge` (tones + dot), `Avatar`, `Stat` (KPI),
`Empty`, `Skeleton`, `Toasts`, `Tabs`, `PageHeader`, `FileUpload` (XHR with
progress — `frontend-spec` §7), a `Logo` that reads its name from the brand
map. Layouts: `PublicLayout` (transparent header over the hero → solid with
`backdrop-blur` on scroll), `PanelLayout` (ink sidebar, active item
`bg-white/10` + accent dot, drawer on mobile), an end-user layout with top
tabs + bottom bar.

## 6. The process that produced a good result (from the atina report)

1. Read `appximo frontend-spec` before the first `fetch`.
2. One `api.js`: `ApiError`, an authenticated `papi` (401 → `/login?next=`),
   a public `pub`, `all()` for paged lists, `uploadFile` (XHR), `fileUrl`
   (signed URL, cached).
3. **Catalogues in ONE public route of your own** (`/api/catalogos`) with a
   declared `Route.RateLimit` and a one-hour client cache. Seven anonymous
   reads on first paint trip the engine's public limiter — by design; see
   `backend-spec` §Route.RateLimit and `frontend-spec` trap 5.
4. Build by area (public → end user → back-office → console) and verify
   **with a real browser** after each, looking at the captures.
5. Realistic demo data **through the API** with a resumable seeder — never
   SQL (defaults, rules and RBAC only apply on the API path).
6. Promotional material generated against the real app, with real data.

## 7. Why this is a document and not `appximo design-spec`

For: the trilogy teaches building and nothing teaches looking good; a fifth
printable doc would be pasted alongside the other four.

Against, and decisive: the four printable specs are **contracts the engine
keeps** — every claim in them is pinned by a test against the running engine.
A design guide is taste over a toolchain (Tailwind 4, Svelte 5, a font
package) that will be stale in a year, and embedding it in the binary would
make the engine *carry* that staleness and make every release re-verify a
document the engine cannot verify. So: the engine facts a design must respect
live in `frontend-spec` (they are contracts); the craft lives here, in
`docs/`, versioned with the repo and free to age. Reconsider if a second
third-party frontend arrives with a *different* stack and the same rules
survive the translation — then the rules are the product, not the stack.
