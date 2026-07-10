// Package fleet is the MT-STRUCT-S1 orchestrator: ONE server serving N
// DISTINCT apps (different schemas → different APIs) as N engine processes —
// the Option-A architecture of docs/design/MT-STRUCT.md. Each app is today's
// engine, unmodified on its hot path; the fleet adds only a supervisor
// (spawn/health/restart-on-exit) and a Host-routing reverse proxy in front.
//
// Taxonomy (MT-STRUCT §1): an APP is one schema compiled into one API surface;
// a TENANT is an isolated data instance INSIDE an app; the FLEET is the set of
// apps on one server. A request resolves app (Host → proxy) then tenant
// (subdomain → engine middleware), two independent axes.
package fleet

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// AppSpec declares one app of the fleet: its schema, the domains the proxy
// routes to it, and its OWN config/secrets (per-app by design — MT-STRUCT §7:
// a shared JWT_SECRET would let a token minted for app X validate on app Y).
type AppSpec struct {
	// Name identifies the app (lowercase, ^[a-z][a-z0-9_-]*$, unique).
	Name string `json:"name"`
	// Schema is the path to the app's schema JSON (the engine's --schema).
	// Relative paths resolve against the manifest file's directory.
	Schema string `json:"schema"`
	// Domains are the public hostnames of this app. A request whose Host is a
	// domain OR any subdomain of it routes here — subdomains stay free for the
	// engine's tenant resolution (acme.crm.example.com → app crm, tenant acme).
	Domains []string `json:"domains"`
	// Port / ControlPort pin the app's internal data/control ports; 0 (the
	// default) auto-allocates a free port. Internal only — the proxy is the
	// public face.
	Port        int `json:"port,omitempty"`
	ControlPort int `json:"control_port,omitempty"`
	// EnvFile is an optional KEY=VALUE file (e.g. the app's secrets) loaded
	// first; Env entries override it. Relative to the manifest directory.
	EnvFile string `json:"env_file,omitempty"`
	// Env is the app's environment: DATABASE_URL, JWT_SECRET, ADMIN_KEY are
	// REQUIRED (the engine refuses to boot without them); anything else the
	// engine reads (APPITOOLS_*, RATE_LIMIT_*, …) may be set per app.
	Env map[string]string `json:"env,omitempty"`

	// mergedEnv is EnvFile+Env resolved at load (Env wins). Not serialized.
	mergedEnv map[string]string
}

// MergedEnv returns the app's resolved environment (EnvFile overlaid by Env).
func (a *AppSpec) MergedEnv() map[string]string { return a.mergedEnv }

// Manifest is the fleet config file: the proxy listen address, the status
// endpoint, the per-app data root, and the apps.
type Manifest struct {
	// Listen is the proxy's public address (default ":8080").
	Listen string `json:"listen,omitempty"`
	// StatusAddr serves the fleet status/control API (default "127.0.0.1:9601").
	// Internal-only, like the engine control plane — keep it off the internet.
	StatusAddr string `json:"status_addr,omitempty"`
	// DataDir roots per-app state the fleet assigns when the app's env does not
	// set it: <DataDir>/<app>/obs.db, <DataDir>/<app>/files, logs. Default
	// "/var/lib/appitools/fleet".
	DataDir string `json:"data_dir,omitempty"`
	// OperatorKey is the FLEET-OPERATOR credential (MT-STRUCT-S5): it gates the
	// unified fleet console (`/fleet` on the in-process runtime's process-level
	// handler) — the server owner's view over ALL apps. It is a level ABOVE the
	// per-app credentials and is deliberately DISTINCT from every app's
	// ADMIN_KEY/JWT_SECRET: holding one app's keys never reveals the fleet, and
	// the fleet key opens NO app API (per-app JWT/RBAC/admin auth still applies
	// underneath — the S3 isolation is not bypassable from the console). Empty
	// falls back to APPITOOLS_FLEET_OPERATOR_KEY; still empty ⇒ the console is
	// DISABLED (safe by default).
	OperatorKey string `json:"operator_key,omitempty"`
	// OperatorAdminEmail enables the UNIFIED OPERATOR IDENTITY
	// (FLEET-CONSOLE-S2): the in-process runtime ensures a platform
	// super-admin with this email exists in EVERY app's database (idempotent,
	// never overwriting an existing account), so ONE login works on every
	// app's /admin — without weakening the S3 isolation (each app keeps its
	// own admin row, DB and tokens). The PASSWORD is deliberately NOT a
	// manifest key: it comes from the APPITOOLS_FLEET_ADMIN_PASSWORD env var
	// (an env-file, like the app secrets), so the manifest stays committable.
	// Empty email falls back to APPITOOLS_FLEET_ADMIN_EMAIL; both empty ⇒
	// feature off (each app manages its own admins, the pre-S2 behavior).
	OperatorAdminEmail string `json:"operator_admin_email,omitempty"`
	// Apps are the fleet's apps (≥1).
	Apps []AppSpec `json:"apps"`

	dir  string // manifest file directory, for resolving relative paths
	path string // absolute manifest file path (lifecycle persistence)
}

// OperatorAdmin returns the unified operator identity (email, password), or
// ("", "") when disabled. Password always from env — never the manifest.
func (m *Manifest) OperatorAdmin() (string, string) {
	if m.OperatorAdminEmail == "" {
		return "", ""
	}
	return m.OperatorAdminEmail, os.Getenv("APPITOOLS_FLEET_ADMIN_PASSWORD")
}

var appNameRe = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// LoadManifest reads, resolves and validates a fleet manifest. Every error is
// actionable (names the app and the rule) — the same load-fails-loud contract
// as the engine's schema validation.
func LoadManifest(path string) (*Manifest, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("fleet: read manifest: %w", err)
	}
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("fleet: parse manifest %s: %w", path, err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("fleet: resolve manifest path: %w", err)
	}
	m.dir = filepath.Dir(abs)
	m.path = abs

	if m.Listen == "" {
		m.Listen = ":8080"
	}
	if m.StatusAddr == "" {
		m.StatusAddr = "127.0.0.1:9601"
	}
	if m.DataDir == "" {
		m.DataDir = "/var/lib/appitools/fleet"
	}

	if len(m.Apps) == 0 {
		return nil, fmt.Errorf("fleet: manifest declares no apps")
	}

	seenNames := map[string]bool{}
	seenDomains := map[string]string{} // domain → app name
	seenSecrets := map[string]string{} // JWT_SECRET → app name
	dsnApps := map[string][]string{}   // DATABASE_URL → app names (warn on share)

	for i := range m.Apps {
		a := &m.Apps[i]
		if !appNameRe.MatchString(a.Name) {
			return nil, fmt.Errorf("fleet: app %d: name %q must match %s", i, a.Name, appNameRe)
		}
		if seenNames[a.Name] {
			return nil, fmt.Errorf("fleet: duplicate app name %q", a.Name)
		}
		seenNames[a.Name] = true

		if a.Schema == "" {
			return nil, fmt.Errorf("fleet: app %q: schema is required", a.Name)
		}
		if !filepath.IsAbs(a.Schema) {
			a.Schema = filepath.Join(m.dir, a.Schema)
		}
		if _, err := os.Stat(a.Schema); err != nil {
			return nil, fmt.Errorf("fleet: app %q: schema %s: %w", a.Name, a.Schema, err)
		}

		if len(a.Domains) == 0 {
			return nil, fmt.Errorf("fleet: app %q: at least one domain is required", a.Name)
		}
		for j, d := range a.Domains {
			d = strings.ToLower(strings.TrimSpace(d))
			if d == "" || strings.Contains(d, "/") || strings.Contains(d, ":") {
				return nil, fmt.Errorf("fleet: app %q: invalid domain %q (hostname only, no port/path)", a.Name, a.Domains[j])
			}
			a.Domains[j] = d
			if owner, dup := seenDomains[d]; dup {
				return nil, fmt.Errorf("fleet: domain %q claimed by both %q and %q", d, owner, a.Name)
			}
			seenDomains[d] = a.Name
		}

		// Resolve env: file first, explicit entries win.
		merged := map[string]string{}
		if a.EnvFile != "" {
			ef := a.EnvFile
			if !filepath.IsAbs(ef) {
				ef = filepath.Join(m.dir, ef)
			}
			fileEnv, err := parseEnvFile(ef)
			if err != nil {
				return nil, fmt.Errorf("fleet: app %q: env_file: %w", a.Name, err)
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
				return nil, fmt.Errorf("fleet: app %q: env %s is required (the engine refuses to boot without it)", a.Name, req)
			}
		}
		// Per-app JWT secret is a hard rule (MT-STRUCT §7): a shared secret
		// would make a token minted for one app verify on another.
		if owner, dup := seenSecrets[merged["JWT_SECRET"]]; dup {
			return nil, fmt.Errorf("fleet: apps %q and %q share the same JWT_SECRET — each app must have its own (a shared secret lets one app's tokens validate on the other)", owner, a.Name)
		}
		seenSecrets[merged["JWT_SECRET"]] = a.Name
		dsnApps[merged["DATABASE_URL"]] = append(dsnApps[merged["DATABASE_URL"]], a.Name)
	}

	// Fleet-operator key (S5): env fallback, then guard the level separation —
	// the fleet credential must not COINCIDE with any app's credentials (a
	// shared value would collapse the fleet level into an app level).
	if m.OperatorKey == "" {
		m.OperatorKey = os.Getenv("APPITOOLS_FLEET_OPERATOR_KEY")
	}
	if m.OperatorKey != "" {
		for i := range m.Apps {
			env := m.Apps[i].MergedEnv()
			if m.OperatorKey == env["ADMIN_KEY"] || m.OperatorKey == env["JWT_SECRET"] {
				return nil, fmt.Errorf("fleet: operator_key must differ from every app's ADMIN_KEY/JWT_SECRET (matches app %q) — the fleet level is above the app level", m.Apps[i].Name)
			}
		}
	}

	// Unified operator identity (FLEET-CONSOLE-S2): env fallback for the email;
	// the password ONLY ever comes from env (never a manifest key). Declaring
	// the email without the password would silently disable the provisioning —
	// fail loud instead, with the fix in the message.
	if m.OperatorAdminEmail == "" {
		m.OperatorAdminEmail = os.Getenv("APPITOOLS_FLEET_ADMIN_EMAIL")
	}
	if m.OperatorAdminEmail != "" && os.Getenv("APPITOOLS_FLEET_ADMIN_PASSWORD") == "" {
		return nil, fmt.Errorf("fleet: operator_admin_email %q is set but APPITOOLS_FLEET_ADMIN_PASSWORD is not — the operator admin password comes from the environment (an env-file), never the manifest", m.OperatorAdminEmail)
	}

	// Sharing one DATABASE_URL means sharing public.tenants / outbox / the
	// system schema — two apps with the same tenant id would collide. Loud
	// warning, not an error (a demo may accept it knowingly).
	for dsn, names := range dsnApps {
		if len(names) > 1 {
			fmt.Fprintf(os.Stderr, "fleet: WARNING: apps %s share one DATABASE_URL — they share public.tenants/outbox and tenant schemas; identical tenant ids WILL collide. Give each app its own database. (dsn host: %s)\n",
				strings.Join(names, ", "), redactDSN(dsn))
		}
	}

	return &m, nil
}

// parseEnvFile reads KEY=VALUE lines (blank lines and #-comments ignored;
// values may be single- or double-quoted).
func parseEnvFile(path string) (map[string]string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for ln, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("%s:%d: not KEY=VALUE", path, ln+1)
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		out[strings.TrimSpace(k)] = v
	}
	return out, nil
}

// redactDSN keeps only the host part of a DSN for log output.
func redactDSN(dsn string) string {
	if at := strings.LastIndex(dsn, "@"); at >= 0 {
		return dsn[at+1:]
	}
	if len(dsn) > 24 {
		return dsn[:24] + "…"
	}
	return dsn
}
