package schema

import "strings"

// GraphQL type/field name derivation — the SINGLE source for turning a resource
// name into its GraphQL identifiers. pkg/graphql derives every generated type
// (`<Type>`, `<Type>Filter`, `create<Type>`, …) from GraphQLTypeName, and the
// validator uses the SAME function to detect a collision at LOAD instead of a
// boot panic (FRESH-AGENT-GAPS-S1). One function means the two can never
// disagree about which resources collide.

// GraphQLSingular strips a trailing plural so a resource `guides` yields the
// type stem `guide`. It is intentionally simple (the engine's naming, not a
// linguistics library); its ONLY contract is that pkg/graphql and the validator
// compute the same value.
func GraphQLSingular(name string) string {
	switch {
	case strings.HasSuffix(name, "ches"):
		return strings.TrimSuffix(name, "es")
	case strings.HasSuffix(name, "ses"):
		return strings.TrimSuffix(name, "ses") + "s"
	case strings.HasSuffix(name, "ies"):
		return strings.TrimSuffix(name, "ies") + "y"
	default:
		return strings.TrimSuffix(name, "s")
	}
}

// GraphQLPascal upper-cases each `_`/`-`-separated segment and joins them.
func GraphQLPascal(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' })
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}

// GraphQLTypeName is the type stem a resource contributes to the GraphQL schema
// (e.g. `categorias` and `categoria` both → `Categoria`, which is why two such
// resources collide). Every generated GraphQL type name is built from this.
func GraphQLTypeName(resourceName string) string {
	return GraphQLPascal(GraphQLSingular(resourceName))
}
