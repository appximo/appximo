package userauth

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/tenant"
)

// maxAuthBodyBytes caps an auth request body. Credentials are tiny; this just
// stops an oversized JSON from forcing unbounded allocation (OWASP API4).
const maxAuthBodyBytes = 1 << 16 // 64 KiB

// Router returns the /auth subrouter (mounted at /auth by the engine). The
// routes are UNAUTHENTICATED by design — signup/login happen BEFORE a token
// exists — but tenant-aware: every handler resolves the tenant from the Host
// subdomain (TenantMiddleware ran upstream), so a user is always created and
// authenticated within ONE tenant's schema. These paths sit outside /api/, so
// the RBAC middleware passes them through; the engine adds "/auth/" to the JWT
// skip list so no Bearer token is required to reach them.
func (s *Service) Router() http.Handler {
	r := chi.NewRouter()
	r.Post("/signup", s.handleSignup)
	r.Post("/login", s.handleLogin)
	r.Post("/refresh", s.handleRefresh)
	// AUTH-EMAIL-V1: email verification + password reset. The *_request endpoints
	// enqueue an email (async via the outbox); confirm endpoints consume the
	// single-use token. /verify accepts GET (the clickable email link) or POST.
	r.Post("/verify/request", s.handleVerifyRequest)
	r.Get("/verify", s.handleVerifyConfirm)
	r.Post("/verify", s.handleVerifyConfirm)
	r.Post("/reset/request", s.handleResetRequest)
	r.Post("/reset/confirm", s.handleResetConfirm)
	// AUTH-OAUTH-V1: social login. /oauth lists configured providers; /oauth/{p}
	// starts the flow (302 to the provider); /oauth/{p}/callback completes it. The
	// callback derives the tenant from the SIGNED STATE, never from the Host.
	r.Get("/oauth", s.handleOAuthProviders)
	r.Get("/oauth/{provider}", s.handleOAuthStart)
	r.Get("/oauth/{provider}/callback", s.handleOAuthCallback)
	// AUTH-MFA-V1: TOTP second factor. enable/confirm/disable require a valid
	// session JWT (parsed here since /auth/ is JWT-skipped); verify uses the
	// intermediate mfa_token from the login challenge (no session yet).
	r.Post("/mfa/enable", s.handleMFAEnable)
	r.Post("/mfa/confirm", s.handleMFAConfirm)
	r.Post("/mfa/verify", s.handleMFAVerify)
	r.Post("/mfa/disable", s.handleMFADisable)
	return r
}

// authClaims extracts and validates the session JWT from the Authorization header
// for the JWT-skipped /auth/ routes that DO need authentication (mfa enable/confirm/
// disable). It also enforces that the token's tenant matches the request Host
// tenant. Returns nil when there is no valid, tenant-matching token.
func (s *Service) authClaims(r *http.Request, tenantID string) *auth.Claims {
	h := r.Header.Get("Authorization")
	tok, ok := auth.BearerToken(h)
	if !ok {
		return nil
	}
	claims, err := auth.ValidateToken(tok, s.cfg.JWTSecret)
	if err != nil || claims.UserID == "" {
		return nil
	}
	if claims.TenantID != "" && claims.TenantID != tenantID {
		return nil
	}
	return claims
}

type mfaCodeRequest struct {
	Code string `json:"code"`
}

type mfaVerifyRequest struct {
	MFAToken string `json:"mfa_token"`
	Code     string `json:"code"`
}

type mfaDisableRequest struct {
	Code     string `json:"code,omitempty"`
	Password string `json:"password,omitempty"`
}

func (s *Service) handleMFAEnable(w http.ResponseWriter, r *http.Request) {
	tc := tenant.FromCtx(r.Context())
	if tc == nil {
		writeAuthErr(w, http.StatusBadRequest, "invalid tenant")
		return
	}
	claims := s.authClaims(r, tc.ID)
	if claims == nil {
		writeAuthErr(w, http.StatusUnauthorized, "authentication required")
		return
	}
	secret, uri, err := s.EnableMFA(r.Context(), tc.ID, claims.UserID)
	if err != nil {
		writeAuthErr(w, http.StatusInternalServerError, "mfa enable failed")
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]string{"secret": secret, "otpauth_uri": uri})
}

func (s *Service) handleMFAConfirm(w http.ResponseWriter, r *http.Request) {
	tc := tenant.FromCtx(r.Context())
	if tc == nil {
		writeAuthErr(w, http.StatusBadRequest, "invalid tenant")
		return
	}
	claims := s.authClaims(r, tc.ID)
	if claims == nil {
		writeAuthErr(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req mfaCodeRequest
	if !decodeBody(w, r, &req) {
		return
	}
	codes, err := s.ConfirmMFA(r.Context(), tc.ID, claims.UserID, req.Code)
	if err != nil {
		switch {
		case errors.Is(err, ErrMFANotEnrolled):
			writeAuthErr(w, http.StatusBadRequest, "mfa not started")
		case errors.Is(err, ErrMFAInvalidCode):
			writeAuthErr(w, http.StatusUnauthorized, "invalid code")
		default:
			writeAuthErr(w, http.StatusInternalServerError, "mfa confirm failed")
		}
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"enabled": true, "backup_codes": codes})
}

func (s *Service) handleMFAVerify(w http.ResponseWriter, r *http.Request) {
	tc := tenant.FromCtx(r.Context())
	if tc == nil {
		writeAuthErr(w, http.StatusBadRequest, "invalid tenant")
		return
	}
	var req mfaVerifyRequest
	if !decodeBody(w, r, &req) {
		return
	}
	res, err := s.MFAVerify(r.Context(), tc.ID, req.MFAToken, req.Code)
	if err != nil {
		switch {
		case errors.Is(err, ErrTooManyAttempts):
			w.Header().Set("Retry-After", "60")
			writeAuthErr(w, http.StatusTooManyRequests, "too many attempts, try again later")
		case errors.Is(err, ErrMFAToken):
			writeAuthErr(w, http.StatusUnauthorized, "invalid or expired mfa token")
		case errors.Is(err, ErrMFAInvalidCode), errors.Is(err, ErrMFANotEnrolled):
			writeAuthErr(w, http.StatusUnauthorized, "invalid code")
		default:
			writeAuthErr(w, http.StatusInternalServerError, "mfa verify failed")
		}
		return
	}
	writeAuthJSON(w, http.StatusOK, res)
}

func (s *Service) handleMFADisable(w http.ResponseWriter, r *http.Request) {
	tc := tenant.FromCtx(r.Context())
	if tc == nil {
		writeAuthErr(w, http.StatusBadRequest, "invalid tenant")
		return
	}
	claims := s.authClaims(r, tc.ID)
	if claims == nil {
		writeAuthErr(w, http.StatusUnauthorized, "authentication required")
		return
	}
	var req mfaDisableRequest
	if !decodeBody(w, r, &req) {
		return
	}
	err := s.DisableMFA(r.Context(), tc.ID, claims.UserID, req.Code, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, ErrMFANotEnrolled):
			writeAuthErr(w, http.StatusBadRequest, "mfa not enabled")
		case errors.Is(err, ErrMFAInvalidCode):
			// Disable requires a SECOND FACTOR (code or password) — the session JWT alone is not enough.
			writeAuthErr(w, http.StatusUnauthorized, "second factor required to disable mfa")
		default:
			writeAuthErr(w, http.StatusInternalServerError, "mfa disable failed")
		}
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"enabled": false})
}

// oauthCallbackURI builds the redirect_uri for a provider. It MUST be byte-identical
// at initiate and at the token exchange (OAuth requires the match). The configured
// fixed callback origin wins (production multi-tenant: one registered URI per
// provider, tenant carried in state); otherwise it is derived from the request,
// which is consistent in dev because the provider redirects back to exactly the
// URI we sent.
func (s *Service) oauthCallbackURI(r *http.Request, provider string) string {
	base := ""
	if s.oauth != nil {
		base = s.oauth.callbackURL
	}
	if base == "" {
		scheme := "http"
		if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
			scheme = proto
		} else if r.TLS != nil {
			scheme = "https"
		}
		host := r.Host
		if xf := r.Header.Get("X-Forwarded-Host"); xf != "" {
			host = xf
		}
		base = scheme + "://" + host
	}
	return strings.TrimRight(base, "/") + "/auth/oauth/" + provider + "/callback"
}

func (s *Service) handleOAuthProviders(w http.ResponseWriter, r *http.Request) {
	names := []string{}
	if s.oauth != nil {
		for _, n := range []string{"google", "github", "microsoft"} {
			if s.oauth.providers[n] != nil {
				names = append(names, n)
			}
		}
	}
	writeAuthJSON(w, http.StatusOK, map[string]any{"providers": names})
}

func (s *Service) handleOAuthStart(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	// The tenant for the flow comes from the Host at INITIATE (it is then sealed
	// into the signed state and travels through the provider round-trip).
	tc := tenant.FromCtx(r.Context())
	if tc == nil {
		writeAuthErr(w, http.StatusBadRequest, "invalid tenant")
		return
	}
	if !s.OAuthProviderConfigured(provider) {
		writeAuthErr(w, http.StatusNotFound, "oauth provider not configured")
		return
	}
	authURL, err := s.OAuthAuthCodeURL(tc.ID, provider, s.oauthCallbackURI(r, provider))
	if err != nil {
		writeAuthErr(w, http.StatusInternalServerError, "oauth start failed")
		return
	}
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (s *Service) handleOAuthCallback(w http.ResponseWriter, r *http.Request) {
	provider := chi.URLParam(r, "provider")
	if errParam := r.URL.Query().Get("error"); errParam != "" {
		// The user denied consent, or the provider returned an error.
		writeAuthErr(w, http.StatusBadRequest, "oauth authorization failed")
		return
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	res, err := s.OAuthCallback(r.Context(), provider, code, state, s.oauthCallbackURI(r, provider))
	if err != nil {
		switch {
		case errors.Is(err, ErrOAuthDisabled):
			writeAuthErr(w, http.StatusNotFound, "oauth provider not configured")
		case errors.Is(err, ErrOAuthState):
			writeAuthErr(w, http.StatusBadRequest, "invalid or expired oauth state")
		case errors.Is(err, ErrOAuthNoEmail):
			writeAuthErr(w, http.StatusUnprocessableEntity, "provider returned no email")
		case errors.Is(err, ErrOAuthNoAutoUser):
			writeAuthErr(w, http.StatusForbidden, "registration is disabled")
		case errors.Is(err, ErrOAuthExchange):
			writeAuthErr(w, http.StatusBadGateway, "oauth exchange failed")
		default:
			writeAuthErr(w, http.StatusInternalServerError, "oauth login failed")
		}
		return
	}
	// Success: redirect to the SPA with the token in the fragment, or return JSON.
	if redir := s.OAuthSuccessRedirect(); redir != "" {
		sep := "#"
		if strings.Contains(redir, "#") {
			sep = "&"
		}
		http.Redirect(w, r, redir+sep+"token="+url.QueryEscape(res.Token), http.StatusFound)
		return
	}
	writeAuthJSON(w, http.StatusOK, res)
}

// uniformEmailResponse is the SAME body returned by /verify/request and
// /reset/request whether or not the email exists — the anti-enumeration contract.
var uniformEmailResponse = map[string]string{"message": "if that email is registered, a link has been sent"}

type emailRequest struct {
	Email string `json:"email"`
}

type resetConfirmRequest struct {
	Token       string `json:"token"`
	NewPassword string `json:"new_password"`
}

type tokenRequest struct {
	Token string `json:"token,omitempty"`
}

// linkBaseFor resolves the origin used to build email links. The Service's
// configured BaseURL wins (for deployments whose public URL differs from the
// internal Host); otherwise it is derived from the request, which keeps the link
// on the SAME tenant subdomain the request arrived on (multi-tenant-correct).
func (s *Service) linkBaseFor(r *http.Request) string {
	if s.cfg.BaseURL != "" {
		return strings.TrimRight(s.cfg.BaseURL, "/")
	}
	scheme := "http"
	if proto := r.Header.Get("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	} else if r.TLS != nil {
		scheme = "https"
	}
	host := r.Host
	if xf := r.Header.Get("X-Forwarded-Host"); xf != "" {
		host = xf
	}
	return scheme + "://" + host
}

func (s *Service) handleVerifyRequest(w http.ResponseWriter, r *http.Request) {
	tc := tenant.FromCtx(r.Context())
	if tc == nil {
		writeAuthErr(w, http.StatusBadRequest, "invalid tenant")
		return
	}
	var req emailRequest
	if !decodeBody(w, r, &req) {
		return
	}
	err := s.RequestVerify(r.Context(), tc.ID, req.Email, s.linkBaseFor(r))
	switch {
	case err == nil:
		writeAuthJSON(w, http.StatusOK, uniformEmailResponse)
	case errors.Is(err, ErrTooManyAttempts):
		w.Header().Set("Retry-After", "60")
		writeAuthErr(w, http.StatusTooManyRequests, "too many requests, try again later")
	default:
		writeAuthErr(w, http.StatusInternalServerError, "request failed")
	}
}

func (s *Service) handleResetRequest(w http.ResponseWriter, r *http.Request) {
	tc := tenant.FromCtx(r.Context())
	if tc == nil {
		writeAuthErr(w, http.StatusBadRequest, "invalid tenant")
		return
	}
	var req emailRequest
	if !decodeBody(w, r, &req) {
		return
	}
	err := s.RequestReset(r.Context(), tc.ID, req.Email, s.linkBaseFor(r))
	switch {
	case err == nil:
		writeAuthJSON(w, http.StatusOK, uniformEmailResponse)
	case errors.Is(err, ErrTooManyAttempts):
		w.Header().Set("Retry-After", "60")
		writeAuthErr(w, http.StatusTooManyRequests, "too many requests, try again later")
	default:
		writeAuthErr(w, http.StatusInternalServerError, "request failed")
	}
}

func (s *Service) handleVerifyConfirm(w http.ResponseWriter, r *http.Request) {
	tc := tenant.FromCtx(r.Context())
	if tc == nil {
		writeAuthErr(w, http.StatusBadRequest, "invalid tenant")
		return
	}
	token := r.URL.Query().Get("token")
	if token == "" && r.Method == http.MethodPost {
		var req tokenRequest
		if !decodeBodyAllowEmpty(w, r, &req) {
			return
		}
		token = req.Token
	}
	if token == "" {
		// NIGHT-SWEEP-S1: a MISSING/empty token used to be answered "invalid or
		// expired token" — misleading (the caller sent no token and goes off to
		// debug one; the F-8 naming axis). Say what is actually wrong.
		writeAuthErr(w, http.StatusBadRequest, "missing token: pass ?token=… on GET, or {\"token\":\"…\"} in the POST body")
		return
	}
	err := s.ConfirmVerify(r.Context(), tc.ID, token)
	switch {
	case err == nil:
		writeAuthJSON(w, http.StatusOK, map[string]string{"message": "email verified"})
	case errors.Is(err, ErrInvalidToken):
		writeAuthErr(w, http.StatusBadRequest, "invalid or expired token")
	default:
		writeAuthErr(w, http.StatusInternalServerError, "verification failed")
	}
}

func (s *Service) handleResetConfirm(w http.ResponseWriter, r *http.Request) {
	tc := tenant.FromCtx(r.Context())
	if tc == nil {
		writeAuthErr(w, http.StatusBadRequest, "invalid tenant")
		return
	}
	var req resetConfirmRequest
	if !decodeBody(w, r, &req) {
		return
	}
	err := s.ConfirmReset(r.Context(), tc.ID, req.Token, req.NewPassword)
	switch {
	case err == nil:
		writeAuthJSON(w, http.StatusOK, map[string]string{"message": "password updated"})
	case errors.Is(err, ErrWeakPassword):
		writeAuthErr(w, http.StatusUnprocessableEntity, "password too short")
	case errors.Is(err, ErrInvalidToken):
		writeAuthErr(w, http.StatusBadRequest, "invalid or expired token")
	default:
		writeAuthErr(w, http.StatusInternalServerError, "reset failed")
	}
}

type signupRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	// Role is accepted in the body for forward-compat but IGNORED for public
	// signup (a public endpoint must never let a caller choose its own role).
	Role string `json:"role,omitempty"`
}

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type refreshRequest struct {
	Token string `json:"token,omitempty"`
}

func (s *Service) handleSignup(w http.ResponseWriter, r *http.Request) {
	tc := tenant.FromCtx(r.Context())
	if tc == nil {
		writeAuthErr(w, http.StatusBadRequest, "invalid tenant")
		return
	}
	var req signupRequest
	if !decodeBody(w, r, &req) {
		return
	}
	res, err := s.Signup(r.Context(), tc.ID, req.Email, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, ErrSignupDisabled):
			// PHASE4-FIRST-MILE-S1: getting the FIRST user used to be a puzzle —
			// this 403 said "signup is disabled" and stopped. Name both ways out:
			// the operator's env switch, and the admin-created-user path.
			writeAuthErr(w, http.StatusForbidden,
				"signup is disabled: to enable public signup set APPXIMO_AUTH_SIGNUP_ROLE to a role your schema's rbac declares and restart; or have an admin create users in the admin panel (/admin → Users)")
		case errors.Is(err, ErrInvalidEmail):
			writeAuthErr(w, http.StatusUnprocessableEntity, "invalid email")
		case errors.Is(err, ErrWeakPassword):
			writeAuthErr(w, http.StatusUnprocessableEntity, "password too short")
		case errors.Is(err, ErrEmailTaken):
			writeAuthErr(w, http.StatusConflict, "email already registered")
		default:
			writeAuthErr(w, http.StatusInternalServerError, "signup failed")
		}
		return
	}
	writeAuthJSON(w, http.StatusCreated, res)
}

func (s *Service) handleLogin(w http.ResponseWriter, r *http.Request) {
	tc := tenant.FromCtx(r.Context())
	if tc == nil {
		writeAuthErr(w, http.StatusBadRequest, "invalid tenant")
		return
	}
	var req loginRequest
	if !decodeBody(w, r, &req) {
		return
	}
	res, err := s.Login(r.Context(), tc.ID, req.Email, req.Password)
	if err != nil {
		switch {
		case errors.Is(err, ErrTooManyAttempts):
			w.Header().Set("Retry-After", "60")
			writeAuthErr(w, http.StatusTooManyRequests, "too many attempts, try again later")
		case errors.Is(err, ErrInvalidCredentials):
			// Identical message + status for unknown-email and wrong-password.
			writeAuthErr(w, http.StatusUnauthorized, "invalid credentials")
		case errors.Is(err, ErrUserSuspended):
			writeAuthErr(w, http.StatusForbidden, "account suspended")
		case errors.Is(err, ErrTenantSuspended):
			writeAuthErr(w, http.StatusForbidden, "tenant suspended")
		default:
			writeAuthErr(w, http.StatusInternalServerError, "login failed")
		}
		return
	}
	writeAuthJSON(w, http.StatusOK, res)
}

func (s *Service) handleRefresh(w http.ResponseWriter, r *http.Request) {
	tc := tenant.FromCtx(r.Context())
	if tc == nil {
		writeAuthErr(w, http.StatusBadRequest, "invalid tenant")
		return
	}
	// The token to refresh may come from the Authorization: Bearer header or the
	// JSON body's "token" field. A body that is SENT must parse
	// (decodeBodyAllowEmpty tolerates only the truly-empty body — it used to
	// swallow every decode error, so a misspelled key silently fell through to
	// the header); and when BOTH carry a token they must AGREE — the body used
	// to win silently, the conflicting-parameters class (ADR-024 rule 11).
	var req refreshRequest
	if !decodeBodyAllowEmpty(w, r, &req) {
		return
	}
	tokenStr := req.Token
	if bt, ok := auth.BearerToken(r.Header.Get("Authorization")); ok {
		if tokenStr != "" && tokenStr != bt {
			writeAuthErr(w, http.StatusBadRequest, "two different tokens were sent (Authorization header and body): send one, or the same one")
			return
		}
		if tokenStr == "" {
			tokenStr = bt
		}
	}
	if tokenStr == "" {
		writeAuthErr(w, http.StatusBadRequest, "missing token")
		return
	}
	newToken, err := s.Refresh(r.Context(), tc.ID, tokenStr)
	if err != nil {
		switch {
		case errors.Is(err, ErrTenantMismatch):
			writeAuthErr(w, http.StatusUnauthorized, "token tenant mismatch")
		case errors.Is(err, ErrInvalidToken):
			writeAuthErr(w, http.StatusUnauthorized, "invalid or expired token")
		default:
			writeAuthErr(w, http.StatusInternalServerError, "refresh failed")
		}
		return
	}
	writeAuthJSON(w, http.StatusOK, map[string]string{"token": newToken})
}

// decodeBody reads a JSON body (capped, unknown fields rejected) into dst,
// writing a 400 and returning false on any decode error.
//
// NIGHT-SWEEP-S1: the flat "invalid JSON body" used to discard the decoder's
// own `json: unknown field "emial"` — the exact F-8 defect ADR-024's sweep
// fixed on /admin (platformadmin.decode) and left here. Same rule, same
// security reasoning: these routes are PRE-AUTH, so only the unknown-field
// form is surfaced (it echoes a key the CALLER just sent and discloses
// nothing); every other decode message can carry Go struct internals and
// stays terse.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeAuthErr(w, http.StatusBadRequest, authDecodeMsg(err))
		return false
	}
	return true
}

// authDecodeMsg maps a body-decode error to the safe client message (see
// decodeBody).
func authDecodeMsg(err error) string {
	msg := "invalid JSON body"
	if e := err.Error(); strings.HasPrefix(e, "json: unknown field ") {
		msg += ": " + e
	}
	return msg
}

// decodeBodyAllowEmpty is decodeBody but tolerates a genuinely EMPTY body
// (refresh can carry its token in the Authorization header instead).
//
// ONLY the empty body is tolerated (io.EOF before any JSON value). It used to
// swallow EVERY decode error — nullifying DisallowUnknownFields on the one
// route that used this helper: a misspelled {"tok": …} fell through to the
// header in silence (NIGHT-SWEEP-S1). A body that IS sent must parse.
func decodeBodyAllowEmpty(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			return true // no body at all — the tolerated case
		}
		writeAuthErr(w, http.StatusBadRequest, authDecodeMsg(err))
		return false
	}
	return true
}

func writeAuthErr(w http.ResponseWriter, status int, msg string) {
	writeAuthJSON(w, status, map[string]string{"error": msg})
}

func writeAuthJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
