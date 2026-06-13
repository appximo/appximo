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

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
