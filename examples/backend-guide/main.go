// Command backend-guide is the companion example to docs/BACKEND_SPEC_LLM.md —
// a complete, COMPILING Appitools backend built the library way (ADR-016): a
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
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/miguelangel/appitools"
)

// httpClient is shared by the outbound integrations. Give it a ceiling of its
// own; the per-call context (bounded by Route.Timeout) is the real deadline.
var httpClient = &http.Client{Timeout: 10 * time.Second}

func main() {
	schemaPath := flag.String("schema", "examples/backend-guide/schema.json", "path to schema.json")
	port := flag.Int("port", 8080, "HTTP port")
	flag.Parse()

	app, err := appitools.New(appitools.Config{
		SchemaPath: *schemaPath,
		Port:       *port,
		// DSN / JWTSecret / AdminKey / Env fall back to the standard env vars.
	})
	if err != nil {
		log.Fatal(err)
	}

	register(app, appitools.Route{
		// GET /api/ops/overview — an AUTHENTICATED, admin-scoped custom route.
		// Path-based RBAC authorizes the first segment ("ops") as a VIRTUAL
		// resource: a wildcard role (admin) reaches it; a restricted role is 403.
		// (End users read their OWN data through the GENERATED routes — GET
		// /api/students, /api/enrollments — which the student role owner-scopes by
		// user_id automatically; a custom route is for logic that spans resources,
		// like this cross-resource snapshot.) Inside, ctx.Query still enforces RBAC
		// on the REAL resources, so an admin sees every row.
		Method: "GET", Path: "/api/ops/overview", RequireRole: "admin",
		Handler: func(ctx appitools.Ctx) error {
			students, err := ctx.Query("students", appitools.QueryOpts{Limit: 1000})
			if err != nil {
				return ctx.Error(500, "students lookup failed", err)
			}
			enrollments, err := ctx.Query("enrollments", appitools.QueryOpts{Limit: 1000})
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

	register(app, appitools.Route{
		// POST /api/register — the Hotmart pattern: a PUBLIC (pre-auth) endpoint
		// that runs business logic and creates the user, related records and a
		// follow-up job ATOMICALLY in one transaction. Any failure rolls back
		// everything — including the user. This is the differential: no network
		// hop, one transaction, the engine's own validation + RBAC still in force.
		Method: "POST", Path: "/api/register", Public: true, Timeout: 15 * time.Second,
		Handler: func(ctx appitools.Ctx) error {
			var body struct {
				Email    string  `json:"email"`
				Password string  `json:"password"`
				FullName string  `json:"full_name"`
				License  string  `json:"license"`
				CourseID string  `json:"course_id"`
				Amount   float64 `json:"amount"`
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
			case errors.Is(err, appitools.ErrEmailTaken):
				return ctx.Error(409, "email already registered", err)
			case errors.Is(err, appitools.ErrInvalidEmail), errors.Is(err, appitools.ErrWeakPassword):
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
				`INSERT INTO enrollments (user_id, course_id, amount, external_ref) VALUES ($1, $2, $3, $4)`,
				user.ID, body.CourseID, body.Amount, "lic-"+body.License); err != nil {
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

	register(app, appitools.Route{
		// POST /api/reports/ratings — heavy fan-out. Enrich a list of courses with
		// their external rating IN PARALLEL, bounded and panic-safe. SafeParallel
		// caps concurrency (backpressure) and turns a task panic into an error, so
		// a bad provider response never crashes the process; Route.Timeout bounds
		// the whole batch. The tasks do EXTERNAL I/O only — never the handler tx
		// (a single connection is not safe for concurrent use).
		Method: "POST", Path: "/api/reports/ratings", RequireRole: "admin", Timeout: 8 * time.Second,
		Handler: func(ctx appitools.Ctx) error {
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
			if err := appitools.SafeParallel(ctx.Context(), 8, tasks...); err != nil {
				return ctx.Error(502, "ratings service failed", err)
			}
			out := make(map[string]float64, len(ratings))
			for i, id := range body.CourseIDs {
				out[id] = ratings[i]
			}
			return ctx.JSON(200, map[string]any{"ratings": out})
		},
	})

	register(app, appitools.Route{
		// POST /api/track — fire-and-forget. Record nothing durable; just ping an
		// external analytics service off the response path. SafeGo is the ONLY
		// sanctioned way to launch that goroutine: its panic is recovered + logged
		// + metered, never a process crash. The goroutine gets a fresh context (no
		// request values) with its own deadline, which fn must honor. This is
		// at-most-once — for durable work, Enqueue to the outbox instead.
		Method: "POST", Path: "/api/track", Public: true,
		Handler: func(ctx appitools.Ctx) error {
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

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}

// register aborts boot on a bad route — a registration error is a programming
// mistake, caught here, never at request time.
func register(app *appitools.App, rt appitools.Route) {
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
