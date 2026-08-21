package graphql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/codegen"
	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/pkg/events"
	"github.com/appximo/appximo/pkg/extensions"
	pkghandlers "github.com/appximo/appximo/pkg/handlers"
	"github.com/appximo/appximo/pkg/query"
	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/tenant"
	"github.com/google/uuid"
	gql "github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"
	"github.com/graphql-go/graphql/language/parser"
	"github.com/graphql-go/graphql/language/source"
	"github.com/jackc/pgx/v5"
)

type httpReqKey struct{}

// rbacResultFilterKey is the per-request context key for RBAC field scrubbing.
type rbacResultFilterKey struct{}

// rbacResultFilter stores per-resolver allowed-field lists so that BuildHandler
// can strip forbidden fields from the graphql-go result before writing it.
// graphql-go resolves every requested field (returning nil for missing keys),
// which would otherwise leak forbidden columns as JSON null.
type rbacResultFilter struct {
	mu     sync.Mutex
	fields map[string][]string // GQL query field name → allowed DB columns
}

func (f *rbacResultFilter) store(gqlField string, allowed []string) {
	f.mu.Lock()
	f.fields[gqlField] = allowed
	f.mu.Unlock()
}

// BuildHandler constructs an http.Handler for the /graphql endpoint.
// Callers must ensure tenant.TenantMiddleware runs before this handler.
//
// allowIntrospection gates the __schema/__type introspection fields (needed by
// ANY schema explorer — GraphiQL, Apollo Sandbox, codegen tools) — an explicit
// parameter, not read from os.Getenv here, so a caller compiling several
// engine instances in one process (the in-process fleet, MT-STRUCT-S3) can
// resolve it PER APP from that app's own env/Config, not the process-wide
// env (GRAPHQL-EXPLORER-S1 — see app.go buildRouter and multiapp.go
// buildFleetApp for the resolution: Env=="development" or the explicit
// APPXIMO_GRAPHQL_PLAYGROUND opt-in).
func BuildHandler(s *schema.APISchema, tdb *db.TenantDB, hr *extensions.HookRunner, policy *rbac.Policy, hub *events.Hub, allowIntrospection bool) http.Handler {
	gqlSchema := buildGQLSchema(s, tdb, hr, policy, hub)
	isDev := allowIntrospection
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// FIX 8: cap request body at 1 MB to prevent OOM from oversized payloads.
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

		filterStore := &rbacResultFilter{fields: make(map[string][]string)}
		ctx := context.WithValue(r.Context(), httpReqKey{}, r)
		ctx = context.WithValue(ctx, rbacResultFilterKey{}, filterStore)

		var params struct {
			Query         string         `json:"query"`
			Variables     map[string]any `json:"variables"`
			OperationName string         `json:"operationName"`
		}
		if r.Method == http.MethodGet {
			params.Query = r.URL.Query().Get("query")
			params.OperationName = r.URL.Query().Get("operationName")
		} else {
			if err := json.NewDecoder(r.Body).Decode(&params); err != nil {
				// The comment here used to say "MaxBytesReader sets a specific
				// error when the limit is exceeded" — and then never looked at
				// err, so EVERY decode failure was answered 413 "request body
				// too large". Measured before the fix: an 18-byte malformed body
				// got an oversize-body error, sending the caller to raise a limit
				// that was never the problem (ADR-024). Now the error is asked
				// which of the two it is.
				status, msg := http.StatusBadRequest, "invalid JSON body"
				var tooLarge *http.MaxBytesError
				if errors.As(err, &tooLarge) {
					status, msg = http.StatusRequestEntityTooLarge, "request body too large"
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				json.NewEncoder(w).Encode(map[string]string{"error": msg}) //nolint:errcheck
				return
			}
		}

		// Analyze the query (single AST parse) for: introspection (blocked outside
		// dev), runaway complexity / alias amplification (1 rate-limited request must
		// not fan out into thousands of concurrent resolvers + DB queries), and
		// state-changing operations over GET. Reject with a safe message.
		if reason, ok := analyzeQuery(params.Query, r.Method == http.MethodGet, isDev); !ok {
			writeGraphQLError(w, reason)
			return
		}

		result := gql.Do(gql.Params{
			Schema:         gqlSchema,
			RequestString:  params.Query,
			VariableValues: params.Variables,
			OperationName:  params.OperationName,
			Context:        ctx,
		})
		// Strip fields that RBAC disallows. graphql-go resolves every requested
		// field (returning nil for missing map keys), which would otherwise
		// serialize as "secret": null even after FilterFields.
		scrubResultByRBAC(result, filterStore)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(result) //nolint:errcheck
	})
}

// scrubResultByRBAC removes forbidden fields from graphql-go's serialized result.
// graphql-go resolves every requested field against the source map and sets
// absent keys to nil (→ JSON null). This post-processes the result.Data map
// to delete those nil entries for fields the resolver already filtered out.
func scrubResultByRBAC(result *gql.Result, store *rbacResultFilter) {
	if result == nil || result.Data == nil {
		return
	}
	data, ok := result.Data.(map[string]any)
	if !ok {
		return
	}
	store.mu.Lock()
	filters := make(map[string][]string, len(store.fields))
	for k, v := range store.fields {
		filters[k] = v
	}
	store.mu.Unlock()

	for fieldName, allowed := range filters {
		if val, exists := data[fieldName]; exists {
			data[fieldName] = applyAllowedFieldsToResult(val, allowed)
		}
	}
}

func applyAllowedFieldsToResult(val any, allowed []string) any {
	if len(allowed) == 0 {
		return val
	}
	allowedSet := make(map[string]bool, len(allowed)+1)
	allowedSet["id"] = true
	for _, f := range allowed {
		allowedSet[f] = true
	}
	scrub := func(m map[string]any) {
		for k := range m {
			if !allowedSet[k] {
				delete(m, k)
			}
		}
	}
	switch v := val.(type) {
	case map[string]any:
		if items, ok := v["data"].([]any); ok {
			// Connection type (list resolver) — scrub each item.
			for _, item := range items {
				if m, ok := item.(map[string]any); ok {
					scrub(m)
				}
			}
		} else {
			// Direct object (getByID resolver).
			scrub(v)
		}
	}
	return val
}

// Query guard limits. Legitimate queries are tiny; these bound a single request's
// fan-out so it cannot turn one rate-limited HTTP request into thousands of
// concurrent resolvers/DB queries (graphql-go resolves sibling fields in parallel).
const (
	maxRootSelections  = 50   // top-level fields/aliases per operation
	maxTotalSelections = 2000 // total selections across the whole document
)

// analyzeQuery parses the GraphQL query once and enforces three policies, returning
// a client-safe rejection reason (or ok=true to proceed):
//   - introspection (__schema/__type, not __typename) is blocked outside development;
//   - root/total selection counts are bounded (alias-amplification DoS guard);
//   - mutations are rejected on GET (state changes must use POST).
//
// A syntactically invalid query is allowed through so gql.Do returns the precise
// parser error to the client (matching prior behaviour).
func analyzeQuery(queryStr string, isGet, isDev bool) (string, bool) {
	if queryStr == "" {
		return "", true
	}
	doc, err := parser.Parse(parser.ParseParams{
		Source: source.NewSource(&source.Source{Body: []byte(queryStr), Name: "GraphQL"}),
	})
	if err != nil {
		return "", true
	}

	introspection := false
	// Detect introspection by FIELD NAME anywhere in the document. The "__" prefix is
	// reserved, so __schema/__type can only be introspection (and __typename, which we
	// deliberately allow). Walking every selection — not just operation roots — closes
	// the fragment-spread bypass (`query{...f} fragment f on Query{__schema{…}}`).
	//
	// COUNTING (ENG-28): a fragment's cost is charged AT EVERY SPREAD SITE, not
	// once globally. The old counter walked each fragment's body once, so a
	// document it counted at exactly 2000 — the advertised cap — resolved a
	// measured ~92,500 schema-level selections (~46×, 21.4 MB in ~2 s): the
	// executor expands the fragment at every DISTINCT root alias, and 50 aliases
	// × one 40-field fragment cost the analyzer 40, not 2000. (Repeating the
	// SAME spread — `{...F ...F}` — does NOT amplify: the executor merges by
	// response key. Charging per spread therefore over-counts that shape, which
	// is the safe direction: counted >= resolved, so the advertised cap holds.)
	frags := map[string]*ast.FragmentDefinition{}
	for _, def := range doc.Definitions {
		if fd, ok := def.(*ast.FragmentDefinition); ok && fd.Name != nil {
			frags[fd.Name.Value] = fd
		}
	}
	fragCost := make(map[string]int, len(frags))
	visiting := map[string]bool{}
	var costOf func(name string) int
	var walkCost func(ss *ast.SelectionSet) int
	walkCost = func(ss *ast.SelectionSet) int {
		if ss == nil {
			return 0
		}
		c := 0
		for _, sel := range ss.Selections {
			c++
			switch s := sel.(type) {
			case *ast.Field:
				if s.Name != nil && (s.Name.Value == "__schema" || s.Name.Value == "__type") {
					introspection = true
				}
				c += walkCost(s.GetSelectionSet())
			case *ast.FragmentSpread:
				if s.Name != nil {
					c += costOf(s.Name.Value)
				}
			default: // inline fragment
				c += walkCost(sel.GetSelectionSet())
			}
		}
		return c
	}
	costOf = func(name string) int {
		if c, done := fragCost[name]; done {
			return c
		}
		frag, ok := frags[name]
		if !ok || visiting[name] {
			// Unknown fragment or a spread cycle — both are validation errors
			// the executor reports precisely; cost 0 lets that error surface.
			return 0
		}
		visiting[name] = true
		c := walkCost(frag.SelectionSet)
		delete(visiting, name)
		fragCost[name] = c
		return c
	}

	total := 0
	for _, def := range doc.Definitions {
		if d, ok := def.(*ast.OperationDefinition); ok {
			if isGet && d.Operation == "mutation" {
				return "mutations must use POST", false
			}
			if d.SelectionSet != nil && len(d.SelectionSet.Selections) > maxRootSelections {
				return "query too complex", false
			}
			total += walkCost(d.SelectionSet)
		}
	}
	// Fragments nothing spreads cost the executor nothing, but are still walked
	// so introspection hidden in an unused fragment stays detected (and so the
	// old counter's coverage is not reduced).
	for name := range frags {
		costOf(name)
	}
	if total > maxTotalSelections {
		return "query too complex", false
	}
	if introspection && !isDev {
		return "introspection disabled in production", false
	}
	return "", true
}

// validationError carries declarative-validation violations (S44) into the
// GraphQL errors array. It implements gqlerrors.ExtendedError, so the response
// mirrors REST's 422 contract in GraphQL form:
//
//	{"errors":[{"message":"validation_failed","extensions":{"fields":[{field,rule,message}...]}}]}
type validationError struct {
	fields []schema.FieldRuleError
}

func (e *validationError) Error() string { return "validation_failed" }

func (e *validationError) Extensions() map[string]any {
	return map[string]any{"fields": e.fields}
}

// safeDBErr maps a database-layer error to a client-safe GraphQL error so internal
// details (schema/table/column names, raw SQL, SQLSTATE) are never serialized into
// the GraphQL errors array. It is the GraphQL rendering of the ONE classifier
// (handlers.ClassifyWriteError, ENG-42) — the same ladder REST, the batch
// transaction and Ctx.Insert/Update render from. Always returns a safe message —
// never the original error.
func safeDBErr(err error) error {
	switch v := pkghandlers.ClassifyWriteError(err); v.Kind {
	case pkghandlers.WriteErrUnique:
		// A unique-constraint collision is a clean conflict (G6), identical on
		// create and update — not a masked DB error.
		return fmt.Errorf("field %q: value already exists", v.Field)
	case pkghandlers.WriteErrFileRef:
		// A `file` field referencing no file of the tenant (FILES-LINK-S1) → the
		// same field-addressed validation_failed shape REST answers with 422, so
		// both APIs present a bad file reference as input validation on that field.
		return &validationError{fields: []schema.FieldRuleError{
			{Field: v.Field, Rule: "file_not_found", Message: pkghandlers.FileRefMessage},
		}}
	case pkghandlers.WriteErrForeignKey:
		// Foreign-key violation → a clear referential message (MIG-F1-S1), mirroring
		// the REST 409 so a RESTRICT delete / bad reference is never a masked
		// "internal error".
		return fmt.Errorf("%s", v.Message)
	case pkghandlers.WriteErrMissingTenant:
		return fmt.Errorf("invalid tenant")
	case pkghandlers.WriteErrBadInput:
		return fmt.Errorf("invalid request")
	case pkghandlers.WriteErrUnavailable:
		return fmt.Errorf("service unavailable")
	default:
		// WriteErrUnknownColumn deliberately lands here: the GraphQL input types
		// are boot-compiled, so an unknown field is rejected at parse and a 42703
		// reaching this point is an engine fault, not client input — masked, as
		// it always was on this surface.
		return fmt.Errorf("internal error")
	}
}

func writeGraphQLError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"errors": []map[string]any{{"message": msg}},
	})
}

// PlaygroundHandler serves GraphiQL — the standard visual GraphQL explorer
// (schema browser, introspection-driven autocomplete, run queries/mutations
// in place, a Headers editor for testing with a real Authorization token) —
// mounted at /graphiql alongside REST's Swagger UI at /docs (API-PRODUCTIVA-V1),
// only when BuildHandler's introspection gate is open (dev, or the
// APPXIMO_GRAPHQL_PLAYGROUND opt-in) — GraphiQL is unusable without it.
//
// The CDN build is version-PINNED (same discipline as openapi_serve.go's
// Swagger UI), not left to resolve "latest": GraphiQL dropped its standalone
// UMD bundle after 3.9.0 (4.x+ is ESM/import-map only), so an unpinned
// unpkg.com/graphiql/graphiql.min.js 404s — verified live. 3.9.0 is the last
// version shipping graphiql.min.js.
func PlaygroundHandler(endpoint string) http.Handler {
	// Every request to /graphql — including the schema-introspection fetch
	// GraphiQL itself makes on load to populate the Explorer/autocomplete — goes
	// through the SAME JWT+RBAC chain as any other request (deny by default;
	// unlike a public GraphQL API, there is no anonymous introspection). So the
	// Headers tab starts EMPTY, not pre-filled with a fake "Bearer <token>"
	// value: a literal placeholder would be sent as a real header on that very
	// first fetch and fail with a confusing "token is malformed" — a live
	// finding. The default QUERY comment explains what to paste instead, and
	// the engine's own "missing token" error on the first (unauthenticated) run
	// is the honest, actionable signal.
	defaultQuery := `# Paste a JWT into the Headers tab below to authenticate:
#   { "Authorization": "Bearer <token>" }
# Mint one with: appximo token --secret "$JWT_SECRET" --tenant <id> --role <role>
#
# Then explore the schema (left panel) and run a query, e.g.:
#
# { __typename }
`
	page := fmt.Sprintf(`<!DOCTYPE html><html><head>
<title>GraphiQL — Appximo</title>
<link rel="stylesheet" href="https://unpkg.com/graphiql@3.9.0/graphiql.min.css"/>
</head><body style="margin:0"><div id="graphiql" style="height:100vh"></div>
<script src="https://unpkg.com/react@18.3.1/umd/react.production.min.js"></script>
<script src="https://unpkg.com/react-dom@18.3.1/umd/react-dom.production.min.js"></script>
<script src="https://unpkg.com/graphiql@3.9.0/graphiql.min.js"></script>
<script>
ReactDOM.render(
  React.createElement(GraphiQL,{
    fetcher: GraphiQL.createFetcher({url:%q}),
    headerEditorEnabled: true,
    shouldPersistHeaders: true,
    defaultQuery: %q
  }),
  document.getElementById('graphiql')
);
</script></body></html>`, endpoint, defaultQuery)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, page)
	})
}

// ── schema builder ────────────────────────────────────────────────────────────

func buildGQLSchema(s *schema.APISchema, tdb *db.TenantDB, hr *extensions.HookRunner, policy *rbac.Policy, hub *events.Hub) gql.Schema {
	// Shared scalar/enum/input types — created once per schema instance.
	orderDir := gql.NewEnum(gql.EnumConfig{
		Name: "OrderDirection",
		Values: gql.EnumValueConfigMap{
			"ASC":  &gql.EnumValueConfig{Value: "asc"},
			"DESC": &gql.EnumValueConfig{Value: "desc"},
		},
	})
	// is_null (SCHEMA-6): REST parity — true → IS NULL, false → IS NOT NULL.
	// Present on every filter input; the query builder still rejects it on a
	// `required` (NOT NULL) field with the same named error as REST.
	isNullField := &gql.InputObjectFieldConfig{Type: gql.Boolean, Description: "true → IS NULL, false → IS NOT NULL. Rejected on a required (NOT NULL) field."}
	stringFilter := gql.NewInputObject(gql.InputObjectConfig{
		Name: "StringFilter",
		Fields: gql.InputObjectConfigFieldMap{
			"exact":   &gql.InputObjectFieldConfig{Type: gql.String},
			"partial": &gql.InputObjectFieldConfig{Type: gql.String},
			"start":   &gql.InputObjectFieldConfig{Type: gql.String},
			"is_null": isNullField,
		},
	})
	dateFilter := gql.NewInputObject(gql.InputObjectConfig{
		Name: "DateFilter",
		Fields: gql.InputObjectConfigFieldMap{
			"after":   &gql.InputObjectFieldConfig{Type: gql.String},
			"before":  &gql.InputObjectFieldConfig{Type: gql.String},
			"gte":     &gql.InputObjectFieldConfig{Type: gql.String},
			"lte":     &gql.InputObjectFieldConfig{Type: gql.String},
			"is_null": isNullField,
		},
	})
	rangeFilter := gql.NewInputObject(gql.InputObjectConfig{
		Name: "RangeFilter",
		Fields: gql.InputObjectConfigFieldMap{
			"gte":     &gql.InputObjectFieldConfig{Type: gql.Float},
			"lte":     &gql.InputObjectFieldConfig{Type: gql.Float},
			"is_null": isNullField,
		},
	})
	// NullFilter carries is_null for the types that have no other filter input
	// in GraphQL (uuid, bool, file, json, jsonb). Their eq filtering remains
	// REST-only — a pre-existing gap this deliberately does not widen.
	nullFilter := gql.NewInputObject(gql.InputObjectConfig{
		Name: "NullFilter",
		Fields: gql.InputObjectConfigFieldMap{
			"is_null": isNullField,
		},
	})
	// total / total_pages are LAZY (SEC-AUDIT-V2 Hallazgo C): their resolvers run
	// the COUNT(*) only when the field is actually selected, so a list query that
	// does not ask for the total pays no COUNT (consistent with REST's opt-in
	// ?count=true, and cheaper). A client that DOES select total still gets it
	// (no breakage). graphql-go invokes a field resolver iff the field is in the
	// selection — fragment-correct, no AST walking needed.
	pageMeta := gql.NewObject(gql.ObjectConfig{
		Name: "PageMeta",
		Fields: gql.Fields{
			"page":        &gql.Field{Type: gql.NewNonNull(gql.Int)},
			"per_page":    &gql.Field{Type: gql.NewNonNull(gql.Int)},
			"total":       &gql.Field{Type: gql.NewNonNull(gql.Int), Resolve: resolveTotal},
			"total_pages": &gql.Field{Type: gql.NewNonNull(gql.Int), Resolve: resolveTotalPages},
			"has_next":    &gql.Field{Type: gql.NewNonNull(gql.Boolean)},
			"has_prev":    &gql.Field{Type: gql.NewNonNull(gql.Boolean)},
		},
	})
	pageLinks := gql.NewObject(gql.ObjectConfig{
		Name: "PageLinks",
		Fields: gql.Fields{
			"self":  &gql.Field{Type: gql.String},
			"first": &gql.Field{Type: gql.String},
			"last":  &gql.Field{Type: gql.String, Resolve: resolveLastLink}, // lazy: needs the COUNT
			"next":  &gql.Field{Type: gql.String},
			"prev":  &gql.Field{Type: gql.String},
		},
	})

	// Aggregation result types (G3) — shared across resources. An aggregate value
	// is typed String so ONE shape carries every result (ints, floats, and
	// timestamps for min/max on a time field) without a custom scalar; the client
	// parses by the known field type. `count` is null unless count was requested.
	aggValue := gql.NewObject(gql.ObjectConfig{
		Name: "AggValue",
		Fields: gql.Fields{
			"fn":    &gql.Field{Type: gql.NewNonNull(gql.String)},
			"field": &gql.Field{Type: gql.NewNonNull(gql.String)},
			"value": &gql.Field{Type: gql.String},
		},
	})
	aggGroupKey := gql.NewObject(gql.ObjectConfig{
		Name: "AggGroupKey",
		Fields: gql.Fields{
			"field": &gql.Field{Type: gql.NewNonNull(gql.String)},
			"value": &gql.Field{Type: gql.String},
		},
	})
	aggGroup := gql.NewObject(gql.ObjectConfig{
		Name: "AggGroup",
		Fields: gql.Fields{
			"key":    &gql.Field{Type: gql.NewNonNull(gql.NewList(gql.NewNonNull(aggGroupKey)))},
			"count":  &gql.Field{Type: gql.Int},
			"values": &gql.Field{Type: gql.NewNonNull(gql.NewList(gql.NewNonNull(aggValue)))},
		},
	})
	aggregateResult := gql.NewObject(gql.ObjectConfig{
		Name: "AggregateResult",
		Fields: gql.Fields{
			"count":  &gql.Field{Type: gql.Int},
			"values": &gql.Field{Type: gql.NewNonNull(gql.NewList(gql.NewNonNull(aggValue)))},
			"groups": &gql.Field{Type: gql.NewNonNull(gql.NewList(gql.NewNonNull(aggGroup)))},
		},
	})

	names := sortedNames(s)

	// Build per-resource types.
	objectTypes := make(map[string]*gql.Object, len(names))
	inputTypes := make(map[string]*gql.InputObject, len(names))
	updateInputTypes := make(map[string]*gql.InputObject, len(names))
	filterTypes := make(map[string]*gql.InputObject, len(names))
	orderTypes := make(map[string]*gql.InputObject, len(names))

	for _, name := range names {
		res := s.Resources[name]
		title := toPascalCase(singular(name)) // singular: Guide, not Guides

		// Object type (Query response)
		fields := gql.Fields{
			"id": &gql.Field{Type: gql.NewNonNull(gql.ID)},
		}
		for _, fname := range sortedFieldNames(&res) {
			fd := res.Fields[fname]
			fields[fname] = &gql.Field{Type: scalarOutput(fd)}
		}
		objectTypes[name] = gql.NewObject(gql.ObjectConfig{Name: title, Fields: fields})

		// Input type (Mutation args)
		inputFields := gql.InputObjectConfigFieldMap{}
		for _, fname := range sortedFieldNames(&res) {
			fd := res.Fields[fname]
			if fd.Auto.Enabled() {
				continue
			}
			t := scalarInput(fd)
			if fd.Required {
				inputFields[fname] = &gql.InputObjectFieldConfig{Type: gql.NewNonNull(t)}
			} else {
				inputFields[fname] = &gql.InputObjectFieldConfig{Type: t}
			}
		}
		// Import (WRITE-ASYMMETRY-S1): a resource that DECLARES import exposes
		// its declared governed fields (id / auto timestamps) as OPTIONAL
		// create inputs — the structural half of the door. WHO may actually
		// supply them is decided at resolve time by the same
		// schema.GovernedFieldViolations every other door consults (the type
		// system is role-independent, exactly like the rest of RBAC). A
		// resource with no declaration keeps its historical input shape:
		// governed fields are not part of the type at all.
		for _, fname := range res.ImportDeclaredFields() {
			desc := "Import-only (WRITE-ASYMMETRY-S1): accepted solely from a role the resource's \"import\" declaration grants; every other caller gets a read_only validation error."
			if fname == "id" {
				inputFields["id"] = &gql.InputObjectFieldConfig{Type: gql.ID, Description: desc}
				continue
			}
			inputFields[fname] = &gql.InputObjectFieldConfig{Type: scalarInput(res.Fields[fname]), Description: desc}
		}
		// graphql-go PANICS on an input object with zero fields, so a type is
		// created ONLY when it has at least one field (BUG2). A resource whose
		// columns are all uuid (no orderable/filterable fields) or all auto (no
		// writable input) simply omits that input — the corresponding arg/mutation
		// is skipped below rather than emitting an invalid empty SDL type.
		if len(inputFields) > 0 {
			inputTypes[name] = gql.NewInputObject(gql.InputObjectConfig{
				Name:   title + "Input",
				Fields: inputFields,
			})
		}

		// Update input type (SCHEMA-CLOSE-V1): same non-auto fields as create but
		// ALL optional — an update mutation is a partial update (PATCH semantics),
		// so `required` does not apply.
		updateInputFields := gql.InputObjectConfigFieldMap{}
		for _, fname := range sortedFieldNames(&res) {
			fd := res.Fields[fname]
			if fd.Auto.Enabled() {
				continue
			}
			updateInputFields[fname] = &gql.InputObjectFieldConfig{Type: scalarInput(fd)}
		}
		if len(updateInputFields) > 0 {
			updateInputTypes[name] = gql.NewInputObject(gql.InputObjectConfig{
				Name:   title + "UpdateInput",
				Fields: updateInputFields,
			})
		}

		// Filter input type
		filterFields := gql.InputObjectConfigFieldMap{}
		for _, fname := range sortedFieldNames(&res) {
			fd := res.Fields[fname]
			if ft := filterInputFor(fd.Type, stringFilter, dateFilter, rangeFilter, nullFilter); ft != nil {
				filterFields[fname] = &gql.InputObjectFieldConfig{Type: ft}
			}
		}
		if len(filterFields) > 0 {
			filterTypes[name] = gql.NewInputObject(gql.InputObjectConfig{
				Name:   title + "Filter",
				Fields: filterFields,
			})
		}

		// Order input type
		orderFields := gql.InputObjectConfigFieldMap{}
		for _, fname := range sortedFieldNames(&res) {
			fd := res.Fields[fname]
			if isOrderable(fd.Type) {
				orderFields[fname] = &gql.InputObjectFieldConfig{Type: orderDir}
			}
		}
		if len(orderFields) > 0 {
			orderTypes[name] = gql.NewInputObject(gql.InputObjectConfig{
				Name:   title + "Order",
				Fields: orderFields,
			})
		}
	}

	// RELATIONS-V1: nested relation fields on each object type, typed to the
	// target object type (a list for has_many/many_to_many, a single object for
	// belongs_to). The default resolver reads the nested map that the list /
	// get-by-id resolver populates from the json_agg+LATERAL query when the
	// relation is actually selected — no dataloader, no per-field DB query.
	for _, name := range names {
		res := s.Resources[name]
		for _, relName := range sortedRelationNames(&res) {
			rel := res.Relations[relName]
			target, ok := objectTypes[rel.Target]
			if !ok {
				continue
			}
			var ftype gql.Output = target
			if rel.Type == schema.RelationHasMany || rel.Type == schema.RelationManyToMany {
				ftype = gql.NewList(target)
			}
			objectTypes[name].AddFieldConfig(relName, &gql.Field{Type: ftype})
		}
	}

	// Connection types (built after object types exist)
	connectionTypes := make(map[string]*gql.Object, len(names))
	for _, name := range names {
		title := toPascalCase(singular(name))
		connectionTypes[name] = gql.NewObject(gql.ObjectConfig{
			Name: title + "Connection",
			Fields: gql.Fields{
				"data":  &gql.Field{Type: gql.NewNonNull(gql.NewList(gql.NewNonNull(objectTypes[name])))},
				"meta":  &gql.Field{Type: gql.NewNonNull(pageMeta)},
				"links": &gql.Field{Type: gql.NewNonNull(pageLinks)},
			},
		})
	}

	// Query type
	queryFields := gql.Fields{}
	for _, name := range names {
		name := name
		res := s.Resources[name]
		resCopy := res
		title := toPascalCase(singular(name))
		listArgs := gql.FieldConfigArgument{
			"page":     &gql.ArgumentConfig{Type: gql.Int},
			"per_page": &gql.ArgumentConfig{Type: gql.Int},
		}
		// Only expose filter/order args for resources that actually have
		// filterable/orderable fields (BUG2 — no empty input type to reference).
		if ft := filterTypes[name]; ft != nil {
			listArgs["filter"] = &gql.ArgumentConfig{Type: ft}
		}
		if ot := orderTypes[name]; ot != nil {
			listArgs["order"] = &gql.ArgumentConfig{Type: ot}
		}
		queryFields[name] = &gql.Field{ // list: guides(...)
			Type:    gql.NewNonNull(connectionTypes[name]),
			Args:    listArgs,
			Resolve: listResolver(name, &resCopy, tdb, policy, s),
		}
		queryFields[singular(name)] = &gql.Field{ // by ID: guide(id: ID!)
			Type: objectTypes[name],
			Args: gql.FieldConfigArgument{
				"id": &gql.ArgumentConfig{Type: gql.NewNonNull(gql.ID)},
			},
			Resolve: getByIDResolver(name, tdb, policy, s),
		}
		// Aggregate (G3): <resource>Aggregate(filter, count, sum/avg/min/max, group_by).
		// Same RBAC scope (row condition + field allowlist) and filters as the list,
		// reusing query.BuildAggregate via the SAME args→url.Values translation.
		aggArgs := gql.FieldConfigArgument{
			"count":    &gql.ArgumentConfig{Type: gql.Boolean},
			"sum":      &gql.ArgumentConfig{Type: gql.NewList(gql.NewNonNull(gql.String))},
			"avg":      &gql.ArgumentConfig{Type: gql.NewList(gql.NewNonNull(gql.String))},
			"min":      &gql.ArgumentConfig{Type: gql.NewList(gql.NewNonNull(gql.String))},
			"max":      &gql.ArgumentConfig{Type: gql.NewList(gql.NewNonNull(gql.String))},
			"group_by": &gql.ArgumentConfig{Type: gql.NewList(gql.NewNonNull(gql.String))},
		}
		if ft := filterTypes[name]; ft != nil {
			aggArgs["filter"] = &gql.ArgumentConfig{Type: ft}
		}
		queryFields[name+"Aggregate"] = &gql.Field{
			Type:    gql.NewNonNull(aggregateResult),
			Args:    aggArgs,
			Resolve: aggregateResolver(name, &resCopy, tdb, policy),
		}
		_ = title // title used for type names above; suppress unused warning
	}

	// Mutation type
	mutationFields := gql.Fields{}
	for _, name := range names {
		name := name
		res := s.Resources[name]
		resCopy := res
		// Same compiled validator as REST (S44): built once at schema build time
		// (= schema load), never per request.
		rv := schema.CompileRules(&resCopy)
		title := toPascalCase(singular(name)) // createGuide, deleteGuide
		// createX exists only when the resource has writable (non-auto) fields —
		// otherwise there is no input type to reference (BUG2). deleteX always
		// exists (it needs only an id).
		if inputTypes[name] != nil {
			mutationFields["create"+title] = &gql.Field{
				Type: gql.NewNonNull(objectTypes[name]),
				Args: gql.FieldConfigArgument{
					"input": &gql.ArgumentConfig{Type: gql.NewNonNull(inputTypes[name])},
				},
				Resolve: createResolver(name, &resCopy, rv, tdb, hr, policy, hub),
			}
		}
		// updateX(id, input): partial update (PATCH semantics) — exists only when
		// the resource has writable (non-auto) fields. Shares the REST update core
		// (codegen.RunUpdate) so RBAC, identifier safety, and event emission match.
		if updateInputTypes[name] != nil {
			mutationFields["update"+title] = &gql.Field{
				Type: gql.NewNonNull(objectTypes[name]),
				Args: gql.FieldConfigArgument{
					"id":    &gql.ArgumentConfig{Type: gql.NewNonNull(gql.ID)},
					"input": &gql.ArgumentConfig{Type: gql.NewNonNull(updateInputTypes[name])},
				},
				Resolve: updateResolver(name, &resCopy, rv, tdb, hr, policy, hub),
			}
		}
		mutationFields["delete"+title] = &gql.Field{
			Type: gql.NewNonNull(gql.Boolean),
			Args: gql.FieldConfigArgument{
				"id": &gql.ArgumentConfig{Type: gql.NewNonNull(gql.ID)},
			},
			Resolve: deleteResolver(name, &resCopy, tdb, policy, hub),
		}
	}

	queryType := gql.NewObject(gql.ObjectConfig{Name: "Query", Fields: queryFields})
	mutationType := gql.NewObject(gql.ObjectConfig{Name: "Mutation", Fields: mutationFields})

	sc, err := gql.NewSchema(gql.SchemaConfig{Query: queryType, Mutation: mutationType})
	if err != nil {
		panic("appximo/graphql: failed to build schema: " + err.Error())
	}
	return sc
}

// ── resolvers ─────────────────────────────────────────────────────────────────

// aggregateResolver serves <resource>Aggregate (G3): count/sum/avg/min/max +
// group_by, scoped EXACTLY like the list read — the RBAC row condition is in the
// WHERE and the field allowlist forbids aggregating a hidden field (→ error, no
// leak via aggregates). Reuses query.BuildAggregate by translating the GraphQL
// args into url.Values the same way the list resolver does.
func aggregateResolver(name string, res *schema.ResourceSchema, tdb *db.TenantDB, policy *rbac.Policy) gql.FieldResolveFn {
	return func(p gql.ResolveParams) (any, error) {
		tc := tenant.MustFromCtx(p.Context)
		evalResult, err := checkRBAC(p.Context, policy, name, "read")
		if err != nil {
			return nil, err
		}
		var cond *rbac.WhereCondition
		var allowed []string
		if evalResult != nil {
			cond = evalResult.Condition
			allowed = evalResult.AllowedFields
		}

		params, err := argsToURLValues(p.Args) // filter (the agg args below are ignored here)
		if err != nil {
			return nil, err
		}
		if b, _ := p.Args["count"].(bool); b {
			params.Set("count", "true")
		}
		for _, fn := range []string{"sum", "avg", "min", "max"} {
			if fs := stringList(p.Args[fn]); len(fs) > 0 {
				params.Set(fn, strings.Join(fs, ","))
			}
		}
		if fs := stringList(p.Args["group_by"]); len(fs) > 0 {
			params.Set("group_by", strings.Join(fs, ","))
		}

		aq, err := query.BuildAggregate(name, res, params, cond, allowed)
		if err != nil {
			if errors.Is(err, query.ErrAggForbiddenField) {
				return nil, fmt.Errorf("forbidden: %s", err.Error())
			}
			return nil, err
		}

		sql, args := aq.SQL()
		rows, err := tdb.QueryTenant(p.Context, tc.PGSchema, sql, args...)
		if err != nil {
			return nil, safeDBErr(err)
		}
		defer rows.Close()
		recs, err := pkghandlers.RowsToMaps(rows)
		if err != nil {
			return nil, safeDBErr(err)
		}
		return shapeGQLAggregate(aq, recs), nil
	}
}

// stringList coerces a GraphQL [String!] arg ([]any of strings) to []string.
// Empty strings are KEPT (ENG-24): they used to be silently dropped here, so
// `sum:["monto",""]` aggregated as if the empty entry had never been sent.
// Keeping them lets query.splitCSVStrict reject the empty entry BY NAME —
// one validation site for REST and GraphQL.
func stringList(v any) []string {
	lst, ok := v.([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(lst))
	for _, e := range lst {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// aggValueString renders an aggregate cell as a String (numbers stringified,
// timestamps as RFC3339) so the single AggValue.value shape carries any type.
func aggValueString(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case time.Time:
		return t.Format(time.RFC3339Nano)
	case []byte:
		return string(t)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// aggCountInt coerces a COUNT(*) cell (int64 from pgx) to int for gql.Int.
func aggCountInt(v any) any {
	switch n := v.(type) {
	case int64:
		return int(n)
	case int:
		return n
	case float64:
		return int(n)
	default:
		return nil
	}
}

// shapeGQLAggregate maps aggregate rows to the AggregateResult shape.
func shapeGQLAggregate(aq *query.AggregateQuery, recs []map[string]any) map[string]any {
	valuesOf := func(rec map[string]any) []map[string]any {
		vals := make([]map[string]any, 0, len(aq.Metrics()))
		for _, m := range aq.Metrics() {
			vals = append(vals, map[string]any{"fn": m.Fn, "field": m.Field, "value": aggValueString(rec[m.Alias])})
		}
		return vals
	}
	countOf := func(rec map[string]any) any {
		if aq.HasCount() {
			return aggCountInt(rec[query.CountAlias])
		}
		return nil
	}

	if len(aq.GroupBy()) == 0 {
		rec := map[string]any{}
		if len(recs) > 0 {
			rec = recs[0]
		}
		return map[string]any{"count": countOf(rec), "values": valuesOf(rec), "groups": []any{}}
	}

	groups := make([]map[string]any, 0, len(recs))
	for _, rec := range recs {
		key := make([]map[string]any, 0, len(aq.GroupBy()))
		for _, gb := range aq.GroupBy() {
			key = append(key, map[string]any{"field": gb, "value": aggValueString(rec[gb])})
		}
		groups = append(groups, map[string]any{"key": key, "count": countOf(rec), "values": valuesOf(rec)})
	}
	return map[string]any{"count": nil, "values": []any{}, "groups": groups}
}

func listResolver(name string, res *schema.ResourceSchema, tdb *db.TenantDB, policy *rbac.Policy, s *schema.APISchema) gql.FieldResolveFn {
	return func(p gql.ResolveParams) (any, error) {
		tc := tenant.MustFromCtx(p.Context)
		evalResult, err := checkRBAC(p.Context, policy, name, "read")
		if err != nil {
			return nil, err
		}

		params, err := argsToURLValues(p.Args)
		if err != nil {
			return nil, err
		}
		var cond *rbac.WhereCondition
		var allowedFields []string
		if evalResult != nil {
			cond = evalResult.Condition
			allowedFields = evalResult.AllowedFields
		}

		// SEC-5 closed generally (PUBLIC-SURFACE-S1): the filter/order arguments
		// are held to the role's field allowlist, exactly like REST (a named
		// forbidden field is an error in `errors[]`, never an oracle).
		qb, err := query.BuildQuery(name, res, params, cond, allowedFields)
		if err != nil {
			return nil, err
		}

		selectQ, countQ, selectArgs, countArgs := qb.SQL()

		// COUNT is LAZY (SEC-AUDIT-V2 Hallazgo C): memoized, run at most once, and
		// ONLY if meta.total / meta.total_pages / links.last is actually selected
		// (their field resolvers call this). A list query that doesn't ask for the
		// total pays no COUNT — consistent with REST's opt-in ?count=true.
		var (
			countMu  sync.Mutex
			counted  bool
			countVal int64
			countErr error
		)
		countFn := func() (int64, error) {
			countMu.Lock()
			defer countMu.Unlock()
			if !counted {
				countVal, countErr = tdb.QueryScalarTenant(p.Context, tc.PGSchema, countQ, countArgs...)
				counted = true
			}
			return countVal, countErr
		}

		// Register the base field allowlist so the result scrubber drops any
		// forbidden top-level field (shared by both data paths below).
		if evalResult != nil && len(evalResult.AllowedFields) > 0 {
			if fs, ok := p.Context.Value(rbacResultFilterKey{}).(*rbacResultFilter); ok {
				fs.store(p.Info.FieldName, evalResult.AllowedFields)
			}
		}

		var data []map[string]any
		// RELATIONS-V1: when the selection set requests nested relation fields,
		// fetch the whole tree with the SAME json_agg+LATERAL query as REST
		// ?include= (one query, no N+1, RBAC-compiled). Otherwise the unchanged
		// flat select runs.
		if includePaths := listIncludePaths(p.Info, name, s); len(includePaths) > 0 {
			of, od := qb.EffectiveOrder()
			incSQL, incArgs, ierr := query.BuildListInclude(name, joinPaths(includePaths), selectQ, selectArgs, of, od, s, schema.DefaultMaxIncludeDepth, makeRelRBAC(p.Context, policy))
			if ierr != nil {
				return nil, fmt.Errorf("%s", ierr.Msg)
			}
			dataBytes, _, derr := tdb.IncludeListJSON(p.Context, tc.PGSchema, incSQL, incArgs...)
			if derr != nil {
				return nil, safeDBErr(derr)
			}
			if data, err = unmarshalList(dataBytes); err != nil {
				return nil, safeDBErr(err)
			}
		} else {
			rows, rerr := tdb.QueryTenant(p.Context, tc.PGSchema, selectQ, selectArgs...)
			if rerr != nil {
				return nil, safeDBErr(rerr)
			}
			defer rows.Close()
			if data, err = pkghandlers.RowsToMaps(rows); err != nil {
				return nil, safeDBErr(err)
			}
			if evalResult != nil && len(evalResult.AllowedFields) > 0 {
				for i, rec := range data {
					data[i] = pkghandlers.FilterFields(rec, evalResult.AllowedFields)
				}
			}
		}
		if data == nil {
			data = []map[string]any{}
		}

		// has_next from page fullness (len(data) == per_page), MATCHING REST — no
		// COUNT needed, and it removes the prior REST↔GraphQL disagreement on a full
		// final page (SEC-AUDIT-V2 Hallazgo C).
		hasNext := len(data) == qb.PerPage()
		hasPrev := qb.Page() > 1

		buildLink := func(pg int) string {
			v := url.Values{}
			v.Set("page", strconv.Itoa(pg))
			v.Set("per_page", strconv.Itoa(qb.PerPage()))
			return fmt.Sprintf("/graphql?resource=%s&%s", name, v.Encode())
		}
		links := map[string]any{
			"self":  buildLink(qb.Page()),
			"first": buildLink(1),
			// last is lazy (resolveLastLink): it needs the total, so it runs the COUNT
			// only when links.last is selected.
			"__last": func() (any, error) {
				total, err := countFn()
				if err != nil {
					return nil, err
				}
				tp := (total + int64(qb.PerPage()) - 1) / int64(qb.PerPage())
				if tp == 0 {
					tp = 1
				}
				return buildLink(int(tp)), nil
			},
		}
		if hasNext {
			links["next"] = buildLink(qb.Page() + 1)
		}
		if hasPrev {
			links["prev"] = buildLink(qb.Page() - 1)
		}

		return map[string]any{
			"data": data,
			"meta": map[string]any{
				"page":     qb.Page(),
				"per_page": qb.PerPage(),
				"has_next": hasNext,
				"has_prev": hasPrev,
				// total / total_pages are resolved lazily from this COUNT closure
				// (resolveTotal / resolveTotalPages) — only if selected.
				"__count": countFn,
			},
			"links": links,
		}, nil
	}
}

// resolveTotal is the lazy resolver for PageMeta.total: it runs the list's memoized
// COUNT closure (stored under "__count" by listResolver) only when total is
// selected. Absent closure (defensive) ⇒ 0.
func resolveTotal(p gql.ResolveParams) (any, error) {
	n, err := countFromSource(p.Source)
	return n, err
}

// resolveTotalPages is the lazy resolver for PageMeta.total_pages: COUNT → ceil by
// per_page (min 1).
func resolveTotalPages(p gql.ResolveParams) (any, error) {
	total, err := countFromSource(p.Source)
	if err != nil {
		return nil, err
	}
	per := 1
	if m, ok := p.Source.(map[string]any); ok {
		if pp, ok := m["per_page"].(int); ok && pp > 0 {
			per = pp
		}
	}
	tp := (total + per - 1) / per
	if tp == 0 {
		tp = 1
	}
	return tp, nil
}

// resolveLastLink is the lazy resolver for PageLinks.last: it calls the "__last"
// closure (which runs the COUNT) only when links.last is selected.
func resolveLastLink(p gql.ResolveParams) (any, error) {
	m, ok := p.Source.(map[string]any)
	if !ok {
		return nil, nil
	}
	if lf, ok := m["__last"].(func() (any, error)); ok {
		return lf()
	}
	return m["last"], nil
}

// countFromSource pulls the memoized COUNT closure ("__count") from a PageMeta
// source map and runs it, returning the total as an int. Absent ⇒ 0.
func countFromSource(src any) (int, error) {
	m, ok := src.(map[string]any)
	if !ok {
		return 0, nil
	}
	if cf, ok := m["__count"].(func() (int64, error)); ok {
		n, err := cf()
		return int(n), err
	}
	return 0, nil
}

func getByIDResolver(name string, tdb *db.TenantDB, policy *rbac.Policy, s *schema.APISchema) gql.FieldResolveFn {
	return func(p gql.ResolveParams) (any, error) {
		tc := tenant.MustFromCtx(p.Context)
		evalResult, err := checkRBAC(p.Context, policy, name, "read")
		if err != nil {
			return nil, err
		}

		idStr, _ := p.Args["id"].(string)
		if _, err := uuid.Parse(idStr); err != nil {
			return nil, fmt.Errorf("invalid id format")
		}

		// Enforce the row-level RBAC condition (BOLA guard) — a restricted role
		// must not read another principal's row by guessing its id. Mirrors the
		// REST get-by-id path.
		q := "SELECT * FROM " + pgx.Identifier{name}.Sanitize() + " WHERE id = $1"
		qargs := []any{idStr}
		if evalResult != nil {
			q, qargs, err = query.AppendRowCondition(q, qargs, evalResult.Condition)
			if err != nil {
				return nil, err
			}
		}

		// RELATIONS-V1: nested selection → one json_agg+LATERAL query (the embed
		// cannot resurrect a row the base WHERE + row-cond excluded).
		if includePaths := objectIncludePaths(p.Info, name, s); len(includePaths) > 0 {
			if evalResult != nil && len(evalResult.AllowedFields) > 0 {
				if fs, ok := p.Context.Value(rbacResultFilterKey{}).(*rbacResultFilter); ok {
					fs.store(p.Info.FieldName, evalResult.AllowedFields)
				}
			}
			incSQL, incArgs, ierr := query.BuildGetInclude(name, joinPaths(includePaths), q, qargs, s, schema.DefaultMaxIncludeDepth, makeRelRBAC(p.Context, policy))
			if ierr != nil {
				return nil, fmt.Errorf("%s", ierr.Msg)
			}
			dataBytes, found, derr := tdb.IncludeOneJSON(p.Context, tc.PGSchema, incSQL, incArgs...)
			if derr != nil {
				return nil, safeDBErr(derr)
			}
			if !found {
				return nil, nil
			}
			var rec map[string]any
			if err := json.Unmarshal(dataBytes, &rec); err != nil {
				return nil, safeDBErr(err)
			}
			return rec, nil
		}

		rows, err := tdb.QueryTenant(p.Context, tc.PGSchema, q, qargs...)
		if err != nil {
			return nil, safeDBErr(err)
		}
		defer rows.Close()
		result, err := pkghandlers.RowsToMaps(rows)
		if err != nil {
			return nil, safeDBErr(err)
		}
		if len(result) == 0 {
			return nil, nil
		}
		record := result[0]
		if evalResult != nil && len(evalResult.AllowedFields) > 0 {
			if fs, ok := p.Context.Value(rbacResultFilterKey{}).(*rbacResultFilter); ok {
				fs.store(p.Info.FieldName, evalResult.AllowedFields)
			}
			record = pkghandlers.FilterFields(record, evalResult.AllowedFields)
		}
		return record, nil
	}
}

func createResolver(name string, res *schema.ResourceSchema, rv *schema.ResourceValidator, tdb *db.TenantDB, hr *extensions.HookRunner, policy *rbac.Policy, hub *events.Hub) gql.FieldResolveFn {
	// Decided once at schema build (boot), like the REST create path — never per
	// request (AGENTS: compile at schema load, not the hot path). emitCreate gates
	// same-tx outbox emission; tbl is the quoted table identifier.
	emitCreate := res.EmitsOn("create")
	tbl := pgx.Identifier{name}.Sanitize()
	return func(p gql.ResolveParams) (any, error) {
		tc := tenant.MustFromCtx(p.Context)
		evalResult, err := checkRBAC(p.Context, policy, name, "create")
		if err != nil {
			return nil, err
		}

		input, _ := p.Args["input"].(map[string]any)
		if input == nil {
			input = map[string]any{}
		}
		// Schema defaults (SCHEMA-CLOSE-V1): fill omitted fields with declared
		// defaults BEFORE the empty/required checks — same as the REST create path.
		rv.ApplyDefaults(input)
		if len(input) == 0 {
			return nil, fmt.Errorf("empty input")
		}

		// Declarative validation (S44): same precompiled validator and rule
		// semantics as REST POST, BEFORE the before_create hook. graphql-go
		// coerces Int args to Go int while the validator (like REST's JSON
		// decoding) expects float64 — validate a normalized shallow copy so the
		// original input keeps its types for the INSERT.
		norm := make(map[string]any, len(input))
		for k, v := range input {
			switch n := v.(type) {
			case int:
				norm[k] = float64(n)
			case int32:
				norm[k] = float64(n)
			case int64:
				norm[k] = float64(n)
			default:
				norm[k] = v
			}
		}
		// Governed fields (WRITE-ASYMMETRY-S1): for an import-declaring
		// resource the create input structurally CARRIES id/auto fields, so
		// the role gate runs here — the SAME schema.GovernedFieldViolations
		// the REST POST, the batch transaction and Ctx.Insert consult (via
		// PrepareCreate), collected with the declarative violations so one
		// response names every failing field (S44). On a resource with no
		// declaration the input type already rejected the keys structurally.
		verrs := schema.GovernedFieldViolations(res, norm, schema.GovernedCreate, callerRole(p.Context))
		verrs = append(verrs, rv.ValidateWrite(norm, true)...)
		if len(verrs) > 0 {
			return nil, &validationError{fields: verrs}
		}
		if verrs := rv.ValidateInitialStates(norm); len(verrs) > 0 { // G5: create in an initial state
			return nil, &validationError{fields: verrs}
		}

		hc := hookCfg(name, "before_create", res)
		hookRes, err := hr.RunBeforeHook(p.Context, hc, input, auth.HookUserContext(p.Context))
		if err != nil {
			return nil, err
		}
		if !hookRes.Proceed {
			return nil, fmt.Errorf("%s", hookRes.Error)
		}
		body := hookRes.Data

		// HALLAZGO-2 / FASE3-SEC: enforce the role's row-level condition + field
		// allowlist on the create, identically to the REST POST path — same shared
		// EnforceCreateRBAC. A row-scoped role's record is forced to its own id; a
		// body claiming another principal's id is rejected (403 → GraphQL error).
		if status, msg := codegen.EnforceCreateRBAC(body, evalResult); status != 0 {
			return nil, fmt.Errorf("%s", msg)
		}

		// Per-field file attach policy (FILES-1) — same check, same S44 fields
		// as the REST create (a violation lands in errors[].extensions.fields).
		if fpErrs, fpErr := codegen.CheckFilePoliciesTenant(p.Context, tdb, tc.PGSchema, res, body); fpErr != nil {
			return nil, safeDBErr(fpErr)
		} else if len(fpErrs) > 0 {
			return nil, &validationError{fields: fpErrs}
		}

		// Shared create core: the SAME codegen.RunInsert the REST POST handler uses,
		// so a resource with events:["create"] emits an IDENTICAL {resource}.created
		// event (same topic + lean payload) atomically with the insert — closing the
		// REST/GraphQL create inconsistency. Identifiers are quoted by BuildInsertArgs
		// (injection-safe); the DB stays the source of truth for the writable column
		// set (no res.Fields whitelist), mirroring REST.
		result, err := codegen.RunInsert(p.Context, tdb, tbl, name, tc.ID, tc.PGSchema, body, emitCreate)
		if err != nil {
			// safeDBErr renders the shared classifier — the unique-collision
			// conflict (G6) included, identically to the update resolver.
			return nil, safeDBErr(err)
		}

		// SSE broadcast (S45): same post-commit point as the REST create path.
		if len(result) > 0 {
			gqlPublish(hub, tc.ID, name, "create", result[0], "")
		}

		if afterHook := hookCfg(name, "after_create", res); afterHook != nil {
			var record map[string]any
			if len(result) > 0 {
				record = result[0]
			}
			// Bounded async dispatch — same as the REST path. A previous version
			// spawned an unbounded `go RunAfterHook` per mutation, which a create
			// storm could turn into unbounded in-flight goroutines.
			hr.FireAfterHook(afterHook, "after_create", record, tc.ID)
		}

		if len(result) > 0 {
			return result[0], nil
		}
		return nil, nil
	}
}

// updateResolver implements the updateX mutation (SCHEMA-CLOSE-V1) with the same
// semantics as the REST PATCH path: partial update of the provided fields,
// declarative validation, field-level RBAC allowlist, row-level RBAC condition,
// before/after_update hooks, SSE broadcast, and same-tx outbox emission when the
// resource opts into events:["update"]. It reuses the engine's shared update core
// (codegen.CollectUpdate + codegen.RunUpdate) so REST and GraphQL never diverge.
func updateResolver(name string, res *schema.ResourceSchema, rv *schema.ResourceValidator, tdb *db.TenantDB, hr *extensions.HookRunner, policy *rbac.Policy, hub *events.Hub) gql.FieldResolveFn {
	return func(p gql.ResolveParams) (any, error) {
		tc := tenant.MustFromCtx(p.Context)
		evalResult, err := checkRBAC(p.Context, policy, name, "update")
		if err != nil {
			return nil, err
		}

		idStr, _ := p.Args["id"].(string)
		if _, err := uuid.Parse(idStr); err != nil {
			return nil, fmt.Errorf("invalid id format")
		}
		input, _ := p.Args["input"].(map[string]any)
		if len(input) == 0 {
			return nil, fmt.Errorf("empty input")
		}

		// graphql-go coerces Int args to Go int; the validator and CollectUpdate
		// (like REST's JSON decoding) expect float64 — normalize a copy.
		norm := make(map[string]any, len(input))
		for k, v := range input {
			switch n := v.(type) {
			case int:
				norm[k] = float64(n)
			case int32:
				norm[k] = float64(n)
			case int64:
				norm[k] = float64(n)
			default:
				norm[k] = v
			}
		}

		// Partial (PATCH) validation: only the fields present are checked.
		if verrs := rv.ValidateWrite(norm, false); len(verrs) > 0 {
			return nil, &validationError{fields: verrs}
		}

		// Field-level RBAC allowlist — only writable columns survive.
		writable := func(string) bool { return true }
		if evalResult != nil && len(evalResult.AllowedFields) > 0 {
			allow := make(map[string]struct{}, len(evalResult.AllowedFields))
			for _, f := range evalResult.AllowedFields {
				allow[f] = struct{}{}
			}
			writable = func(f string) bool { _, ok := allow[f]; return ok }
		}
		// A violation carries the S44 fields[] into errors[].extensions — the
		// same shape ValidateWrite failures already use, and the same contract
		// REST's 422 answers (ENG-29).
		sets, cerrs := codegen.CollectUpdate(res, norm, false, writable)
		if len(cerrs) > 0 {
			return nil, &validationError{fields: cerrs}
		}

		// before_update hook (same contract as REST / GraphQL create).
		hc := hookCfg(name, "before_update", res)
		hookRes, herr := hr.RunBeforeHook(p.Context, hc, input, auth.HookUserContext(p.Context))
		if herr != nil {
			return nil, herr
		}
		if !hookRes.Proceed {
			return nil, fmt.Errorf("%s", hookRes.Error)
		}
		if hc != nil {
			for col := range sets {
				if nv, ok := hookRes.Data[col]; ok {
					sets[col] = nv
				}
			}
		}

		// Per-field file attach policy (FILES-1) on the final SET values — same
		// check, same S44 fields as the REST update.
		if fpErrs, fpErr := codegen.CheckFilePoliciesTenant(p.Context, tdb, tc.PGSchema, res, sets); fpErr != nil {
			return nil, safeDBErr(fpErr)
		} else if len(fpErrs) > 0 {
			return nil, &validationError{fields: fpErrs}
		}

		var cond *rbac.WhereCondition
		if evalResult != nil {
			cond = evalResult.Condition
		}
		rows, err := codegen.RunUpdate(p.Context, tdb, res, pgx.Identifier{name}.Sanitize(),
			name, tc.ID, tc.PGSchema, idStr, sets, cond, res.EmitsOn("update"))
		if err != nil {
			if err == codegen.ErrNoWritableUpdate {
				return nil, fmt.Errorf("no writable fields in request")
			}
			return nil, safeDBErr(err)
		}
		if len(rows) == 0 {
			// Zero rows: not found, RBAC-excluded, or a state-machine transition
			// rejected the move — explain precisely (a plain "not found" for a resource
			// without a state machine, no extra read).
			_, msg := codegen.ExplainTransitionFailure(p.Context, tdb, tc.PGSchema, pgx.Identifier{name}.Sanitize(), idStr, res, sets)
			return nil, fmt.Errorf("%s", msg)
		}
		record := rows[0]

		// SSE broadcast (post-commit, unfiltered — delivery applies per-sub RBAC).
		gqlPublish(hub, tc.ID, name, "update", record, "")

		if ah := hookCfg(name, "after_update", res); ah != nil {
			hr.FireAfterHook(ah, "after_update", record, tc.ID)
		}

		if evalResult != nil && len(evalResult.AllowedFields) > 0 {
			if fs, ok := p.Context.Value(rbacResultFilterKey{}).(*rbacResultFilter); ok {
				fs.store(p.Info.FieldName, evalResult.AllowedFields)
			}
			record = pkghandlers.FilterFields(record, evalResult.AllowedFields)
		}
		return record, nil
	}
}

func deleteResolver(name string, res *schema.ResourceSchema, tdb *db.TenantDB, policy *rbac.Policy, hub *events.Hub) gql.FieldResolveFn {
	// Decided once at schema build (boot), like the REST delete path — never per
	// request. emitDelete gates same-tx outbox emission; tbl is the quoted table.
	emitDelete := res.EmitsOn("delete")
	tbl := pgx.Identifier{name}.Sanitize()
	return func(p gql.ResolveParams) (any, error) {
		tc := tenant.MustFromCtx(p.Context)
		evalResult, err := checkRBAC(p.Context, policy, name, "delete")
		if err != nil {
			return false, err
		}

		idStr, _ := p.Args["id"].(string)
		if _, err := uuid.Parse(idStr); err != nil {
			return false, fmt.Errorf("invalid id format")
		}

		// Shared delete core: the SAME codegen.RunDelete the REST DELETE handler
		// uses (DELETE + row-level RBAC condition + same-tx {resource}.deleted
		// emission when opted in), so REST and GraphQL delete emit identically —
		// completing REST/GraphQL write consistency across create/update/delete.
		var cond *rbac.WhereCondition
		if evalResult != nil {
			cond = evalResult.Condition
		}
		affected, err := codegen.RunDelete(p.Context, tdb, tbl, name, tc.ID, tc.PGSchema, idStr, cond, emitDelete)
		if err != nil {
			return false, safeDBErr(err)
		}
		if affected > 0 {
			// SSE broadcast (S45): row is gone → id with null record.
			gqlPublish(hub, tc.ID, name, "delete", nil, idStr)
		}
		return affected > 0, nil
	}
}

// gqlPublish mirrors codegen's publishEvent for the GraphQL write resolvers:
// nil-hub no-op, non-blocking, id taken from the record when present.
func gqlPublish(hub *events.Hub, tenantID, resource, typ string, record map[string]any, fallbackID string) {
	if hub == nil {
		return
	}
	id := fallbackID
	if record != nil {
		if s, ok := record["id"].(string); ok {
			id = s
		}
	}
	hub.Publish(tenantID, events.Event{Type: typ, Resource: resource, ID: id, Record: record})
}

// ── RBAC helper ───────────────────────────────────────────────────────────────

// callerRole extracts the authenticated JWT role — consulted only by the
// governed-field import grant (WRITE-ASYMMETRY-S1). Empty when the context
// carries no claims, which can only permit less (matches no grant).
func callerRole(ctx context.Context) string {
	if c := auth.ClaimsFromCtx(ctx); c != nil {
		return c.Role
	}
	return ""
}

func checkRBAC(ctx context.Context, policy *rbac.Policy, resource, action string) (*rbac.EvalResult, error) {
	claims := auth.ClaimsFromCtx(ctx)
	if claims == nil {
		return nil, fmt.Errorf("unauthorized")
	}
	evalCtx := rbac.EvalContext{
		Role:             claims.Role,
		UserID:           claims.UserID,
		ExternalClientID: claims.ExternalClientID,
	}

	result := policy.Evaluate(evalCtx, resource, action)
	if !result.Allowed {
		// ENG-27: log whether the role exists at all — the GraphQL error stays
		// the bare "forbidden" (same asymmetry as the REST middleware).
		log.Printf("rbac: denied graphql %s %s — %s (user_id=%q)",
			action, resource, policy.DenyDetail(evalCtx.Role, resource, action), evalCtx.UserID)
		return nil, fmt.Errorf("forbidden")
	}
	return &result, nil
}

// ── arg conversion ────────────────────────────────────────────────────────────

func argsToURLValues(args map[string]any) (url.Values, error) {
	params := url.Values{}
	if page, ok := args["page"].(int); ok && page > 0 {
		params.Set("page", strconv.Itoa(page))
	}
	if pp, ok := args["per_page"].(int); ok && pp > 0 {
		params.Set("per_page", strconv.Itoa(pp))
	}
	if filterMap, ok := args["filter"].(map[string]any); ok {
		for field, val := range filterMap {
			switch fv := val.(type) {
			case map[string]any:
				for op, v := range fv {
					dbOp := mapOp(op)
					switch vs := v.(type) {
					case string:
						params.Set("filter["+field+"]["+dbOp+"]", vs)
					case float64:
						params.Set("filter["+field+"]["+dbOp+"]", strconv.FormatFloat(vs, 'f', -1, 64))
					case bool:
						// is_null: Boolean (SCHEMA-6) — the only bool-valued op.
						params.Set("filter["+field+"]["+dbOp+"]", strconv.FormatBool(vs))
					}
				}
			case string:
				params.Set("filter["+field+"]", fv)
			}
		}
	}
	if orderMap, ok := args["order"].(map[string]any); ok {
		// ENG-16 (the GraphQL half): the loop used to take whichever field Go's
		// map iteration yielded first and break — `order:{a:ASC, b:DESC}` sorted
		// by a DIFFERENT field between identical requests. One field is the
		// surface; more than one is an error naming them (same contract as
		// REST's order[…]).
		if len(orderMap) > 1 {
			fields := make([]string, 0, len(orderMap))
			for f := range orderMap {
				fields = append(fields, f)
			}
			sort.Strings(fields)
			return nil, fmt.Errorf("order names %d fields (%s): one sort field is supported — multi-field sort does not exist", len(orderMap), strings.Join(fields, ", "))
		}
		for field, dir := range orderMap {
			if ds, ok := dir.(string); ok {
				params.Set("order["+field+"]", ds)
			}
		}
	}
	return params, nil
}

// mapOp maps GraphQL filter op names to BuildQuery op names.
func mapOp(op string) string {
	if op == "exact" {
		return "eq"
	}
	return op // partial, start, after, before, gte, lte, is_null pass through
}

// ── type helpers ──────────────────────────────────────────────────────────────

func scalarOutput(fd schema.FieldDef) gql.Output {
	switch fd.Type {
	case "int", "int64":
		return gql.Int
	case "float64":
		return gql.Float
	case "bool":
		return gql.Boolean
	case "uuid", "file":
		return gql.ID
	case "jsonb":
		return jsonScalar
	default:
		return gql.String
	}
}

func scalarInput(fd schema.FieldDef) gql.Input {
	switch fd.Type {
	case "int", "int64":
		return gql.Int
	case "float64":
		return gql.Float
	case "bool":
		return gql.Boolean
	case "uuid", "file":
		return gql.ID
	case "jsonb":
		return jsonScalar
	default:
		return gql.String
	}
}

// jsonScalar is the GraphQL scalar for a `jsonb` field (LIBRARY-GAPS-S1): an
// arbitrary JSON document, passed through unchanged in both directions. pgx
// decodes a jsonb column into a Go map/slice, so serializing it as gql.String
// would print Go syntax (`map[marca:Acme]`) — a lie about the contract. The
// scalar carries the real document instead.
//
// `json` (the TEXT-backed type) deliberately stays gql.String: its column value
// IS a string, so every existing schema's SDL is byte-unchanged.
var jsonScalar = gql.NewScalar(gql.ScalarConfig{
	Name:         "JSON",
	Description:  "An arbitrary JSON document (a jsonb column), passed through unchanged.",
	Serialize:    func(v any) any { return v },
	ParseValue:   func(v any) any { return v }, // a variable already carries real JSON
	ParseLiteral: parseJSONLiteral,
})

// parseJSONLiteral converts an inline GraphQL literal into the Go value a jsonb
// column takes (map / slice / scalar). Inline literals are the awkward half of a
// JSON scalar — a variable is the normal path — but supporting them means
// `createProduct(input: {attrs: {brand: "Acme"}})` works as written.
func parseJSONLiteral(v ast.Value) any {
	switch val := v.(type) {
	case *ast.ObjectValue:
		out := make(map[string]any, len(val.Fields))
		for _, f := range val.Fields {
			out[f.Name.Value] = parseJSONLiteral(f.Value)
		}
		return out
	case *ast.ListValue:
		out := make([]any, 0, len(val.Values))
		for _, item := range val.Values {
			out = append(out, parseJSONLiteral(item))
		}
		return out
	case *ast.StringValue:
		return val.Value
	case *ast.BooleanValue:
		return val.Value
	case *ast.IntValue:
		if n, err := strconv.ParseInt(val.Value, 10, 64); err == nil {
			return n
		}
		return val.Value
	case *ast.FloatValue:
		if f, err := strconv.ParseFloat(val.Value, 64); err == nil {
			return f
		}
		return val.Value
	case *ast.EnumValue:
		return val.Value
	default: // NullValue and anything else
		return nil
	}
}

func filterInputFor(fieldType string, strF, dateF, rangeF, nullF *gql.InputObject) gql.Input {
	switch fieldType {
	case "string", "text":
		return strF
	case "time":
		return dateF
	case "int", "int64", "float64":
		return rangeF
	case "uuid", "bool", "file", "json", "jsonb":
		// is_null only (SCHEMA-6) — these types have no other GraphQL filter
		// input; their eq filtering remains REST-only (pre-existing gap).
		return nullF
	default:
		return nil
	}
}

func isOrderable(fieldType string) bool {
	switch fieldType {
	case "string", "text", "int", "int64", "float64", "time":
		return true
	default:
		return false
	}
}

func hookCfg(name, lifecycle string, res *schema.ResourceSchema) *schema.HookConfig {
	hc, ok := res.Hooks[lifecycle]
	if !ok {
		return nil
	}
	c := hc
	return &c
}

// singular / toPascalCase delegate to pkg/schema — the ONE source for GraphQL
// name derivation, so the validator's collision check (FRESH-AGENT-GAPS-S1) can
// never disagree with what this builder actually generates.
func toPascalCase(s string) string { return schema.GraphQLPascal(s) }

func singular(name string) string { return schema.GraphQLSingular(name) }

func sortedNames(s *schema.APISchema) []string {
	names := make([]string, 0, len(s.Resources))
	for n := range s.Resources {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func sortedRelationNames(res *schema.ResourceSchema) []string {
	keys := make([]string, 0, len(res.Relations))
	for k := range res.Relations {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func sortedFieldNames(res *schema.ResourceSchema) []string {
	keys := make([]string, 0, len(res.Fields))
	for k := range res.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
