// Command fullstack is ONE BINARY that serves a frontend, an API, an admin panel
// and the API docs — the shape LOOSE-ENDS-SWEEP-S1 unlocked with
// Config.Static.
//
// Before it, the engine could only serve its OWN embedded UIs (/editor, /admin,
// /docs, /graphiql): a third-party SPA had to be deployed separately behind Caddy
// or a CDN, because a custom route must live under /api/ and runs inside a
// per-request tenant TRANSACTION — wrong for a .js file, which needs no database.
//
// What this binary answers, from one process and one deploy:
//
//	GET /                     the SPA shell        (no-cache — it names the bundles)
//	GET /assets/app-*.js|css  content-hashed       (immutable, cached for a year)
//	GET /orders/42            the SPA shell again  (client-side routing, SPA: true)
//	GET /api/tasks            the generated API    (JWT + RBAC + tenant, untouched)
//	GET /api/nope             a real 404           (never the shell — see below)
//	GET /admin, /editor, /docs, /graphiql, /healthz … the engine's own routes
//
// Headers worth asserting when verifying this live (LIBRARY-GAPS-S2, ENG-5 —
// and assert them with a BROWSER or an explicit header check, never a bare
// curl status: CSP failures render a blank page that still returns 200):
//
//	GET /            Content-Security-Policy: appitools.DefaultStaticCSP
//	                 (same-origin SPA policy — NOT the API's default-src 'none';
//	                 override per mount with StaticMount.CSP, disable with CSPOff)
//	GET /api/tasks   Content-Security-Policy: default-src 'none'; … (the API keeps its own)
//
// Run it:
//
//	DATABASE_URL=… JWT_SECRET=… ADMIN_KEY=… \
//	  go run ./examples/fullstack --schema examples/fullstack/schema.json
//
// In a real project `web/dist` is your `npm run build` output; commit the
// directory (or build it in CI before `go build`) so go:embed has something to
// embed — the same discipline the engine's own SPAs use.
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"

	"github.com/miguelangel/appitools"
)

// all: is required — Vite emits hashed chunks whose names the default go:embed
// pattern would skip (it ignores files starting with "_" or ".").
//
//go:embed all:web/dist
var frontend embed.FS

func main() {
	schemaPath := flag.String("schema", "examples/fullstack/schema.json", "path to schema.json")
	port := flag.Int("port", 8080, "HTTP port")
	flag.Parse()

	// fs.Sub strips the build directory so "/" maps to web/dist/index.html.
	dist, err := fs.Sub(frontend, "web/dist")
	if err != nil {
		log.Fatal(err)
	}

	app, err := appitools.New(appitools.Config{
		SchemaPath: *schemaPath,
		Port:       *port,
		Static: []appitools.StaticMount{{
			Path: "/",
			FS:   dist,
			// SPA: unmatched CLIENT routes (/orders/42) serve index.html so the
			// browser router can take over. It is opt-in because it is wrong for a
			// plain static site, and it never applies to a path the engine owns:
			// /api/nope stays a 404, not a 200 page.
			SPA: true,
		}},
	})
	if err != nil {
		log.Fatal(err)
	}

	// Custom API routes still work exactly as before — they live under /api/ and
	// get the tenant transaction; the static tree is a different surface entirely.
	if err := app.Register(appitools.Route{
		Method: "GET", Path: "/api/ping",
		Handler: func(ctx appitools.Ctx) error {
			return ctx.JSON(200, map[string]any{"pong": true, "tenant": ctx.Tenant()})
		},
	}); err != nil {
		log.Fatal(err)
	}

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
