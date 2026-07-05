package appitools

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/miguelangel/appitools/pkg/codegen"
	appmiddleware "github.com/miguelangel/appitools/pkg/middleware"
	"github.com/miguelangel/appitools/pkg/schema"
)

// swaggerUIHTML is a self-contained Swagger UI page (loaded from a pinned CDN, the
// same model as the GraphiQL playground) that renders the engine's generated spec
// at /openapi.json. It is served at GET /docs so any consumer can explore the API
// interactively. Single-origin "Try it out" calls hit /api on the same host.
const swaggerUIHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>API documentation — Appitools</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.17.14/swagger-ui.css">
  <style>body{margin:0}</style>
</head>
<body>
  <div id="swagger-ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.17.14/swagger-ui-bundle.js" crossorigin></script>
  <script>
    window.onload = function () {
      window.ui = SwaggerUIBundle({
        url: '/openapi.json',
        dom_id: '#swagger-ui',
        deepLinking: true,
        persistAuthorization: true,
      });
    };
  </script>
</body>
</html>`

// registerOpenAPIRoutes mounts the spec + Swagger UI routes on r. The spec is
// derived from the SURFACE schema (sch) — engine-global for that app, identical
// for every tenant — so it is served unauthenticated (the JWT skip list covers
// /openapi and /docs) and computed once per surface build. A hot-swap
// (MT-STRUCT-S4) rebuilds the router from the new schema, so /openapi.json and
// /docs reflect the new structure live. Server URL is "/" so a browser's "Try it
// out" calls the same origin the docs were loaded from.
func (a *App) registerOpenAPIRoutes(r chi.Router, sch *schema.APISchema) {
	jsonSpec, jerr := codegen.GenerateOpenAPIJSON(sch, "/")
	yamlSpec, yerr := codegen.GenerateOpenAPI(sch, "/")

	r.Get("/openapi.json", func(w http.ResponseWriter, req *http.Request) {
		if jerr != nil || len(jsonSpec) == 0 {
			http.Error(w, `{"error":"openapi generation failed"}`, http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Write(jsonSpec) //nolint:errcheck
	})
	r.Get("/openapi.yaml", func(w http.ResponseWriter, req *http.Request) {
		if yerr != nil || len(yamlSpec) == 0 {
			http.Error(w, "openapi generation failed", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
		w.Write(yamlSpec) //nolint:errcheck
	})
	docs := func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(swaggerUIHTML)) //nolint:errcheck
	}
	r.With(appmiddleware.PermissiveCSP).Get("/docs", docs)
}
