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

// TestManifestFileEdit (FLEET-EDIT-S1): EditAppInManifestFile replaces an
// EXISTING entry's env_file/env pointer (not append, not the AddApp path),
// errors on an app the manifest does not declare, and every OTHER field
// (schema/domains/port) plus every OTHER app's entry survives byte-untouched.
func TestManifestFileEdit(t *testing.T) {
	path := writeLifecycleFixture(t)
	dir := filepath.Dir(path)

	// Editing an app the manifest doesn't declare must fail (edit, not create).
	ghost := &AppSpec{Name: "ghost", Schema: filepath.Join(dir, "crm.json"), Domains: []string{"ghost.test"}}
	if err := EditAppInManifestFile(path, ghost); err == nil {
		t.Fatal("editing an undeclared app must error")
	}

	// "crm" now moves to an env_file (simulating what writeAppEnvFile does
	// before EditApp calls this) instead of its original inline env. The file
	// is written to disk first — same order EditApp uses (writeAppEnvFile
	// before EditAppInManifestFile) — so the manifest stays loadable.
	envFile := filepath.Join(dir, "crm.env")
	envBody := "ADMIN_KEY=crm-admin\nDATABASE_URL=postgres://x/crm\nJWT_SECRET=crm-secret-32-chars-loooooooooong\n"
	if err := os.WriteFile(envFile, []byte(envBody), 0o600); err != nil {
		t.Fatal(err)
	}
	edited := &AppSpec{
		Name: "crm", Schema: "crm.json", Domains: []string{"crm.test"},
		EnvFile: envFile,
	}
	if err := EditAppInManifestFile(path, edited); err != nil {
		t.Fatal(err)
	}

	raw, _ := os.ReadFile(path)
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	apps := doc["apps"].([]any)
	if len(apps) != 2 {
		t.Fatalf("edit must REPLACE in place, not append: raw apps = %d, want 2", len(apps))
	}
	var crmEntry, shopEntry map[string]any
	for _, e := range apps {
		m := e.(map[string]any)
		switch m["name"] {
		case "crm":
			crmEntry = m
		case "shop":
			shopEntry = m
		}
	}
	if crmEntry == nil {
		t.Fatal("crm entry missing after edit")
	}
	if crmEntry["env"] != nil {
		t.Fatalf("edited entry must clear the old inline env, got %v", crmEntry["env"])
	}
	if crmEntry["env_file"] != edited.EnvFile {
		t.Fatalf("edited entry env_file = %v, want %v", crmEntry["env_file"], edited.EnvFile)
	}
	if shopEntry == nil || shopEntry["env"] == nil {
		t.Fatal("the OTHER app's entry (shop) must survive the edit untouched")
	}

	// The edited manifest must still LOAD (the restart contract) with the
	// edited app's env resolved from its NEW env_file.
	mf2, err := LoadManifest(path)
	if err != nil {
		t.Fatalf("manifest after edit does not load: %v", err)
	}
	if got := mf2.AppByName("crm").MergedEnv()["ADMIN_KEY"]; got != "crm-admin" {
		t.Fatalf("edited app's env not resolved from its new env_file: %q", got)
	}
}
