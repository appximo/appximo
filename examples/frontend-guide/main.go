// Command frontend-guide is the runnable companion to docs/FRONTEND_SPEC_LLM.md
// (`appitools frontend-spec`): ONE binary serving a frontend + the generated
// API + one custom byte-serving route, exercising every pattern the spec
// teaches —
//
//   - Config.Static:   a no-build vanilla SPA (web/) embedded and served at "/"
//   - auth:            signup/login from the SPA (enable signup with
//     APPITOOLS_AUTH_SIGNUP_ROLE=editor), the 401-bounce, expiry
//   - generated CRUD:  posts list/create with the multi-field 422 mapped to
//     inputs and the screen states (loading/empty/error+retry)
//   - files:           upload with XHR progress → attach via the `photo` file
//     field → display through a short-lived signed URL (authenticated) and
//     through the PUBLIC route below (anonymous)
//   - Ctx.ServeFile:   /api/public-photo — the storefront-image pattern: a
//     Public + ByteServing route that authorizes by relationship ("the photo
//     of a PUBLISHED post") and streams with Range/ETag/sendfile
//
// The SPA is deliberately plain JS with no build step so this example runs
// from a bare clone (`go run ./examples/frontend-guide`) — a real product
// would use SvelteKit + adapter-static exactly as the spec §2 recommends; the
// contract exercised here is identical.
//
// Run it:
//
//	DATABASE_URL=… JWT_SECRET=… ADMIN_KEY=… APPITOOLS_AUTH_SIGNUP_ROLE=editor \
//	  go run ./examples/frontend-guide --schema examples/frontend-guide/schema.json
//
// then register a tenant on the control plane and open http://<tenant>.localhost:8080/.
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"

	"github.com/miguelangel/appitools"
)

//go:embed all:web
var frontend embed.FS

func main() {
	schemaPath := flag.String("schema", "examples/frontend-guide/schema.json", "path to schema.json")
	port := flag.Int("port", 8080, "HTTP port")
	flag.Parse()

	dist, err := fs.Sub(frontend, "web")
	if err != nil {
		log.Fatal(err)
	}

	app, err := appitools.New(appitools.Config{
		SchemaPath: *schemaPath,
		Port:       *port,
		Static: []appitools.StaticMount{{
			Path: "/",
			FS:   dist,
			SPA:  true,
		}},
	})
	if err != nil {
		log.Fatal(err)
	}

	// The PUBLIC image pattern (spec §7.5): the engine's file routes are
	// authenticated by design; a storefront exposes a file ONLY by relationship.
	// Here: the photo of a PUBLISHED post is public; a draft's photo, or any
	// unattached upload, stays 404 to the world. ByteServing routes the stream
	// around the response cache and compression (Range/ETag/sendfile intact);
	// the generous RateLimit is the read-sized budget a public image route
	// needs (the 5 rps public default would trip on one page of thumbnails).
	if err := app.Register(appitools.Route{
		Method: "GET", Path: "/api/public-photo",
		Public:      true,
		ByteServing: true,
		RateLimit:   &appitools.RateLimit{RPS: 200, Burst: 400},
		Handler: func(ctx appitools.Ctx) error {
			id := ctx.Request().URL.Query().Get("id")
			var ok bool
			if err := ctx.UnsafeTx().QueryRow(ctx.Context(),
				`SELECT EXISTS(SELECT 1 FROM posts WHERE photo = $1 AND published)`, id).Scan(&ok); err != nil || !ok {
				return ctx.Error(404, "not found", err) // uniform miss — no oracle
			}
			return ctx.ServeFile(id)
		},
	}); err != nil {
		log.Fatal(err)
	}

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
