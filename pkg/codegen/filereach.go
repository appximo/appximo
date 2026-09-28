package codegen

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/appximo/appximo/pkg/auth"

	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/pkg/query"
	"github.com/appximo/appximo/pkg/rbac"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/jackc/pgx/v5"
)

// FILES-3 (2026-09-28, measured with two real users): the file store
// authorizes by the `files` ACTION alone, so a second user with
// `files: ["read"]` read the bytes of a file attached to a row they could not
// see — `GET /api/files/{id}` answered 200, `GET /api/files/{id}/url` minted
// a no-auth URL for it, and the id could even be attached to a row of their
// own. The id itself is not discoverable (every row-scoped door hides it),
// but a known id was enough.
//
// The rule now: **a file that at least one row REFERENCES is reachable only
// through a row the caller may read** — the referencing resource's own RBAC
// and row condition, the same ones a list obeys. A file that NO row
// references stays reachable to the role (the upload→attach window: the
// uploader is not recorded, so the engine cannot tell whose it is). A schema
// that declares no `file` field pays nothing: the guard is not installed.

// FileRefColumn is one (resource, column) pair that holds a file id.
type FileRefColumn struct {
	Resource string
	Column   string
}

// FileRefColumns lists every file field the schema declares.
func FileRefColumns(s *schema.APISchema) []FileRefColumn {
	var out []FileRefColumn
	for _, name := range sortedResourceNames(s) {
		res := s.Resources[name]
		for _, f := range sortedFieldNames(res) {
			if res.Fields[f].Type == "file" {
				out = append(out, FileRefColumn{Resource: name, Column: f})
			}
		}
	}
	return out
}

// FileReachable reports whether this caller may reach the file: referenced by
// a row they can read, or referenced by nothing at all. An error is a denial
// for the caller and is returned for the log.
func FileReachable(ctx context.Context, tdb *db.TenantDB, pgSchema string, cols []FileRefColumn, pol *rbac.Policy, ev rbac.EvalContext, fileID string) (bool, error) {
	if len(cols) == 0 {
		return true, nil
	}
	referenced := false
	for _, c := range cols {
		tbl := pgx.Identifier{pgSchema, c.Resource}.Sanitize()
		col := pgx.Identifier{c.Column}.Sanitize()
		anySQL := fmt.Sprintf("SELECT 1 FROM %s WHERE %s = $1 LIMIT 1", tbl, col)
		hit, err := existsTenant(ctx, tdb, pgSchema, anySQL, []any{fileID})
		if err != nil {
			return false, err
		}
		if !hit {
			continue
		}
		referenced = true
		ev := pol.Evaluate(ev, c.Resource, "read")
		if !ev.Allowed {
			continue
		}
		mineSQL := fmt.Sprintf("SELECT 1 FROM %s WHERE %s = $1", tbl, col)
		args := []any{fileID}
		mineSQL, args, err = query.AppendRowCondition(mineSQL, args, ev.Condition)
		if err != nil {
			return false, err
		}
		mine, err := existsTenant(ctx, tdb, pgSchema, mineSQL+" LIMIT 1", args)
		if err != nil {
			return false, err
		}
		if mine {
			return true, nil
		}
	}
	if referenced {
		return false, nil
	}
	// Nothing references it: the upload→attach window. Only the UPLOADER may
	// reach it (FILES-3, 2026-09-28 — measured: before recording the uploader,
	// a second user read a file the first had uploaded and not yet attached).
	// A file whose uploader is unknown (stored before this, or uploaded with
	// no identity) stays reachable to the role — the rows that already exist
	// do not change meaning under an upgrade.
	return uploadedByCaller(ctx, ev, fileID)
}

// FileUploader answers who uploaded a file ("" when unknown). The app puts
// one in the request context (WithFileUploader) so every write door — REST,
// GraphQL, the batch and the library Ctx — asks the SAME question without
// threading the store through a dozen signatures.
type FileUploader func(ctx context.Context, fileID string) (string, error)

type fileUploaderKey struct{}

// WithFileUploader carries the store's uploader lookup on the request context.
func WithFileUploader(ctx context.Context, fn FileUploader) context.Context {
	return context.WithValue(ctx, fileUploaderKey{}, fn)
}

func uploadedByCaller(ctx context.Context, ev rbac.EvalContext, fileID string) (bool, error) {
	fn, _ := ctx.Value(fileUploaderKey{}).(FileUploader)
	if fn == nil {
		return true, nil // no store to ask
	}
	who, err := fn(ctx, fileID)
	if err != nil {
		return false, err
	}
	return who == "" || who == ev.UserID, nil
}

func existsTenant(ctx context.Context, tdb *db.TenantDB, pgSchema, sql string, args []any) (bool, error) {
	rows, err := tdb.QueryTenant(ctx, pgSchema, sql, args...)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	if rows.Next() {
		return true, rows.Err()
	}
	return false, rows.Err()
}

func sortedFieldNames(res schema.ResourceSchema) []string {
	out := make([]string, 0, len(res.Fields))
	for n := range res.Fields {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// CheckFileAttachReach is the same rule at ATTACH time: a caller may only
// point a `file` field at a file they can already reach. Without it the byte
// guard is a revolving door — the second user attached the first user's file
// id to a row of their own and then read it legitimately (measured). The
// refusal is the SAME uniform `file_not_found` an unknown id gets: whether
// the file exists is not the caller's business.
func CheckFileAttachReach(ctx context.Context, tdb *db.TenantDB, pgSchema string, cols []FileRefColumn, pol *rbac.Policy, ev rbac.EvalContext, res *schema.ResourceSchema, vals map[string]any) ([]schema.FieldRuleError, error) {
	if len(cols) == 0 || len(vals) == 0 || res == nil {
		return nil, nil
	}
	var errs []schema.FieldRuleError
	for name, fd := range res.Fields {
		if fd.Type != "file" {
			continue
		}
		raw, ok := vals[name]
		if !ok || raw == nil {
			continue
		}
		id, ok := raw.(string)
		if !ok || id == "" {
			continue
		}
		reach, err := FileReachable(ctx, tdb, pgSchema, cols, pol, ev, id)
		if err != nil {
			return nil, err
		}
		if !reach {
			errs = append(errs, schema.FieldRuleError{Field: name, Rule: "file_not_found",
				Message: "no such file for this tenant (or not yours)"})
		}
	}
	return errs, nil
}

// FileReachableTx is FileReachable over an ALREADY-OPEN tenant transaction
// (search_path set): the batch executor, GraphQL and Ctx.Insert/Update.
func FileReachableTx(ctx context.Context, tx pgx.Tx, cols []FileRefColumn, pol *rbac.Policy, ev rbac.EvalContext, fileID string) (bool, error) {
	if len(cols) == 0 {
		return true, nil
	}
	referenced := false
	for _, c := range cols {
		tbl := pgx.Identifier{c.Resource}.Sanitize()
		col := pgx.Identifier{c.Column}.Sanitize()
		var one int
		err := tx.QueryRow(ctx, fmt.Sprintf("SELECT 1 FROM %s WHERE %s = $1 LIMIT 1", tbl, col), fileID).Scan(&one)
		if err != nil {
			if isNoRows(err) {
				continue
			}
			return false, err
		}
		referenced = true
		res := pol.Evaluate(ev, c.Resource, "read")
		if !res.Allowed {
			continue
		}
		sql := fmt.Sprintf("SELECT 1 FROM %s WHERE %s = $1", tbl, col)
		args := []any{fileID}
		sql, args, err = query.AppendRowCondition(sql, args, res.Condition)
		if err != nil {
			return false, err
		}
		if err := tx.QueryRow(ctx, sql+" LIMIT 1", args...).Scan(&one); err != nil {
			if isNoRows(err) {
				continue
			}
			return false, err
		}
		return true, nil
	}
	if referenced {
		return false, nil
	}
	return uploadedByCaller(ctx, ev, fileID)
}

// CheckFileAttachReachTx is CheckFileAttachReach on an open tenant tx.
func CheckFileAttachReachTx(ctx context.Context, tx pgx.Tx, cols []FileRefColumn, pol *rbac.Policy, ev rbac.EvalContext, res *schema.ResourceSchema, vals map[string]any) ([]schema.FieldRuleError, error) {
	if len(cols) == 0 || len(vals) == 0 || res == nil {
		return nil, nil
	}
	var errs []schema.FieldRuleError
	for name, fd := range res.Fields {
		if fd.Type != "file" {
			continue
		}
		id, ok := vals[name].(string)
		if !ok || id == "" {
			continue
		}
		reach, err := FileReachableTx(ctx, tx, cols, pol, ev, id)
		if err != nil {
			return nil, err
		}
		if !reach {
			errs = append(errs, schema.FieldRuleError{Field: name, Rule: "file_not_found",
				Message: "no such file for this tenant (or not yours)"})
		}
	}
	return errs, nil
}

func isNoRows(err error) bool { return errors.Is(err, pgx.ErrNoRows) }

// EvalContextFromCtx is the caller's identity for a policy evaluation, taken
// from the request context (GraphQL resolvers have no *http.Request).
func EvalContextFromCtx(ctx context.Context) rbac.EvalContext {
	if c := auth.ClaimsFromCtx(ctx); c != nil {
		return rbac.EvalContext{Role: c.Role, UserID: c.UserID, ExternalClientID: c.ExternalClientID}
	}
	return rbac.EvalContext{}
}
