package codegen

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"
	pkghandlers "github.com/miguelangel/appitools/pkg/handlers"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/extensions"
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
		r.Get("/api/"+name, func(w http.ResponseWriter, req *http.Request) {
			tc := tenant.MustFromCtx(req.Context())
			rows, err := tdb.QueryTenant(req.Context(), tc.PGSchema,
				fmt.Sprintf("SELECT * FROM %s ORDER BY id LIMIT 100", name))
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
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(result)
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
			query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s) RETURNING *", name, cols, placeholders)
			result, err := tdb.ExecRowsTenant(req.Context(), tc.PGSchema, query, args...)
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
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(result[0])
		})

		// --- Delete ---
		r.Delete("/api/"+name+"/{id}", func(w http.ResponseWriter, req *http.Request) {
			tc := tenant.MustFromCtx(req.Context())
			id := chi.URLParam(req, "id")
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
