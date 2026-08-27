package codegen

import (
	"fmt"
	"net/http"

	"github.com/appximo/appximo/pkg/rbac"
)

// This file is THE single implementation of the row-condition write rule:
// what a role whose grant carries a row condition may do with the condition
// COLUMN in a write body. Both halves live here so a reader sees the whole
// policy at once, and so a door cannot honor one half and forget the other
// (MOTOR-AUTORIZACION-S1 — ENG-45 family 1, the row give-away).
//
// THE RULE. A row condition bound to the caller's identity ($user_id /
// $external_client_id — rbac.WhereCondition.Identity) makes its column the
// row's OWNERSHIP, and ownership is the server's to assign, never the
// client's:
//
//   - on CREATE the column is FORCED to the caller (a body naming another
//     principal is 403) — EnforceCreateRBAC, since FASE3-SEC;
//   - on UPDATE a body that names the column with anything but the caller's
//     own id — another principal, null — is 403 with the SAME message; a
//     full-replacement PUT that omits the column keeps the caller's value
//     instead of writing NULL; re-sending the caller's own id is a no-op —
//     EnforceUpdateRBAC, new in this session.
//
// THE HOLE THE UPDATE HALF CLOSES. Create had this rule; update put the
// condition only in the WHERE, so `PATCH {"owner_id": "<other>"}` on an OWNED
// row answered 200 and handed the row to another principal — through REST
// PATCH/PUT, GraphQL, the batch transaction and Ctx.Update, in the
// per-resource, role-global and condition_actions forms alike; a PUT that
// omitted a nullable condition column wrote NULL (the row left every
// principal's scope); and a role whose field allowlist excluded the column
// had the attempt DROPPED silently. Verified request by request against the
// published v0.1.8 and v0.1.9 (docs/audits/AUTHZ_WRITE_AUDIT_S1.md).
//
// The rule is checked on the CLIENT body, before the row lookup and before
// the before_update hook: it depends only on the body and the token, so it
// can never become an existence oracle (a hidden row stays 404 whether or
// not the body names the column), and a before_update hook — the app
// owner's server-side code — keeps the ability to reassign deliberately.
// The allowlist does not hide the attempt: the body is judged before the
// allowlist projection, so the answer is an explicit 403, never a 200 that
// silently ignored the field.
//
// WHAT IS NOT BOUND, on purpose. A LITERAL condition (status = "pending") is
// a visibility filter, not ownership: a moderator scoped to pending tickets
// approving one moves it out of scope and that is the workflow. Roles with
// no condition on update (admin, a `condition_actions` list that leaves
// update unconditional) reassign freely — that is the declared server-side
// path for "transfer this record to that user", together with a custom
// handler running as such a role or on its own SQL.

// EnforceCreateRBAC applies a role's field allowlist and row-level condition to a
// CREATE body, in place, identically for the REST POST and the GraphQL create:
//
//   - Field allowlist: fields outside the role's allowlist are DROPPED from the
//     body (silently — the same contract CollectUpdate uses for update; never an
//     error), EXCEPT the row-condition field, which is server-forced below and
//     therefore implicitly allowed.
//   - Row-level condition: a row-scoped role (e.g. user_id = $user_id) must create
//     rows attributed to ITSELF. The condition field is FORCED to the principal's
//     resolved value; if the body supplies a DIFFERENT non-null value, the create is
//     REJECTED with 403 — a client can never create a row owned by another principal.
//
// Returns (0,"") to proceed, or (403, msg) to reject. ev may be nil (no policy result
// — e.g. a test without the RBAC middleware, or the library Ctx path), and a role with
// neither an allowlist nor a condition (e.g. rrhh-admin) is a cheap no-op, so the
// unrestricted create path keeps behaving exactly as before (the GATE-WRITE case).
func EnforceCreateRBAC(body map[string]any, ev *rbac.EvalResult) (int, string) {
	if ev == nil {
		return 0, ""
	}
	condField := ""
	if ev.Condition != nil {
		condField = ev.Condition.Field
	}
	if len(ev.AllowedFields) > 0 {
		allow := make(map[string]struct{}, len(ev.AllowedFields))
		for _, f := range ev.AllowedFields {
			allow[f] = struct{}{}
		}
		for k := range body {
			if k == condField {
				continue // server-forced below; never client-droppable
			}
			if _, ok := allow[k]; !ok {
				delete(body, k)
			}
		}
	}
	if ev.Condition != nil {
		if cur, present := body[condField]; present && cur != nil {
			if fmt.Sprintf("%v", cur) != ev.Condition.Value {
				return http.StatusForbidden, principalMismatch(condField)
			}
		}
		body[condField] = ev.Condition.Value
	}
	return 0, ""
}

// EnforceUpdateRBAC applies the identity-column rule to an UPDATE. body is the
// client's request body as decoded (BEFORE any allowlist projection — the
// attempt must be judged, not hidden); sets is the column set the update will
// write (CollectUpdate's result: for a PUT it carries every writable column,
// absent ones as nil). It returns (403, msg) when the body names the
// identity column with anything but the caller's own id, and otherwise
// proceeds — forcing, in place, a PUT-omitted nullable identity column back
// to the caller's value so a full replacement never orphans the row.
//
// A nil ev, a role without a condition, or a LITERAL condition is a no-op:
// one nil check on the unscoped hot path (measured no_change).
func EnforceUpdateRBAC(body, sets map[string]any, ev *rbac.EvalResult) (int, string) {
	if ev == nil || ev.Condition == nil || !ev.Condition.Identity {
		return 0, ""
	}
	col := ev.Condition.Field
	if cur, present := body[col]; present {
		if cur == nil || fmt.Sprintf("%v", cur) != ev.Condition.Value {
			return http.StatusForbidden, principalMismatch(col)
		}
		return 0, "" // the caller's own id: a no-op re-send (full-object saves)
	}
	// Absent from the body. A full replacement writes every writable column,
	// this one as NULL — keep it the caller's instead (create's forcing, on
	// the update side). A partial update leaves an absent column untouched.
	if v, inSets := sets[col]; inSets && v == nil {
		sets[col] = ev.Condition.Value
	}
	return 0, ""
}

// principalMismatch is the ONE message both halves answer — a pinned public
// contract (the create side has answered it since FASE3-SEC).
func principalMismatch(col string) string {
	return fmt.Sprintf("field %q must match the authenticated principal", col)
}
