package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/appximo/appximo"
	"github.com/appximo/appximo/pkg/schema"
)

// The starter `up` writes must be the real, valid quickstart schema — the
// embed guarantees byte identity with examples/quickstart/schema.json; this
// pins that it stays LOADABLE (a broken starter would brick every first run).
func TestStarterSchemaIsValid(t *testing.T) {
	s, err := schema.LoadFromBytes(appximo.StarterSchema())
	if err != nil {
		t.Fatalf("starter schema does not load: %v", err)
	}
	if _, ok := s.Resources["tasks"]; !ok {
		t.Fatalf("starter lost its tasks resource")
	}
	if pickRole(s) != "admin" {
		t.Fatalf("pickRole(starter) = %q, want admin", pickRole(s))
	}
}

func TestNameFromIdea(t *testing.T) {
	cases := map[string]string{
		"reservas de clases de un gimnasio": "reservas",
		"a lending library for schools":     "lending",
		"un CRM":                            "crm",
		"de la 42":                          "app", // nothing salvageable ≥3 chars
	}
	for idea, want := range cases {
		if got := nameFromIdea(idea); got != want {
			t.Errorf("nameFromIdea(%q) = %q, want %q", idea, got, want)
		}
	}
}

// ensureEnvFile must APPEND missing keys without rewriting existing lines, and
// never downgrade the file's permissions.
func TestEnsureEnvFileMergesWithoutRewriting(t *testing.T) {
	dir := t.TempDir()
	cwd, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(cwd) //nolint:errcheck

	if err := os.WriteFile(".env", []byte("# mine\nJWT_SECRET=keepme-keepme-keepme-keepme-keepme\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("JWT_SECRET", "keepme-keepme-keepme-keepme-keepme")
	t.Setenv("ADMIN_KEY", "")
	jwt, admin, wrote, err := ensureEnvFile("postgres://u:p@127.0.0.1:5/db")
	if err != nil {
		t.Fatal(err)
	}
	if jwt != "keepme-keepme-keepme-keepme-keepme" {
		t.Fatalf("existing JWT_SECRET not reused: %q", jwt)
	}
	if admin == "" {
		t.Fatal("ADMIN_KEY not generated")
	}
	got, _ := os.ReadFile(".env")
	text := string(got)
	if !strings.Contains(text, "# mine\nJWT_SECRET=keepme") {
		t.Fatalf("existing lines rewritten:\n%s", text)
	}
	if !strings.Contains(text, "DATABASE_URL=postgres://u:p@127.0.0.1:5/db") {
		t.Fatalf("DATABASE_URL not appended:\n%s", text)
	}
	for _, k := range wrote {
		if k == "JWT_SECRET" {
			t.Fatalf("JWT_SECRET reported as written though it existed")
		}
	}
	st, _ := os.Stat(filepath.Join(dir, ".env"))
	if st.Mode().Perm() != 0o600 {
		t.Fatalf(".env permissions = %v, want 0600", st.Mode().Perm())
	}
}

// The example curl must be a POST only when every required field is safely
// fabricable; a required relation/uuid downgrades it to the GET that always
// works.
func TestExampleCurlShape(t *testing.T) {
	s, err := schema.LoadFromBytes(appximo.StarterSchema())
	if err != nil {
		t.Fatal(err)
	}
	c := exampleCurl(s, "tasks", "demo.localhost", 8080)
	if !strings.Contains(c, "-d '{\"title\":") || !strings.Contains(c, "POST") == strings.Contains(c, "GET /") {
		if !strings.Contains(c, "title") {
			t.Fatalf("starter example curl should POST a title: %s", c)
		}
	}

	raw := []byte(`{"$schema":"https://appximo.com/schema/v1","version":"1","name":"x",
	  "resources":{"lines":{"fields":{"order_id":{"type":"uuid","required":true,"relation":"orders"}}},
	               "orders":{"fields":{"total":{"type":"int64"}}}}}`)
	s2, err := schema.LoadFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	c2 := exampleCurl(s2, "lines", "demo.localhost", 8080)
	if strings.Contains(c2, "-d '") {
		t.Fatalf("required uuid relation cannot be fabricated — expected a GET example, got: %s", c2)
	}
}

// TestReconcileSchema pins the PUBLIC-SURFACE-S1 Part C contract: a re-run of
// `up` over an existing tenant NEVER reports success while silently keeping an
// older registered schema. Same schema → "unchanged" with no write; changed →
// migrated through PUT /tenants/{id}/schema (gated drops passed through);
// failed migration → an error naming the way out.
func TestReconcileSchema(t *testing.T) {
	local := []byte(`{"$schema":"https://appximo.com/schema/v1","version":"1","resources":{"tasks":{"fields":{"title":{"type":"string","minLength":1}}}}}`)
	reordered := []byte(`{"version":"1","$schema":"https://appximo.com/schema/v1","resources":{"tasks":{"fields":{"title":{"minLength":1,"type":"string"}}}}}`)
	older := []byte(`{"$schema":"https://appximo.com/schema/v1","version":"1","resources":{"tasks":{"fields":{"title":{"type":"string"}}}}}`)

	t.Run("unchanged means no PUT", func(t *testing.T) {
		var putCalled bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Write(reordered) // key order differs — still the same schema
			case http.MethodPut:
				putCalled = true
			}
		}))
		defer srv.Close()
		rec, err := reconcileSchema(srv.URL, "k", "acme", "schema.json", local)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rec.state != "unchanged" || putCalled {
			t.Fatalf("want unchanged with no PUT, got state=%q putCalled=%v", rec.state, putCalled)
		}
	})

	t.Run("changed migrates and reports gated drops", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Write(older)
			case http.MethodPut:
				var body struct {
					Schema json.RawMessage `json:"schema"`
				}
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Schema) == 0 {
					t.Errorf("PUT body must carry the schema: %v", err)
				}
				w.Write([]byte(`{"status":"migration_queued","gated_drops":["tasks.old_col"]}`))
			}
		}))
		defer srv.Close()
		rec, err := reconcileSchema(srv.URL, "k", "acme", "schema.json", local)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if rec.state != "migrated" || len(rec.gated) != 1 || rec.gated[0] != "tasks.old_col" {
			t.Fatalf("want migrated with the gated drop, got %+v", rec)
		}
	})

	t.Run("failed migration is a loud error, never ok", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Write(older)
			case http.MethodPut:
				w.WriteHeader(http.StatusInternalServerError)
				w.Write([]byte(`{"error":"internal error"}`))
			}
		}))
		defer srv.Close()
		_, err := reconcileSchema(srv.URL, "k", "acme", "schema.json", local)
		if err == nil {
			t.Fatal("a failed migration must be an error")
		}
		if !strings.Contains(err.Error(), "appximo migrate") {
			t.Fatalf("the error must name the way out, got: %v", err)
		}
	})

	t.Run("tenant with no stored schema migrates", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				w.Write([]byte("null"))
			case http.MethodPut:
				w.Write([]byte(`{"status":"migration_queued"}`))
			}
		}))
		defer srv.Close()
		rec, err := reconcileSchema(srv.URL, "k", "acme", "schema.json", local)
		if err != nil || rec.state != "migrated" {
			t.Fatalf("want migrated, got %+v err=%v", rec, err)
		}
	})
}

// LAUNCHPAD-S1: `up` hands the operator the MOST privileged role the schema
// declares. The old rule (admin-by-name, else alphabetically first) gave a
// {member, staff} schema the `member` identity, so the printed token could not
// write the app's own main resource — found by a third-party agent following
// the master prompt.
func TestPickRolePrefersTheMostPrivileged(t *testing.T) {
	role := func(res string, actions ...string) schema.RolePolicy {
		return schema.RolePolicy{Resources: json.RawMessage(res), Actions: actions}
	}
	cases := []struct {
		name  string
		roles map[string]schema.RolePolicy
		want  string
	}{
		{
			name: "admin by name always wins",
			roles: map[string]schema.RolePolicy{
				"admin":     role(`"*"`, "*"),
				"aaa_first": role(`"*"`, "*"),
			},
			want: "admin",
		},
		{
			name: "full access beats the alphabetically first role",
			roles: map[string]schema.RolePolicy{
				"member": role(`["games","loans"]`, "read"),
				"staff":  role(`"*"`, "*"),
			},
			want: "staff",
		},
		{
			name: "widest grant surface wins when nobody has wildcards",
			roles: map[string]schema.RolePolicy{
				"auditor": role(`["games"]`, "read"),
				"manager": role(`["games","loans","members"]`, "read", "create", "update"),
			},
			want: "manager",
		},
		{
			name: "an unrestricted role beats a row-scoped one of equal reach",
			roles: map[string]schema.RolePolicy{
				"owner": {Resources: json.RawMessage(`["orders"]`), Actions: []string{"read", "create", "update", "delete"},
					Conditions: &schema.Condition{Field: "user_id", Op: "eq", Val: "$user_id"}},
				"support": role(`["orders"]`, "read", "create", "update", "delete"),
			},
			want: "support",
		},
		{
			name: "per-resource permissions are scored too",
			roles: map[string]schema.RolePolicy{
				"cashier": {Permissions: map[string]schema.ResourcePermission{
					"orders": {Actions: []string{"read"}},
				}},
				"boss": {Permissions: map[string]schema.ResourcePermission{
					"orders":   {Actions: []string{"read", "create", "update", "delete"}},
					"products": {Actions: []string{"read", "create", "update", "delete"}},
				}},
			},
			want: "boss",
		},
		{
			name:  "no roles at all → empty (the caller warns)",
			roles: map[string]schema.RolePolicy{},
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := &schema.APISchema{
				Resources: map[string]schema.ResourceSchema{
					"games": {}, "loans": {}, "members": {}, "orders": {}, "products": {},
				},
				RBAC: schema.RBACPolicy{Roles: tc.roles},
			}
			if got := pickRole(s); got != tc.want {
				t.Errorf("pickRole = %q, want %q", got, tc.want)
			}
		})
	}
}

// Determinism: equally-privileged roles must never flip between runs (Go map
// iteration order would otherwise decide the operator's identity).
func TestPickRoleIsDeterministicOnTies(t *testing.T) {
	s := &schema.APISchema{
		Resources: map[string]schema.ResourceSchema{"a": {}},
		RBAC: schema.RBACPolicy{Roles: map[string]schema.RolePolicy{
			"zeta":  {Resources: json.RawMessage(`["a"]`), Actions: []string{"read"}},
			"alpha": {Resources: json.RawMessage(`["a"]`), Actions: []string{"read"}},
			"mid":   {Resources: json.RawMessage(`["a"]`), Actions: []string{"read"}},
		}},
	}
	first := pickRole(s)
	for i := 0; i < 50; i++ {
		if got := pickRole(s); got != first {
			t.Fatalf("pickRole flipped between runs: %q then %q", first, got)
		}
	}
	if first != "alpha" {
		t.Errorf("tie must break alphabetically, got %q", first)
	}
}
