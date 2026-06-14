package userauth

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/miguelangel/appitools/pkg/tenant"
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
	return r
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
		_ = decodeBodyAllowEmpty(w, r, &req)
		token = req.Token
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
			writeAuthErr(w, http.StatusForbidden, "signup is disabled")
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
	// JSON body's "token" field.
	var req refreshRequest
	_ = decodeBodyAllowEmpty(w, r, &req)
	tokenStr := req.Token
	if tokenStr == "" {
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			tokenStr = strings.TrimPrefix(h, "Bearer ")
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
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeAuthErr(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

// decodeBodyAllowEmpty is decodeBody but tolerates an empty body (refresh can
// carry its token in the Authorization header instead).
func decodeBodyAllowEmpty(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxAuthBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
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
