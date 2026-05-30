package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	gql "github.com/graphql-go/graphql"
	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/extensions"
	pkghandlers "github.com/miguelangel/appitools/pkg/handlers"
	"github.com/miguelangel/appitools/pkg/query"
	"github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
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
func BuildHandler(s *schema.APISchema, tdb *db.TenantDB, hr *extensions.HookRunner, policy *rbac.Policy) http.Handler {
	gqlSchema := buildGQLSchema(s, tdb, hr, policy)
	isDev := os.Getenv("APPITOOLS_ENV") == "development"
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
				// MaxBytesReader sets a specific error when the limit is exceeded.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusRequestEntityTooLarge)
				json.NewEncoder(w).Encode(map[string]string{"error": "request body too large"}) //nolint:errcheck
				return
			}
		}

		// FIX 7: block introspection queries outside development to reduce attack surface.
		if !isDev && isIntrospectionQuery(params.Query) {
			writeGraphQLError(w, "introspection disabled in production")
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

func isIntrospectionQuery(query string) bool {
	return strings.Contains(query, "__schema") || strings.Contains(query, "__type")
}

func writeGraphQLError(w http.ResponseWriter, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"errors": []map[string]any{{"message": msg}},
	})
}

// PlaygroundHandler serves GraphiQL in development mode.
func PlaygroundHandler(endpoint string) http.Handler {
	page := fmt.Sprintf(`<!DOCTYPE html><html><head>
<title>GraphiQL — Appitools</title>
<link rel="stylesheet" href="https://unpkg.com/graphiql/graphiql.min.css"/>
</head><body style="margin:0"><div id="graphiql" style="height:100vh"></div>
<script src="https://unpkg.com/react/umd/react.production.min.js"></script>
<script src="https://unpkg.com/react-dom/umd/react-dom.production.min.js"></script>
<script src="https://unpkg.com/graphiql/graphiql.min.js"></script>
<script>
ReactDOM.render(
  React.createElement(GraphiQL,{fetcher:GraphiQL.createFetcher({url:%q})}),
  document.getElementById('graphiql')
);
</script></body></html>`, endpoint)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, page)
	})
}

// ── schema builder ────────────────────────────────────────────────────────────

func buildGQLSchema(s *schema.APISchema, tdb *db.TenantDB, hr *extensions.HookRunner, policy *rbac.Policy) gql.Schema {
	// Shared scalar/enum/input types — created once per schema instance.
	orderDir := gql.NewEnum(gql.EnumConfig{
		Name: "OrderDirection",
		Values: gql.EnumValueConfigMap{
			"ASC":  &gql.EnumValueConfig{Value: "asc"},
			"DESC": &gql.EnumValueConfig{Value: "desc"},
		},
	})
	stringFilter := gql.NewInputObject(gql.InputObjectConfig{
		Name: "StringFilter",
		Fields: gql.InputObjectConfigFieldMap{
			"exact":   &gql.InputObjectFieldConfig{Type: gql.String},
			"partial": &gql.InputObjectFieldConfig{Type: gql.String},
			"start":   &gql.InputObjectFieldConfig{Type: gql.String},
		},
	})
	dateFilter := gql.NewInputObject(gql.InputObjectConfig{
		Name: "DateFilter",
		Fields: gql.InputObjectConfigFieldMap{
			"after":  &gql.InputObjectFieldConfig{Type: gql.String},
			"before": &gql.InputObjectFieldConfig{Type: gql.String},
			"gte":    &gql.InputObjectFieldConfig{Type: gql.String},
			"lte":    &gql.InputObjectFieldConfig{Type: gql.String},
		},
	})
	rangeFilter := gql.NewInputObject(gql.InputObjectConfig{
		Name: "RangeFilter",
		Fields: gql.InputObjectConfigFieldMap{
			"gte": &gql.InputObjectFieldConfig{Type: gql.Float},
			"lte": &gql.InputObjectFieldConfig{Type: gql.Float},
		},
	})
	pageMeta := gql.NewObject(gql.ObjectConfig{
		Name: "PageMeta",
		Fields: gql.Fields{
			"page":        &gql.Field{Type: gql.NewNonNull(gql.Int)},
			"per_page":    &gql.Field{Type: gql.NewNonNull(gql.Int)},
			"total":       &gql.Field{Type: gql.NewNonNull(gql.Int)},
			"total_pages": &gql.Field{Type: gql.NewNonNull(gql.Int)},
			"has_next":    &gql.Field{Type: gql.NewNonNull(gql.Boolean)},
			"has_prev":    &gql.Field{Type: gql.NewNonNull(gql.Boolean)},
		},
	})
	pageLinks := gql.NewObject(gql.ObjectConfig{
		Name: "PageLinks",
		Fields: gql.Fields{
			"self":  &gql.Field{Type: gql.String},
			"first": &gql.Field{Type: gql.String},
			"last":  &gql.Field{Type: gql.String},
			"next":  &gql.Field{Type: gql.String},
			"prev":  &gql.Field{Type: gql.String},
		},
	})

	names := sortedNames(s)

	// Build per-resource types.
	objectTypes := make(map[string]*gql.Object, len(names))
	inputTypes := make(map[string]*gql.InputObject, len(names))
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
			if fd.Auto {
				continue
			}
			t := scalarInput(fd)
			if fd.Required {
				inputFields[fname] = &gql.InputObjectFieldConfig{Type: gql.NewNonNull(t)}
			} else {
				inputFields[fname] = &gql.InputObjectFieldConfig{Type: t}
			}
		}
		inputTypes[name] = gql.NewInputObject(gql.InputObjectConfig{
			Name:   title + "Input",
			Fields: inputFields,
		})

		// Filter input type
		filterFields := gql.InputObjectConfigFieldMap{}
		for _, fname := range sortedFieldNames(&res) {
			fd := res.Fields[fname]
			if ft := filterInputFor(fd.Type, stringFilter, dateFilter, rangeFilter); ft != nil {
				filterFields[fname] = &gql.InputObjectFieldConfig{Type: ft}
			}
		}
		filterTypes[name] = gql.NewInputObject(gql.InputObjectConfig{
			Name:   title + "Filter",
			Fields: filterFields,
		})

		// Order input type
		orderFields := gql.InputObjectConfigFieldMap{}
		for _, fname := range sortedFieldNames(&res) {
			fd := res.Fields[fname]
			if isOrderable(fd.Type) {
				orderFields[fname] = &gql.InputObjectFieldConfig{Type: orderDir}
			}
		}
		orderTypes[name] = gql.NewInputObject(gql.InputObjectConfig{
			Name:   title + "Order",
			Fields: orderFields,
		})
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
		queryFields[name] = &gql.Field{ // list: guides(...)
			Type: gql.NewNonNull(connectionTypes[name]),
			Args: gql.FieldConfigArgument{
				"page":     &gql.ArgumentConfig{Type: gql.Int},
				"per_page": &gql.ArgumentConfig{Type: gql.Int},
				"filter":   &gql.ArgumentConfig{Type: filterTypes[name]},
				"order":    &gql.ArgumentConfig{Type: orderTypes[name]},
			},
			Resolve: listResolver(name, &resCopy, tdb, policy),
		}
		queryFields[singular(name)] = &gql.Field{ // by ID: guide(id: ID!)
			Type: objectTypes[name],
			Args: gql.FieldConfigArgument{
				"id": &gql.ArgumentConfig{Type: gql.NewNonNull(gql.ID)},
			},
			Resolve: getByIDResolver(name, tdb, policy),
		}
		_ = title // title used for type names above; suppress unused warning
	}

	// Mutation type
	mutationFields := gql.Fields{}
	for _, name := range names {
		name := name
		res := s.Resources[name]
		resCopy := res
		title := toPascalCase(singular(name)) // createGuide, deleteGuide
		mutationFields["create"+title] = &gql.Field{
			Type: gql.NewNonNull(objectTypes[name]),
			Args: gql.FieldConfigArgument{
				"input": &gql.ArgumentConfig{Type: gql.NewNonNull(inputTypes[name])},
			},
			Resolve: createResolver(name, &resCopy, tdb, hr, policy),
		}
		mutationFields["delete"+title] = &gql.Field{
			Type: gql.NewNonNull(gql.Boolean),
			Args: gql.FieldConfigArgument{
				"id": &gql.ArgumentConfig{Type: gql.NewNonNull(gql.ID)},
			},
			Resolve: deleteResolver(name, tdb, policy),
		}
	}

	queryType := gql.NewObject(gql.ObjectConfig{Name: "Query", Fields: queryFields})
	mutationType := gql.NewObject(gql.ObjectConfig{Name: "Mutation", Fields: mutationFields})

	sc, err := gql.NewSchema(gql.SchemaConfig{Query: queryType, Mutation: mutationType})
	if err != nil {
		panic("appitools/graphql: failed to build schema: " + err.Error())
	}
	return sc
}

// ── resolvers ─────────────────────────────────────────────────────────────────

func listResolver(name string, res *schema.ResourceSchema, tdb *db.TenantDB, policy *rbac.Policy) gql.FieldResolveFn {
	return func(p gql.ResolveParams) (any, error) {
		tc := tenant.MustFromCtx(p.Context)
		evalResult, err := checkRBAC(p.Context, policy, name, "read")
		if err != nil {
			return nil, err
		}

		params := argsToURLValues(p.Args)
		var cond *rbac.WhereCondition
		if evalResult != nil {
			cond = evalResult.Condition
		}

		qb, err := query.BuildQuery(name, res, params, cond)
		if err != nil {
			return nil, err
		}

		selectQ, countQ, selectArgs, countArgs := qb.SQL()

		total, err := tdb.QueryScalarTenant(p.Context, tc.PGSchema, countQ, countArgs...)
		if err != nil {
			return nil, err
		}

		rows, err := tdb.QueryTenant(p.Context, tc.PGSchema, selectQ, selectArgs...)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		data, err := pkghandlers.RowsToMaps(rows)
		if err != nil {
			return nil, err
		}

		if evalResult != nil && len(evalResult.AllowedFields) > 0 {
			if fs, ok := p.Context.Value(rbacResultFilterKey{}).(*rbacResultFilter); ok {
				fs.store(p.Info.FieldName, evalResult.AllowedFields)
			}
			for i, rec := range data {
				data[i] = pkghandlers.FilterFields(rec, evalResult.AllowedFields)
			}
		}
		if data == nil {
			data = []map[string]any{}
		}

		totalPages := (total + int64(qb.PerPage()) - 1) / int64(qb.PerPage())
		if totalPages == 0 {
			totalPages = 1
		}
		hasNext := total > int64(qb.Page()*qb.PerPage())
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
			"last":  buildLink(int(totalPages)),
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
				"page":        qb.Page(),
				"per_page":    qb.PerPage(),
				"total":       int(total),
				"total_pages": int(totalPages),
				"has_next":    hasNext,
				"has_prev":    hasPrev,
			},
			"links": links,
		}, nil
	}
}

func getByIDResolver(name string, tdb *db.TenantDB, policy *rbac.Policy) gql.FieldResolveFn {
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

		rows, err := tdb.QueryTenant(p.Context, tc.PGSchema,
			"SELECT * FROM "+name+" WHERE id = $1", idStr)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		result, err := pkghandlers.RowsToMaps(rows)
		if err != nil {
			return nil, err
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

func createResolver(name string, res *schema.ResourceSchema, tdb *db.TenantDB, hr *extensions.HookRunner, policy *rbac.Policy) gql.FieldResolveFn {
	return func(p gql.ResolveParams) (any, error) {
		tc := tenant.MustFromCtx(p.Context)
		if _, err := checkRBAC(p.Context, policy, name, "create"); err != nil {
			return nil, err
		}

		input, _ := p.Args["input"].(map[string]any)
		if len(input) == 0 {
			return nil, fmt.Errorf("empty input")
		}

		hc := hookCfg(name, "before_create", res)
		hookRes, err := hr.RunBeforeHook(p.Context, hc, input, nil)
		if err != nil {
			return nil, err
		}
		if !hookRes.Proceed {
			return nil, fmt.Errorf("%s", hookRes.Error)
		}
		body := hookRes.Data

		cols, placeholders, args := pkghandlers.BuildInsertArgs(body)
		q := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING *", name, cols, placeholders)
		result, err := tdb.ExecRowsTenant(p.Context, tc.PGSchema, q, args...)
		if err != nil {
			return nil, err
		}

		if afterHook := hookCfg(name, "after_create", res); afterHook != nil {
			var record map[string]any
			if len(result) > 0 {
				record = result[0]
			}
			go hr.RunAfterHook(context.Background(), afterHook, record, tc.ID)
		}

		if len(result) > 0 {
			return result[0], nil
		}
		return nil, nil
	}
}

func deleteResolver(name string, tdb *db.TenantDB, policy *rbac.Policy) gql.FieldResolveFn {
	return func(p gql.ResolveParams) (any, error) {
		tc := tenant.MustFromCtx(p.Context)
		if _, err := checkRBAC(p.Context, policy, name, "delete"); err != nil {
			return false, err
		}

		idStr, _ := p.Args["id"].(string)
		if _, err := uuid.Parse(idStr); err != nil {
			return false, fmt.Errorf("invalid id format")
		}

		affected, err := tdb.ExecTenant(p.Context, tc.PGSchema,
			"DELETE FROM "+name+" WHERE id = $1", idStr)
		if err != nil {
			return false, err
		}
		return affected > 0, nil
	}
}

// ── RBAC helper ───────────────────────────────────────────────────────────────

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
		return nil, fmt.Errorf("forbidden")
	}
	return &result, nil
}

// ── arg conversion ────────────────────────────────────────────────────────────

func argsToURLValues(args map[string]any) url.Values {
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
					}
				}
			case string:
				params.Set("filter["+field+"]", fv)
			}
		}
	}
	if orderMap, ok := args["order"].(map[string]any); ok {
		for field, dir := range orderMap {
			if ds, ok := dir.(string); ok {
				params.Set("order["+field+"]", ds)
				break // BuildQuery only supports single-field ordering
			}
		}
	}
	return params
}

// mapOp maps GraphQL filter op names to BuildQuery op names.
func mapOp(op string) string {
	if op == "exact" {
		return "eq"
	}
	return op // partial, start, after, before, gte, lte pass through
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
	case "uuid":
		return gql.ID
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
	case "uuid":
		return gql.ID
	default:
		return gql.String
	}
}

func filterInputFor(fieldType string, strF, dateF, rangeF *gql.InputObject) gql.Input {
	switch fieldType {
	case "string", "text":
		return strF
	case "time":
		return dateF
	case "int", "int64", "float64":
		return rangeF
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

func toPascalCase(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' })
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}

func singular(name string) string {
	if strings.HasSuffix(name, "ches") {
		return strings.TrimSuffix(name, "es")
	}
	if strings.HasSuffix(name, "ses") {
		return strings.TrimSuffix(name, "ses") + "s"
	}
	if strings.HasSuffix(name, "ies") {
		return strings.TrimSuffix(name, "ies") + "y"
	}
	return strings.TrimSuffix(name, "s")
}

func sortedNames(s *schema.APISchema) []string {
	names := make([]string, 0, len(s.Resources))
	for n := range s.Resources {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func sortedFieldNames(res *schema.ResourceSchema) []string {
	keys := make([]string, 0, len(res.Fields))
	for k := range res.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
