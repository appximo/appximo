package integration_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/miguelangel/appitools/pkg/auth"
	"github.com/miguelangel/appitools/pkg/cache"
	"github.com/miguelangel/appitools/pkg/codegen"
	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/db"
	"github.com/miguelangel/appitools/pkg/extensions"
	rbacpkg "github.com/miguelangel/appitools/pkg/rbac"
	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/pkg/tenant"
)

// updSchema is a logistics-style schema exercising the update paths: guides has a
// UNIQUE+required field (code), an enum (status), a numeric field (weight_kg), an
// owner (operator_id) for the row-level RBAC condition, and BOTH auto timestamps
// so the updated_at=NOW() behaviour is covered.
func updSchema() *schema.APISchema {
	return &schema.APISchema{
		Schema:  "https://appitools.dev/schema/v1",
		Version: "1",
		Name:    "logistics-upd",
		Resources: map[string]schema.ResourceSchema{
			"guides": {
				Fields: map[string]schema.FieldDef{
					"code":        {Type: "string", Required: true, Unique: true},
					"status":      {Type: "string", Enum: []string{"pending", "delivered", "cancelled"}},
					"weight_kg":   {Type: "float64"},
					"operator_id": {Type: "uuid"},
					"created_at":  {Type: "time", Auto: true},
					"updated_at":  {Type: "time", Auto: true},
				},
			},
		},
		RBAC: schema.RBACPolicy{
			Roles: map[string]schema.RolePolicy{
				"super_admin": {
					Resources: json.RawMessage(`"*"`),
					Actions:   []string{"*"},
				},
				"operario": {
					Resources:  json.RawMessage(`["guides"]`),
					Actions:    []string{"read", "create", "update"},
					Conditions: &schema.Condition{Field: "operator_id", Op: "eq", Val: "$user_id"},
				},
				"public": {
					Resources: json.RawMessage(`["guides"]`),
					Actions:   []string{"read"},
				},
			},
		},
	}
}

// setupUpd registers a fresh tenant with updSchema and returns a running data-plane
// server (no response cache — uses buildDP from e2e_test.go) plus a super_admin token.
func setupUpd(t *testing.T) (*httptest.Server, string, func()) {
	t.Helper()
	if testing.Short() {
		t.Skip("update: skipping in -short mode")
	}
	pool, cleanPG := startPG(t)
	applyControlPlane(t, pool)
	s := updSchema()
	if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID: tenantID, DisplayName: "Upd Co", Email: "u@u.com", Plan: "free", Schema: s,
	}); err != nil {
		cleanPG()
		t.Fatalf("register tenant: %v", err)
	}
	srv := httptest.NewServer(buildDP(s, pool, tenantID+".localhost"))
	return srv, genToken("super_admin", superID), func() { srv.Close(); cleanPG() }
}

// newGuide creates a guide as super_admin and returns its id.
func newGuide(t *testing.T, srv *httptest.Server, tok string, body map[string]any) string {
	t.Helper()
	g := dpDo(t, srv, "POST", "/api/guides", tok, body, http.StatusCreated)
	id, _ := g["id"].(string)
	if id == "" {
		t.Fatal("create guide: no id in response")
	}
	return id
}

func TestPUTHandler(t *testing.T) {
	srv, super, done := setupUpd(t)
	defer done()

	t.Run("happy path: full replace; omitted optional → NULL", func(t *testing.T) {
		id := newGuide(t, srv, super, map[string]any{"code": "PUT-1", "status": "pending", "weight_kg": 1.5})
		got := dpDo(t, srv, "PUT", "/api/guides/"+id, super, map[string]any{
			"code": "PUT-1B", "operator_id": operario1ID, // status & weight_kg omitted → NULL
		}, http.StatusOK)
		if got["code"] != "PUT-1B" {
			t.Errorf("code not replaced: %v", got["code"])
		}
		if got["status"] != nil {
			t.Errorf("omitted optional status should be NULL, got %v", got["status"])
		}
		if got["weight_kg"] != nil {
			t.Errorf("omitted optional weight_kg should be NULL, got %v", got["weight_kg"])
		}
	})

	t.Run("missing required field → 422", func(t *testing.T) {
		id := newGuide(t, srv, super, map[string]any{"code": "PUT-REQ"})
		if got := dpStatus(t, srv, "PUT", "/api/guides/"+id, super, map[string]any{"status": "delivered"}); got != 422 {
			t.Fatalf("expected 422, got %d", got)
		}
	})

	t.Run("unknown field → 422", func(t *testing.T) {
		id := newGuide(t, srv, super, map[string]any{"code": "PUT-EXTRA"})
		if got := dpStatus(t, srv, "PUT", "/api/guides/"+id, super, map[string]any{"code": "x", "bogus": "y"}); got != 422 {
			t.Fatalf("expected 422, got %d", got)
		}
	})

	t.Run("id or created_at in body → 422", func(t *testing.T) {
		id := newGuide(t, srv, super, map[string]any{"code": "PUT-IMMUT"})
		if got := dpStatus(t, srv, "PUT", "/api/guides/"+id, super, map[string]any{"code": "x", "id": superID}); got != 422 {
			t.Fatalf("id in body: expected 422, got %d", got)
		}
		if got := dpStatus(t, srv, "PUT", "/api/guides/"+id, super, map[string]any{"code": "x", "created_at": "2020-01-01T00:00:00Z"}); got != 422 {
			t.Fatalf("created_at in body: expected 422, got %d", got)
		}
	})

	t.Run("nonexistent record → 404", func(t *testing.T) {
		if got := dpStatus(t, srv, "PUT", "/api/guides/00000000-0000-0000-0000-0000000000ff", super, map[string]any{"code": "x"}); got != 404 {
			t.Fatalf("expected 404, got %d", got)
		}
	})

	t.Run("no auth → 401", func(t *testing.T) {
		id := newGuide(t, srv, super, map[string]any{"code": "PUT-401"})
		if got := dpStatus(t, srv, "PUT", "/api/guides/"+id, "", map[string]any{"code": "x"}); got != 401 {
			t.Fatalf("expected 401, got %d", got)
		}
	})

	t.Run("role without update permission → 403", func(t *testing.T) {
		id := newGuide(t, srv, super, map[string]any{"code": "PUT-403"})
		pub := genToken("public", "")
		if got := dpStatus(t, srv, "PUT", "/api/guides/"+id, pub, map[string]any{"code": "x"}); got != 403 {
			t.Fatalf("expected 403, got %d", got)
		}
	})

	t.Run("unique constraint violation → 409", func(t *testing.T) {
		newGuide(t, srv, super, map[string]any{"code": "DUP-A"})
		idB := newGuide(t, srv, super, map[string]any{"code": "DUP-B"})
		got := dpDo(t, srv, "PUT", "/api/guides/"+idB, super, map[string]any{"code": "DUP-A"}, http.StatusConflict)
		if msg, _ := got["error"].(string); !strings.Contains(msg, "code") {
			t.Errorf("409 message should name the field 'code', got %q", msg)
		}
	})

	t.Run("body > 1MB → 413", func(t *testing.T) {
		id := newGuide(t, srv, super, map[string]any{"code": "PUT-BIG"})
		big := strings.Repeat("x", (1<<20)+512)
		if got := dpStatus(t, srv, "PUT", "/api/guides/"+id, super, map[string]any{"code": big}); got != http.StatusRequestEntityTooLarge {
			t.Fatalf("expected 413, got %d", got)
		}
	})
}

func TestPATCHHandler(t *testing.T) {
	srv, super, done := setupUpd(t)
	defer done()

	t.Run("happy path: one field; rest intact", func(t *testing.T) {
		id := newGuide(t, srv, super, map[string]any{"code": "PA-1", "status": "pending", "weight_kg": 1.5})
		got := dpDo(t, srv, "PATCH", "/api/guides/"+id, super, map[string]any{"status": "delivered"}, http.StatusOK)
		if got["status"] != "delivered" {
			t.Errorf("status not patched: %v", got["status"])
		}
		if got["code"] != "PA-1" {
			t.Errorf("code should be intact, got %v", got["code"])
		}
		if got["weight_kg"] != 1.5 {
			t.Errorf("weight_kg should be intact (1.5), got %v", got["weight_kg"])
		}
	})

	t.Run("happy path: multiple fields", func(t *testing.T) {
		id := newGuide(t, srv, super, map[string]any{"code": "PA-MULTI", "status": "pending"})
		got := dpDo(t, srv, "PATCH", "/api/guides/"+id, super, map[string]any{"status": "cancelled", "weight_kg": 9.25}, http.StatusOK)
		if got["status"] != "cancelled" || got["weight_kg"] != 9.25 {
			t.Errorf("multi-field patch failed: %v / %v", got["status"], got["weight_kg"])
		}
	})

	t.Run("invalid field → 422", func(t *testing.T) {
		id := newGuide(t, srv, super, map[string]any{"code": "PA-BAD"})
		if got := dpStatus(t, srv, "PATCH", "/api/guides/"+id, super, map[string]any{"nope": 1}); got != 422 {
			t.Fatalf("expected 422, got %d", got)
		}
	})

	t.Run("invalid enum value → 422", func(t *testing.T) {
		id := newGuide(t, srv, super, map[string]any{"code": "PA-ENUM"})
		if got := dpStatus(t, srv, "PATCH", "/api/guides/"+id, super, map[string]any{"status": "not-a-status"}); got != 422 {
			t.Fatalf("expected 422, got %d", got)
		}
	})

	t.Run("null on optional field → 200 (NULL)", func(t *testing.T) {
		id := newGuide(t, srv, super, map[string]any{"code": "PA-NULLOPT", "status": "pending"})
		got := dpDo(t, srv, "PATCH", "/api/guides/"+id, super, map[string]any{"status": nil}, http.StatusOK)
		if got["status"] != nil {
			t.Errorf("status should be NULL, got %v", got["status"])
		}
	})

	t.Run("null on required field → 422", func(t *testing.T) {
		id := newGuide(t, srv, super, map[string]any{"code": "PA-NULLREQ"})
		if got := dpStatus(t, srv, "PATCH", "/api/guides/"+id, super, map[string]any{"code": nil}); got != 422 {
			t.Fatalf("expected 422, got %d", got)
		}
	})

	t.Run("nonexistent record → 404", func(t *testing.T) {
		if got := dpStatus(t, srv, "PATCH", "/api/guides/00000000-0000-0000-0000-0000000000ee", super, map[string]any{"status": "delivered"}); got != 404 {
			t.Fatalf("expected 404, got %d", got)
		}
	})

	// Row-level RBAC: a row owned by another principal is invisible → 404 (NOT 403).
	// This matches the GET-by-id/DELETE pattern and the S33/S34 BOLA fixes, which
	// deliberately do not reveal that another principal's row exists. (The brief's
	// "403 (no revelar que existe)" is self-contradictory; 404 is the secure form.)
	t.Run("RBAC condition: cannot patch another operator's row → 404", func(t *testing.T) {
		id := newGuide(t, srv, super, map[string]any{"code": "PA-OWNED", "operator_id": operario1ID})
		op1 := genToken("operario", operario1ID)
		op2 := genToken("operario", operario2ID)
		// Owner can patch.
		dpDo(t, srv, "PATCH", "/api/guides/"+id, op1, map[string]any{"status": "delivered"}, http.StatusOK)
		// Non-owner gets 404, not 403.
		if got := dpStatus(t, srv, "PATCH", "/api/guides/"+id, op2, map[string]any{"status": "cancelled"}); got != 404 {
			t.Fatalf("non-owner patch: expected 404, got %d", got)
		}
	})

	t.Run("updated_at advances past the pre-PATCH value", func(t *testing.T) {
		created := dpDo(t, srv, "POST", "/api/guides", super, map[string]any{"code": "PA-TS"}, http.StatusCreated)
		before, err := time.Parse(time.RFC3339, asString(created["updated_at"]))
		if err != nil {
			t.Fatalf("parse pre-patch updated_at %q: %v", created["updated_at"], err)
		}
		id, _ := created["id"].(string)
		time.Sleep(15 * time.Millisecond)
		patched := dpDo(t, srv, "PATCH", "/api/guides/"+id, super, map[string]any{"status": "delivered"}, http.StatusOK)
		after, err := time.Parse(time.RFC3339, asString(patched["updated_at"]))
		if err != nil {
			t.Fatalf("parse post-patch updated_at %q: %v", patched["updated_at"], err)
		}
		if !after.After(before) {
			t.Errorf("updated_at did not advance: before=%v after=%v", before, after)
		}
	})
}

// asString coerces a decoded JSON value to its string form for time parsing.
func asString(v any) string {
	s, _ := v.(string)
	return s
}

// TestPATCHInvalidatesCache proves the response-cache eviction has real effect: with
// a long TTL, a GET cached before the PATCH would return stale data — but the PATCH
// invalidates the tenant's cache, so the next GET reflects the write immediately.
func TestPATCHInvalidatesCache(t *testing.T) {
	if testing.Short() {
		t.Skip("update: skipping in -short mode")
	}
	pool, cleanPG := startPG(t)
	defer cleanPG()
	applyControlPlane(t, pool)
	s := updSchema()
	if _, err := controlplane.RegisterTenant(context.Background(), pool, controlplane.RegisterRequest{
		TenantID: tenantID, DisplayName: "Cache Co", Email: "c@c.com", Plan: "free", Schema: s,
	}); err != nil {
		t.Fatalf("register tenant: %v", err)
	}

	// Data plane with the response cache wired (30s TTL → stale data would persist
	// well past the test if eviction did not happen) and passed as the invalidator.
	rc := cache.New(30 * time.Second)
	policyJSON, _ := json.Marshal(s.RBAC)
	tdb := db.NewTenantDB(pool)
	hr := extensions.NewHookRunner(extensions.NewJSSandbox())
	root := chi.NewMux()
	root.Use(tenant.TenantMiddleware)
	root.Use(rc.Middleware) // before JWT, exactly like cmd_serve
	root.Use(auth.JWTMiddleware(jwtSecret))
	root.Use(rbacpkg.RBACMiddleware(policyJSON))
	root.Mount("/", codegen.BuildRouter(s, tdb, hr, rc, nil))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.Host = tenantID + ".localhost"
		root.ServeHTTP(w, req)
	}))
	defer srv.Close()

	super := genToken("super_admin", superID)
	id := newGuide(t, srv, super, map[string]any{"code": "CACHE-1", "status": "pending"})

	// Warm the cache: two GETs (first populates, second is a HIT).
	if got := dpDo(t, srv, "GET", "/api/guides/"+id, super, nil, http.StatusOK); got["status"] != "pending" {
		t.Fatalf("warm GET: expected pending, got %v", got["status"])
	}
	dpDo(t, srv, "GET", "/api/guides/"+id, super, nil, http.StatusOK)

	// Mutate; the handler must invalidate the cache.
	dpDo(t, srv, "PATCH", "/api/guides/"+id, super, map[string]any{"status": "delivered"}, http.StatusOK)

	// With a 30s TTL, a non-invalidated cache would still serve "pending" here.
	fresh := dpDo(t, srv, "GET", "/api/guides/"+id, super, nil, http.StatusOK)
	if fresh["status"] != "delivered" {
		t.Fatalf("post-PATCH GET should be fresh (delivered), got %v — cache not invalidated", fresh["status"])
	}
}
