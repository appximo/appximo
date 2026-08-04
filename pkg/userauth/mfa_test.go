package userauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/tenant"
)

func mfaService(t *testing.T) *Service {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test requires Postgres")
	}
	return NewService(NewStore(testPool), Config{
		JWTSecret: testSecret, SignupRole: "viewer", LoginBurst: 100, TokenTTL: time.Hour,
	})
}

func currentTOTP(t *testing.T, secret string) string {
	t.Helper()
	key, err := base32NoPad.DecodeString(secret)
	if err != nil {
		t.Fatalf("decode secret: %v", err)
	}
	return totpCodeAt(key, time.Now())
}

// enroll signs up a user and fully enables MFA, returning the user id, the TOTP
// secret, and the one-time backup codes.
func enroll(t *testing.T, svc *Service, tn, email, password string) (userID, secret string, backup []string) {
	t.Helper()
	ctx := context.Background()
	signup, err := svc.Signup(ctx, tn, email, password)
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	secret, _, err = svc.EnableMFA(ctx, tn, signup.User.ID)
	if err != nil {
		t.Fatalf("enable: %v", err)
	}
	backup, err = svc.ConfirmMFA(ctx, tn, signup.User.ID, currentTOTP(t, secret))
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	return signup.User.ID, secret, backup
}

func TestMFA_EnableConfirmFlow(t *testing.T) {
	svc := mfaService(t)
	const tn = "mfaenroll"
	createTenantSchema(t, tn)
	ctx := context.Background()
	uid, secret, backup := enroll(t, svc, tn, "m@x.com", "the-password-1")

	if secret == "" {
		t.Fatal("no secret returned")
	}
	if len(backup) != backupCodeCount {
		t.Fatalf("expected %d backup codes, got %d", backupCodeCount, len(backup))
	}
	enabled, err := svc.store.MFAEnabled(ctx, tn, uid)
	if err != nil || !enabled {
		t.Fatalf("MFA should be enabled: enabled=%v err=%v", enabled, err)
	}
	// A wrong confirmation code is rejected (so a mis-scanned secret never enables).
	_, _, _ = svc.EnableMFA(ctx, tn, uid) // reset to a fresh, unconfirmed secret
	if _, err := svc.ConfirmMFA(ctx, tn, uid, "000000"); err != ErrMFAInvalidCode {
		t.Fatalf("wrong confirm code: got %v, want ErrMFAInvalidCode", err)
	}
}

func TestMFA_LoginTwoStep(t *testing.T) {
	svc := mfaService(t)
	const tn = "mfalogin"
	createTenantSchema(t, tn)
	ctx := context.Background()
	_, secret, _ := enroll(t, svc, tn, "two@x.com", "the-password-1")

	// Step 1: password login returns a challenge, NOT the final token.
	res, err := svc.Login(ctx, tn, "two@x.com", "the-password-1")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if !res.MFARequired || res.MFAToken == "" {
		t.Fatalf("expected mfa challenge, got %+v", res)
	}
	if res.Token != "" {
		t.Fatal("final token leaked before the second factor")
	}

	// Step 2: verify the TOTP code → final engine-valid JWT.
	final, err := svc.MFAVerify(ctx, tn, res.MFAToken, currentTOTP(t, secret))
	if err != nil {
		t.Fatalf("mfa verify: %v", err)
	}
	if final.Token == "" {
		t.Fatal("no final token after verify")
	}
	if _, err := auth.ValidateToken(final.Token, testSecret); err != nil {
		t.Fatalf("final token rejected by engine validator: %v", err)
	}
}

func TestMFA_VerifyWrongCode(t *testing.T) {
	svc := mfaService(t)
	const tn = "mfawrong"
	createTenantSchema(t, tn)
	ctx := context.Background()
	_, _, _ = enroll(t, svc, tn, "w@x.com", "the-password-1")
	res, _ := svc.Login(ctx, tn, "w@x.com", "the-password-1")
	if _, err := svc.MFAVerify(ctx, tn, res.MFAToken, "000000"); err != ErrMFAInvalidCode {
		t.Fatalf("wrong code: got %v, want ErrMFAInvalidCode", err)
	}
}

func TestMFA_BackupCodeOneUse(t *testing.T) {
	svc := mfaService(t)
	const tn = "mfabackup"
	createTenantSchema(t, tn)
	ctx := context.Background()
	_, _, backup := enroll(t, svc, tn, "b@x.com", "the-password-1")

	res, _ := svc.Login(ctx, tn, "b@x.com", "the-password-1")
	// A backup code works in place of the TOTP.
	if _, err := svc.MFAVerify(ctx, tn, res.MFAToken, backup[0]); err != nil {
		t.Fatalf("backup code verify: %v", err)
	}
	// The SAME backup code cannot be reused.
	res2, _ := svc.Login(ctx, tn, "b@x.com", "the-password-1")
	if _, err := svc.MFAVerify(ctx, tn, res2.MFAToken, backup[0]); err != ErrMFAInvalidCode {
		t.Fatalf("reused backup code: got %v, want ErrMFAInvalidCode", err)
	}
	// A different, unused backup code still works.
	res3, _ := svc.Login(ctx, tn, "b@x.com", "the-password-1")
	if _, err := svc.MFAVerify(ctx, tn, res3.MFAToken, backup[1]); err != nil {
		t.Fatalf("second backup code: %v", err)
	}
}

func TestMFA_PendingTokenIsNotAnAccessToken(t *testing.T) {
	svc := mfaService(t)
	const tn = "mfatok"
	createTenantSchema(t, tn)
	ctx := context.Background()
	_, _, _ = enroll(t, svc, tn, "p@x.com", "the-password-1")
	res, _ := svc.Login(ctx, tn, "p@x.com", "the-password-1")

	// The mfa_token is signed with the engine secret, but carries NO engine identity
	// (different claim keys) — parsed as an access token it yields empty user/role,
	// so RBAC denies. It can never authorize a CRUD request.
	claims, err := auth.ValidateToken(res.MFAToken, testSecret)
	if err != nil {
		t.Fatalf("mfa token should still be a valid JWT shape: %v", err)
	}
	if claims.UserID != "" || claims.Role != "" || claims.TenantID != "" {
		t.Fatalf("mfa token carries engine identity — must be inert: %+v", claims)
	}
}

func TestMFA_DisableRequiresSecondFactor(t *testing.T) {
	svc := mfaService(t)
	const tn = "mfadisable"
	createTenantSchema(t, tn)
	ctx := context.Background()
	uid, secret, _ := enroll(t, svc, tn, "d@x.com", "the-password-1")

	// No factor / wrong code → refused.
	if err := svc.DisableMFA(ctx, tn, uid, "000000", ""); err != ErrMFAInvalidCode {
		t.Fatalf("disable with wrong code: got %v, want ErrMFAInvalidCode", err)
	}
	if err := svc.DisableMFA(ctx, tn, uid, "", ""); err != ErrMFAInvalidCode {
		t.Fatalf("disable with nothing: got %v, want ErrMFAInvalidCode", err)
	}
	// Correct TOTP → disabled.
	if err := svc.DisableMFA(ctx, tn, uid, currentTOTP(t, secret), ""); err != nil {
		t.Fatalf("disable with code: %v", err)
	}
	enabled, _ := svc.store.MFAEnabled(ctx, tn, uid)
	if enabled {
		t.Fatal("MFA still enabled after disable")
	}
	// And login no longer challenges.
	res, _ := svc.Login(ctx, tn, "d@x.com", "the-password-1")
	if res.MFARequired || res.Token == "" {
		t.Fatalf("login still challenged after disable: %+v", res)
	}
}

func TestMFA_DisableWithPassword(t *testing.T) {
	svc := mfaService(t)
	const tn = "mfadispw"
	createTenantSchema(t, tn)
	ctx := context.Background()
	uid, _, _ := enroll(t, svc, tn, "dp@x.com", "the-password-1")
	// Account password is an accepted second factor for disable.
	if err := svc.DisableMFA(ctx, tn, uid, "", "the-password-1"); err != nil {
		t.Fatalf("disable with password: %v", err)
	}
	if err := svc.DisableMFA(ctx, tn, uid, "", "the-password-1"); err != ErrMFANotEnrolled {
		t.Fatalf("disable again: got %v, want ErrMFANotEnrolled", err)
	}
}

func TestMFA_CrossTenantIsolation(t *testing.T) {
	svc := mfaService(t)
	const tnA, tnB = "mfaisoa", "mfaisob"
	createTenantSchema(t, tnA)
	createTenantSchema(t, tnB)
	ctx := context.Background()
	_, secretA, _ := enroll(t, svc, tnA, "iso@x.com", "the-password-1")
	// Same email in tenant B, no MFA.
	if _, err := svc.Signup(ctx, tnB, "iso@x.com", "the-password-1"); err != nil {
		t.Fatalf("signup B: %v", err)
	}
	// Tenant B's user is not MFA-challenged (A's enrollment is isolated).
	resB, _ := svc.Login(ctx, tnB, "iso@x.com", "the-password-1")
	if resB.MFARequired {
		t.Fatal("tenant B user wrongly challenged — MFA crossed tenants")
	}
	// An mfa_token from tenant A cannot be verified under tenant B.
	resA, _ := svc.Login(ctx, tnA, "iso@x.com", "the-password-1")
	if _, err := svc.MFAVerify(ctx, tnB, resA.MFAToken, currentTOTP(t, secretA)); err != ErrMFAToken {
		t.Fatalf("cross-tenant mfa_token: got %v, want ErrMFAToken", err)
	}
}

func TestMFA_SecretEncryptedAtRest(t *testing.T) {
	svc := mfaService(t)
	const tn = "mfaenc"
	createTenantSchema(t, tn)
	ctx := context.Background()
	uid, secret, _ := enroll(t, svc, tn, "enc@x.com", "the-password-1")

	var stored string
	tbl, _ := svc.store.mfaTbl(tn)
	if err := testPool.QueryRow(ctx, "SELECT totp_secret_encrypted FROM "+tbl+" WHERE user_id=$1", uid).Scan(&stored); err != nil {
		t.Fatalf("read stored secret: %v", err)
	}
	if stored == secret {
		t.Fatal("TOTP secret stored in CLEARTEXT")
	}
	// And it round-trips through the cipher back to the original.
	dec, err := svc.mfaCipher.decrypt(stored)
	if err != nil || dec != secret {
		t.Fatalf("encrypted secret does not decrypt to the original: %v", err)
	}
}

func TestMFA_E2E_TokenWorksThroughMiddleware(t *testing.T) {
	svc := mfaService(t)
	const tn = "mfae2e"
	createTenantSchema(t, tn)
	ctx := context.Background()
	_, secret, _ := enroll(t, svc, tn, "e2e@x.com", "the-password-1")

	res, _ := svc.Login(ctx, tn, "e2e@x.com", "the-password-1")
	final, err := svc.MFAVerify(ctx, tn, res.MFAToken, currentTOTP(t, secret))
	if err != nil {
		t.Fatalf("verify: %v", err)
	}

	var gotRole, gotTenant string
	h := auth.JWTMiddleware(testSecret)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c := auth.ClaimsFromCtx(r.Context()); c != nil {
			gotRole, gotTenant = c.Role, c.TenantID
		}
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/api/tasks", nil)
	req = req.WithContext(tenant.WithContext(req.Context(), &tenant.TenantCtx{ID: tn, PGSchema: "tenant_" + tn}))
	req.Header.Set("Authorization", "Bearer "+final.Token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || gotRole != "viewer" || gotTenant != tn {
		t.Fatalf("final MFA token failed through middleware: code=%d role=%q tenant=%q", rec.Code, gotRole, gotTenant)
	}
}
