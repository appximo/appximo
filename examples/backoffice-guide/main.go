// Command backoffice-guide is the runnable companion to
// docs/BACKOFFICE_SPEC_LLM.md (`appximo backoffice-spec`): ONE binary serving
// a back-office CRUD UI generated ENTIRELY from /openapi.json at runtime —
// zero resource-specific screens, zero hardcoded domain knowledge. The
// vanilla SPA in web/ implements every section of the spec:
//
//   - the contract reader (web/contract.js): resources, fields, buttons,
//     x-appximo-relation/references, x-appximo-file(+policy),
//     x-appximo-initial/transitions, x-appximo-virtual-resources
//   - permissions by probing (a 403 dims the resource, no role matrix)
//   - the generic form with the five rules (omit empty on create, PATCH
//     partial, null clears, paint the whole 422, offer only legal state moves)
//   - relation selectors that send row[x-appximo-references] — the FE5 fix
//   - a file field with upload→attach and the declared policy shown
//
// The schema (schema.json) deliberately exercises the hard cases: a FK whose
// references is user_id (the $user_id RBAC pattern), a file field with an
// attach policy, a 5-state machine with two terminal states, and a role
// (recepcion) that reaches only part of the surface.
//
// Run it:
//
//	DATABASE_URL=… JWT_SECRET=… ADMIN_KEY=… APPXIMO_AUTH_SIGNUP_ROLE=admin \
//	  go run ./examples/backoffice-guide --schema examples/backoffice-guide/schema.json
//
// then register a tenant on the control plane, sign up from the UI, and open
// http://<tenant>.localhost:8080/.
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"

	"github.com/appximo/appximo"
)

//go:embed all:web
var ui embed.FS

func main() {
	schemaPath := flag.String("schema", "examples/backoffice-guide/schema.json", "path to schema.json")
	port := flag.Int("port", 8080, "HTTP port")
	flag.Parse()

	dist, err := fs.Sub(ui, "web")
	if err != nil {
		log.Fatal(err)
	}
	app, err := appximo.New(appximo.Config{
		SchemaPath: *schemaPath,
		Port:       *port,
		Static: []appximo.StaticMount{{
			Path: "/",
			FS:   dist,
			SPA:  true,
		}},
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}
