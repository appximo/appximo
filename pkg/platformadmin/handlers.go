package platformadmin

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/userauth"
)

// --- tenant management handlers ---------------------------------------------

func (s *Service) handleListTenants(w http.ResponseWriter, r *http.Request) {
	ts, err := s.ListTenants(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list tenants failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"tenants": ts})
}

func (s *Service) handleGetTenant(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	t, err := s.GetTenant(r.Context(), id)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, t)
	case errors.Is(err, ErrTenantNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "get tenant failed"})
	}
}

func (s *Service) handleCreateTenant(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBody)
	var raw struct {
		TenantID    string          `json:"tenant_id"`
		DisplayName string          `json:"display_name"`
		Email       string          `json:"email"`
		Plan        string          `json:"plan"`
		Schema      json.RawMessage `json:"schema"`
	}
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	parsed, errs := parseSchema(raw.Schema)
	if len(errs) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"errors": errs})
		return
	}
	t, err := s.CreateTenant(r.Context(), controlplane.RegisterRequest{
		TenantID:    raw.TenantID,
		DisplayName: raw.DisplayName,
		Email:       raw.Email,
		Plan:        raw.Plan,
		Schema:      parsed,
	})
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, t)
	case errors.Is(err, controlplane.ErrAlreadyExists):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, controlplane.ErrInvalidInput):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create tenant failed"})
	}
}

func (s *Service) handleSuspendTenant(w http.ResponseWriter, r *http.Request) {
	s.setSuspendHandler(w, r, true)
}

func (s *Service) handleActivateTenant(w http.ResponseWriter, r *http.Request) {
	s.setSuspendHandler(w, r, false)
}

func (s *Service) setSuspendHandler(w http.ResponseWriter, r *http.Request, suspend bool) {
	id := chi.URLParam(r, "id")
	var err error
	if suspend {
		err = s.SuspendTenant(r.Context(), id)
	} else {
		err = s.ActivateTenant(r.Context(), id)
	}
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"tenant_id": id, "suspended": suspend})
	case errors.Is(err, ErrTenantNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "operation failed"})
	}
}

func (s *Service) handleDeleteTenant(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Confirm string `json:"confirm"`
	}
	_ = decodeAllowEmpty(w, r, &req)
	err := s.DeleteTenant(r.Context(), id, req.Confirm)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"tenant_id": id, "deleted": true})
	case errors.Is(err, ErrConfirmRequired):
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "destructive: send {\"confirm\":\"" + id + "\"} to delete this tenant and ALL its data"})
	case errors.Is(err, ErrTenantNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete tenant failed"})
	}
}

// --- tenant user management handlers ----------------------------------------

func (s *Service) handleListUsers(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	us, err := s.ListUsers(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list users failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": us})
}

func (s *Service) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	var req struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !decode(w, r, &req) {
		return
	}
	u, err := s.CreateUser(r.Context(), id, req.Email, req.Password, req.Role)
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, u)
	case errors.Is(err, ErrInvalidEmail):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid email"})
	case errors.Is(err, ErrWeakPassword):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "password too short"})
	case errors.Is(err, ErrUnknownRole):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role not declared in the schema RBAC"})
	case isEmailTaken(err):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "email already registered in this tenant"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "create user failed"})
	}
}

func (s *Service) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	uid := chi.URLParam(r, "uid")
	var req struct {
		Role      *string `json:"role,omitempty"`
		Suspended *bool   `json:"suspended,omitempty"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.Role == nil && req.Suspended == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "nothing to update (send role and/or suspended)"})
		return
	}
	if req.Role != nil {
		if err := s.UpdateUserRole(r.Context(), id, uid, *req.Role); err != nil {
			s.writeUserUpdateErr(w, err)
			return
		}
	}
	if req.Suspended != nil {
		if err := s.SetUserSuspended(r.Context(), id, uid, *req.Suspended); err != nil {
			s.writeUserUpdateErr(w, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"user_id": uid, "updated": true})
}

func (s *Service) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	uid := chi.URLParam(r, "uid")
	if err := s.DeleteUser(r.Context(), id, uid); err != nil {
		s.writeUserUpdateErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user_id": uid, "deleted": true})
}

func (s *Service) writeUserUpdateErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrUnknownRole):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "role not declared in the schema RBAC"})
	case isUserNotFound(err):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "user not found"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "update failed"})
	}
}

// --- data navigation handlers (read-only browse) ----------------------------

func (s *Service) handleListResources(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rs, err := s.ListResources(r.Context(), id)
	switch {
	case err == nil:
		roles, _ := s.ListRoles(r.Context(), id) // best-effort; empty on error
		writeJSON(w, http.StatusOK, map[string]any{"resources": rs, "roles": roles})
	case errors.Is(err, controlplane.ErrNotFound), errors.Is(err, ErrTenantNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list resources failed"})
	}
}

func (s *Service) handleListData(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	resource := chi.URLParam(r, "resource")
	res, err := s.ListData(r.Context(), id, resource, r.URL.Query())
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, res)
	case errors.Is(err, ErrResourceNotFound), errors.Is(err, controlplane.ErrNotFound), errors.Is(err, ErrTenantNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "resource not found"})
	case isQueryParamError(err):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list data failed"})
	}
}

// isQueryParamError reports whether err is a client-side query-builder error
// (unknown field, bad op/param) — those map to 400, not 500. The query builder
// returns plain errors, so we classify by the absence of our known sentinels and
// the presence of the builder's phrasing.
func isQueryParamError(err error) bool {
	if err == nil {
		return false
	}
	m := err.Error()
	return strings.Contains(m, "invalid") || strings.Contains(m, "unknown") || strings.Contains(m, "filter") || strings.Contains(m, "parameter")
}

// --- observability handler --------------------------------------------------

// observabilityHandler authorizes a per-tenant observability read and delegates to
// the EXISTING observability server (no duplicated logic). Authorization inherits
// the existing model:
//   - a platform super-admin (platform token, or the admin key) sees ANY tenant;
//   - a tenant admin (valid tenant JWT whose tenant matches AND whose role is
//     admin-grade in the schema RBAC) sees ONLY its own tenant;
//   - everyone else → 403.
//
// The data is tenant-scoped at the store, so this never leaks across tenants.
func (s *Service) observabilityHandler(obs ObsHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if obs == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "observability store disabled"})
			return
		}
		id := chi.URLParam(r, "id")
		if !s.authorizeObs(r, id) {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "not authorized for this tenant's observability"})
			return
		}
		obs.ServeTenantData(w, r)
	}
}

func (s *Service) authorizeObs(r *http.Request, tenantID string) bool {
	// Platform super-admin (token) or machine admin key → any tenant.
	if s.platformClaimsFromRequest(r) != nil || s.adminKeyOK(r) {
		return true
	}
	// Tenant admin → its own tenant only.
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return false
	}
	claims, err := validateTenantToken(strings.TrimPrefix(h, "Bearer "), s.cfg.JWTSecret)
	if err != nil || claims.TenantID != tenantID {
		return false
	}
	return s.cfg.TenantAdminRole != nil && s.cfg.TenantAdminRole(claims.Role)
}

// --- helpers ----------------------------------------------------------------

// parseSchema validates a raw schema (strict keys + semantic validate) the same way
// the control plane does, returning the parsed schema or error strings.
func parseSchema(raw json.RawMessage) (*schema.APISchema, []string) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, []string{"schema is required"}
	}
	if errs := schema.CheckUnknownKeys(raw); len(errs) > 0 {
		return nil, msgStrings(errs)
	}
	var sc schema.APISchema
	if err := json.Unmarshal(raw, &sc); err != nil {
		return nil, []string{"schema is not valid JSON: " + err.Error()}
	}
	if errs := schema.Validate(&sc); len(errs) > 0 {
		return nil, msgStrings(errs)
	}
	return &sc, nil
}

// msgStrings flattens schema validation errors into client-safe strings.
func msgStrings(errs []schema.ValidationError) []string {
	out := make([]string, len(errs))
	for i, e := range errs {
		out[i] = e.Error()
	}
	return out
}

// isEmailTaken / isUserNotFound classify the wrapped userauth sentinels surfaced
// through the user-management methods.
func isEmailTaken(err error) bool   { return errors.Is(err, userauth.ErrEmailTaken) }
func isUserNotFound(err error) bool { return errors.Is(err, userauth.ErrUserNotFound) }
