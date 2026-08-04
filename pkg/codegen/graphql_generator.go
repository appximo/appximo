package codegen

import (
	"fmt"
	"strings"

	"github.com/appximo/appximo/pkg/schema"
)

const graphqlSharedTypes = `enum OrderDirection { ASC DESC }

input StringFilter {
  exact:   String
  partial: String
  start:   String
}

input DateFilter {
  after:  String
  before: String
  gte:    String
  lte:    String
}

input RangeFilter {
  gte: Float
  lte: Float
}

type PageMeta {
  page:        Int!
  per_page:    Int!
  total:       Int!
  total_pages: Int!
  has_next:    Boolean!
  has_prev:    Boolean!
}

type PageLinks {
  self:  String
  first: String
  last:  String
  next:  String
  prev:  String
}

`

// GenerateGraphQL emits a GraphQL SDL schema document from an APISchema.
func GenerateGraphQL(s *schema.APISchema) string {
	var sb strings.Builder
	sb.WriteString(graphqlSharedTypes)
	// The JSON scalar is declared only when the schema actually has a jsonb
	// field, so an SDL for a schema without one is byte-identical to before.
	if schemaHasJSONB(s) {
		sb.WriteString("scalar JSON\n\n")
	}

	names := sortedResourceKeys(s)

	for _, name := range names {
		res := s.Resources[name]
		title := toPascalCase(gqlSingular(name)) // singular: Guide, not Guides

		// type T { ... }
		sb.WriteString(fmt.Sprintf("type %s {\n", title))
		sb.WriteString("  id: ID!\n")
		for _, fname := range sortedFieldKeys(&res) {
			fd := res.Fields[fname]
			gt := gqlFieldType(fd)
			bang := ""
			if fd.Required && !fd.Auto {
				bang = "!"
			}
			sb.WriteString(fmt.Sprintf("  %s: %s%s\n", fname, gt, bang))
		}
		sb.WriteString("}\n\n")

		// type TConnection { ... }
		sb.WriteString(fmt.Sprintf("type %sConnection {\n", title))
		sb.WriteString(fmt.Sprintf("  data:  [%s!]!\n", title))
		sb.WriteString("  meta:  PageMeta!\n")
		sb.WriteString("  links: PageLinks!\n")
		sb.WriteString("}\n\n")

		// input TInput { ... }
		sb.WriteString(fmt.Sprintf("input %sInput {\n", title))
		for _, fname := range sortedFieldKeys(&res) {
			fd := res.Fields[fname]
			if fd.Auto {
				continue
			}
			gt := gqlFieldType(fd)
			bang := ""
			if fd.Required {
				bang = "!"
			}
			sb.WriteString(fmt.Sprintf("  %s: %s%s\n", fname, gt, bang))
		}
		sb.WriteString("}\n\n")

		// input TFilter { ... }
		sb.WriteString(fmt.Sprintf("input %sFilter {\n", title))
		for _, fname := range sortedFieldKeys(&res) {
			fd := res.Fields[fname]
			ft := gqlFilterType(fd.Type)
			if ft == "" {
				continue
			}
			sb.WriteString(fmt.Sprintf("  %s: %s\n", fname, ft))
		}
		sb.WriteString("}\n\n")

		// input TOrder { ... }
		sb.WriteString(fmt.Sprintf("input %sOrder {\n", title))
		for _, fname := range sortedFieldKeys(&res) {
			fd := res.Fields[fname]
			if gqlIsOrderable(fd.Type) {
				sb.WriteString(fmt.Sprintf("  %s: OrderDirection\n", fname))
			}
		}
		sb.WriteString("}\n\n")
	}

	// type Query { ... }
	sb.WriteString("type Query {\n")
	for _, name := range names {
		title := toPascalCase(gqlSingular(name))
		sing := gqlSingular(name)
		sb.WriteString(fmt.Sprintf("  %s(\n    page:     Int\n    per_page: Int\n    filter:   %sFilter\n    order:    %sOrder\n  ): %sConnection!\n",
			name, title, title, title))
		sb.WriteString(fmt.Sprintf("  %s(id: ID!): %s\n", sing, title))
	}
	sb.WriteString("}\n\n")

	// type Mutation { ... }
	sb.WriteString("type Mutation {\n")
	for _, name := range names {
		title := toPascalCase(gqlSingular(name))
		sb.WriteString(fmt.Sprintf("  create%s(input: %sInput!): %s!\n", title, title, title))
		sb.WriteString(fmt.Sprintf("  delete%s(id: ID!): Boolean!\n", title))
	}
	sb.WriteString("}\n")

	return sb.String()
}

// schemaHasJSONB reports whether any resource declares a `jsonb` field — the
// condition for emitting the JSON scalar into the SDL.
func schemaHasJSONB(s *schema.APISchema) bool {
	for _, res := range s.Resources {
		for _, fd := range res.Fields {
			if fd.Type == "jsonb" {
				return true
			}
		}
	}
	return false
}

func gqlFieldType(fd schema.FieldDef) string {
	switch fd.Type {
	case "int", "int64":
		return "Int"
	case "float64":
		return "Float"
	case "bool":
		return "Boolean"
	case "uuid", "file":
		return "ID"
	case "jsonb":
		// A jsonb column is an arbitrary document — the JSON scalar carries it
		// through unchanged (pkg/graphql.jsonScalar). `json` (TEXT-backed) stays
		// String, so every pre-existing schema's SDL is byte-unchanged.
		return "JSON"
	default: // string, text, time, json
		return "String"
	}
}

func gqlFilterType(fieldType string) string {
	switch fieldType {
	case "string", "text":
		return "StringFilter"
	case "time":
		return "DateFilter"
	case "int", "int64", "float64":
		return "RangeFilter"
	default:
		return ""
	}
}

func gqlIsOrderable(fieldType string) bool {
	switch fieldType {
	case "string", "text", "int", "int64", "float64", "time":
		return true
	default:
		return false
	}
}

// gqlSingular converts a plural resource name to its singular form.
func gqlSingular(name string) string {
	if strings.HasSuffix(name, "ches") {
		return strings.TrimSuffix(name, "es") // dispatches → dispatch
	}
	if strings.HasSuffix(name, "ses") {
		return strings.TrimSuffix(name, "ses") + "s" // uses → use
	}
	if strings.HasSuffix(name, "ies") {
		return strings.TrimSuffix(name, "ies") + "y" // categories → category
	}
	return strings.TrimSuffix(name, "s") // guides → guide
}
