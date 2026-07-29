package rbac

// EvalContext carries the identity of the caller for a single request.
type EvalContext struct {
	Role             string
	UserID           string
	ExternalClientID string
}

// EvalResult is the outcome of a policy evaluation.
type EvalResult struct {
	Allowed       bool
	AllowedFields []string        // non-nil means restrict to these fields
	Condition     *WhereCondition // non-nil means append this WHERE clause
}

// WhereCondition is a resolved predicate ready to be appended to SQL.
type WhereCondition struct {
	Field string
	Op    string
	Value string // dynamic variables already substituted
}

// Evaluate determines whether evalCtx.Role may perform action on resource,
// resolves any dynamic variables in the applicable Condition, and returns the full
// result. The condition + field allowlist returned are ALWAYS the ones belonging to
// THIS resource: in the legacy form the role-global ones, in the per-resource form
// the matched resource's own — so every operation (read/create/update/delete/
// aggregate, on REST and GraphQL, which all funnel through this one call) scopes by
// the correct resource's condition.
func (p *Policy) Evaluate(evalCtx EvalContext, resource, action string) EvalResult {
	rp, ok := p.Roles[evalCtx.Role]
	if !ok {
		return EvalResult{Allowed: false}
	}

	// Custom-route grants (LIBRARY-GAPS-S1): a VIRTUAL segment, not a table. The
	// result therefore never carries a Condition or a field allowlist — there are no
	// rows to filter and no columns to project, and injecting a WHERE for a
	// non-existent table is exactly the runtime failure this design forbids. What the
	// handler does with real resources is authorized separately, by Ctx.Query/Insert/
	// Update, which re-evaluate the role against those resources.
	if grant, isRoute := routeGrantFor(&rp, resource); isRoute {
		return EvalResult{Allowed: actionAllowed(grant.Actions, action)}
	}

	// Per-resource form (G2): the role's permissions map is the sole source of
	// truth. A resource absent from the map is denied (deny-by-default), and the
	// matched entry carries its OWN condition/allowlist.
	if len(rp.Permissions) > 0 {
		perm, ok := rp.Permissions[resource]
		if !ok || !actionAllowed(perm.Actions, action) {
			return EvalResult{Allowed: false}
		}
		result := EvalResult{
			Allowed:       true,
			AllowedFields: perm.Fields,
		}
		if perm.Conditions != nil && conditionAppliesToAction(perm, action) {
			result.Condition = &WhereCondition{
				Field: perm.Conditions.Field,
				Op:    perm.Conditions.Op,
				Value: resolveVar(perm.Conditions.Val, evalCtx),
			}
		}
		return result
	}

	// Legacy role-global form (unchanged): one condition/allowlist for every
	// listed resource.
	if !resourceAllowed(rp.Resources, resource) {
		return EvalResult{Allowed: false}
	}
	if !actionAllowed(rp.Actions, action) {
		return EvalResult{Allowed: false}
	}

	result := EvalResult{
		Allowed:       true,
		AllowedFields: rp.FieldsAllow,
	}

	if rp.Conditions != nil {
		result.Condition = &WhereCondition{
			Field: rp.Conditions.Field,
			Op:    rp.Conditions.Op,
			Value: resolveVar(rp.Conditions.Val, evalCtx),
		}
	}

	return result
}

// conditionAppliesToAction reports whether a per-resource permission's condition
// gates this action. An empty ConditionActions means the condition applies to ALL
// granted actions (the safe default); a non-empty list scopes it (e.g. read absent
// from the list ⇒ reads are unconditional while writes stay owner-scoped).
func conditionAppliesToAction(perm ResourcePermission, action string) bool {
	if len(perm.ConditionActions) == 0 {
		return true
	}
	for _, a := range perm.ConditionActions {
		if a == action {
			return true
		}
	}
	return false
}

func resolveVar(val string, ctx EvalContext) string {
	switch val {
	case "$user_id":
		return ctx.UserID
	case "$external_client_id":
		return ctx.ExternalClientID
	default:
		return val
	}
}
