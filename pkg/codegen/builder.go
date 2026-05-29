package codegen

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

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

type listResponse struct {
	Data []map[string]any `json:"data"`
	Meta paginationMeta   `json:"meta"`
}

type paginationMeta struct {
	Page       int   `json:"page"`
	PerPage    int   `json:"per_page"`
	Total      int64 `json:"total"`
	TotalPages int64 `json:"total_pages"`
}

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
		r.Get("/api/"+name, func(w http.ResponseWriter, req *http.Request) {
			tc := tenant.MustFromCtx(req.Context())
			evalResult := rbac.EvalResultFromCtx(req.Context())

			var cond *rbac.WhereCondition
			if evalResult != nil {
				cond = evalResult.Condition
			}

			qb := query.BuildQuery(name, &res, req.URL.Query(), cond)
			selectQ, countQ, selectArgs, countArgs := qb.SQL()

			total, err := tdb.QueryScalarTenant(req.Context(), tc.PGSchema, countQ, countArgs...)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}

			rows, err := tdb.QueryTenant(req.Context(), tc.PGSchema, selectQ, selectArgs...)
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

			totalPages := (total + int64(qb.PerPage()) - 1) / int64(qb.PerPage())
			if totalPages == 0 {
				totalPages = 1
			}

			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(listResponse{
				Data: data,
				Meta: paginationMeta{
					Page:       qb.Page(),
					PerPage:    qb.PerPage(),
					Total:      total,
					TotalPages: totalPages,
				},
			})
		})

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
				json.NewEncoder(w).Encode(result[0])
			}
		})

		// --- Get by ID ---
		r.Get("/api/"+name+"/{id}", func(w http.ResponseWriter, req *http.Request) {
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
			json.NewEncoder(w).Encode(record)
		})

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
	}

	return r
}
