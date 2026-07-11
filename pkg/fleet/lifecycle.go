package fleet

// FLEET-LIFECYCLE-S1 — manifest-side support for the fleet's app lifecycle
// (`appitools fleet add/remove/list` + the console actions). Two concerns live
// here, both OFF the engine:
//
//   - ValidateNewApp: the SAME rules LoadManifest enforces, applied to ONE
//     candidate app against an already-loaded manifest (unique name, unclaimed
//     domains, required env, the per-app JWT_SECRET rule) — so a hot add can
//     never introduce a state a fleet restart would refuse to load.
//   - AddAppToManifestFile / RemoveAppFromManifestFile: SURGICAL edits of the
//     manifest file (raw JSON, atomic tmp+rename), so a hot add/remove is
//     PERSISTED and the live registry and fleet.json never drift — the
//     manifest stays the persistent source of truth, the registry the live
//     state, and lifecycle operations update both. Editing the raw document
//     (not re-marshaling the loaded struct) keeps untouched fields byte-exact
//     and never bakes in load-time resolution (absolutized paths of OTHER
//     apps, an operator_key that came from the environment).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Path returns the absolute path of the file this manifest was loaded from
// (empty for a manifest constructed in code).
func (m *Manifest) Path() string {
	if m.dir == "" {
		return ""
	}
	return m.path
}

// ValidateNewApp checks a candidate app against this manifest with the SAME
// rules LoadManifest applies, resolving the candidate's env (env_file + env)
// as a side effect. The candidate is NOT added; callers add it after the whole
// add operation (compile, registry, persist) succeeds.
func (m *Manifest) ValidateNewApp(a *AppSpec) error {
	if !appNameRe.MatchString(a.Name) {
		return fmt.Errorf("fleet: app name %q must match %s", a.Name, appNameRe)
	}
	for i := range m.Apps {
		if m.Apps[i].Name == a.Name {
			return fmt.Errorf("fleet: an app named %q already exists", a.Name)
		}
	}

	if a.Schema == "" {
		return fmt.Errorf("fleet: app %q: schema is required", a.Name)
	}
	if !filepath.IsAbs(a.Schema) && m.dir != "" {
		a.Schema = filepath.Join(m.dir, a.Schema)
	}
	if _, err := os.Stat(a.Schema); err != nil {
		return fmt.Errorf("fleet: app %q: schema %s: %w", a.Name, a.Schema, err)
	}

	if len(a.Domains) == 0 {
		return fmt.Errorf("fleet: app %q: at least one domain is required", a.Name)
	}
	claimed := map[string]string{}
	for i := range m.Apps {
		for _, d := range m.Apps[i].Domains {
			claimed[d] = m.Apps[i].Name
		}
	}
	for j, d := range a.Domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" || strings.Contains(d, "/") || strings.Contains(d, ":") {
			return fmt.Errorf("fleet: app %q: invalid domain %q (hostname only, no port/path)", a.Name, a.Domains[j])
		}
		a.Domains[j] = d
		if owner, dup := claimed[d]; dup {
			return fmt.Errorf("fleet: domain %q is already claimed by app %q", d, owner)
		}
	}

	merged := map[string]string{}
	if a.EnvFile != "" {
		ef := a.EnvFile
		if !filepath.IsAbs(ef) && m.dir != "" {
			ef = filepath.Join(m.dir, ef)
		}
		fileEnv, err := parseEnvFile(ef)
		if err != nil {
			return fmt.Errorf("fleet: app %q: env_file: %w", a.Name, err)
		}
		for k, v := range fileEnv {
			merged[k] = v
		}
	}
	for k, v := range a.Env {
		merged[k] = v
	}
	a.mergedEnv = merged

	for _, req := range []string{"DATABASE_URL", "JWT_SECRET", "ADMIN_KEY"} {
		if merged[req] == "" {
			return fmt.Errorf("fleet: app %q: env %s is required (the engine refuses to boot without it)", a.Name, req)
		}
	}
	for i := range m.Apps {
		if env := m.Apps[i].MergedEnv(); env["JWT_SECRET"] == merged["JWT_SECRET"] {
			return fmt.Errorf("fleet: app %q would share app %q's JWT_SECRET — each app must have its own (a shared secret lets one app's tokens validate on the other)", a.Name, m.Apps[i].Name)
		}
	}
	if m.OperatorKey != "" && (m.OperatorKey == merged["ADMIN_KEY"] || m.OperatorKey == merged["JWT_SECRET"]) {
		return fmt.Errorf("fleet: app %q: its ADMIN_KEY/JWT_SECRET must differ from the fleet operator_key — the fleet level is above the app level", a.Name)
	}
	return nil
}

// AppByName returns the manifest entry named name, or nil.
func (m *Manifest) AppByName(name string) *AppSpec {
	for i := range m.Apps {
		if m.Apps[i].Name == name {
			return &m.Apps[i]
		}
	}
	return nil
}

// manifestAppJSON is the serialized form of one added app — only the fields the
// operator provided (omitempty keeps the file minimal, like a hand-written one).
type manifestAppJSON struct {
	Name        string            `json:"name"`
	Schema      string            `json:"schema"`
	Domains     []string          `json:"domains"`
	Port        int               `json:"port,omitempty"`
	ControlPort int               `json:"control_port,omitempty"`
	EnvFile     string            `json:"env_file,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
}

// editManifestFile applies mutate to the manifest file's raw JSON document and
// writes it back atomically (tmp+rename, same directory). The document is
// decoded generically so every field this code does not touch survives
// byte-meaning-exact (values unchanged; formatting is re-indented).
func editManifestFile(path string, mutate func(doc map[string]any) error) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("fleet: read manifest: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("fleet: parse manifest %s: %w", path, err)
	}
	if err := mutate(doc); err != nil {
		return err
	}
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("fleet: serialize manifest: %w", err)
	}
	out = append(out, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return fmt.Errorf("fleet: write manifest: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp) //nolint:errcheck
		return fmt.Errorf("fleet: replace manifest: %w", err)
	}
	return nil
}

// AddAppToManifestFile appends spec to the manifest file's apps array
// (atomically). The caller has already validated spec (ValidateNewApp).
func AddAppToManifestFile(path string, spec *AppSpec) error {
	entry := manifestAppJSON{
		Name: spec.Name, Schema: spec.Schema, Domains: spec.Domains,
		Port: spec.Port, ControlPort: spec.ControlPort,
		EnvFile: spec.EnvFile, Env: spec.Env,
	}
	// Round-trip through JSON to the generic form the document holds.
	b, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	var generic map[string]any
	if err := json.Unmarshal(b, &generic); err != nil {
		return err
	}
	return editManifestFile(path, func(doc map[string]any) error {
		apps, _ := doc["apps"].([]any)
		for _, e := range apps {
			if m, ok := e.(map[string]any); ok && m["name"] == spec.Name {
				return fmt.Errorf("fleet: manifest already declares app %q", spec.Name)
			}
		}
		doc["apps"] = append(apps, generic)
		return nil
	})
}

// EditAppInManifestFile replaces the manifest entry named spec.Name with spec
// (atomically) — used after EditApp (FLEET-EDIT-S1) resolves a new merged env:
// the app's env now lives in a (possibly freshly written) env_file, so the
// entry's env_file pointer is updated and any old inline env is cleared. This
// is EDIT, not create: it errors if the manifest does not already declare the
// app (mirrors RemoveAppFromManifestFile's not-found handling; see
// AddAppToManifestFile for the create path).
func EditAppInManifestFile(path string, spec *AppSpec) error {
	entry := manifestAppJSON{
		Name: spec.Name, Schema: spec.Schema, Domains: spec.Domains,
		Port: spec.Port, ControlPort: spec.ControlPort,
		EnvFile: spec.EnvFile, Env: spec.Env,
	}
	b, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	var generic map[string]any
	if err := json.Unmarshal(b, &generic); err != nil {
		return err
	}
	return editManifestFile(path, func(doc map[string]any) error {
		apps, _ := doc["apps"].([]any)
		for i, e := range apps {
			if m, ok := e.(map[string]any); ok && m["name"] == spec.Name {
				apps[i] = generic
				doc["apps"] = apps
				return nil
			}
		}
		return fmt.Errorf("fleet: manifest does not declare app %q", spec.Name)
	})
}

// RemoveAppFromManifestFile removes the app named name from the manifest file
// (atomically). Returns an error if the manifest does not declare it.
func RemoveAppFromManifestFile(path, name string) error {
	return editManifestFile(path, func(doc map[string]any) error {
		apps, _ := doc["apps"].([]any)
		next := make([]any, 0, len(apps))
		found := false
		for _, e := range apps {
			if m, ok := e.(map[string]any); ok && m["name"] == name {
				found = true
				continue
			}
			next = append(next, e)
		}
		if !found {
			return fmt.Errorf("fleet: manifest does not declare app %q", name)
		}
		doc["apps"] = next
		return nil
	})
}
