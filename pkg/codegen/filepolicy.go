package codegen

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/appximo/appximo/pkg/db"
	"github.com/appximo/appximo/pkg/schema"
)

// FILES-1 — per-FIELD file attach policy.
//
// The upload endpoint (POST /api/files) is field-agnostic: at upload time the
// engine cannot know which record field the file is destined for, so the only
// upload-time knobs are instance-wide (APPXIMO_FILES_MAX_BYTES /
// _ALLOWED_EXT). The per-field policy a real app needs ("this field takes
// images ≤ 5 MB, that one PDFs ≤ 20 MB") therefore runs at ATTACH time: when a
// write references a file_id in a `file` field declaring `accept`/`max_bytes`,
// the referenced file's STORED metadata (size + the magic-byte-sniffed content
// type — never the client's declaration) is checked against the field's policy.
//
// One evaluation core, two transports (the engine's two write shapes):
//   - CheckFilePoliciesTenant — pool path (REST + GraphQL handlers, which run
//     outside the write tx; ExecRowsTenant opens it later). TOCTOU is harmless:
//     ids are content-immutable, and a file deleted between check and INSERT
//     still hits the real FK (422 file_not_found).
//   - CheckFilePoliciesTx — an open tenant transaction (the batch
//     /api/transaction executor and the library's Ctx.Insert/Update).
//
// A file_id that references NO file is deliberately NOT reported here — the FK
// remains the single authority for existence (422 file_not_found); this check
// only ever adds `file_policy` violations. A resource with no policy-declaring
// file field pays one boolean scan of its fields and no query.
func resourceHasFilePolicy(res *schema.ResourceSchema) bool {
	for name := range res.Fields {
		fd := res.Fields[name]
		if fd.HasFilePolicy() {
			return true
		}
	}
	return false
}

// fileMetaFetch resolves a file id to its stored (size, content type);
// found=false when the tenant has no such file.
type fileMetaFetch func(id string) (size int64, contentType string, found bool, err error)

// checkFilePolicies is the shared evaluation: every file field PRESENT in vals
// with a declared policy is resolved and checked. All violations are collected
// (the S44 contract: one response carries every failing field).
func checkFilePolicies(res *schema.ResourceSchema, vals map[string]any, fetch fileMetaFetch) ([]schema.FieldRuleError, error) {
	var out []schema.FieldRuleError
	for name := range res.Fields {
		fd := res.Fields[name]
		if !fd.HasFilePolicy() {
			continue
		}
		raw, present := vals[name]
		if !present || raw == nil {
			continue // absence/null is governed by `required`, not the policy
		}
		id, ok := raw.(string)
		if !ok {
			continue // wrong TYPE is the type check's finding, not the policy's
		}
		if _, err := uuid.Parse(strings.TrimSpace(id)); err != nil {
			continue // malformed ids are the type check's finding too
		}
		size, ct, found, err := fetch(strings.TrimSpace(id))
		if err != nil {
			return nil, err
		}
		if !found {
			continue // existence is the FK's verdict (422 file_not_found)
		}
		if fd.MaxBytes > 0 && size > fd.MaxBytes {
			out = append(out, schema.FieldRuleError{
				Field: name, Rule: "file_policy",
				Message: fmt.Sprintf("references a %d-byte file; this field accepts at most %d bytes (max_bytes)", size, fd.MaxBytes),
			})
			continue // one violation per field is enough to act on
		}
		if !fd.FileAcceptMatches(ct) {
			shown := ct
			if shown == "" {
				shown = "an undetected content type"
			}
			out = append(out, schema.FieldRuleError{
				Field: name, Rule: "file_policy",
				Message: fmt.Sprintf("references a file of type %s; this field accepts: %s", shown, strings.Join(fd.Accept, ", ")),
			})
		}
	}
	return out, nil
}

// fileMetaQuery is the lookup both transports run. The files table lives in the
// tenant schema (per-tenant isolation is structural: a foreign tenant's id
// simply finds no row and falls through to the FK's file_not_found).
const fileMetaQuery = `SELECT size, COALESCE(content_type, '') FROM files WHERE id = $1`

// CheckFilePoliciesTenant runs the FILES-1 attach check through the tenant-
// scoped pool (REST and GraphQL write handlers). Returns the violations (S44
// fields) and any infrastructure error (mapped by the caller like any DB error).
func CheckFilePoliciesTenant(ctx context.Context, tdb *db.TenantDB, pgSchema string, res *schema.ResourceSchema, vals map[string]any) ([]schema.FieldRuleError, error) {
	if !resourceHasFilePolicy(res) {
		return nil, nil
	}
	return checkFilePolicies(res, vals, func(id string) (int64, string, bool, error) {
		rows, err := tdb.QueryTenant(ctx, pgSchema, fileMetaQuery, id)
		if err != nil {
			return 0, "", false, err
		}
		defer rows.Close()
		if !rows.Next() {
			return 0, "", false, rows.Err()
		}
		var size int64
		var ct string
		if err := rows.Scan(&size, &ct); err != nil {
			return 0, "", false, err
		}
		return size, ct, true, nil
	})
}

// CheckFilePoliciesTx is CheckFilePoliciesTenant over an ALREADY-OPEN tenant
// transaction (search_path set) — the batch executor and Ctx.Insert/Update.
func CheckFilePoliciesTx(ctx context.Context, tx pgx.Tx, res *schema.ResourceSchema, vals map[string]any) ([]schema.FieldRuleError, error) {
	if !resourceHasFilePolicy(res) {
		return nil, nil
	}
	return checkFilePolicies(res, vals, func(id string) (int64, string, bool, error) {
		var size int64
		var ct string
		err := tx.QueryRow(ctx, fileMetaQuery, id).Scan(&size, &ct)
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, "", false, nil
		}
		if err != nil {
			return 0, "", false, err
		}
		return size, ct, true, nil
	})
}
