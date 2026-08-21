package query

import (
	"sort"
	"strconv"
	"strings"

	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
)

// RELATIONS-V1 (ADR-019): the ?include= → SQL compiler. Given a base resource and
// a comma/dot include spec, it emits ONE query that builds the nested JSON inside
// Postgres with json_build_object + json_agg + LEFT JOIN LATERAL — no N+1, no Go
// struct round-trip (the caller scans the single json column straight to the
// ResponseWriter). RBAC is applied AT COMPILATION: a relation fragment is emitted
// only if the role may read the target (its field allowlist scopes the embedded
// json_build_object, its row condition is injected into the lateral WHERE), so
// there is no path that returns a child the role may not see.
//
// The path WITHOUT ?include= never reaches this file: the handler keeps its exact
// previous SQL, so the no-include hot path is byte-identical (the gate).

// maxIncludeNodes bounds the total number of relation embeds in one request — an
// alias-amplification / fan-out guard (depth alone is not enough; ?include= could
// list many siblings). Generous for real use, fatal for abuse.
const maxIncludeNodes = 25

// RelationRBAC resolves, for a target resource, whether the request's role may
// read it, the field allowlist (empty = all fields), and the row-level condition
// to inject into the embed's WHERE. The caller wires this to policy.Evaluate so
// pkg/query stays decoupled from how RBAC is evaluated.
type RelationRBAC func(resource string) (allowed bool, fields []string, cond *rbac.WhereCondition)

// IncludeError carries an HTTP status so the handler maps a bad/forbidden include
// to 400 / 403 (never a 500). A nil *IncludeError means success.
type IncludeError struct {
	Status int
	Msg    string
}

func (e *IncludeError) Error() string { return e.Msg }

// includeNode is one node of the parsed ?include= tree (children keyed by embed name).
type includeNode struct {
	children map[string]*includeNode
}

func newIncludeNode() *includeNode { return &includeNode{children: map[string]*includeNode{}} }

// parseIncludeTree parses "lines.product,customer" into a tree. An empty entry
// (`include=a,` — a trailing comma; `include=,,,`; `include=a..b`) is an ERROR
// naming it (NIGHT-SWEEP-S1): empty segments used to be silently dropped, and a
// separator-only value even switched the response onto the include
// serialization path with zero embeds — accepted-and-ignored, the ADR-024
// class. (A wholly empty/absent ?include= never reaches here — HasInclude
// gates it in the handlers.)
func parseIncludeTree(include string) (*includeNode, *IncludeError) {
	root := newIncludeNode()
	for _, path := range strings.Split(include, ",") {
		path = strings.TrimSpace(path)
		if path == "" {
			return nil, &IncludeError{Status: 400, Msg: "include has an empty entry in " + strconv.Quote(include) + " — remove the extra comma"}
		}
		cur := root
		for _, seg := range strings.Split(path, ".") {
			seg = strings.TrimSpace(seg)
			if seg == "" {
				return nil, &IncludeError{Status: 400, Msg: "include has an empty segment in " + strconv.Quote(path) + " — remove the extra dot"}
			}
			next, ok := cur.children[seg]
			if !ok {
				next = newIncludeNode()
				cur.children[seg] = next
			}
			cur = next
		}
	}
	return root, nil
}

// includeBuilder accumulates the bound args (continuing from the base query's
// placeholders) and a monotonic alias counter while compiling the lateral tree.
type includeBuilder struct {
	s        *schema.APISchema
	rbacFor  RelationRBAC
	maxDepth int
	args     []any // starts as the base query's args; embeds append more
	aliasN   int
	nodes    int
}

func (b *includeBuilder) next() string {
	b.aliasN++
	return strconv.Itoa(b.aliasN)
}

// placeholder binds val and returns its $N reference (numbered across the whole
// statement: base args first, then embed args in emission order).
func (b *includeBuilder) placeholder(val any) string {
	b.args = append(b.args, val)
	return "$" + strconv.Itoa(len(b.args))
}

// quoteIdent wraps a schema-validated identifier in double quotes. Resource and
// field names are validated at schema load (both ^[a-z][a-z0-9_]*$) so they can
// never contain a quote — the wrap is injection-inert and lets a name work
// unqualified under the tenant search_path (it still safely quotes a hyphenated
// junction-table name, which the SQL layer tolerates).
func quoteIdent(s string) string { return `"` + s + `"` }

// BuildListInclude wraps a base list SELECT so each row becomes a nested JSON
// object with its requested embeds, returning ONE query that yields two columns:
// `data` (a json array of the wrapped rows, ordered as the base query ordered
// them) and `n` (the row count, for has_next). baseSelect/baseArgs come from
// QueryBuilder.SQL(); orderField/orderDir from QueryBuilder.EffectiveOrder().
func BuildListInclude(baseResource, include, baseSelect string, baseArgs []any, orderField, orderDir string, s *schema.APISchema, maxDepth int, rbacFor RelationRBAC) (sql string, args []any, ierr *IncludeError) {
	b := &includeBuilder{s: s, rbacFor: rbacFor, maxDepth: maxDepth, args: append([]any(nil), baseArgs...)}
	root, perr := parseIncludeTree(include)
	if perr != nil {
		return "", nil, perr
	}

	rowObj, joins, e := b.rowObject("_base", baseResource, root, 0)
	if e != nil {
		return "", nil, e
	}

	var sb strings.Builder
	sb.WriteString("SELECT COALESCE(json_agg(_q.row ORDER BY _q._ord ")
	sb.WriteString(orderDir)
	sb.WriteString("), '[]'::json)::text AS data, count(*) AS n FROM (SELECT ")
	sb.WriteString(rowObj)
	sb.WriteString(" AS row, _base.")
	sb.WriteString(quoteIdent(orderField))
	sb.WriteString(" AS _ord FROM (")
	sb.WriteString(baseSelect)
	sb.WriteString(") _base")
	for _, j := range joins {
		sb.WriteString(" ")
		sb.WriteString(j)
	}
	sb.WriteString(") _q")
	return sb.String(), b.args, nil
}

// BuildGetInclude wraps a single-row base SELECT (SELECT * ... WHERE id=$1 [+ row
// cond]) so the result is one nested JSON object (column `data`), or zero rows
// (→ 404). baseSelect/baseArgs come from the caller's get-by-id SQL.
func BuildGetInclude(baseResource, include, baseSelect string, baseArgs []any, s *schema.APISchema, maxDepth int, rbacFor RelationRBAC) (sql string, args []any, ierr *IncludeError) {
	b := &includeBuilder{s: s, rbacFor: rbacFor, maxDepth: maxDepth, args: append([]any(nil), baseArgs...)}
	root, perr := parseIncludeTree(include)
	if perr != nil {
		return "", nil, perr
	}

	rowObj, joins, e := b.rowObject("_base", baseResource, root, 0)
	if e != nil {
		return "", nil, e
	}

	var sb strings.Builder
	sb.WriteString("SELECT (")
	sb.WriteString(rowObj)
	sb.WriteString(")::text AS data FROM (")
	sb.WriteString(baseSelect)
	sb.WriteString(") _base")
	for _, j := range joins {
		sb.WriteString(" ")
		sb.WriteString(j)
	}
	sb.WriteString(" LIMIT 1")
	return sb.String(), b.args, nil
}

// rowObject builds a json_build_object expression for `resource` rows aliased as
// `alias`: the role-permitted scalar fields plus, for each requested child in
// node, the embed value. It also returns the LATERAL joins those embeds need
// (attached to the FROM that has `alias`). depth is the hop count to this row
// (base = 0); a child at depth+1 beyond maxDepth is a 400.
func (b *includeBuilder) rowObject(alias, resource string, node *includeNode, depth int) (string, []string, *IncludeError) {
	res, ok := b.s.Resources[resource]
	if !ok {
		return "", nil, &IncludeError{Status: 500, Msg: "unknown resource in relation: " + resource}
	}

	// Field allowlist for this resource (already known-allowed by the caller for
	// the base; checked before emit for every embed target).
	_, allowFields, _ := b.rbacFor(resource)
	allowSet := map[string]bool{"id": true}
	for _, f := range allowFields {
		allowSet[f] = true
	}
	unrestricted := len(allowFields) == 0

	// Stable column order: id first, then sorted fields.
	cols := make([]string, 0, len(res.Fields)+1)
	for name := range res.Fields {
		if name == "id" {
			continue
		}
		cols = append(cols, name)
	}
	sort.Strings(cols)

	pairs := make([]string, 0, len(cols)+1+len(node.children))
	pairs = append(pairs, "'id', "+alias+"."+quoteIdent("id"))
	for _, c := range cols {
		if unrestricted || allowSet[c] {
			pairs = append(pairs, "'"+c+"', "+alias+"."+quoteIdent(c))
		}
	}

	var joins []string
	// Embeds in deterministic order.
	relNames := make([]string, 0, len(node.children))
	for n := range node.children {
		relNames = append(relNames, n)
	}
	sort.Strings(relNames)

	for _, relName := range relNames {
		childDepth := depth + 1
		if childDepth > b.maxDepth {
			return "", nil, &IncludeError{Status: 400, Msg: "include nesting exceeds max depth " + strconv.Itoa(b.maxDepth)}
		}
		rel, ok := res.Relations[relName]
		if !ok {
			// Name the alternatives (ADR-024's second axis, NIGHT-SWEEP-S1): the
			// error used to name only the offender, and a resource with NO
			// relations gave the caller nothing to discover that the feature is
			// absent there.
			avail := make([]string, 0, len(res.Relations))
			for n := range res.Relations {
				avail = append(avail, n)
			}
			sort.Strings(avail)
			// FRESH-AGENT-GAPS-S1: a FK column alone does not enable ?include= —
			// the embed needs a declared `relations` entry. Say HOW, not just what.
			hint := "this resource declares no relations — a FK column alone does not enable ?include=; declare a `relations` block on the resource (see the schema docs)"
			if len(avail) > 0 {
				hint = "available: " + strings.Join(avail, ", ")
			}
			return "", nil, &IncludeError{Status: 400, Msg: "unknown relation " + relName + " on " + resource + " (" + hint + ")"}
		}
		join, valueExpr, e := b.buildEmbed(alias, resource, rel, node.children[relName], childDepth)
		if e != nil {
			return "", nil, e
		}
		pairs = append(pairs, "'"+relName+"', "+valueExpr)
		joins = append(joins, join)
	}

	return "json_build_object(" + strings.Join(pairs, ", ") + ")", joins, nil
}

// refColumnOf resolves the column a relation join matches on the side OPPOSITE
// the FK column: the FK field's `references` when declared (MIG-F1-S5), else
// "id" — via the SAME schema.FieldDef.ReferencedColumn the relation subroute
// uses, so the two surfaces can never diverge again (PUBLIC-SURFACE-S1: the
// embed used to hardcode id and answered null for a non-id `references` FK the
// subroute followed correctly). ownerResource is the resource the FK column
// lives ON (source for belongs_to, target for has_many, the junction for
// many_to_many). An owner that is not a declared resource, an FK column not
// declared as a field, or a field whose `relation` names a DIFFERENT resource
// than the one this join targets (an incoherent relations-block declaration —
// its `references` would name a column of the wrong table) keeps the implicit
// "id" (the only possibility before `references` existed).
func (b *includeBuilder) refColumnOf(ownerResource, fkField, joinTarget string) string {
	res, ok := b.s.Resources[ownerResource]
	if !ok {
		return "id"
	}
	fd, ok := res.Fields[fkField]
	if !ok {
		return "id"
	}
	if fd.Relation != joinTarget {
		return "id"
	}
	return fd.ReferencedColumn()
}

// buildEmbed compiles one relation embed correlated to parentAlias, returning the
// LEFT JOIN LATERAL fragment and the json value expression (latAlias.j) to drop
// into the parent's json_build_object. RBAC for the target is enforced here.
// sourceResource is the resource parentAlias rows belong to (needed to resolve
// a belongs_to FK's `references`).
func (b *includeBuilder) buildEmbed(parentAlias, sourceResource string, rel schema.RelationDef, node *includeNode, depth int) (string, string, *IncludeError) {
	b.nodes++
	if b.nodes > maxIncludeNodes {
		return "", "", &IncludeError{Status: 400, Msg: "too many includes"}
	}

	allowed, _, cond := b.rbacFor(rel.Target)
	if !allowed {
		return "", "", &IncludeError{Status: 403, Msg: "forbidden: role may not read " + rel.Target + " via include"}
	}

	n := b.next()
	childAlias := "_c" + n
	latAlias := "_r" + n

	// The target row's JSON (recurses for nested embeds; nested joins live INSIDE
	// the embed subquery so they correlate to childAlias).
	rowObj, nestedJoins, e := b.rowObject(childAlias, rel.Target, node, depth)
	if e != nil {
		return "", "", e
	}
	nested := ""
	if len(nestedJoins) > 0 {
		nested = " " + strings.Join(nestedJoins, " ")
	}

	// Target row-level RBAC condition, injected into the embed WHERE.
	rowCond := ""
	if cond != nil && cond.Field != "" {
		if !conditionFieldRe.MatchString(cond.Field) {
			return "", "", &IncludeError{Status: 500, Msg: "invalid rbac condition field"}
		}
		rowCond = " AND " + childAlias + "." + quoteIdent(cond.Field) + " = " + b.placeholder(cond.Value)
	}

	limit := rel.Limit
	if limit <= 0 {
		limit = schema.DefaultEmbedLimit
	}
	limitStr := strconv.Itoa(limit)

	var sb strings.Builder
	switch rel.Type {
	case schema.RelationBelongsTo:
		// parent → its one target: target.<ref> = parent.<fk>, where <ref> is the
		// FK field's `references` (default id). Null when absent.
		sb.WriteString("LEFT JOIN LATERAL (SELECT ")
		sb.WriteString(rowObj)
		sb.WriteString(" AS j FROM ")
		sb.WriteString(quoteIdent(rel.Target))
		sb.WriteString(" ")
		sb.WriteString(childAlias)
		sb.WriteString(nested)
		sb.WriteString(" WHERE ")
		sb.WriteString(childAlias)
		sb.WriteString(".")
		sb.WriteString(quoteIdent(b.refColumnOf(sourceResource, rel.FK, rel.Target)))
		sb.WriteString(" = ")
		sb.WriteString(parentAlias)
		sb.WriteString(".")
		sb.WriteString(quoteIdent(rel.FK))
		sb.WriteString(rowCond)
		sb.WriteString(" LIMIT 1) ")
		sb.WriteString(latAlias)
		sb.WriteString(" ON TRUE")

	case schema.RelationHasMany:
		// parent → many children: child.<fk> = parent.<ref>, where <ref> is the
		// child FK field's `references` (default id). Top-N by id.
		sb.WriteString("LEFT JOIN LATERAL (SELECT COALESCE(json_agg(_e ORDER BY _ord), '[]'::json) AS j FROM (SELECT ")
		sb.WriteString(rowObj)
		sb.WriteString(" AS _e, ")
		sb.WriteString(childAlias)
		sb.WriteString(".")
		sb.WriteString(quoteIdent("id"))
		sb.WriteString(" AS _ord FROM ")
		sb.WriteString(quoteIdent(rel.Target))
		sb.WriteString(" ")
		sb.WriteString(childAlias)
		sb.WriteString(nested)
		sb.WriteString(" WHERE ")
		sb.WriteString(childAlias)
		sb.WriteString(".")
		sb.WriteString(quoteIdent(rel.FK))
		sb.WriteString(" = ")
		sb.WriteString(parentAlias)
		sb.WriteString(".")
		sb.WriteString(quoteIdent(b.refColumnOf(rel.Target, rel.FK, sourceResource)))
		sb.WriteString(rowCond)
		sb.WriteString(" ORDER BY ")
		sb.WriteString(childAlias)
		sb.WriteString(".")
		sb.WriteString(quoteIdent("id"))
		sb.WriteString(" LIMIT ")
		sb.WriteString(limitStr)
		sb.WriteString(") _sub) ")
		sb.WriteString(latAlias)
		sb.WriteString(" ON TRUE")

	case schema.RelationManyToMany:
		// parent ↔ targets via junction: through.<fk> = parent.<refP>, join target
		// on target.<refT> = through.<target_fk> — each junction FK field's
		// `references` resolved like any other FK (default id), top-N by target id.
		thrAlias := "_h" + n
		sb.WriteString("LEFT JOIN LATERAL (SELECT COALESCE(json_agg(_e ORDER BY _ord), '[]'::json) AS j FROM (SELECT ")
		sb.WriteString(rowObj)
		sb.WriteString(" AS _e, ")
		sb.WriteString(childAlias)
		sb.WriteString(".")
		sb.WriteString(quoteIdent("id"))
		sb.WriteString(" AS _ord FROM ")
		sb.WriteString(quoteIdent(rel.Through))
		sb.WriteString(" ")
		sb.WriteString(thrAlias)
		sb.WriteString(" JOIN ")
		sb.WriteString(quoteIdent(rel.Target))
		sb.WriteString(" ")
		sb.WriteString(childAlias)
		sb.WriteString(" ON ")
		sb.WriteString(childAlias)
		sb.WriteString(".")
		sb.WriteString(quoteIdent(b.refColumnOf(rel.Through, rel.TargetFK, rel.Target)))
		sb.WriteString(" = ")
		sb.WriteString(thrAlias)
		sb.WriteString(".")
		sb.WriteString(quoteIdent(rel.TargetFK))
		sb.WriteString(nested)
		sb.WriteString(" WHERE ")
		sb.WriteString(thrAlias)
		sb.WriteString(".")
		sb.WriteString(quoteIdent(rel.FK))
		sb.WriteString(" = ")
		sb.WriteString(parentAlias)
		sb.WriteString(".")
		sb.WriteString(quoteIdent(b.refColumnOf(rel.Through, rel.FK, sourceResource)))
		sb.WriteString(rowCond)
		sb.WriteString(" ORDER BY ")
		sb.WriteString(childAlias)
		sb.WriteString(".")
		sb.WriteString(quoteIdent("id"))
		sb.WriteString(" LIMIT ")
		sb.WriteString(limitStr)
		sb.WriteString(") _sub) ")
		sb.WriteString(latAlias)
		sb.WriteString(" ON TRUE")

	default:
		return "", "", &IncludeError{Status: 500, Msg: "unknown relation type " + rel.Type}
	}

	return sb.String(), latAlias + ".j", nil
}

// HasInclude reports whether the request opted into relation embedding. Kept tiny
// so the no-include hot path is a single empty-string check at the call site.
func HasInclude(include string) bool { return strings.TrimSpace(include) != "" }
