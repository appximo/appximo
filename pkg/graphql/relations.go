package graphql

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	gql "github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/query"
	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
)

// RELATIONS-V1 for GraphQL (ADR-019 §6): a nested relation field selection compiles
// to the SAME json_agg + LATERAL query as REST ?include= — one round-trip, no
// dataloader, no N+1 at the SQL layer. The nested SQL is built by pkg/query; this
// file only (a) discovers which relations a query selects and (b) surfaces the
// resulting nested maps so graphql-go's default resolver can read them.

// makeRelRBAC binds the request identity to policy.Evaluate for the include
// compiler (read access / field allowlist / row condition per relation target).
func makeRelRBAC(ctx context.Context, policy *rbac.Policy) query.RelationRBAC {
	var ev rbac.EvalContext
	if c := auth.ClaimsFromCtx(ctx); c != nil {
		ev = rbac.EvalContext{Role: c.Role, UserID: c.UserID, ExternalClientID: c.ExternalClientID}
	}
	return func(resource string) (bool, []string, *rbac.WhereCondition) {
		r := policy.Evaluate(ev, resource, "read")
		return r.Allowed, r.AllowedFields, r.Condition
	}
}

// includePathsForObject walks an object-type selection set and returns the
// requested relation paths (e.g. ["lines.product","customer"]) rooted at
// resource. Scalar fields are ignored; inline fragments and named fragment
// spreads are followed so the include tree is complete.
func includePathsForObject(ss *ast.SelectionSet, resource string, s *schema.APISchema, fragments map[string]ast.Definition) []string {
	if ss == nil {
		return nil
	}
	res, ok := s.Resources[resource]
	if !ok {
		return nil
	}
	var paths []string
	for _, sel := range ss.Selections {
		switch f := sel.(type) {
		case *ast.Field:
			if f.Name == nil {
				continue
			}
			rel, isRel := res.Relations[f.Name.Value]
			if !isRel {
				continue // scalar field
			}
			sub := includePathsForObject(f.SelectionSet, rel.Target, s, fragments)
			if len(sub) == 0 {
				paths = append(paths, f.Name.Value)
			} else {
				for _, sp := range sub {
					paths = append(paths, f.Name.Value+"."+sp)
				}
			}
		case *ast.InlineFragment:
			paths = append(paths, includePathsForObject(f.SelectionSet, resource, s, fragments)...)
		case *ast.FragmentSpread:
			if f.Name == nil {
				continue
			}
			if frag, ok := fragments[f.Name.Value].(*ast.FragmentDefinition); ok {
				paths = append(paths, includePathsForObject(frag.SelectionSet, resource, s, fragments)...)
			}
		}
	}
	return paths
}

// listIncludePaths extracts the relation paths from a LIST query's selection: the
// relations live under the connection's `data` field, so it descends into `data`
// before walking the object selection.
func listIncludePaths(info gql.ResolveInfo, resource string, s *schema.APISchema) []string {
	for _, fa := range info.FieldASTs {
		if fa.SelectionSet == nil {
			continue
		}
		for _, sel := range fa.SelectionSet.Selections {
			if f, ok := sel.(*ast.Field); ok && f.Name != nil && f.Name.Value == "data" {
				return includePathsForObject(f.SelectionSet, resource, s, info.Fragments)
			}
		}
	}
	return nil
}

// objectIncludePaths extracts relation paths from a single-object (get-by-id)
// query selection.
func objectIncludePaths(info gql.ResolveInfo, resource string, s *schema.APISchema) []string {
	for _, fa := range info.FieldASTs {
		if p := includePathsForObject(fa.SelectionSet, resource, s, info.Fragments); len(p) > 0 {
			return p
		}
	}
	return nil
}

// scalarFieldsForObject collects the DECLARED columns an object selection set
// names on resource (MOTOR-FIELDS-S1): the projection GraphQL pushes into the
// SQL. Relation fields (embeds), `__typename` and `id` are skipped or implied;
// inline fragments and named spreads are followed like includePathsForObject.
// It returns ok=false when a selection names something that is neither a
// declared field, a relation nor a meta field — the caller then keeps
// `SELECT *`, so an unknown shape can never project a column away.
func scalarFieldsForObject(ss *ast.SelectionSet, res *schema.ResourceSchema, fragments map[string]ast.Definition, out map[string]bool) bool {
	if ss == nil {
		return false
	}
	for _, sel := range ss.Selections {
		switch f := sel.(type) {
		case *ast.Field:
			if f.Name == nil {
				return false
			}
			n := f.Name.Value
			if n == "id" || strings.HasPrefix(n, "__") {
				continue
			}
			if _, isRel := res.Relations[n]; isRel {
				continue
			}
			if _, ok := res.Fields[n]; !ok {
				return false
			}
			out[n] = true
		case *ast.InlineFragment:
			if !scalarFieldsForObject(f.SelectionSet, res, fragments, out) {
				return false
			}
		case *ast.FragmentSpread:
			if f.Name == nil {
				return false
			}
			frag, ok := fragments[f.Name.Value].(*ast.FragmentDefinition)
			if !ok || !scalarFieldsForObject(frag.SelectionSet, res, fragments, out) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// projectionFor turns the collected selection into the column list handed to
// QueryBuilder.SelectOnly, bounded by the role's allowlist (nil = all): a
// hidden field the client selected is simply NOT read — it resolves `null`
// through the result scrubber exactly as before, so GraphQL's contract for a
// forbidden field is unchanged while the SQL stops reading it. Sorted for
// deterministic SQL.
func projectionFor(sel map[string]bool, allowed []string) []string {
	cols := make([]string, 0, len(sel)+1)
	for n := range sel {
		if len(allowed) == 0 || fieldAllowedGQL(n, allowed) {
			cols = append(cols, n)
		}
	}
	sort.Strings(cols)
	return append([]string{"id"}, cols...)
}

func fieldAllowedGQL(field string, allowlist []string) bool {
	for _, a := range allowlist {
		if a == field {
			return true
		}
	}
	return false
}

// listProjection is the projection of a LIST query's `data { … }` selection
// (nil when the selection cannot be trusted as a projection — keep SELECT *).
func listProjection(info gql.ResolveInfo, res *schema.ResourceSchema, allowed []string) []string {
	for _, fa := range info.FieldASTs {
		if fa.SelectionSet == nil {
			continue
		}
		for _, sel := range fa.SelectionSet.Selections {
			if f, ok := sel.(*ast.Field); ok && f.Name != nil && f.Name.Value == "data" {
				out := map[string]bool{}
				if !scalarFieldsForObject(f.SelectionSet, res, info.Fragments, out) {
					return nil
				}
				return projectionFor(out, allowed)
			}
		}
	}
	return nil
}

// objectProjection is listProjection for a single-object (get-by-id) query.
func objectProjection(info gql.ResolveInfo, res *schema.ResourceSchema, allowed []string) []string {
	for _, fa := range info.FieldASTs {
		if fa.SelectionSet == nil {
			continue
		}
		out := map[string]bool{}
		if !scalarFieldsForObject(fa.SelectionSet, res, info.Fragments, out) {
			return nil
		}
		return projectionFor(out, allowed)
	}
	return nil
}

// joinPaths turns include paths into the ?include= comma form the compiler parses.
func joinPaths(paths []string) string {
	out := ""
	for i, p := range paths {
		if i > 0 {
			out += ","
		}
		out += p
	}
	return out
}

// unmarshalList parses the json array bytes (from IncludeListJSON) into the
// []map[string]any graphql-go's default resolver reads (nested relations included).
func unmarshalList(data []byte) ([]map[string]any, error) {
	if len(data) == 0 {
		return []map[string]any{}, nil
	}
	var out []map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out, nil
}
