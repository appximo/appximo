package codegen

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/extensions"
	pkghandlers "github.com/miguelangel/appitools/pkg/handlers"
	"github.com/miguelangel/appitools/pkg/query"
	"github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
)

// BuildRouter creates a chi.Mux with real SQL handlers for every resource in the schema.
// Used by `appitools serve` — no code generation required.
func BuildRouter(s *schema.APISchema, tdb *db.TenantDB, hr *extensions.HookRunner) *chi.Mux {
	names := make([]string, 0, len(s.Resources))
	for name := range s.Resources {
		names = append(names, name)
	}
	sort.Strings(names)

	r := chi.NewMux()

	for _, resName := range names {
		name := resName
		res := s.Resources[resName]

		// --- List ---
		r.Get("/api/"+name, pkghandlers.CachedGet(func(w http.ResponseWriter, req *http.Request) {
			tc := tenant.MustFromCtx(req.Context())
			evalResult := rbac.EvalResultFromCtx(req.Context())

			var cond *rbac.WhereCondition
			if evalResult != nil {
				cond = evalResult.Condition
			}

			qb, err := query.BuildQuery(name, &res, req.URL.Query(), cond)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
				return
			}

			selectQ, _, selectArgs, _ := qb.SQL()

			// QueryDirect: schema-qualified table name — no transaction, no SET LOCAL.
			rows, err := tdb.QueryDirect(req.Context(), tc.PGSchema, name, selectQ, selectArgs...)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer rows.Close()
			data, err := pkghandlers.RowsToMaps(rows)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			if evalResult != nil && len(evalResult.AllowedFields) > 0 {
				for i, rec := range data {
					data[i] = pkghandlers.FilterFields(rec, evalResult.AllowedFields)
				}
			}
			if data == nil {
				data = []map[string]any{}
			}

			hasNext := len(data) == qb.PerPage()
			hasPrev := qb.Page() > 1

			w.Header().Set("Content-Type", "application/json")
			pkghandlers.WriteJSON(w, map[string]any{ //nolint:errcheck
				"data": data,
				"meta": map[string]any{
					"page":     qb.Page(),
					"per_page": qb.PerPage(),
					"has_next": hasNext,
					"has_prev": hasPrev,
				},
			})
		}))

		// --- Create ---
		r.Post("/api/"+name, func(w http.ResponseWriter, req *http.Request) {
			tc := tenant.MustFromCtx(req.Context())
			var body map[string]any
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if len(body) == 0 {
				http.Error(w, "empty body", http.StatusBadRequest)
				return
			}

			var beforeHook *schema.HookConfig
			if hc, ok := res.Hooks["before_create"]; ok {
				c := hc
				beforeHook = &c
			}
			hookRes, hookErr := hr.RunBeforeHook(req.Context(), beforeHook, body, nil)
			if hookErr != nil {
				http.Error(w, hookErr.Error(), http.StatusInternalServerError)
				return
			}
			if !hookRes.Proceed {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnprocessableEntity)
				json.NewEncoder(w).Encode(map[string]string{"error": hookRes.Error})
				return
			}
			body = hookRes.Data

			cols, placeholders, args := pkghandlers.BuildInsertArgs(body)
			insertQ := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING *", name, cols, placeholders)
			result, err := tdb.ExecRowsTenant(req.Context(), tc.PGSchema, insertQ, args...)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			if hc, ok := res.Hooks["after_create"]; ok {
				afterHook := hc
				var record map[string]any
				if len(result) > 0 {
					record = result[0]
				}
				go hr.RunAfterHook(context.Background(), &afterHook, record, tc.ID)
			}

			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			if len(result) > 0 {
				pkghandlers.WriteJSON(w, result[0]) //nolint:errcheck
			}
		})

		// --- Get by ID ---
		r.Get("/api/"+name+"/{id}", pkghandlers.CachedGet(func(w http.ResponseWriter, req *http.Request) {
			tc := tenant.MustFromCtx(req.Context())
			id := chi.URLParam(req, "id")
			if _, err := uuid.Parse(id); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid id format"})
				return
			}
			rows, err := tdb.QueryTenant(req.Context(), tc.PGSchema,
				fmt.Sprintf("SELECT * FROM %s WHERE id = $1", name), id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			defer rows.Close()
			result, err := pkghandlers.RowsToMaps(rows)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if len(result) == 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
				return
			}
			record := result[0]
			evalResult := rbac.EvalResultFromCtx(req.Context())
			if evalResult != nil && len(evalResult.AllowedFields) > 0 {
				record = pkghandlers.FilterFields(record, evalResult.AllowedFields)
			}
			w.Header().Set("Content-Type", "application/json")
			pkghandlers.WriteJSON(w, record) //nolint:errcheck
		}))

		// --- Delete ---
		r.Delete("/api/"+name+"/{id}", func(w http.ResponseWriter, req *http.Request) {
			tc := tenant.MustFromCtx(req.Context())
			id := chi.URLParam(req, "id")
			if _, err := uuid.Parse(id); err != nil {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid id format"})
				return
			}
			affected, err := tdb.ExecTenant(req.Context(), tc.PGSchema,
				fmt.Sprintf("DELETE FROM %s WHERE id = $1", name), id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if affected == 0 {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusNotFound)
				json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
				return
			}
			w.WriteHeader(http.StatusNoContent)
		})

		// --- Subresources: GET /api/{resource}/{id}/{relName} ---
		for fieldName, fd := range res.Fields {
			if fd.Relation == "" {
				continue
			}
			fn := fieldName       // capture
			relResource := fd.Relation
			relRoute := strings.TrimSuffix(fn, "_id")

			r.Get("/api/"+name+"/{id}/"+relRoute, pkghandlers.CachedGet(func(w http.ResponseWriter, req *http.Request) {
				tc := tenant.MustFromCtx(req.Context())
				parentID := chi.URLParam(req, "id")
				if _, err := uuid.Parse(parentID); err != nil {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusBadRequest)
					json.NewEncoder(w).Encode(map[string]string{"error": "invalid id format"})
					return
				}
				q := fmt.Sprintf(
					"SELECT r.* FROM %s r JOIN %s src ON src.%s = r.id WHERE src.id = $1",
					relResource, name, fn,
				)
				rows, err := tdb.QueryTenant(req.Context(), tc.PGSchema, q, parentID)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				defer rows.Close()
				result, err := pkghandlers.RowsToMaps(rows)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				if len(result) == 0 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusNotFound)
					json.NewEncoder(w).Encode(map[string]string{"error": "not found"})
					return
				}
				w.Header().Set("Content-Type", "application/json")
				pkghandlers.WriteJSON(w, result[0]) //nolint:errcheck
			}))
		}
	}

	return r
}
