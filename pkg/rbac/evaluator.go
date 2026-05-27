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
// resolves any dynamic variables in Conditions, and returns the full result.
func (p *Policy) Evaluate(evalCtx EvalContext, resource, action string) EvalResult {
	rp, ok := p.Roles[evalCtx.Role]
	if !ok {
		return EvalResult{Allowed: false}
	}

	if !resourceAllowed(rp.Resources, resource) {
		return EvalResult{Allowed: false}
	}

	allowed := false
	for _, a := range rp.Actions {
		if a == "*" || a == action {
			allowed = true
			break
		}
	}
	if !allowed {
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
