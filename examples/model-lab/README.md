# Model Lab schemas

Six schemas modelling the data patterns of the most common modern app archetypes,
built to map **what the Appitools engine can and cannot model today**. Each one
**validates** (`appitools validate`) and **boots** (`serve`). The full diagnostic
— per-archetype verdict, live evidence, the prioritized gap catalog, and the
recommendation of what to close first — is in
[**docs/MODEL_LAB.md**](../../docs/MODEL_LAB.md).

| Schema | Archetype | Verdict | Exercises |
|---|---|---|---|
| [`saas.json`](saas.json) | SaaS / productivity (Notion/Trello/Linear) | 🟡 partial (best fit) | deep nesting, m2m tags, threaded comments, assignee, status |
| [`ecommerce.json`](ecommerce.json) | E-commerce / marketplace | 🟡 partial | variants, **self-ref category tree**, product↔category m2m, orders/lines |
| [`social.json`](social.json) | Social / content | 🟡 partial | **follows (self-ref user↔user m2m)**, threaded comments, likes, feed |
| [`booking.json`](booking.json) | Booking / reservations | 🟡 partial | listings, availability slots, bookings, time ranges, payments |
| [`chat.json`](chat.json) | Messaging / chat | 🟡 partial | conversations, participants m2m, keyset message stream, receipts, SSE |
| [`fintech.json`](fintech.json) | Fintech / wallet | 🔴 no | accounts, immutable ledger, transfers, balance (SUM), idempotency |

**Note on naming:** these use **concatenated** resource names (`cartitems`, not
`cart-items`). Resource names allow `-` but forbid `_`, while GraphQL forbids `-`
in field names — so a hyphenated resource name passes `validate` but **panics the
engine at boot** building the GraphQL schema (finding **G1** in the report). The
concatenated form is the only name that is valid in both. This is engine behavior
to be fixed, documented here so the examples stay boot-clean.

These are **reference models for a diagnostic**, not production templates — they
deliberately reach for what each archetype really needs so the gaps are honest.
