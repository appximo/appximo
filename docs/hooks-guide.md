# Hooks Guide

JS hooks let you run custom logic before or after a record is created —  
without writing a Go handler. Validation, transformation, rejection: all in a JSON string.

---

## How hooks work

When a `POST /api/guides` request comes in:

1. The handler decodes the JSON body into `data`.
2. If a `before_create` JS hook exists, it calls `RunBeforeHook(script, data)`.
3. The script runs in an **isolated Goja VM** with a **500 ms timeout**.
4. If the script sets `result.proceed = false`, the request returns `422 Unprocessable Entity`.
5. If the script sets `result.data = data` (with modifications), those modifications become the INSERT payload.
6. After a successful INSERT, if an `after_create` **webhook** hook exists, it fires asynchronously (does not block the response).

> **Note:** `after_create` with `"type": "js"` is accepted by the validator but is currently a no-op. Only `after_create` with `"type": "webhook"` fires after the INSERT.

---

## Available variables

The sandbox exposes three objects to your script:

### `data` — the request body (mutable)

The decoded JSON from the `POST` body. You can read and write any field.

```js
data.code       // → "GU-001" (whatever the client sent)
data.iva = 19   // add a new field
```

### `user` — caller context (read-only)

```js
user.role      // → "operario"
user.user_id   // → "550e8400-..." (from X-User-ID header)
```

> `user` is passed from the handler — currently it contains whatever you inject. Full JWT support in v0.2.

### `result` — the hook outcome (mutable)

You must write to this to control the hook behavior.

```js
result.proceed = true    // default — allow the request
result.proceed = false   // abort — send 422 to the client
result.error = "reason"  // error message returned in the 422 body
result.data = data        // pass modified data to the INSERT
```

If you modify `data` but forget `result.data = data`, the original unmodified body is used. Always set `result.data = data` after mutations.

---

## Available functions

These are the **only** functions available in the sandbox. Nothing else.

| Function | Signature | Returns | Example |
|---|---|---|---|
| `parseFloat` | `(s string, bitSize int)` | `float64` | `parseFloat("3.14", 64)[0]` |
| `parseInt` | `(s string)` | `int` | `parseInt("42")[0]` |
| `now` | `()` | `int64` (Unix timestamp) | `now()` |
| `formatMoney` | `(v float64)` | `string` | `formatMoney(19.5)` → `"19.50"` |
| `isValidEmail` | `(addr string)` | `bool` | `isValidEmail("a@b.com")` → `true` |
| `isValidNIT` | `(nit string)` | `bool` | `isValidNIT("900123456")` → `true` |

> `parseFloat` and `parseInt` are Go functions with multiple return values. Call them as:  
> `var price = parseFloat(data.price_str, 64); var val = price[0];`

---

## 5 copy-paste examples

All examples assume you have a server running and a token set:

```bash
export JWT_SECRET="dev-secret"
export TOKEN=$(appitools token --role super_admin --tenant test --secret "$JWT_SECRET")
```

### 1. Validate a required field

Reject the request if a critical field is missing or empty.

```json
"before_create": {
  "type": "js",
  "script": "if (!data.code || data.code.trim() === '') { result.proceed = false; result.error = 'field code is required and cannot be blank'; }"
}
```

**Test:**
```bash
curl -X POST http://localhost:8080/api/guides \
  -H "Host: test.localhost" -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"origin": "Bogotá", "destination": "Medellín"}'
# → 422 {"error":"field code is required and cannot be blank"}

curl -X POST http://localhost:8080/api/guides \
  -H "Host: test.localhost" -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"code": "GU-001", "origin": "Bogotá", "destination": "Medellín"}'
# → 201 Created ✓
```

---

### 2. Calculate IVA automatically

Add `iva` and `total` to the record based on `subtotal`, without requiring the client to compute it.

```json
"before_create": {
  "type": "js",
  "script": "if (data.subtotal) { data.iva = data.subtotal * 0.19; data.total = data.subtotal + data.iva; } result.data = data;"
}
```

**Test:**
```bash
curl -X POST http://localhost:8080/api/invoices \
  -H "Host: test.localhost" -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"subtotal": 100000}'
# → 201 {"subtotal":100000, "iva":19000, "total":119000, ...}
```

Your `invoices` table needs `iva float64` and `total float64` columns in the schema.

---

### 3. Reject if amount exceeds a limit

Block large transactions that exceed a per-operation cap.

```json
"before_create": {
  "type": "js",
  "script": "var MAX = 5000000; if (data.declared_value && data.declared_value > MAX) { result.proceed = false; result.error = 'declared_value exceeds maximum of ' + MAX; }"
}
```

**Test:**
```bash
# Under limit → OK
curl -X POST http://localhost:8080/api/guides \
  -H "Host: test.localhost" -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"code":"GU-100","declared_value":100000}'
# → 201 ✓

# Over limit → 422
curl -X POST http://localhost:8080/api/guides \
  -H "Host: test.localhost" -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"code":"GU-101","declared_value":9999999}'
# → 422 {"error":"declared_value exceeds maximum of 5000000"}
```

---

### 4. Normalize text to uppercase

Ensure consistency: codes and plate numbers always stored in uppercase.

```json
"before_create": {
  "type": "js",
  "script": "if (data.code) { data.code = data.code.toUpperCase().trim(); } if (data.plate) { data.plate = data.plate.toUpperCase().trim(); } result.data = data;"
}
```

**Test:**
```bash
curl -X POST http://localhost:8080/api/vehicles \
  -H "Host: test.localhost" -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"plate": "abc-123d"}'
# → 201 {"plate": "ABC-123D", ...} ✓
```

Standard JS string methods (`toUpperCase`, `trim`, `replace`, `split`, `indexOf`) are available — they're part of the JS language, not external libraries.

---

### 5. Validate that a date is not in the past

Block backdating — ensure `scheduled_at` is today or in the future.

```json
"before_create": {
  "type": "js",
  "script": "if (data.scheduled_at) { var scheduled = new Date(data.scheduled_at).getTime(); var today = now() * 1000; if (scheduled < today - 86400000) { result.proceed = false; result.error = 'scheduled_at cannot be in the past'; } } result.data = data;"
}
```

> `now()` returns seconds since epoch. Multiply by 1000 to get milliseconds (JS Date uses ms).  
> The `- 86400000` gives a 24-hour grace window for same-day scheduling.

**Test:**
```bash
# Past date → 422
curl -X POST http://localhost:8080/api/dispatches \
  -H "Host: test.localhost" -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"guide_id":"...","scheduled_at":"2020-01-01T00:00:00Z"}'
# → 422 {"error":"scheduled_at cannot be in the past"}

# Future date → 201
curl -X POST http://localhost:8080/api/dispatches \
  -H "Host: test.localhost" -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"guide_id":"...","scheduled_at":"2030-12-31T00:00:00Z"}'
# → 201 ✓
```

---

## What you CANNOT do

The sandbox is a strict whitelist. Any attempt to access external resources is silently unavailable (Goja does not include these by default — they are never added):

| Blocked | Why |
|---|---|
| `require(...)` | No Node.js module system |
| `fetch(...)` | No HTTP client |
| `XMLHttpRequest` | No browser APIs |
| `import` | No ES modules |
| `process`, `os`, `fs` | No system access |
| `setTimeout`, `setInterval` | No async; hooks are synchronous |
| `console.log` | No I/O (won't crash, just discarded) |

**Why so restrictive?** A hook script comes from a user-defined JSON file. If it could make HTTP calls or access the filesystem, a malicious or buggy script could exfiltrate data, trigger internal APIs, or cause I/O storms. The whitelist ensures hooks are pure computation only.

---

## The 500 ms timeout

Every hook runs with a hard 500 ms deadline. If the script exceeds it, the VM is interrupted and the handler returns:

```
HTTP/1.1 500 Internal Server Error
Content-Type: text/plain

hook timeout: exceeded 500ms
```

**Why 500 ms?** API latency budgets are typically 100–200 ms. A hook that takes 500 ms has an infinite loop or a bug. This prevents one bad hook from blocking all requests.

**What causes timeouts?**

```js
// ❌ Infinite loop — will timeout in 500ms
while (true) {}

// ❌ Deeply recursive function — may timeout
function fib(n) { return n <= 1 ? n : fib(n-1) + fib(n-2); }
fib(50);  // exponential time

// ✅ Simple calculation — runs in < 1ms
data.total = data.price * data.quantity * 1.19;
result.data = data;
```

If you need complex logic that may exceed 500 ms, move it to a webhook handler (your own service, no timeout constraint).
