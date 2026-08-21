package schema

import "strings"

// Relation-subroute name derivation — the SINGLE source for turning a relation
// FIELD name into the URL segment of its generated read-only subroute
// `GET /api/{resource}/{id}/{segment}` (SILENT-CORRUPTION-S1). The route
// builder (pkg/codegen/builder.go), the OpenAPI generator and the write-files
// generator all derive the segment from this function, and the validator uses
// the SAME function to reject a collision at LOAD — the identical pattern to
// GraphQLTypeName / graphql_type_collision.
//
// Before this existed, the derivation lived as three inline TrimSuffix calls,
// and two relation fields collapsing to one segment (`customer` + `customer_id`
// → both `/api/orders/{id}/customer`) were silently overwritten in chi with the
// winner chosen by Go's randomized map iteration: the subroute could serve a
// DIFFERENT relation — with the other target's RBAC — after every restart.

// RelationSubroute is the URL segment a relation field's subroute serves under:
// the field name minus a trailing "_id" (`customer_id` → `customer`; a name
// without the suffix is used as-is).
func RelationSubroute(fieldName string) string {
	return strings.TrimSuffix(fieldName, "_id")
}
