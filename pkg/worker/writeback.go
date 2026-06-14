package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/zerolog"

	"github.com/miguelangel/appitools/pkg/auth"
)

// DefaultServiceTokenTTL is how long a worker's service JWT is valid. Kept SHORT
// (60s, minted per operation) so there is no long-lived credential to leak —
// ADR-016 §Class 2 write-back.
const DefaultServiceTokenTTL = 60 * time.Second

// EngineClient lets the worker write BACK to the engine through its HTTP API
// (not the tenant DB directly) so the write inherits the engine's validation +
// RBAC + the same path any client takes. For each call it mints a fresh,
// short-lived, SCOPED service JWT (the shared JWT_SECRET, a tenant-scoped
// service role — NOT admin) and sets the tenant Host header. The engine resolves
// the tenant from the Host subdomain and enforces the service role's RBAC.
type EngineClient struct {
	baseURL      string // TCP target, e.g. http://localhost:8080 (the engine data plane)
	tenantDomain string // Host = "{tenant}.{tenantDomain}", e.g. "localhost" → acme.localhost
	jwtSecret    string // shared with the engine; signs the service JWT
	role         string // scoped service role, e.g. "service_worker" (NEVER admin)
	ttl          time.Duration
	http         *http.Client
}

// NewEngineClient builds a client. role MUST be a scoped service role defined in
// the schema RBAC (minimal actions/resources), never an admin role. Zero ttl →
// DefaultServiceTokenTTL.
func NewEngineClient(baseURL, tenantDomain, jwtSecret, role string, ttl time.Duration) *EngineClient {
	if ttl <= 0 {
		ttl = DefaultServiceTokenTTL
	}
	if tenantDomain == "" {
		tenantDomain = "localhost"
	}
	return &EngineClient{
		baseURL:      strings.TrimRight(baseURL, "/"),
		tenantDomain: tenantDomain,
		jwtSecret:    jwtSecret,
		role:         role,
		ttl:          ttl,
		http:         &http.Client{Timeout: 10 * time.Second},
	}
}

// mintToken issues a fresh short-lived service JWT for tenant, in the EXACT
// claims shape the engine's auth.ValidateToken accepts (HS256, exp required).
// sub/user_id = "service:worker" for audit; role = the scoped service role;
// tenant_id = the event's tenant. A NEW token per call — never cached/reused.
func (c *EngineClient) mintToken(tenant string) (string, error) {
	return auth.GenerateTokenWithTTL(auth.Claims{
		UserID:           "service:worker",
		Role:             c.role,
		TenantID:         tenant,
		RegisteredClaims: jwt.RegisteredClaims{Subject: "service:worker"},
	}, c.jwtSecret, c.ttl)
}

// Do performs an authenticated request to the engine API for tenant. body is
// JSON-encoded when non-nil. It returns the HTTP status and response body; a
// transport error (not an HTTP error status) returns a non-nil error. The caller
// decides what a non-2xx means — for the outbox, a non-2xx must NOT mark the row
// sent (it is retried, at-least-once).
func (c *EngineClient) Do(ctx context.Context, tenant, method, path string, body any) (int, []byte, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, fmt.Errorf("worker: marshal writeback body: %w", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, rdr)
	if err != nil {
		return 0, nil, fmt.Errorf("worker: build writeback request: %w", err)
	}
	token, err := c.mintToken(tenant)
	if err != nil {
		return 0, nil, fmt.Errorf("worker: mint service token: %w", err)
	}
	// Tenant comes from the Host subdomain (a naming convention; the engine does
	// no tenant-table lookup). The TCP target is baseURL; the Host header carries
	// the tenant — the same split the engine's own tests use.
	req.Host = tenant + "." + c.tenantDomain
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("worker: writeback request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, respBody, nil
}

// WritebackProcessor is the SERVICE-JWT-V1 demonstration consumer: on a
// "{resource}.created" outbox event it mints a scoped service JWT and PATCHes the
// created row's status back through the engine API — proving the authenticated
// write-back chain (event → worker → mint JWT → engine API → RBAC accepts the
// scoped role → row marked sent). It is NOT real business logic; a real consumer
// (XLSX/email) replaces the side-effect and dedups on Row.ID (idempotency key).
//
// Idempotency: setting status to a fixed value is naturally idempotent, which is
// what at-least-once delivery requires. Any non-2xx (or transport error) returns
// an error so the row stays pending and is retried — never marked sent on failure.
type WritebackProcessor struct {
	client      *EngineClient
	statusValue string // PATCH {status: statusValue} on the created row
	log         zerolog.Logger
}

// NewWritebackProcessor builds the demo consumer. statusValue defaults to "done".
func NewWritebackProcessor(client *EngineClient, statusValue string, log zerolog.Logger) *WritebackProcessor {
	if statusValue == "" {
		statusValue = "done"
	}
	return &WritebackProcessor{client: client, statusValue: statusValue, log: log}
}

// crudEvent is the lean payload the generated CRUD path emits (CRUD-EMIT-V1).
type crudEvent struct {
	ID       string `json:"id"`
	TenantID string `json:"tenant_id"`
	Resource string `json:"resource"`
	Action   string `json:"action"`
}

// Process implements Processor. Non-".created" topics are acked (logged) so the
// demo never loops or blocks the queue.
func (p *WritebackProcessor) Process(ctx context.Context, row Row) error {
	if !strings.HasSuffix(row.Topic, ".created") {
		p.log.Debug().Int64("id", row.ID).Str("topic", row.Topic).Msg("writeback: ignoring non-created event")
		return nil
	}
	var ev crudEvent
	if err := json.Unmarshal(row.Payload, &ev); err != nil {
		return fmt.Errorf("worker: decode crud event id=%d: %w", row.ID, err)
	}
	if ev.Resource == "" || ev.ID == "" {
		return fmt.Errorf("worker: crud event id=%d missing resource/id: %s", row.ID, string(row.Payload))
	}

	// Write back via the engine API (tenant from the ROW, not the payload, so the
	// token tenant always matches the owning tenant). PATCH is partial — only the
	// status field — which a scoped service_worker role is allowed to set.
	path := "/api/" + ev.Resource + "/" + ev.ID
	status, respBody, err := p.client.Do(ctx, row.TenantID, http.MethodPatch, path,
		map[string]any{"status": p.statusValue})
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("worker: writeback PATCH %s → %d: %s", path, status, string(respBody))
	}
	p.log.Info().
		Int64("id", row.ID).
		Str("tenant_id", row.TenantID).
		Str("topic", row.Topic).
		Str("writeback", path).
		Int("status", status).
		Msg("worker: authenticated write-back ok")
	return nil
}
