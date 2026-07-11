// Command custom-handler is the canonical example of the ADR-016 library model:
// import appitools, register a Class-1 custom handler, compile a single static
// CGO-free binary. It is the SAME program as the pure `appitools serve` binary
// plus one registered route — not a different runtime mode.
//
// The handler below is the echo endpoint that used to live inline in
// cmd_serve.go. It now demonstrates the real library surface: a Handler that
// receives an appitools.Ctx with the tenant + transaction already resolved,
// binds the body, enqueues an outbox job IN THE SAME TRANSACTION via
// ctx.Enqueue, and returns the event id. If the handler returns an error the
// transaction (and the enqueue) roll back atomically.
//
// Run it like the pure binary (all env vars required):
//
//	DATABASE_URL=... JWT_SECRET=... ADMIN_KEY=... \
//	  go run ./examples/custom-handler --schema examples/quickstart/schema.json
package main

import (
	"errors"
	"flag"
	"log"

	"github.com/miguelangel/appitools"
)

func main() {
	schemaPath := flag.String("schema", "examples/quickstart/schema.json", "path to schema.json")
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

	// POST /api/_echo — Class-1 handler. Goes through the full middleware chain
	// (tenant → rate limit → JWT → RBAC) exactly like a generated route; the
	// transaction handed to the handler is already scoped to the tenant
	// search_path. "_echo" is not a schema resource, so it does not collide.
	if err := app.Register(appitools.Route{
		Method: "POST",
		Path:   "/api/_echo",
		Handler: func(ctx appitools.Ctx) error {
			var body struct {
				Msg string `json:"msg"`
			}
			if err := ctx.Bind(&body); err != nil {
				return ctx.Error(400, "invalid body", err)
			}
			eventID, err := ctx.Enqueue("echo.test", map[string]any{"msg": body.Msg})
			if err != nil {
				return ctx.Error(500, "enqueue failed", err)
			}
			// Returning nil commits the transaction; the pg_notify fired by
			// Enqueue is delivered only on that commit, then this body flushes.
			return ctx.JSON(200, map[string]any{"event_id": eventID})
		},
	}); err != nil {
		log.Fatal(err)
	}

	// POST /api/_register — the CUSTOM REGISTRATION canon (LIBRARY-EXTEND-S1):
	// a PUBLIC route (no Bearer — the caller does not exist yet) that runs
	// arbitrary business logic and creates the user, atomically, in-process.
	// This is the Hotmart-style flow: validate against an external system →
	// check business data → create the identity → create related records →
	// enqueue follow-up work, ALL in one transaction (any failure rolls back
	// everything, the user included).
	//
	// Security posture of a Public route, in order:
	//   1. tenant still resolves from the Host (isolation holds);
	//   2. the shared per-tenant limiter already ran;
	//   3. a DEDICATED tighter limiter (per tenant+IP, default 5 rps/burst 10)
	//      ran before this handler — a burst gets 429, never the pool;
	//   4. Claims() is zero — the RBAC-aware helpers fail closed. Every input
	//      below is OUR responsibility to validate: the role is hardcoded
	//      (never from the request), and CreateUser re-checks it against the
	//      schema RBAC, normalizes the email, and enforces the password rules.
	if err := app.Register(appitools.Route{
		Method: "POST",
		Path:   "/api/_register",
		Public: true,
		Handler: func(ctx appitools.Ctx) error {
			var body struct {
				Email    string `json:"email"`
				Password string `json:"password"`
				License  string `json:"license"`
			}
			if err := ctx.Bind(&body); err != nil {
				return ctx.Error(400, "invalid body", err)
			}
			// Business gate — in a real flow this is the outbound call to the
			// external platform (plain Go: http.Post to its API, no sandbox).
			// Simulated here so the example runs standalone.
			if len(body.License) < 8 {
				return ctx.Error(403, "license not valid", nil)
			}
			// The role comes from THIS code, never from the caller; an empty
			// Password would create an invitation-style user (no password
			// login until reset) — the OTP/invite variant of this same flow.
			user, err := ctx.CreateUser(body.Email, body.Password, "viewer")
			switch {
			case err == nil:
			case errors.Is(err, appitools.ErrEmailTaken):
				return ctx.Error(409, "email already registered", err)
			case errors.Is(err, appitools.ErrInvalidEmail), errors.Is(err, appitools.ErrWeakPassword):
				return ctx.Error(422, err.Error(), err)
			default:
				return ctx.Error(500, "registration failed", err)
			}
			// Related record in the SAME tx. The RBAC-aware Insert would deny
			// (no identity on a public route), so this write is a deliberate,
			// greppable UnsafeTx — tenant isolation still holds; the handler
			// owns the validation of what it writes.
			if _, err := ctx.UnsafeTx().Exec(ctx.Context(),
				`INSERT INTO tasks (title, status) VALUES ($1, 'open')`,
				"onboard "+user.Email); err != nil {
				return ctx.Error(500, "profile creation failed", err)
			}
			// Follow-up work (welcome email, external sync) — atomic with the
			// user: if anything above had failed, this event would not exist.
			if _, err := ctx.Enqueue("user.registered", map[string]any{
				"user_id": user.ID, "email": user.Email,
			}); err != nil {
				return ctx.Error(500, "enqueue failed", err)
			}
			return ctx.JSON(201, map[string]any{
				"user_id": user.ID, "email": user.Email, "role": user.Role,
			})
		},
	}); err != nil {
		log.Fatal(err)
	}

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
