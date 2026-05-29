package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/miguelangel/appitools/pkg/auth"
)

const testSecret = "super-secret-key-for-tests"

// --- GenerateToken / ValidateToken ---

func TestGenerateAndValidate_ValidToken(t *testing.T) {
	c := auth.Claims{
		UserID:   "user-123",
		Role:     "gerente",
		TenantID: "acme",
	}
	token, err := auth.GenerateToken(c, testSecret)
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}

	got, err := auth.ValidateToken(token, testSecret)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if got.UserID != c.UserID {
		t.Errorf("UserID: got %q, want %q", got.UserID, c.UserID)
	}
	if got.Role != c.Role {
		t.Errorf("Role: got %q, want %q", got.Role, c.Role)
	}
	if got.TenantID != c.TenantID {
		t.Errorf("TenantID: got %q, want %q", got.TenantID, c.TenantID)
	}
}

func TestValidateToken_Expired(t *testing.T) {
	c := auth.Claims{
		UserID: "user-456",
		Role:   "operario",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-1 * time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
	}
	token, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(testSecret))
	if err != nil {
		t.Fatalf("sign expired token: %v", err)
	}

	_, err = auth.ValidateToken(token, testSecret)
	if err == nil {
		t.Fatal("expected error for expired token, got nil")
	}
}

func TestValidateToken_WrongSecret(t *testing.T) {
	c := auth.Claims{UserID: "u", Role: "public", TenantID: "x"}
	token, _ := auth.GenerateToken(c, testSecret)

	_, err := auth.ValidateToken(token, "wrong-secret")
	if err == nil {
		t.Fatal("expected error for wrong secret, got nil")
	}
}

func TestValidateToken_Malformed(t *testing.T) {
	_, err := auth.ValidateToken("this.is.not.a.jwt", testSecret)
	if err == nil {
		t.Fatal("expected error for malformed token, got nil")
	}
}

// --- JWTMiddleware ---

// dummyHandler returns 200 and the role from Claims (if present).
var dummyHandler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	claims := auth.ClaimsFromCtx(r.Context())
	if claims == nil {
		http.Error(w, "no claims", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"role": claims.Role})
})

func TestJWTMiddleware_ValidToken(t *testing.T) {
	c := auth.Claims{UserID: "u1", Role: "super_admin", TenantID: "acme"}
	token, _ := auth.GenerateToken(c, testSecret)

	req := httptest.NewRequest(http.MethodGet, "/api/guides", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	auth.JWTMiddleware(testSecret)(dummyHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var body map[string]string
	json.NewDecoder(rr.Body).Decode(&body)
	if body["role"] != "super_admin" {
		t.Errorf("role: got %q, want %q", body["role"], "super_admin")
	}
}

func TestJWTMiddleware_MissingHeader(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/guides", nil)
	rr := httptest.NewRecorder()

	auth.JWTMiddleware(testSecret)(dummyHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "missing token") {
		t.Errorf("expected 'missing token' in body, got %s", rr.Body.String())
	}
}

func TestJWTMiddleware_InvalidToken(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/guides", nil)
	req.Header.Set("Authorization", "Bearer not.a.real.token")
	rr := httptest.NewRecorder()

	auth.JWTMiddleware(testSecret)(dummyHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "invalid token") {
		t.Errorf("expected 'invalid token' in body, got %s", rr.Body.String())
	}
}

func TestJWTMiddleware_HealthPassthrough(t *testing.T) {
	healthHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rr := httptest.NewRecorder()

	// No Authorization header — /health must pass through.
	auth.JWTMiddleware(testSecret)(healthHandler).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for /health without token, got %d", rr.Code)
	}
}
