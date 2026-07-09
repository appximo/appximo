package fleet

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// writeLifecycleFixture creates a manifest with two apps + schemas on disk and
// returns the manifest path.
func writeLifecycleFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	schemaJSON := `{"$schema":"https://appitools.dev/schema/v1","version":"1","name":"x","resources":{"tasks":{"fields":{"title":{"type":"string"}}}}}`
	for _, n := range []string{"crm", "shop", "optica"} {
		if err := os.WriteFile(filepath.Join(dir, n+".json"), []byte(schemaJSON), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `{
  "listen": ":18099",
  "data_dir": "` + dir + `/data",
  "operator_key": "op-key-1234",
  "apps": [
    { "name": "crm",  "schema": "crm.json",  "domains": ["crm.test"],
      "env": { "DATABASE_URL": "postgres://x/crm", "JWT_SECRET": "crm-secret-32-chars-loooooooooong", "ADMIN_KEY": "crm-admin" } },
    { "name": "shop", "schema": "shop.json", "domains": ["shop.test"],
      "env": { "DATABASE_URL": "postgres://x/shop", "JWT_SECRET": "shop-secret-32-chars-looooooooong", "ADMIN_KEY": "shop-admin" } }
  ]
}`
	path := filepath.Join(dir, "fleet.json")
	if err := os.WriteFile(path, []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func newAppSpec(dir string) *AppSpec {
	return &AppSpec{
		Name:    "optica",
		Schema:  filepath.Join(dir, "optica.json"),
		Domains: []string{"optica.test"},
		Env: map[string]string{
			"DATABASE_URL": "postgres://x/optica",
			"JWT_SECRET":   "optica-secret-32-chars-loooooooog",
			"ADMIN_KEY":    "optica-admin",
		},
	}
}

func TestValidateNewApp(t *testing.T) {
	path := writeLifecycleFixture(t)
	dir := filepath.Dir(path)
	mf, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}

	if err := mf.ValidateNewApp(newAppSpec(dir)); err != nil {
		t.Fatalf("valid new app rejected: %v", err)
	}

	cases := map[string]func(a *AppSpec){
		"duplicate name":        func(a *AppSpec) { a.Name = "crm" },
		"bad name":              func(a *AppSpec) { a.Name = "Optica!" },
		"claimed domain":        func(a *AppSpec) { a.Domains = []string{"shop.test"} },
		"no domains":            func(a *AppSpec) { a.Domains = nil },
		"missing schema":        func(a *AppSpec) { a.Schema = filepath.Join(dir, "nope.json") },
		"missing DATABASE_URL":  func(a *AppSpec) { delete(a.Env, "DATABASE_URL") },
		"shared JWT_SECRET":     func(a *AppSpec) { a.Env["JWT_SECRET"] = "crm-secret-32-chars-loooooooooong" },
		"operator key collides": func(a *AppSpec) { a.Env["ADMIN_KEY"] = "op-key-1234" },
	}
	for name, mutate := range cases {
		a := newAppSpec(dir)
		mutate(a)
		if err := mf.ValidateNewApp(a); err == nil {
			t.Errorf("%s: expected rejection, got nil", name)
		}
	}
}

func TestManifestFileAddRemove(t *testing.T) {
	path := writeLifecycleFixture(t)
	dir := filepath.Dir(path)
	spec := newAppSpec(dir)

	if err := AddAppToManifestFile(path, spec); err != nil {
		t.Fatal(err)
	}
	// A second add of the same name must fail (no silent duplicates).
	if err := AddAppToManifestFile(path, spec); err == nil {
		t.Fatal("duplicate add accepted")
	}

	// The persisted manifest must LOAD (the restart contract): 3 apps, and the
	// untouched fields survive.
	mf, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("manifest after add does not load: %v", err)
	}
	if len(mf.Apps) != 3 || mf.AppByName("optica") == nil {
		t.Fatalf("added app not in reloaded manifest: %d apps", len(mf.Apps))
	}
	if mf.OperatorKey != "op-key-1234" || mf.Listen != ":18099" {
		t.Fatal("untouched manifest fields did not survive the edit")
	}
	if got := mf.AppByName("optica").MergedEnv()["ADMIN_KEY"]; got != "optica-admin" {
		t.Fatalf("added app env not persisted: %q", got)
	}

	if err := RemoveAppFromManifestFile(path, "optica"); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAppFromManifestFile(path, "optica"); err == nil {
		t.Fatal("removing a non-declared app must error")
	}
	mf, err = LoadManifest(path)
	if err != nil {
		t.Fatalf("manifest after remove does not load: %v", err)
	}
	if len(mf.Apps) != 2 || mf.AppByName("optica") != nil {
		t.Fatal("removed app still in the manifest")
	}

	// The raw document still has exactly the original two app entries.
	raw, _ := os.ReadFile(path)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if apps := doc["apps"].([]any); len(apps) != 2 {
		t.Fatalf("raw apps = %d, want 2", len(apps))
	}
}
