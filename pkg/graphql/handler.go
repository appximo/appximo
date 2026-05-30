package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

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

// BuildHandler constructs an http.Handler for the /graphql endpoint.
// Callers must ensure tenant.TenantMiddleware runs before this handler.
func BuildHandler(s *schema.APISchema, tdb *db.TenantDB, hr *extensions.HookRunner, policy *rbac.Policy) http.Handler {
	gqlSchema := buildGQLSchema(s, tdb, hr, policy)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := context.WithValue(r.Context(), httpReqKey{}, r)

		var params struct {
			Query         string         `json:"query"`
			Variables     map[string]any `json:"variables"`
			OperationName string         `json:"operationName"`
		}
		if r.Method == http.MethodGet {
			params.Query = r.URL.Query().Get("query")
			params.OperationName = r.URL.Query().Get("operationName")
		} else {
			json.NewDecoder(r.Body).Decode(&params) //nolint:errcheck
		}

		result := gql.Do(gql.Params{
			Schema:         gqlSchema,
			RequestString:  params.Query,
			VariableValues: params.Variables,
			OperationName:  params.OperationName,
			Context:        ctx,
		})

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(result) //nolint:errcheck
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
		if _, err := checkRBAC(p.Context, policy, name, "read"); err != nil {
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
		return result[0], nil
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
	r, _ := ctx.Value(httpReqKey{}).(*http.Request)

	evalCtx := rbac.EvalContext{}
	if r != nil {
		evalCtx.Role = r.Header.Get("X-User-Role")
		evalCtx.UserID = r.Header.Get("X-User-ID")
		evalCtx.ExternalClientID = r.Header.Get("X-External-Client-ID")
	}
	if claims := auth.ClaimsFromCtx(ctx); claims != nil {
		evalCtx.Role = claims.Role
		evalCtx.UserID = claims.UserID
		evalCtx.ExternalClientID = claims.ExternalClientID
	}
	if evalCtx.Role == "" {
		return nil, fmt.Errorf("missing token")
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
