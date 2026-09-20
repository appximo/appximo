package codegen

import (
	"context"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/appximo/appximo/pkg/ask"
	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/appximo/appximo/pkg/tenant"
)

// txWriter executes ONE confirmed voice write (VOZ-ESCRITURAS-S1) through
// the SAME cores the batch transaction runs — prepareTxOp (per-resource RBAC,
// the compiled validator, EnforceCreateRBAC / EnforceUpdateRBAC, the
// before_* hooks, the state-machine guard) and execPreparedOp (the SQL, the
// outbox event in the same tx) — then invalidates the tenant's read cache
// like every write path. It is built once per app by registerTransactionRoute
// and bound per request to a tenant + identity by askWriter. There is no
// third write path: what the API refuses, the voice refuses identically.
type txWriter struct {
	refs     map[string]*txResource
	policy   *rbac.Policy
	tdb      *db.TenantDB
	inv      CacheInvalidator
	hookEval func(ctx context.Context, hook *schema.HookConfig, body map[string]any) (map[string]any, int, string)
}

// askWriter is txWriter bound to one request's tenant and identity.
type askWriter struct {
	tw      *txWriter
	tc      *tenant.TenantCtx
	evalCtx rbac.EvalContext
}

func (w *askWriter) Write(ctx context.Context, kind, resource, id string, data map[string]any) (map[string]any, error) {
	op := txOp{Op: kind, Resource: resource, ID: id, Data: data}
	p, terr := prepareTxOp(ctx, &op, w.tw.refs, w.tw.policy, w.evalCtx, w.tw.hookEval)
	if terr != nil {
		return nil, toWriteError(terr)
	}
	var out map[string]any
	err := w.tw.tdb.WithTenantTx(ctx, w.tc.PGSchema, func(ctx context.Context, tx pgx.Tx) error {
		row, xerr := execPreparedOp(ctx, tx, w.tc.ID, p)
		if xerr != nil {
			return xerr
		}
		out = row
		return nil
	})
	if err != nil {
		if te, ok := err.(*txError); ok {
			return nil, toWriteError(te)
		}
		return nil, err
	}
	if w.tw.inv != nil {
		w.tw.inv.Invalidate(w.tc.ID)
	}
	return out, nil
}

func toWriteError(te *txError) *ask.WriteError {
	we := &ask.WriteError{Status: te.status, Msg: te.msg}
	for _, f := range te.fields {
		if f.Rule == "__unavailable__" {
			return &ask.WriteError{Status: 503, Msg: "database unavailable"}
		}
		we.Fields = append(we.Fields, ask.FieldError{Field: f.Field, Rule: f.Rule, Message: f.Message})
	}
	return we
}

// askWritesEnabled reads APPXIMO_ASK_WRITES: on (default) | off. Off makes
// every voice channel read-only again (a write plan is refused in words);
// the RBAC and the confirmation are the boundaries when it is on.
func askWritesEnabled() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("APPXIMO_ASK_WRITES")), "off")
}
