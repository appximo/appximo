package graphql

import (
	"context"
	"encoding/json"

	gql "github.com/graphql-go/graphql"
	"github.com/graphql-go/graphql/language/ast"

	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/query"
	"github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
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
