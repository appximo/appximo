package integration_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/controlplane"
	"github.com/appximo/appximo/pkg/outbox"
	"github.com/appximo/appximo/pkg/schema"
	"github.com/jackc/pgx/v5/pgxpool"
)

// genTokenFor mints a JWT whose tenant claim is tid (genToken uses the fixed
// tenantID; the tenant-scope test needs a token for a second tenant).
func genTokenFor(role, userID, tid string) string {
	tok, _ := auth.GenerateToken(auth.Claims{UserID: userID, Role: role, TenantID: tid}, jwtSecret)
	return tok
}

// G4 (FIX-G4): atomic multi-resource transactions — POST /api/transaction runs N
// create/update/delete operations in ONE Postgres transaction, all-or-nothing, each
// op authorized (per-resource RBAC, G2) and validated exactly like its single-op
// counterpart. These tests prove: a transfer commits atomically, a failing op rolls
// the WHOLE batch back (no partial state), RBAC denies an unauthorized op (nothing
// applies), the guard gives compare-and-set, the op limit is enforced, and the tx
// never crosses tenants.

const (
	txAcct1 = "aaaa1111-1111-1111-1111-111111111111"
	txAcct2 = "bbbb2222-2222-2222-2222-222222222222"
)

func txSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema:  "https://appximo.com/schema/v1",
		Version: "1",
		Name:    "tx-lab",
		Resources: map[string]schema.ResourceSchema{
			"accounts": {Fields: map[string]schema.FieldDef{
				"owner_id": {Type: "uuid"},
				"balance":  {Type: "float64"},
			}},
			"ledger": {
				Fields: map[string]schema.FieldDef{
					"account_id": {Type: "uuid", Required: true},
					"amount":     {Type: "float64", Required: true},
					"ref":        {Type: "string", Required: true, Unique: true},
					"created_at": {Type: "time", Auto: schema.AutoLegacy},
				},
				Events: []string{"create"}, // emit in the SAME tx — must roll back with the batch
			},
			"products": {Fields: map[string]schema.FieldDef{
				"sku":   {Type: "string", Required: true, Unique: true},
				"stock": {Type: "int", Min: f64(0)},
			}},
		},
		RBAC: schema.RBACPolicy{Roles: map[string]schema.RolePolicy{
			"super_admin": {Resources: json.RawMessage(`"*"`), Actions: []string{"*"}},
			// clerk (per-resource G2): create/read ledger, read accounts, read/update
			// products — but NO delete anywhere and NO create on accounts/products.
			"clerk": {Permissions: map[string]schema.ResourcePermission{
				"ledger":   {Actions: []string{"read", "create"}},
				"accounts": {Actions: []string{"read"}},
				"products": {Actions: []string{"read", "update"}},
			}},
		}},
	}
}

func f64(v float64) *float64 { return &v }

func setupTx(t *testing.T) (*httptest.Server, *pgxpool.Pool, func(role, uid string) string, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("transaction: skipping in -short mode")
	}
	pool, cleanPG := startPG(t)
	applyControlPlane(t, pool)
	if err := outbox.EnsureTable(context.Background(), pool); err != nil {
		cleanPG()
		t.Fatalf("ensure outbox: %v", err)
	}
	s := txSchema()
	if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID: tenantID, DisplayName: "Tx Co", Email: "t@t.com", Plan: "free", Schema: s,
	}); err != nil {
		cleanPG()
		t.Fatalf("register tenant: %v", err)
	}
	srv := httptest.NewServer(buildDP(s, pool, tenantID+".localhost"))
	return srv, pool, genToken, func() { srv.Close(); cleanPG() }
}

// countRows returns COUNT(*) of a tenant table (direct, bypassing the API).
func countRows(t *testing.T, pool *pgxpool.Pool, table string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM tenant_"+tenantID+"."+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func txOutboxCount(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM public.outbox").Scan(&n); err != nil {
		t.Fatalf("count outbox: %v", err)
	}
	return n
}

func batch(ops ...map[string]any) map[string]any { return map[string]any{"operations": ops} }

// TestTx_AtomicTransferCommits: a two-leg transfer commits atomically and emits both
// outbox events in the SAME transaction.
func TestTx_AtomicTransferCommits(t *testing.T) {
	srv, pool, tok, clean := setupTx(t)
	defer clean()
	admin := tok("super_admin", superID)

	obBefore := txOutboxCount(t, pool)
	out := dpDo(t, srv, "POST", "/api/transaction", admin, batch(
		map[string]any{"op": "create", "resource": "ledger", "data": map[string]any{"account_id": txAcct1, "amount": -100, "ref": "xfer-1-debit"}},
		map[string]any{"op": "create", "resource": "ledger", "data": map[string]any{"account_id": txAcct2, "amount": 100, "ref": "xfer-1-credit"}},
	), 200)

	results, _ := out["results"].([]any)
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %v", out["results"])
	}
	if got := countRows(t, pool, "ledger"); got != 2 {
		t.Fatalf("expected 2 ledger rows, got %d", got)
	}
	if got := txOutboxCount(t, pool) - obBefore; got != 2 {
		t.Fatalf("expected 2 outbox events emitted in-tx, got %d", got)
	}
}

// TestTx_RollbackLeavesNoPartialState: when op 1 fails (unique violation in Phase 2),
// op 0's already-executed INSERT is rolled back — THE atomicity guarantee — and no
// outbox event survives either.
func TestTx_RollbackLeavesNoPartialState(t *testing.T) {
	srv, pool, tok, clean := setupTx(t)
	defer clean()
	admin := tok("super_admin", superID)

	// Seed a row whose ref the batch will collide with.
	dpDo(t, srv, "POST", "/api/ledger", admin, map[string]any{"account_id": txAcct1, "amount": 1, "ref": "dup-ref"}, 201)
	rowsBefore := countRows(t, pool, "ledger")
	obBefore := txOutboxCount(t, pool)

	st, body := dpStatusBody(t, srv, "POST", "/api/transaction", admin, batch(
		map[string]any{"op": "create", "resource": "ledger", "data": map[string]any{"account_id": txAcct1, "amount": -5, "ref": "fresh-would-be-rolled-back"}},
		map[string]any{"op": "create", "resource": "ledger", "data": map[string]any{"account_id": txAcct2, "amount": 5, "ref": "dup-ref"}}, // collides → 409
	))
	if st != 409 {
		t.Fatalf("expected 409, got %d — %s", st, body)
	}
	var er map[string]any
	json.Unmarshal([]byte(body), &er)
	if er["failed_operation"] != float64(1) {
		t.Fatalf("expected failed_operation=1, got %v — %s", er["failed_operation"], body)
	}
	// The first op's row must NOT exist (rolled back), and no event leaked.
	if got := countRows(t, pool, "ledger"); got != rowsBefore {
		t.Fatalf("ROLLBACK FAILED: ledger went %d→%d (partial state persisted)", rowsBefore, got)
	}
	if got := txOutboxCount(t, pool); got != obBefore {
		t.Fatalf("ROLLBACK FAILED: outbox went %d→%d (event leaked)", obBefore, got)
	}
}

// TestTx_RBACForbiddenInBatch: a clerk batch with one forbidden op (delete, which the
// clerk's per-resource policy never grants) fails 403 and applies NOTHING.
func TestTx_RBACForbiddenInBatch(t *testing.T) {
	srv, pool, tok, clean := setupTx(t)
	defer clean()
	admin := tok("super_admin", superID)
	clerk := tok("clerk", txAcct1)

	// A product the forbidden delete will name.
	prod := dpDo(t, srv, "POST", "/api/products", admin, map[string]any{"sku": "rbac-sku", "stock": 5}, 201)
	pid, _ := prod["id"].(string)
	rowsBefore := countRows(t, pool, "ledger")

	st, body := dpStatusBody(t, srv, "POST", "/api/transaction", clerk, batch(
		map[string]any{"op": "create", "resource": "ledger", "data": map[string]any{"account_id": txAcct1, "amount": 9, "ref": "rbac-ledger"}},
		map[string]any{"op": "delete", "resource": "products", "id": pid}, // clerk may NOT delete products
	))
	if st != 403 {
		t.Fatalf("expected 403, got %d — %s", st, body)
	}
	var er map[string]any
	json.Unmarshal([]byte(body), &er)
	if er["failed_operation"] != float64(1) {
		t.Fatalf("expected failed_operation=1, got %v", er["failed_operation"])
	}
	if got := countRows(t, pool, "ledger"); got != rowsBefore {
		t.Fatalf("forbidden batch wrote a row: ledger %d→%d", rowsBefore, got)
	}
}

// TestTx_GuardCompareAndSet: the guard is the optimistic-lock tool — an update applies
// only if the guarded value still holds; a stale guard matches no row → 409/404 and
// changes nothing.
func TestTx_GuardCompareAndSet(t *testing.T) {
	srv, pool, tok, clean := setupTx(t)
	defer clean()
	admin := tok("super_admin", superID)

	prod := dpDo(t, srv, "POST", "/api/products", admin, map[string]any{"sku": "cas-sku", "stock": 10}, 201)
	pid, _ := prod["id"].(string)

	// Guard passes (stock is 10) → applies.
	dpDo(t, srv, "POST", "/api/transaction", admin, batch(
		map[string]any{"op": "update", "resource": "products", "id": pid, "data": map[string]any{"stock": 5},
			"guard": []map[string]any{{"field": "stock", "op": "eq", "value": 10}}},
	), 200)
	if got := productStock(t, pool, pid); got != 5 {
		t.Fatalf("after CAS, stock=%d want 5", got)
	}

	// Guard now stale (stock is 5, not 10) → no row matches → 404, unchanged.
	st, _ := dpStatusBody(t, srv, "POST", "/api/transaction", admin, batch(
		map[string]any{"op": "update", "resource": "products", "id": pid, "data": map[string]any{"stock": 0},
			"guard": []map[string]any{{"field": "stock", "op": "eq", "value": 10}}},
	))
	if st != 404 {
		t.Fatalf("stale guard: expected 404, got %d", st)
	}
	if got := productStock(t, pool, pid); got != 5 {
		t.Fatalf("stale-guard update changed stock to %d (should stay 5)", got)
	}
}

// TestTx_GuardFailRollsBackSiblings: a guard failure on op 1 rolls back op 0's write.
func TestTx_GuardFailRollsBackSiblings(t *testing.T) {
	srv, pool, tok, clean := setupTx(t)
	defer clean()
	admin := tok("super_admin", superID)

	prod := dpDo(t, srv, "POST", "/api/products", admin, map[string]any{"sku": "gfr-sku", "stock": 10}, 201)
	pid, _ := prod["id"].(string)
	rowsBefore := countRows(t, pool, "ledger")

	st, _ := dpStatusBody(t, srv, "POST", "/api/transaction", admin, batch(
		map[string]any{"op": "create", "resource": "ledger", "data": map[string]any{"account_id": txAcct1, "amount": 1, "ref": "gfr-ledger"}},
		map[string]any{"op": "update", "resource": "products", "id": pid, "data": map[string]any{"stock": 0},
			"guard": []map[string]any{{"field": "stock", "op": "eq", "value": 999}}}, // never matches → fails
	))
	if st != 404 {
		t.Fatalf("expected 404 (guard), got %d", st)
	}
	if got := countRows(t, pool, "ledger"); got != rowsBefore {
		t.Fatalf("guard rollback failed: ledger %d→%d", rowsBefore, got)
	}
}

// TestTx_LimitExceeded: more than the op limit → 400 before anything runs.
func TestTx_LimitExceeded(t *testing.T) {
	srv, _, tok, clean := setupTx(t)
	defer clean()
	admin := tok("super_admin", superID)

	ops := make([]map[string]any, 101) // DefaultMaxTxOps is 100
	for i := range ops {
		ops[i] = map[string]any{"op": "create", "resource": "ledger", "data": map[string]any{"account_id": txAcct1, "amount": 1, "ref": "x"}}
	}
	st, body := dpStatusBody(t, srv, "POST", "/api/transaction", admin, map[string]any{"operations": ops})
	if st != 400 {
		t.Fatalf("expected 400 for over-limit batch, got %d — %s", st, body)
	}
}

// TestTx_BadShape: unknown op and unknown resource both 400, nothing applied.
func TestTx_BadShape(t *testing.T) {
	srv, _, tok, clean := setupTx(t)
	defer clean()
	admin := tok("super_admin", superID)

	if st := dpStatus(t, srv, "POST", "/api/transaction", admin, batch(
		map[string]any{"op": "frobnicate", "resource": "ledger", "data": map[string]any{}},
	)); st != 400 {
		t.Fatalf("unknown op: expected 400, got %d", st)
	}
	if st := dpStatus(t, srv, "POST", "/api/transaction", admin, batch(
		map[string]any{"op": "create", "resource": "ghost", "data": map[string]any{"x": 1}},
	)); st != 400 {
		t.Fatalf("unknown resource: expected 400, got %d", st)
	}
	if st := dpStatus(t, srv, "POST", "/api/transaction", admin, map[string]any{"operations": []any{}}); st != 400 {
		t.Fatalf("empty operations: expected 400, got %d", st)
	}
}

// TestTx_TenantScope: a transaction runs only in its own tenant's schema.
func TestTx_TenantScope(t *testing.T) {
	if testing.Short() {
		t.Skip("transaction: skipping in -short mode")
	}
	pool, cleanPG := startPG(t)
	defer cleanPG()
	applyControlPlane(t, pool)
	if err := outbox.EnsureTable(context.Background(), pool); err != nil {
		t.Fatalf("outbox: %v", err)
	}
	s := txSchema()
	for _, tid := range []string{tenantID, "othertenant"} {
		if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
			TenantID: tid, DisplayName: tid, Email: tid + "@t.com", Plan: "free", Schema: s,
		}); err != nil {
			t.Fatalf("register %s: %v", tid, err)
		}
	}
	srvB := httptest.NewServer(buildDP(s, pool, "othertenant.localhost"))
	defer srvB.Close()

	// Token for tenant B (TenantID must match the Host) — genToken uses tenantID, so
	// build B's token explicitly.
	tokB := genTokenFor("super_admin", superID, "othertenant")
	dpDo(t, srvB, "POST", "/api/transaction", tokB, batch(
		map[string]any{"op": "create", "resource": "ledger", "data": map[string]any{"account_id": txAcct1, "amount": 7, "ref": "b-only"}},
	), 200)

	var nA int
	pool.QueryRow(context.Background(), "SELECT count(*) FROM tenant_"+tenantID+".ledger").Scan(&nA)
	if nA != 0 {
		t.Fatalf("tenant A saw %d ledger rows from tenant B's transaction (cross-tenant leak)", nA)
	}
	var nB int
	pool.QueryRow(context.Background(), "SELECT count(*) FROM tenant_othertenant.ledger").Scan(&nB)
	if nB != 1 {
		t.Fatalf("tenant B expected 1 ledger row, got %d", nB)
	}
}

func productStock(t *testing.T, pool *pgxpool.Pool, id string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(context.Background(), "SELECT stock FROM tenant_"+tenantID+".products WHERE id=$1", id).Scan(&n); err != nil {
		t.Fatalf("product stock: %v", err)
	}
	return n
}
