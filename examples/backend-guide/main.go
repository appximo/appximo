// Command backend-guide is the companion example to docs/BACKEND_SPEC_LLM.md —
// a complete, COMPILING Appximo backend built the library way (ADR-016): a
// schema (schema.json) for the declarative surface, plus custom Class-1 handlers
// for the logic a schema can't express (external calls, cross-resource
// transactions, parallel work). Every handler below is referenced verbatim by
// the guide, and every safety pattern from Phase 0 (LIBRARY-HARDEN-S1) is shown:
// SafeGo for goroutines, SafeParallel for bounded fan-out, Route.Timeout for
// deadlines, a Public route with the caller treated as hostile.
//
// Run it like the pure binary (all three env vars required):
//
//	DATABASE_URL=... JWT_SECRET=... ADMIN_KEY=... \
//	  go run ./examples/backend-guide --schema examples/backend-guide/schema.json
//
// External integrations fall back to a local stub when their *_URL env var is
// unset, so the example runs standalone; set the vars to call the real services.
package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/appximo/appximo"
)

// httpClient is shared by the outbound integrations. Give it a ceiling of its
// own; the per-call context (bounded by Route.Timeout) is the real deadline.
var httpClient = &http.Client{Timeout: 10 * time.Second}

func main() {
	schemaPath := flag.String("schema", "examples/backend-guide/schema.json", "path to schema.json")
	port := flag.Int("port", 8080, "HTTP port")
	flag.Parse()

	app, err := appximo.New(appximo.Config{
		SchemaPath: *schemaPath,
		Port:       *port,
		// DSN / JWTSecret / AdminKey / Env fall back to the standard env vars.

		// BeforeStart runs with the engine fully built (pool open, schema
		// compiled) but BEFORE the listener accepts anything — the seam for boot
		// work: your own DDL for what the schema grammar cannot express, seeds, a
		// warm-up. It hands you the ENGINE'S OWN pool, so you never parse
		// DATABASE_URL and open a second one. An error here ABORTS the boot.
		BeforeStart: ensureInvariants,
	})
	if err != nil {
		log.Fatal(err)
	}

	register(app, appximo.Route{
		// GET /api/ops/overview — an AUTHENTICATED, admin-scoped custom route.
		// Path-based RBAC authorizes the first segment ("ops") as a VIRTUAL
		// resource: a wildcard role (admin) reaches it; a restricted role is 403.
		// (End users read their OWN data through the GENERATED routes — GET
		// /api/students, /api/enrollments — which the student role owner-scopes by
		// user_id automatically; a custom route is for logic that spans resources,
		// like this cross-resource snapshot.) Inside, ctx.Query still enforces RBAC
		// on the REAL resources, so an admin sees every row.
		Method: "GET", Path: "/api/ops/overview", RequireRole: "admin",
		Handler: func(ctx appximo.Ctx) error {
			students, err := ctx.Query("students", appximo.QueryOpts{Limit: 1000})
			if err != nil {
				return ctx.Error(500, "students lookup failed", err)
			}
			enrollments, err := ctx.Query("enrollments", appximo.QueryOpts{Limit: 1000})
			if err != nil {
				return ctx.Error(500, "enrollments lookup failed", err)
			}
			return ctx.JSON(200, map[string]any{
				"tenant":      ctx.Tenant(),
				"students":    len(students),
				"enrollments": len(enrollments),
			})
		},
	})

	register(app, appximo.Route{
		// POST /api/register — the Hotmart pattern: a PUBLIC (pre-auth) endpoint
		// that runs business logic and creates the user, related records and a
		// follow-up job ATOMICALLY in one transaction. Any failure rolls back
		// everything — including the user. This is the differential: no network
		// hop, one transaction, the engine's own validation + RBAC still in force.
		Method: "POST", Path: "/api/register", Public: true, Timeout: 15 * time.Second,
		Handler: func(ctx appximo.Ctx) error {
			var body struct {
				Email    string `json:"email"`
				Password string `json:"password"`
				FullName string `json:"full_name"`
				License  string `json:"license"`
				CourseID string `json:"course_id"`
				// Money is int64 MINOR UNITS (cents), never a float — see schema.json.
				AmountCents int64 `json:"amount_cents"`
			}
			if err := ctx.Bind(&body); err != nil {
				return ctx.Error(400, "invalid body", err)
			}
			if body.Email == "" || body.FullName == "" || body.CourseID == "" {
				return ctx.Error(422, "email, full_name and course_id are required", nil)
			}

			// 1. Business gate: verify the purchase against the external platform
			//    (a plain Go http call — no sandbox). The bounded context aborts a
			//    hung provider at Route.Timeout.
			ok, err := verifyLicense(ctx.Context(), body.License)
			if err != nil {
				return ctx.Error(502, "license service unavailable", err)
			}
			if !ok {
				return ctx.Error(403, "license not valid", nil)
			}

			// 2. Business data check: the course must exist and be open. A public
			//    route has no identity, so the RBAC-aware helpers fail closed —
			//    this raw read is a deliberate, greppable UnsafeTx; tenant
			//    isolation still holds (search_path).
			var published bool
			err = ctx.UnsafeTx().QueryRow(ctx.Context(),
				`SELECT published FROM courses WHERE id = $1`, body.CourseID).Scan(&published)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				return ctx.Error(404, "course not found", nil)
			case err != nil:
				return ctx.Error(500, "course lookup failed", err)
			case !published:
				return ctx.Error(409, "course is not open for enrollment", nil)
			}

			// 3. Create the identity. The role comes from THIS code, NEVER the
			//    request — a public endpoint must never let the caller pick a role.
			user, err := ctx.CreateUser(body.Email, body.Password, "student")
			switch {
			case err == nil:
			case errors.Is(err, appximo.ErrEmailTaken):
				return ctx.Error(409, "email already registered", err)
			case errors.Is(err, appximo.ErrInvalidEmail), errors.Is(err, appximo.ErrWeakPassword):
				return ctx.Error(422, err.Error(), err)
			default:
				return ctx.Error(500, "registration failed", err)
			}

			// 4. Profile + enrollment in the SAME transaction (UnsafeTx: no
			//    identity on a public route, and the handler owns the validation).
			if _, err := ctx.UnsafeTx().Exec(ctx.Context(),
				`INSERT INTO students (user_id, full_name) VALUES ($1, $2)`,
				user.ID, body.FullName); err != nil {
				return ctx.Error(500, "profile creation failed", err)
			}
			if _, err := ctx.UnsafeTx().Exec(ctx.Context(),
				`INSERT INTO enrollments (user_id, course_id, amount_cents, external_ref) VALUES ($1, $2, $3, $4)`,
				user.ID, body.CourseID, body.AmountCents, "lic-"+body.License); err != nil {
				return ctx.Error(500, "enrollment failed", err)
			}

			// 5. Follow-up work, atomic with the user: the welcome email is
			//    enqueued in the outbox in this SAME tx (durable, retryable) and
			//    delivered async by the worker — never blocking the response, and
			//    it does not exist if anything above rolled back.
			if _, err := ctx.Enqueue("email.send", map[string]any{
				"template": "welcome", "to": user.Email, "user_id": user.ID,
			}); err != nil {
				return ctx.Error(500, "enqueue failed", err)
			}
			return ctx.JSON(201, map[string]any{"user_id": user.ID, "email": user.Email})
		},
	})

	register(app, appximo.Route{
		// POST /api/reports/ratings — heavy fan-out. Enrich a list of courses with
		// their external rating IN PARALLEL, bounded and panic-safe. SafeParallel
		// caps concurrency (backpressure) and turns a task panic into an error, so
		// a bad provider response never crashes the process; Route.Timeout bounds
		// the whole batch. The tasks do EXTERNAL I/O only — never the handler tx
		// (a single connection is not safe for concurrent use).
		Method: "POST", Path: "/api/reports/ratings", RequireRole: "admin", Timeout: 8 * time.Second,
		Handler: func(ctx appximo.Ctx) error {
			var body struct {
				CourseIDs []string `json:"course_ids"`
			}
			if err := ctx.Bind(&body); err != nil {
				return ctx.Error(400, "invalid body", err)
			}
			if len(body.CourseIDs) == 0 {
				return ctx.Error(422, "course_ids is required", nil)
			}
			ratings := make([]float64, len(body.CourseIDs))
			tasks := make([]func(context.Context) error, len(body.CourseIDs))
			for i, id := range body.CourseIDs {
				i, id := i, id
				tasks[i] = func(fctx context.Context) error {
					r, err := fetchRating(fctx, id)
					if err != nil {
						return err
					}
					ratings[i] = r // each task writes its OWN slot — no shared mutation
					return nil
				}
			}
			if err := appximo.SafeParallel(ctx.Context(), 8, tasks...); err != nil {
				return ctx.Error(502, "ratings service failed", err)
			}
			out := make(map[string]float64, len(ratings))
			for i, id := range body.CourseIDs {
				out[id] = ratings[i]
			}
			return ctx.JSON(200, map[string]any{"ratings": out})
		},
	})

	register(app, appximo.Route{
		// POST /api/track — fire-and-forget. Record nothing durable; just ping an
		// external analytics service off the response path. SafeGo is the ONLY
		// sanctioned way to launch that goroutine: its panic is recovered + logged
		// + metered, never a process crash. The goroutine gets a fresh context (no
		// request values) with its own deadline, which fn must honor. This is
		// at-most-once — for durable work, Enqueue to the outbox instead.
		Method: "POST", Path: "/api/track", Public: true,
		Handler: func(ctx appximo.Ctx) error {
			var body struct {
				Event string `json:"event"`
			}
			if err := ctx.Bind(&body); err != nil {
				return ctx.Error(400, "invalid body", err)
			}
			event := body.Event
			ctx.SafeGo(func(bg context.Context) {
				_ = pingAnalytics(bg, event)
			})
			return ctx.JSON(202, map[string]any{"accepted": true})
		},
	})

	register(app, appximo.Route{
		// POST /api/checkout — an AUTHENTICATED custom route reachable by a role
		// that uses per-resource `permissions` (the "student" role owner-scopes
		// each resource by its own column). That combination is only expressible
		// because the role declares a `routes` grant in the schema:
		//
		//   "student": { "permissions": {...}, "routes": { "checkout": {"actions":["create"]} } }
		//
		// Without it the middleware would deny-by-default: a custom route's first
		// segment is a VIRTUAL resource, and `permissions` keys must be real ones.
		// See ADR-021.
		Method: "POST", Path: "/api/checkout", Timeout: 10 * time.Second,
		Handler: func(ctx appximo.Ctx) error {
			var body struct {
				CourseID string `json:"course_id"`
			}
			if err := ctx.Bind(&body); err != nil {
				return ctx.Error(400, "invalid body", err)
			}
			if body.CourseID == "" {
				return ctx.Error(422, "course_id is required", nil)
			}
			// Money is int64 MINOR UNITS everywhere (see schema.json price_cents):
			// never float. The read goes through ctx.Query, which re-evaluates the
			// caller's role against the REAL resource — a route grant authorizes the
			// ENDPOINT, never the data.
			courses, err := ctx.Query("courses", appximo.QueryOpts{
				Filters: map[string]any{"id": body.CourseID}, Limit: 1,
			})
			if err != nil {
				return ctx.Error(403, "not allowed to read courses", err)
			}
			if len(courses) == 0 {
				return ctx.Error(404, "course not found", nil)
			}
			priceCents, _ := courses[0]["price_cents"].(int64)

			// The enrollment is created with the caller's OWN user_id: the role's
			// row condition on `enrollments` forces it anyway (mass-assignment is
			// blocked at the engine level), so this cannot attribute a purchase to
			// somebody else.
			row, err := ctx.Insert("enrollments", map[string]any{
				"user_id":      ctx.Claims().UserID,
				"course_id":    body.CourseID,
				"amount_cents": priceCents,
				"external_ref": "chk-" + ctx.Claims().UserID + "-" + body.CourseID,
			})
			if err != nil {
				return ctx.Error(409, "could not enroll", err)
			}
			return ctx.JSON(201, map[string]any{"enrollment": row, "amount_cents": priceCents})
		},
	})

	register(app, appximo.Route{
		// POST /api/webhooks/payments — THE security-critical handler most products
		// write, and the one shape it is easiest to get wrong. Six rules, in order:
		//
		//  1. VERIFY THE SIGNATURE OVER THE RAW BYTES, BEFORE PARSING. ctx.RawBody()
		//     returns the body exactly as sent, under the engine's own 1 MiB cap
		//     (nothing to re-implement), and Bind still works afterwards. Parsing
		//     first and re-serializing changes key order and whitespace and breaks
		//     every signature — the #1 documented payment-integration bug.
		//  2. IDEMPOTENCY IS A UNIQUE CONSTRAINT, not an `if`. Gateways deliver
		//     at-least-once and retries arrive CONCURRENTLY; "SELECT then INSERT"
		//     races with itself. Insert first, let the DB reject the duplicate.
		//  3. THE WEBHOOK IS THE SOURCE OF TRUTH — not the browser redirect (the
		//     customer may close the tab; some methods settle minutes later).
		//  4. RESPOND 200 ONLY AFTER THE STATE IS COMMITTED. A 200 tells the
		//     gateway to stop retrying, so it must mean "recorded", not "received".
		//     The engine buffers ctx.JSON until AFTER the commit — a commit failure
		//     becomes a 500, never a false 200 — which is exactly this rule.
		//  5. HANDLE OUT-OF-ORDER EVENTS: compare the gateway's own timestamp, not
		//     arrival order, before overwriting newer state.
		//  6. NEVER RETRY BUSINESS LOGIC ON A TERMINAL DECLINE. A decline is final;
		//     release what you held and stop.
		//
		// Public (a gateway has no JWT), so the caller is hostile: every input is
		// validated, and the engine's conservative public-route rate limit applies.
		Method: "POST", Path: "/api/webhooks/payments", Public: true, Timeout: 15 * time.Second,
		Handler: func(ctx appximo.Ctx) error {
			// Rule 1 — raw bytes first.
			raw, err := ctx.RawBody()
			if err != nil {
				if errors.Is(err, appximo.ErrBodyTooLarge) {
					return ctx.Error(413, "payload too large", err)
				}
				return ctx.Error(400, "unreadable body", err)
			}
			if !validSignature(raw, ctx.Request().Header.Get("X-Signature")) {
				return ctx.Error(401, "invalid signature", nil)
			}

			// Only NOW is it safe to interpret the bytes.
			var event struct {
				ID     string `json:"id"`
				Type   string `json:"type"`
				Ref    string `json:"external_ref"`
				Status string `json:"status"`
			}
			if err := json.Unmarshal(raw, &event); err != nil {
				return ctx.Error(400, "invalid event", err)
			}
			if event.ID == "" || event.Ref == "" {
				return ctx.Error(422, "id and external_ref are required", nil)
			}

			// Rule 2 — the DATABASE decides whether this is a duplicate.
			tag, err := ctx.UnsafeTx().Exec(ctx.Context(),
				`INSERT INTO webhook_events (event_id, provider, payload)
				 VALUES ($1, 'payments', $2) ON CONFLICT (event_id) DO NOTHING`,
				event.ID, string(raw))
			if err != nil {
				return ctx.Error(500, "could not record the event", err)
			}
			if tag.RowsAffected() == 0 {
				// Already applied. 200 so the gateway stops retrying: this is a
				// success, not an error.
				return ctx.JSON(200, map[string]any{"status": "duplicate", "event_id": event.ID})
			}

			// Rules 3 + 6 — apply the state change in THIS transaction. The guard in
			// the WHERE makes the transition race-safe and makes a terminal state
			// impossible to revive.
			if event.Status == "refunded" {
				if _, err := ctx.UnsafeTx().Exec(ctx.Context(),
					`UPDATE enrollments SET status = 'refunded'
					  WHERE external_ref = $1 AND status = 'active'`, event.Ref); err != nil {
					return ctx.Error(500, "could not apply the refund", err)
				}
			}

			// Side effects go to the OUTBOX, not an inline call: enqueued in the same
			// transaction (it exists iff the event was recorded) and delivered by the
			// worker, so a slow provider can never hold this transaction open.
			if _, err := ctx.Enqueue("email.send", map[string]any{
				"template": "payment_" + event.Status, "external_ref": event.Ref,
			}); err != nil {
				return ctx.Error(500, "enqueue failed", err)
			}
			// Rule 4 — the 200 is flushed after the commit, by the engine.
			return ctx.JSON(200, map[string]any{"status": "processed", "event_id": event.ID})
		},
	})

	register(app, appximo.Route{
		// GET /api/catalogue — a PUBLIC READ endpoint, and the reason
		// Route.RateLimit exists. The public-route default (5 rps / burst 10) is
		// calibrated for a public WRITE endpoint like /api/register above; a
		// catalogue is bursty and read-only, and would trip it under ordinary
		// traffic. Declaring the budget HERE beats raising
		// APPXIMO_PUBLIC_ROUTE_RPS process-wide, which would also loosen the
		// registration and webhook endpoints that want the strict default.
		Method: "GET", Path: "/api/catalogue", Public: true,
		RateLimit: &appximo.RateLimit{RPS: 200, Burst: 400},
		Handler: func(ctx appximo.Ctx) error {
			// No identity on a public route, so the RBAC helpers fail closed: this
			// is a deliberate, greppable UnsafeTx, and the handler owns the filter
			// (only published courses are ever exposed). Tenant isolation still
			// holds — the tx carries the tenant's search_path.
			rows, err := ctx.UnsafeTx().Query(ctx.Context(),
				`SELECT id, title, price_cents, metadata FROM courses
				  WHERE published = true ORDER BY title LIMIT 50`)
			if err != nil {
				return ctx.Error(500, "catalogue unavailable", err)
			}
			defer rows.Close()
			out := []map[string]any{}
			for rows.Next() {
				var id, title string
				var priceCents int64
				var metadata map[string]any // a jsonb column decodes straight into Go
				if err := rows.Scan(&id, &title, &priceCents, &metadata); err != nil {
					return ctx.Error(500, "catalogue row", err)
				}
				out = append(out, map[string]any{
					"id": id, "title": title, "price_cents": priceCents, "metadata": metadata,
				})
			}
			return ctx.JSON(200, map[string]any{"data": out})
		},
	})

	register(app, appximo.Route{
		// POST /api/reprice — the BATCH pattern (safety rule 6). Body:
		// {"items":[{"course_id":"…","price_cents":N}, …]}.
		//
		// The mistake this route exists to teach against: a loop that runs one
		// statement per element. Each statement pays a full round trip to
		// Postgres, so "3 small queries per item" over 400 items is 1,200
		// sequential round trips — at ~120 ms each on a real network that is a
		// TWO-MINUTE request, far past any Route.Timeout, holding the tenant
		// transaction (and its locks) open the whole time. It works in a
		// 5-row test and falls over on the first real batch.
		//
		// The batch shape is TWO statements regardless of N:
		//   1. ONE validation read over the whole set:  WHERE id = ANY($1)
		//   2. ONE write driven by unnest():            UPDATE … FROM unnest(...)
		// pgx binds Go slices to Postgres arrays directly — no string building.
		Method: "POST", Path: "/api/reprice", RequireRole: "admin",
		Handler: func(ctx appximo.Ctx) error {
			var body struct {
				Items []struct {
					CourseID   string `json:"course_id"`
					PriceCents int64  `json:"price_cents"`
				} `json:"items"`
			}
			if err := ctx.Bind(&body); err != nil {
				return ctx.Error(400, "invalid body", err)
			}
			if len(body.Items) == 0 || len(body.Items) > 1000 {
				return ctx.Error(422, "items must have 1–1000 entries", nil)
			}
			ids := make([]string, len(body.Items))
			prices := make([]int64, len(body.Items))
			for i, it := range body.Items {
				if it.CourseID == "" || it.PriceCents < 0 {
					return ctx.Error(422, fmt.Sprintf("items[%d]: course_id and a non-negative price_cents are required", i), nil)
				}
				ids[i] = it.CourseID
				prices[i] = it.PriceCents
			}

			// 1. Validate the WHOLE set in ONE query — never one lookup per id.
			rows, err := ctx.UnsafeTx().Query(ctx.Context(),
				`SELECT id FROM courses WHERE id = ANY($1::uuid[])`, ids)
			if err != nil {
				return ctx.Error(500, "course lookup failed", err)
			}
			found := make(map[string]bool, len(ids))
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					rows.Close()
					return ctx.Error(500, "course lookup row", err)
				}
				found[id] = true
			}
			rows.Close()
			for i, id := range ids {
				if !found[id] {
					return ctx.Error(422, fmt.Sprintf("items[%d]: course %s does not exist", i, id), nil)
				}
			}

			// 2. Apply the WHOLE batch in ONE statement. unnest() turns the two
			// parallel arrays into a (id, price) row set the UPDATE joins against.
			tag, err := ctx.UnsafeTx().Exec(ctx.Context(),
				`UPDATE courses SET price_cents = u.price
				   FROM unnest($1::uuid[], $2::bigint[]) AS u(id, price)
				  WHERE courses.id = u.id`, ids, prices)
			if err != nil {
				return ctx.Error(500, "reprice failed", err)
			}
			return ctx.JSON(200, map[string]any{"updated": tag.RowsAffected()})
		},
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}

// ensureInvariants is the Config.BeforeStart hook: the DDL the schema grammar
// cannot express. Everything the grammar CAN express — columns, indexes (btree and
// gin), foreign keys, unique constraints — belongs in schema.json, where the
// migration engine owns it. What is left is genuinely non-declarative: CHECK
// constraints, generated columns, partial indexes.
//
// It runs on the ENGINE'S OWN pool, which is NOT tenant-scoped: set the search_path
// yourself, transaction-locally, as DATA — never by string concatenation.
func ensureInvariants(ctx context.Context, pool *pgxpool.Pool) error {
	rows, err := pool.Query(ctx, `SELECT pg_schema FROM public.tenants ORDER BY id`)
	if err != nil {
		return fmt.Errorf("list tenants: %w", err)
	}
	var schemas []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			rows.Close()
			return err
		}
		schemas = append(schemas, s)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, pgSchema := range schemas {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, "SELECT set_config('search_path', $1, true)", pgSchema); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("set search_path %s: %w", pgSchema, err)
		}
		// A money invariant the schema's min:0 cannot enforce at the DB level.
		if _, err := tx.Exec(ctx,
			`ALTER TABLE courses DROP CONSTRAINT IF EXISTS chk_courses_price_nonneg`); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if _, err := tx.Exec(ctx,
			`ALTER TABLE courses ADD CONSTRAINT chk_courses_price_nonneg
			   CHECK (price_cents IS NULL OR price_cents >= 0)`); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("price check on %s: %w", pgSchema, err)
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
	}
	log.Printf("boot: invariants ensured for %d tenant(s)", len(schemas))
	return nil
}

// validSignature verifies an HMAC-SHA256 signature over the RAW request bytes.
// WEBHOOK_SECRET unset → the example runs standalone by accepting any signature;
// a real deployment must fail closed instead.
func validSignature(raw []byte, header string) bool {
	secret := os.Getenv("WEBHOOK_SECRET")
	if secret == "" {
		return true // stub for the standalone example — NEVER do this in production
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(raw)
	want := hex.EncodeToString(mac.Sum(nil))
	// Constant time: a byte-by-byte compare leaks the signature through timing.
	return hmac.Equal([]byte(strings.TrimPrefix(header, "sha256=")), []byte(want))
}

// register aborts boot on a bad route — a registration error is a programming
// mistake, caught here, never at request time.
func register(app *appximo.App, rt appximo.Route) {
	if err := app.Register(rt); err != nil {
		log.Fatalf("register %s %s: %v", rt.Method, rt.Path, err)
	}
}

// verifyLicense calls the external purchase-verification API with the handler's
// (bounded) context. With LICENSE_API_URL unset it accepts any plausible license
// so the example runs standalone — replace the stub with the real call.
func verifyLicense(ctx context.Context, license string) (bool, error) {
	url := os.Getenv("LICENSE_API_URL")
	if url == "" {
		return len(license) >= 8, nil
	}
	payload, _ := json.Marshal(map[string]string{"license": license})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close() //nolint:errcheck
	return resp.StatusCode == http.StatusOK, nil
}

// fetchRating fetches one course's rating from an external service. RATINGS_URL
// unset → a deterministic stub so the fan-out example runs standalone.
func fetchRating(ctx context.Context, courseID string) (float64, error) {
	url := os.Getenv("RATINGS_URL")
	if url == "" {
		return 4.5, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"?course_id="+courseID, nil)
	if err != nil {
		return 0, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close() //nolint:errcheck
	var out struct {
		Rating float64 `json:"rating"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	return out.Rating, nil
}

// pingAnalytics sends a best-effort analytics event. Errors are swallowed by the
// caller (fire-and-forget); ANALYTICS_URL unset → a no-op.
func pingAnalytics(ctx context.Context, event string) error {
	url := os.Getenv("ANALYTICS_URL")
	if url == "" {
		return nil
	}
	payload, _ := json.Marshal(map[string]string{"event": event})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	return nil
}
