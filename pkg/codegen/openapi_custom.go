package codegen

import (
	"sort"
	"strings"
)

// CustomRoute describes one custom endpoint a framework-mode backend registered
// with (*appximo.App).Register, for publication in the served OpenAPI document
// (ENG-33, THIRD-PARTY-READY-S1).
//
// Why this exists: before it, the whole custom half of an app's surface — which
// for a storefront is the whole PUBLIC half — was invisible to a third party.
// /openapi.json listed only the generated routes, and probing an unknown /api/
// path answers 401 (auth runs before routing), so an external agent could not
// distinguish "this endpoint does not exist" from "this endpoint wants a token".
// The engine KNOWS the registered routes at boot; this type is how it declares
// them in the contract.
//
// The split of responsibilities is deliberate and documented in the emitted
// operations themselves:
//   - The ENGINE knows (and publishes): method, path, whether the route is
//     Public (Bearer optional) or authenticated (Bearer + which RBAC
//     virtual-resource action it demands), an extra RequireRole if declared,
//     and whether the response is a byte stream (ByteServing).
//   - The AUTHOR may declare (optional): a one-line Summary
//     (appximo.Route.Description). Request/response SHAPES stay the author's
//     job — a Go handler has no declared schema, and inventing one here would
//     publish a guess as a contract. The app's contract sheet (backend-spec
//     §3.6b) remains the authority for shapes; the OpenAPI is the authority for
//     EXISTENCE.
type CustomRoute struct {
	Method      string // uppercased HTTP method (GET/POST/PUT/PATCH/DELETE)
	Path        string // literal route path, e.g. "/api/checkout"
	Summary     string // optional one-liner from Route.Description ("" = generic)
	Public      bool   // Route.Public — no token required, valid token recognized
	RequireRole string // Route.RequireRole — extra role equality demanded by the route
	ByteServing bool   // Route.ByteServing — response is a byte stream (Ctx.ServeFile)
}

// addOACustomPaths merges the registered custom routes into paths. Runs AFTER
// the generated resources (Register rejects a path whose first segment names a
// schema resource, so a custom route can never overwrite a generated path item;
// two custom routes on one path with different methods merge into one item).
func addOACustomPaths(paths map[string]any, routes []CustomRoute) {
	// Deterministic emission order (the map itself is unordered, but building in
	// a fixed order keeps behavior reproducible if two routes ever collide).
	sorted := append([]CustomRoute(nil), routes...)
	sort.Slice(sorted, func(i, j int) bool {
		if sorted[i].Path != sorted[j].Path {
			return sorted[i].Path < sorted[j].Path
		}
		return sorted[i].Method < sorted[j].Method
	})
	for _, rt := range sorted {
		item, _ := paths[rt.Path].(map[string]any)
		if item == nil {
			item = map[string]any{}
		}
		item[strings.ToLower(rt.Method)] = oaCustomRouteOp(rt)
		paths[rt.Path] = item
	}
}

// oaCustomRouteOp builds the OpenAPI operation for one registered custom route:
// everything the engine can honestly assert, and nothing it would have to
// invent. Summary comes from Route.Description when the author set one.
func oaCustomRouteOp(rt CustomRoute) map[string]any {
	seg := customRouteSegment(rt.Path)

	summary := rt.Summary
	if summary == "" {
		summary = "Custom endpoint registered by the application"
	}

	var desc strings.Builder
	desc.WriteString("Custom route (application-registered, not schema-generated). ")
	if rt.Public {
		desc.WriteString("PUBLIC: no token required; a valid Bearer is recognized (identity as input), an invalid one is 401. ")
	} else {
		desc.WriteString("Requires a Bearer JWT; RBAC authorizes it as virtual resource \"" + seg + "\" with action \"" + actionForOAMethod(rt.Method) + "\" (grant it via the role's `routes` block). ")
		if rt.RequireRole != "" {
			desc.WriteString("Additionally requires role \"" + rt.RequireRole + "\". ")
		}
	}
	if rt.ByteServing {
		desc.WriteString("Responds with a byte stream (Range and If-None-Match honored). ")
	}
	desc.WriteString("Request/response shapes are defined by the application handler — the app's contract sheet is the authority for shapes; this document is the authority for existence.")

	op := map[string]any{
		"operationId":            customOpID(rt.Method, rt.Path),
		"summary":                summary,
		"description":            desc.String(),
		"tags":                   []string{seg},
		"x-appximo-custom-route": true,
	}
	if rt.Public {
		op["security"] = []any{} // overrides the global bearerAuth requirement
		op["x-public"] = true
	}
	if rt.RequireRole != "" {
		op["x-required-role"] = rt.RequireRole
	}
	if rt.ByteServing {
		op["x-byte-serving"] = true
	}
	if params := oaCustomPathParams(rt.Path); len(params) > 0 {
		op["parameters"] = params
	}

	responses := map[string]any{
		"default": map[string]any{"description": "Defined by the application handler"},
	}
	if rt.ByteServing {
		responses["200"] = map[string]any{"description": "The bytes", "content": map[string]any{
			"application/octet-stream": map[string]any{"schema": map[string]any{"type": "string", "format": "binary"}},
		}}
		responses["206"] = map[string]any{"description": "Partial content (Range request)"}
		responses["304"] = map[string]any{"description": "Not modified (If-None-Match matched the content ETag)"}
	}
	if !rt.Public {
		responses["401"] = oaRespRef("Error401")
		responses["403"] = oaRespRef("Error403")
	}
	op["responses"] = responses
	return op
}

// oaCustomPathParams turns chi {param} segments into OpenAPI path parameters
// (type string — the engine knows the name, never the semantic type).
func oaCustomPathParams(path string) []any {
	var out []any
	for _, seg := range strings.Split(path, "/") {
		if len(seg) > 2 && strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			out = append(out, map[string]any{
				"name": seg[1 : len(seg)-1], "in": "path", "required": true,
				"schema": map[string]any{"type": "string"},
			})
		}
	}
	return out
}

// customRouteSegment is the first path element after /api/ — the RBAC virtual
// resource (same derivation the middleware uses at request time).
func customRouteSegment(path string) string {
	rest := strings.TrimPrefix(path, "/api/")
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		return rest[:i]
	}
	return rest
}

// actionForOAMethod mirrors rbac.actionFromMethod for the description text.
func actionForOAMethod(method string) string {
	switch strings.ToUpper(method) {
	case "GET", "HEAD":
		return "read"
	case "POST":
		return "create"
	case "PUT", "PATCH":
		return "update"
	case "DELETE":
		return "delete"
	default:
		return "read"
	}
}

// customOpID derives a stable operationId from method+path:
// GET /api/catalogo-imagen → getApiCatalogoImagen.
func customOpID(method, path string) string {
	var b strings.Builder
	b.WriteString(strings.ToLower(method))
	upper := true
	for _, r := range path {
		switch {
		case r == '/' || r == '-' || r == '_' || r == '{' || r == '}' || r == '.':
			upper = true
		case upper:
			b.WriteString(strings.ToUpper(string(r)))
			upper = false
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
