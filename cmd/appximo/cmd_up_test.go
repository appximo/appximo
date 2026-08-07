package main

import (
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
