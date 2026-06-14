package userauth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/miguelangel/appitools/pkg/consumers"
	"github.com/miguelangel/appitools/pkg/worker"
)

const testLinkBase = "https://acme.localhost"

// lastEmailEvent returns the template name and link from the most recent outbox
// email event for tenantID. It is how the tests assert what the engine enqueued.
func lastEmailEvent(t *testing.T, tenantID string) (template, link string) {
	t.Helper()
	var topic string
	var payload []byte
	err := testPool.QueryRow(context.Background(),
		`SELECT topic, payload FROM public.outbox WHERE tenant_id=$1 ORDER BY id DESC LIMIT 1`, tenantID).
		Scan(&topic, &payload)
	if err != nil {
		t.Fatalf("read outbox for %s: %v", tenantID, err)
	}
	if topic != "email.send" {
		t.Fatalf("expected topic email.send, got %q", topic)
	}
	var ev struct {
		To       string `json:"to"`
		Template string `json:"template"`
		Data     struct {
			Link string `json:"link"`
		} `json:"data"`
	}
	if err := json.Unmarshal(payload, &ev); err != nil {
		t.Fatalf("decode outbox payload: %v", err)
	}
	return ev.Template, ev.Data.Link
}

func tokenFromLink(t *testing.T, link string) string {
	t.Helper()
	u, err := url.Parse(link)
	if err != nil {
		t.Fatalf("parse link %q: %v", link, err)
	}
	tok := u.Query().Get("token")
	if tok == "" {
		t.Fatalf("no token in link %q", link)
	}
	return tok
}

func outboxCount(t *testing.T, tenantID string) int {
	t.Helper()
	var n int
	if err := testPool.QueryRow(context.Background(),
		`SELECT count(*) FROM public.outbox WHERE tenant_id=$1`, tenantID).Scan(&n); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	return n
}

func TestIntegration_VerifyFlow(t *testing.T) {
	svc := requirePG(t)
	const tn = "verflow"
	createTenantSchema(t, tn)
	ctx := context.Background()

	if _, err := svc.Signup(ctx, tn, "v@x.com", "verify-password"); err != nil {
		t.Fatalf("signup: %v", err)
	}
	// Request a verification email.
	if err := svc.RequestVerify(ctx, tn, "v@x.com", testLinkBase); err != nil {
		t.Fatalf("request verify: %v", err)
	}
	tmpl, link := lastEmailEvent(t, tn)
	if tmpl != "verification" {
		t.Fatalf("expected verification template, got %q", tmpl)
	}
	if !strings.HasPrefix(link, testLinkBase+"/auth/verify?token=") {
		t.Fatalf("unexpected verify link: %q", link)
	}
	token := tokenFromLink(t, link)

	// Confirm → email_verified flips true.
	if err := svc.ConfirmVerify(ctx, tn, token); err != nil {
		t.Fatalf("confirm verify: %v", err)
	}
	u, _ := svc.store.GetByEmail(ctx, tn, "v@x.com")
	if !u.EmailVerified {
		t.Fatal("email_verified was not set after confirm")
	}
	// Reusing the same token must fail (single-use).
	if err := svc.ConfirmVerify(ctx, tn, token); err != ErrInvalidToken {
		t.Fatalf("reused token: got %v, want ErrInvalidToken", err)
	}
}

func TestIntegration_VerifyExpiredToken(t *testing.T) {
	svc := requirePG(t)
	const tn = "verexp"
	createTenantSchema(t, tn)
	ctx := context.Background()
	res, err := svc.Signup(ctx, tn, "exp@x.com", "verify-password")
	if err != nil {
		t.Fatalf("signup: %v", err)
	}
	if err := svc.store.ensureTokens(ctx, tn); err != nil {
		t.Fatalf("ensure tokens: %v", err)
	}
	// Insert an ALREADY-expired verify token directly.
	plain := "expired-token-plain-value"
	_, err = testPool.Exec(ctx, fmt.Sprintf(
		`INSERT INTO %s (user_id, type, token_hash, expires_at) VALUES ($1,'verify',$2, now() - interval '1 hour')`,
		mustTokensTbl(t, svc, tn)), res.User.ID, hashToken(plain))
	if err != nil {
		t.Fatalf("insert expired token: %v", err)
	}
	if err := svc.ConfirmVerify(ctx, tn, plain); err != ErrInvalidToken {
		t.Fatalf("expired token: got %v, want ErrInvalidToken", err)
	}
}

func TestIntegration_ResetFlow(t *testing.T) {
	svc := requirePG(t)
	const tn = "resflow"
	createTenantSchema(t, tn)
	ctx := context.Background()

	if _, err := svc.Signup(ctx, tn, "r@x.com", "old-password-1"); err != nil {
		t.Fatalf("signup: %v", err)
	}
	if err := svc.RequestReset(ctx, tn, "r@x.com", testLinkBase); err != nil {
		t.Fatalf("request reset: %v", err)
	}
	tmpl, link := lastEmailEvent(t, tn)
	if tmpl != "reset" {
		t.Fatalf("expected reset template, got %q", tmpl)
	}
	token := tokenFromLink(t, link)

	if err := svc.ConfirmReset(ctx, tn, token, "brand-new-password"); err != nil {
		t.Fatalf("confirm reset: %v", err)
	}
	// New password works; old does not.
	if _, err := svc.Login(ctx, tn, "r@x.com", "brand-new-password"); err != nil {
		t.Fatalf("login with new password failed: %v", err)
	}
	if _, err := svc.Login(ctx, tn, "r@x.com", "old-password-1"); err != ErrInvalidCredentials {
		t.Fatalf("old password still works: %v", err)
	}
	// Token is single-use.
	if err := svc.ConfirmReset(ctx, tn, token, "yet-another-pass"); err != ErrInvalidToken {
		t.Fatalf("reused reset token: got %v, want ErrInvalidToken", err)
	}
}

func TestIntegration_ResetInvalidatesOtherTokens(t *testing.T) {
	svc := requirePG(t)
	const tn = "resinval"
	createTenantSchema(t, tn)
	ctx := context.Background()
	if _, err := svc.Signup(ctx, tn, "multi@x.com", "old-password-1"); err != nil {
		t.Fatalf("signup: %v", err)
	}
	// Two outstanding reset tokens.
	if err := svc.RequestReset(ctx, tn, "multi@x.com", testLinkBase); err != nil {
		t.Fatalf("reset 1: %v", err)
	}
	_, link1 := lastEmailEvent(t, tn)
	token1 := tokenFromLink(t, link1)
	if err := svc.RequestReset(ctx, tn, "multi@x.com", testLinkBase); err != nil {
		t.Fatalf("reset 2: %v", err)
	}
	_, link2 := lastEmailEvent(t, tn)
	token2 := tokenFromLink(t, link2)

	// Confirm with token2 → token1 must now be invalid.
	if err := svc.ConfirmReset(ctx, tn, token2, "new-password-xyz"); err != nil {
		t.Fatalf("confirm token2: %v", err)
	}
	if err := svc.ConfirmReset(ctx, tn, token1, "another-new-pass"); err != ErrInvalidToken {
		t.Fatalf("token1 not invalidated by token2's use: got %v", err)
	}
}

func TestIntegration_ResetRequestAntiEnumeration(t *testing.T) {
	svc := requirePG(t)
	const tn = "resenum"
	createTenantSchema(t, tn)
	ctx := context.Background()

	before := outboxCount(t, tn)
	// Nonexistent email: must NOT error and must NOT enqueue an email.
	if err := svc.RequestReset(ctx, tn, "ghost@x.com", testLinkBase); err != nil {
		t.Fatalf("reset for nonexistent email returned error: %v", err)
	}
	if got := outboxCount(t, tn); got != before {
		t.Fatalf("anti-enum: an email was enqueued for a nonexistent user (%d → %d)", before, got)
	}
}

// TestIntegration_ResetAsyncE2E is the first auth↔email integration: a reset
// request enqueues an outbox event that the email CONSUMER (worker.Drain +
// EmailProcessor) delivers to the SMTP sender — proving the two ecosystem pieces
// connect through the outbox.
func TestIntegration_ResetAsyncE2E(t *testing.T) {
	svc := requirePG(t)
	const tn = "resasync"
	createTenantSchema(t, tn)
	ctx := context.Background()
	if _, err := svc.Signup(ctx, tn, "async@x.com", "old-password-1"); err != nil {
		t.Fatalf("signup: %v", err)
	}
	if err := svc.RequestReset(ctx, tn, "async@x.com", testLinkBase); err != nil {
		t.Fatalf("request reset: %v", err)
	}
	_, link := lastEmailEvent(t, tn)
	token := tokenFromLink(t, link)

	sender := &recordingSender{}
	proc := consumers.NewEmailProcessor(sender, zerolog.Nop())
	// Drain the outbox: the EmailProcessor renders + "sends" each email.send row.
	if _, err := worker.Drain(ctx, testPool, proc, 50, 5, nil); err != nil {
		t.Fatalf("drain: %v", err)
	}
	mail, ok := sender.findTo("async@x.com")
	if !ok {
		t.Fatal("email consumer never delivered the reset email")
	}
	if !strings.Contains(mail.HTML, token) {
		t.Fatal("delivered email does not contain the reset link/token")
	}
	if !strings.Contains(strings.ToLower(mail.HTML), "reset") {
		t.Fatalf("delivered email is not the reset template: %s", mail.HTML)
	}
}

func TestIntegration_TokenCrossTenantIsolation(t *testing.T) {
	svc := requirePG(t)
	const tnA, tnB = "tokisoa", "tokisob"
	createTenantSchema(t, tnA)
	createTenantSchema(t, tnB)
	ctx := context.Background()
	if _, err := svc.Signup(ctx, tnA, "iso@x.com", "old-password-1"); err != nil {
		t.Fatalf("signup A: %v", err)
	}
	if _, err := svc.Signup(ctx, tnB, "iso@x.com", "old-password-1"); err != nil {
		t.Fatalf("signup B: %v", err)
	}
	if err := svc.RequestReset(ctx, tnA, "iso@x.com", testLinkBase); err != nil {
		t.Fatalf("reset A: %v", err)
	}
	_, link := lastEmailEvent(t, tnA)
	tokenA := tokenFromLink(t, link)

	// Tenant A's reset token must be useless in tenant B (tokens live in the
	// tenant's own schema — physically unreachable across tenants).
	if err := svc.ConfirmReset(ctx, tnB, tokenA, "hijacked-pass"); err != ErrInvalidToken {
		t.Fatalf("cross-tenant token accepted: got %v, want ErrInvalidToken", err)
	}
}

func TestIntegration_TokenStoredAsHashOnly(t *testing.T) {
	svc := requirePG(t)
	const tn = "tokhash"
	createTenantSchema(t, tn)
	ctx := context.Background()
	if _, err := svc.Signup(ctx, tn, "h@x.com", "old-password-1"); err != nil {
		t.Fatalf("signup: %v", err)
	}
	if err := svc.RequestReset(ctx, tn, "h@x.com", testLinkBase); err != nil {
		t.Fatalf("reset: %v", err)
	}
	_, link := lastEmailEvent(t, tn)
	plain := tokenFromLink(t, link)

	var stored string
	if err := testPool.QueryRow(ctx, fmt.Sprintf(
		`SELECT token_hash FROM %s ORDER BY created_at DESC LIMIT 1`, mustTokensTbl(t, svc, tn))).Scan(&stored); err != nil {
		t.Fatalf("read token_hash: %v", err)
	}
	if stored == plain {
		t.Fatal("the PLAIN token is stored in the DB — must be a hash")
	}
	if stored != hashToken(plain) {
		t.Fatalf("stored value is not sha256(token): %q", stored)
	}
}

func TestIntegration_RequireVerifiedBlocksLogin(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test requires Postgres")
	}
	const tn = "reqver"
	createTenantSchema(t, tn)
	ctx := context.Background()
	svc := NewService(NewStore(testPool), Config{
		JWTSecret: testSecret, SignupRole: "viewer", TokenTTL: time.Hour,
		LoginBurst: 100, RequireVerified: true,
	})
	if _, err := svc.Signup(ctx, tn, "rv@x.com", "verify-me-pass"); err != nil {
		t.Fatalf("signup: %v", err)
	}
	// Login is blocked until verified.
	if _, err := svc.Login(ctx, tn, "rv@x.com", "verify-me-pass"); err != ErrEmailNotVerified {
		t.Fatalf("unverified login: got %v, want ErrEmailNotVerified", err)
	}
	// Wrong password still returns the generic credentials error (not the verify hint).
	if _, err := svc.Login(ctx, tn, "rv@x.com", "WRONG"); err != ErrInvalidCredentials {
		t.Fatalf("wrong password under require-verified: got %v", err)
	}
	// Verify, then login succeeds.
	if err := svc.RequestVerify(ctx, tn, "rv@x.com", testLinkBase); err != nil {
		t.Fatalf("request verify: %v", err)
	}
	_, link := lastEmailEvent(t, tn)
	if err := svc.ConfirmVerify(ctx, tn, tokenFromLink(t, link)); err != nil {
		t.Fatalf("confirm verify: %v", err)
	}
	if _, err := svc.Login(ctx, tn, "rv@x.com", "verify-me-pass"); err != nil {
		t.Fatalf("verified login failed: %v", err)
	}
}

func mustTokensTbl(t *testing.T, svc *Service, tenantID string) string {
	t.Helper()
	tbl, err := svc.store.tokensTbl(tenantID)
	if err != nil {
		t.Fatalf("tokens table: %v", err)
	}
	return tbl
}

// recordingSender implements consumers.EmailSender, capturing every send.
type recordingSender struct {
	mu   sync.Mutex
	sent []consumers.Email
}

func (s *recordingSender) Send(_ context.Context, e consumers.Email) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, e)
	return nil
}

func (s *recordingSender) findTo(to string) (consumers.Email, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.sent {
		if e.To == to {
			return e, true
		}
	}
	return consumers.Email{}, false
}
