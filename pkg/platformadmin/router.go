package platformadmin

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/miguelangel/appitools/pkg/auth"
)

// maxAdminBody caps an admin request body (credentials/role changes are tiny; a
// tenant create carries a schema, so allow 1 MiB like the control plane).
const maxAdminBody = 1 << 20

// ObsHandler is the slice of the observability server the admin API needs: serve
// one tenant's observability JSON (already tenant-scoped). Declared as an interface
// so this package does not import pkg/observability (and to keep it mockable).
type ObsHandler interface {
	ServeTenantData(w http.ResponseWriter, r *http.Request)
}

type platformCtxKey struct{}

// Register wires the admin API onto the data-plane router r. Routes live under
// /admin/ (already JWT-skipped and RBAC-passthrough — they do their OWN auth) and
// are registered individually so they coexist with the existing /admin/backup and
// /admin/tenants/{id}/reload machine endpoints (no Mount collision). adminKey lets
// machine callers (DevHub, scripts) use X-Admin-Key on the management routes — the
// "two paths for two consumers" rule: humans log in, machines present the key. The
// /admin/auth/* identity routes never accept the key (a human session needs an
// identity). obs may be nil (observability route then returns 503).
func (s *Service) Register(r chi.Router, obs ObsHandler, adminKey string) {
	s.adminKey = adminKey

	// --- super-admin auth (humans; no platform token required to obtain one) ---
	r.Post("/admin/auth/login", s.handleLogin)
	r.Post("/admin/auth/refresh", s.handleRefresh)
	r.Post("/admin/auth/mfa/verify", s.handleMFAVerify) // completes the login challenge
	// MFA management needs the admin identity → a platform token specifically.
	r.With(s.requirePlatformToken).Post("/admin/auth/mfa/enable", s.handleMFAEnable)
	r.With(s.requirePlatformToken).Post("/admin/auth/mfa/confirm", s.handleMFAConfirm)
	r.With(s.requirePlatformToken).Post("/admin/auth/mfa/disable", s.handleMFADisable)

	// --- tenant management (platform token OR admin key) ---
	r.With(s.requirePlatform).Get("/admin/tenants", s.handleListTenants)
	r.With(s.requirePlatform).Post("/admin/tenants", s.handleCreateTenant)
	r.With(s.requirePlatform).Get("/admin/tenants/{id}", s.handleGetTenant)
	r.With(s.requirePlatform).Delete("/admin/tenants/{id}", s.handleDeleteTenant)
	// Schema deploy (UI-F1-S1): the editor loads (GET) and deploys (PUT, with the
	// dry-run migration preview + destructive-approval gate) a tenant's schema.
	r.With(s.requirePlatform).Get("/admin/tenants/{id}/schema", s.handleGetTenantSchema)
	r.With(s.requirePlatform).Put("/admin/tenants/{id}/schema", s.handleUpdateTenantSchema)

	// Schema version history + rollback (VERSION-S1): the append-only deploy
	// timeline and "roll back to version N" — the same preview/gate/apply
	// machinery as the deploy above, tagged in the history as a rollback.
	r.With(s.requirePlatform).Get("/admin/tenants/{id}/schema/history", s.handleSchemaHistory)
	r.With(s.requirePlatform).Get("/admin/tenants/{id}/schema/history/{version}", s.handleSchemaVersion)
	r.With(s.requirePlatform).Post("/admin/tenants/{id}/schema/rollback", s.handleSchemaRollback)

	// Flow tests + post-deploy regression (FLOWTEST-S1): persisted multi-step
	// scenarios run against the LIVE app (full chain, tenant-user auth) with
	// live SSE output; runs anchored to the schema version (the trust loop).
	r.With(s.requirePlatform).Get("/admin/tenants/{id}/flows", s.handleListFlows)
	r.With(s.requirePlatform).Post("/admin/tenants/{id}/flows", s.handleSaveFlow)
	r.With(s.requirePlatform).Post("/admin/tenants/{id}/flows/run", s.handleRunSuite)
	r.With(s.requirePlatform).Get("/admin/tenants/{id}/flows/runs", s.handleListRuns)
	r.With(s.requirePlatform).Get("/admin/tenants/{id}/flows/runs/{rid}", s.handleGetRun)
	r.With(s.requirePlatform).Get("/admin/tenants/{id}/flows/{fid}", s.handleGetFlow)
	r.With(s.requirePlatform).Put("/admin/tenants/{id}/flows/{fid}", s.handleSaveFlow)
	r.With(s.requirePlatform).Delete("/admin/tenants/{id}/flows/{fid}", s.handleDeleteFlow)
	r.With(s.requirePlatform).Post("/admin/tenants/{id}/flows/{fid}/run", s.handleRunFlow)
	r.With(s.requirePlatform).Post("/admin/tenants/{id}/suspend", s.handleSuspendTenant)
	r.With(s.requirePlatform).Post("/admin/tenants/{id}/activate", s.handleActivateTenant)

	// --- tenant user management (platform token OR admin key) ---
	r.With(s.requirePlatform).Get("/admin/tenants/{id}/users", s.handleListUsers)
	r.With(s.requirePlatform).Post("/admin/tenants/{id}/users", s.handleCreateUser)
	r.With(s.requirePlatform).Patch("/admin/tenants/{id}/users/{uid}", s.handleUpdateUser)
	r.With(s.requirePlatform).Delete("/admin/tenants/{id}/users/{uid}", s.handleDeleteUser)

	// --- tenant data navigation (read-only browse; platform token OR admin key) ---
	// Resources come from the tenant's stored schema; records reuse the engine's
	// query builder over the tenant-scoped DB. The SPA cannot reach the Host-scoped
	// /api/{resource} (one origin, platform JWT), so these path-scoped endpoints are
	// how the panel browses a tenant's data.
	r.With(s.requirePlatform).Get("/admin/tenants/{id}/resources", s.handleListResources)
	r.With(s.requirePlatform).Get("/admin/tenants/{id}/data/{resource}", s.handleListData)

	// --- tenant files manager (UI-F5-S1; platform token OR admin key) ---
	// Thin delegates into the engine's files.Store — same OWASP upload validation
	// (422/413), same dedup-aware delete, same serve strategy. The download leg is
	// token-authenticated (a browser navigation cannot send headers): the token is
	// minted by the requirePlatform /url route, short-lived, single-file, and any
	// failure is a uniform 404.
	r.With(s.requirePlatform).Get("/admin/tenants/{id}/files", s.handleListFiles)
	r.With(s.requirePlatform).Post("/admin/tenants/{id}/files", s.handleUploadFile)
	r.With(s.requirePlatform).Get("/admin/tenants/{id}/files/{fid}/url", s.handleFileURL)
	r.With(s.requirePlatform).Delete("/admin/tenants/{id}/files/{fid}", s.handleDeleteFile)
	r.Get("/admin/tenants/{id}/files/{fid}/download", s.handleFileDownload)

	// --- served resources (boot-schema introspection; platform token OR admin key) ---
	// The names of resources the engine serves live (routes/GraphQL/RBAC are compiled
	// from the boot --schema). The editor diffs a to-deploy schema against this to warn
	// that a NEW resource is provisioned but needs an engine restart to be served.
	r.With(s.requirePlatform).Get("/admin/served-resources", s.handleServedResources)

	// --- engine self-restart (UI-F4-S2; platform token OR admin key) ---
	// Persists the posted schema as the new BOOT schema (validated first, written
	// atomically, previous one backed up) and gracefully restarts the engine
	// (drain via readyz→503 → http shutdown → relaunch), so a NEW resource's
	// routes/GraphQL/RBAC/docs go live without touching the terminal. PRIVILEGED:
	// it restarts the service and changes the GLOBAL structure for every tenant —
	// gated by the same super-admin auth as the deploy, never public.
	r.With(s.requirePlatform).Post("/admin/engine/schema", s.handleEngineSchema)

	// --- observability (platform → any tenant; tenant admin → its own) ---
	r.Get("/admin/observability/tenants/{id}", s.observabilityHandler(obs))
}

// handleServedResources returns the resource names the engine serves live (from the
// boot --schema). A resource a tenant has but that is absent here is provisioned but
// not served until the engine restarts with a schema that includes it. self_restart
// tells the editor whether POST /admin/engine/schema is available (UI-F4-S2), so the
// restart banner can offer the one-click restart instead of manual instructions.
func (s *Service) handleServedResources(w http.ResponseWriter, r *http.Request) {
	var names []string
	if s.cfg.ServedResourcesFn != nil {
		names = s.cfg.ServedResourcesFn() // live surface (fleet hot-swap, MT-STRUCT-S4)
	} else {
		names = s.cfg.ServedResources
	}
	if names == nil {
		names = []string{}
	}
	activation := "restart"
	if s.cfg.ActivationFn != nil {
		activation = s.cfg.ActivationFn()
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"resources":    names,
		"self_restart": s.cfg.PersistBootSchema != nil && s.cfg.TriggerRestart != nil,
		// How a deploy activates here: "hot_swap" (fleet-serve, no downtime,
		// only this app) or "restart" (single-engine graceful re-exec).
		"activation": activation,
	})
}

// handleEngineSchema is POST /admin/engine/schema (UI-F4-S2): validate + persist
// the posted schema as the new BOOT schema, then trigger the graceful self-restart.
// The safety order is absolute: an invalid schema persists NOTHING and restarts
// NOTHING (422, the engine keeps serving); only a validated, atomically-persisted
// schema (previous kept as .bak for the boot-failure rollback) triggers the drain.
func (s *Service) handleEngineSchema(w http.ResponseWriter, r *http.Request) {
	if s.cfg.PersistBootSchema == nil || s.cfg.TriggerRestart == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "engine self-restart is not available on this deployment",
		})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBody)
	var body struct {
		Schema json.RawMessage `json:"schema"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Schema) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": `body must be {"schema": {…}}`})
		return
	}
	if err := s.cfg.PersistBootSchema(body.Schema); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, ErrSchemaRejected) {
			status = http.StatusUnprocessableEntity
		}
		writeJSON(w, status, map[string]string{"error": err.Error()})
		return
	}
	s.cfg.TriggerRestart()
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"restarting": true,
		"note":       "boot schema persisted (previous kept as .bak); the new schema is being activated — poll /readyz until 200, then verify /admin/served-resources (single-engine drains+re-execs ~6 s; the in-process fleet hot-swaps this app with no downtime)",
	})
}

// adminKey is stored on the Service by Register (machine credential).
// (Declared on the struct in service.go via this field add.)

// --- auth middleware --------------------------------------------------------

// platformClaimsFromRequest parses and validates a platform token from the
// Authorization header. Returns nil when absent/invalid.
func (s *Service) platformClaimsFromRequest(r *http.Request) *PlatformClaims {
	h := r.Header.Get("Authorization")
	tok, ok := auth.BearerToken(h)
	if !ok {
		return nil
	}
	c, err := parsePlatformToken(tok, s.cfg.JWTSecret)
	if err != nil {
		return nil
	}
	return c
}

func (s *Service) adminKeyOK(r *http.Request) bool {
	if s.adminKey == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Admin-Key")), []byte(s.adminKey)) == 1
}

// requirePlatform allows a valid platform token OR the admin key (machine path).
func (s *Service) requirePlatform(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c := s.platformClaimsFromRequest(r); c != nil {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), platformCtxKey{}, c)))
			return
		}
		if s.adminKeyOK(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "platform admin authorization required"})
	})
}

// requirePlatformToken requires a platform token SPECIFICALLY (the admin key is not
// accepted — these routes operate on the authenticated admin's own identity).
func (s *Service) requirePlatformToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := s.platformClaimsFromRequest(r)
		if c == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "platform token required"})
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), platformCtxKey{}, c)))
	})
}

func platformClaimsFromCtx(ctx context.Context) *PlatformClaims {
	c, _ := ctx.Value(platformCtxKey{}).(*PlatformClaims)
	return c
}

// --- auth handlers ----------------------------------------------------------

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct{ Email, Password string }
	if !decode(w, r, &req) {
		return
	}
	res, err := s.Login(r.Context(), req.Email, req.Password)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, res)
	case errors.Is(err, ErrTooManyAttempts):
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many attempts"})
	case errors.Is(err, ErrInvalidCredentials):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "login failed"})
	}
}

func (s *Service) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	_ = decodeAllowEmpty(w, r, &req)
	tok := req.Token
	if tok == "" {
		if bt, ok := auth.BearerToken(r.Header.Get("Authorization")); ok {
			tok = bt
		}
	}
	if tok == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing token"})
		return
	}
	newTok, err := s.Refresh(r.Context(), tok)
	if err != nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired token"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": newTok})
}

func (s *Service) handleMFAEnable(w http.ResponseWriter, r *http.Request) {
	c := platformClaimsFromCtx(r.Context())
	secret, uri, err := s.EnableMFA(r.Context(), c.AdminID)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mfa enable failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "otpauth_uri": uri})
}

func (s *Service) handleMFAConfirm(w http.ResponseWriter, r *http.Request) {
	c := platformClaimsFromCtx(r.Context())
	var req struct{ Code string }
	if !decode(w, r, &req) {
		return
	}
	codes, err := s.ConfirmMFA(r.Context(), c.AdminID, req.Code)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"enabled": true, "backup_codes": codes})
	case errors.Is(err, ErrMFANotEnrolled):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mfa not started"})
	case errors.Is(err, ErrMFAInvalidCode):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid code"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mfa confirm failed"})
	}
}

func (s *Service) handleMFAVerify(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MFAToken string `json:"mfa_token"`
		Code     string `json:"code"`
	}
	if !decode(w, r, &req) {
		return
	}
	res, err := s.MFAVerify(r.Context(), req.MFAToken, req.Code)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, res)
	case errors.Is(err, ErrTooManyAttempts):
		w.Header().Set("Retry-After", "60")
		writeJSON(w, http.StatusTooManyRequests, map[string]string{"error": "too many attempts"})
	case errors.Is(err, ErrPlatformToken):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid or expired mfa token"})
	case errors.Is(err, ErrMFAInvalidCode), errors.Is(err, ErrMFANotEnrolled):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid code"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mfa verify failed"})
	}
}

func (s *Service) handleMFADisable(w http.ResponseWriter, r *http.Request) {
	c := platformClaimsFromCtx(r.Context())
	var req struct {
		Code     string `json:"code"`
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	err := s.DisableMFA(r.Context(), c.AdminID, req.Code, req.Password)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]any{"enabled": false})
	case errors.Is(err, ErrMFANotEnrolled):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mfa not enabled"})
	case errors.Is(err, ErrMFAInvalidCode):
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "second factor required to disable mfa"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "mfa disable failed"})
	}
}

// --- shared helpers ---------------------------------------------------------

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBody)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		// This is the one operator surface that already got the policy right —
		// and then threw the evidence away. encoding/json had produced
		// `json: unknown field "rol"`, naming the exact typo, and it was replaced
		// with a flat "invalid JSON body" that reads identically for a typo'd
		// key, malformed syntax and an oversized body. The operator is left to
		// diff their request against the docs by eye (ADR-024).
		//
		// Name the offending KEY, and only the key.
		//
		// The first version of this echoed err.Error() unconditionally, justified
		// by a comment claiming "this route is already authenticated". That was
		// false: decode() also serves /admin/auth/login and the MFA challenge,
		// which are reachable with no credentials at all. And encoding/json's
		// OTHER messages carry Go struct field names and declared types
		// ("cannot unmarshal string into Go struct field X.y of type int") — real
		// internal structure, handed to an anonymous caller.
		//
		// The unknown-field message is different in kind: `json: unknown field
		// "rol"` echoes a key the CALLER just sent, which is exactly the operator
		// value we wanted (a typo is invisible otherwise) and discloses nothing.
		// So that one is surfaced and every other decode failure stays terse.
		status, msg := http.StatusBadRequest, "invalid JSON body"
		if e := err.Error(); strings.HasPrefix(e, "json: unknown field ") {
			msg += ": " + e
		}
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status, msg = http.StatusRequestEntityTooLarge, "request body too large"
		}
		writeJSON(w, status, map[string]string{"error": msg})
		return false
	}
	return true
}

func decodeAllowEmpty(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBody)
	return json.NewDecoder(r.Body).Decode(dst) == nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// authClaims re-exports auth validation for the observability tenant-admin path.
func validateTenantToken(tokenStr, secret string) (*auth.Claims, error) {
	return auth.ValidateToken(tokenStr, secret)
}
