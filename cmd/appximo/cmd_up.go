package main

// appximo up — one command from an empty directory to a running app (ENG-38,
// FEEDBACK.md §13). The design rule, measured in the field evaluation: the
// schema (the product's core) took MINUTES; everything else — Postgres, .env,
// tenant registration, the first admin — was ~1h30 of friction a command can
// orchestrate. `up` builds NOTHING new: every step drives a seam that already
// exists (the control plane's POST /tenants with the schema in the body, the
// /admin bootstrap, the platform user route, the token minter, install.sh's
// secret recipe), against this same process's own server. One question block at
// the start, then defaults; every step says what it wrote and where; every
// failure names the problem AND the way out (ADR-024).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/spf13/cobra"

	"github.com/appximo/appximo"
	"github.com/appximo/appximo/pkg/auth"
	"github.com/appximo/appximo/pkg/controlplane"
	"github.com/appximo/appximo/pkg/logging"
	"github.com/appximo/appximo/pkg/schema"
)

type upOptions struct {
	Name          string
	SchemaPath    string
	Port          int
	ControlPort   int
	PGImage       string
	PGContainer   string
	PGPort        int
	NoDocker      bool
	Static        []string
	SPA           bool
	JSON          bool
	Yes           bool
	AdminEmail    string
	AdminPassword string

	// progress is where step-by-step announcements go: stdout normally, stderr
	// in --json mode (stdout must carry EXACTLY one JSON object — the C1 rule).
	progress io.Writer
}

// upResult is the machine card (`appximo up --json`): URLs, credentials and
// token as data, no prose to parse — the validate --json pattern applied to
// the first mile, exactly as the field report asked.
type upResult struct {
	OK          bool              `json:"ok"`
	Name        string            `json:"name"`
	Host        string            `json:"host"`
	Port        int               `json:"port"`
	ControlPort int               `json:"control_port"`
	URLs        map[string]string `json:"urls"`
	Credentials *upCredentials    `json:"credentials,omitempty"`
	Token       string            `json:"token,omitempty"`
	TokenRole   string            `json:"token_role,omitempty"`
	Resources   []string          `json:"resources"`
	Files       map[string]string `json:"files"`
	Postgres    upPostgres        `json:"postgres"`
	Tenant      upTenant          `json:"tenant"`
	Smoke       *upSmoke          `json:"smoke,omitempty"`
	ExampleCurl string            `json:"example_curl,omitempty"`
}

type upCredentials struct {
	Email       string `json:"email"`
	Password    string `json:"password,omitempty"` // omitted when reused from an earlier run (printed once)
	PrintedOnce bool   `json:"printed_once"`
	Note        string `json:"note"`
}

type upPostgres struct {
	Mode      string `json:"mode"` // "external" (DATABASE_URL) | "docker"
	Container string `json:"container,omitempty"`
	HostPort  int    `json:"host_port,omitempty"`
	Volume    string `json:"volume,omitempty"`
}

type upTenant struct {
	ID      string `json:"id"`
	Created bool   `json:"created"` // false = it already existed (reused)
	// Schema says what happened to the tenant's REGISTERED schema this run:
	// "registered" (new tenant), "unchanged" (schema.json matches it), or
	// "migrated" (it differed and was migrated — PUBLIC-SURFACE-S1 Part C: a
	// re-run must never answer ok:true while serving an older schema silently).
	Schema string `json:"schema,omitempty"`
	// GatedDrops lists destructive drops the migration left gated (a removed
	// field/resource whose data would be lost) — approve them explicitly with
	// `appximo migrate --approve-drops`; nothing was lost.
	GatedDrops []string `json:"gated_drops,omitempty"`
}

type upSmoke struct {
	Passed  bool   `json:"passed"`
	Request string `json:"request"`
	Detail  string `json:"detail,omitempty"`
}

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "One command to a running app: Postgres + secrets + tenant + admin + server",
	Long: `Starts everything a first local run needs, in one command:

  1. Postgres    — uses DATABASE_URL if set; otherwise starts postgres:16 in
                   Docker (container appximo-pg, loopback-only, data in a
                   volume). --no-docker refuses the Docker path.
  2. Secrets     — generates JWT_SECRET and ADMIN_KEY, writes ./.env (0600,
                   no BOM) and loads it into this process.
  3. Schema      — --schema FILE, or ./schema.json if present, or writes the
                   todo-api starter to ./schema.json for you to replace.
  4. Tenant      — registers <name> on the control plane WITH the schema in
                   the body (the same POST /tenants the docs teach).
  5. First admin — bootstraps the platform admin and a tenant user with the
                   SAME credentials, printed ONCE.
  6. Serves      — foreground, and prints the card: URLs (/app /docs /admin
                   /editor), credentials, a dev token, and a curl that works.

One question block at the start (Postgres? name?) — nothing after that. In a
non-interactive shell (or with --yes) there are no questions: defaults apply.
Running it twice is safe: everything already created is detected and reused,
and a CHANGED schema.json is migrated to the tenant (additive changes apply
live; a destructive drop is never auto-approved — it stays gated and the card
prints the exact "appximo migrate --approve-drops" command). A migration that
fails is a loud failure, never an ok over the old schema.
--json prints the card as ONE JSON object on stdout (progress goes to stderr).

Stop the server with Ctrl+C. Stop the Docker Postgres too: appximo down.`,
	Args: cobra.NoArgs,
	Run: func(cmd *cobra.Command, _ []string) {
		opts := upOptions{}
		opts.Name, _ = cmd.Flags().GetString("name")
		opts.SchemaPath, _ = cmd.Flags().GetString("schema")
		opts.Port, _ = cmd.Flags().GetInt("port")
		opts.ControlPort, _ = cmd.Flags().GetInt("control-port")
		opts.PGImage, _ = cmd.Flags().GetString("pg-image")
		opts.PGContainer, _ = cmd.Flags().GetString("pg-container")
		opts.PGPort, _ = cmd.Flags().GetInt("pg-port")
		opts.NoDocker, _ = cmd.Flags().GetBool("no-docker")
		opts.Static, _ = cmd.Flags().GetStringArray("static")
		opts.SPA, _ = cmd.Flags().GetBool("spa")
		opts.JSON, _ = cmd.Flags().GetBool("json")
		opts.Yes, _ = cmd.Flags().GetBool("yes")
		opts.AdminEmail, _ = cmd.Flags().GetString("admin-email")
		opts.AdminPassword, _ = cmd.Flags().GetString("admin-password")
		if err := runUp(opts); err != nil {
			fmt.Fprintln(os.Stderr, "appximo up:", err)
			os.Exit(1)
		}
	},
}

func init() {
	upCmd.Flags().String("name", "", "app / tenant name (default: derived from the directory name)")
	upCmd.Flags().String("schema", "", "schema file (default: ./schema.json, written with the starter if absent)")
	upCmd.Flags().Int("port", 8080, "HTTP port for the app")
	upCmd.Flags().Int("control-port", 0, "control-plane port (0 = APPXIMO_CONTROL_PORT, then 9090)")
	upCmd.Flags().String("pg-image", "postgres:16", "Postgres image for the Docker path")
	upCmd.Flags().String("pg-container", "appximo-pg", "Docker container name for Postgres")
	upCmd.Flags().Int("pg-port", 54329, "host port for the Docker Postgres (loopback-only)")
	upCmd.Flags().Bool("no-docker", false, "never start Docker; require DATABASE_URL")
	upCmd.Flags().StringArray("static", nil, "serve your frontend from the same binary: [urlpath=]dir (repeatable, same form as serve --static)")
	upCmd.Flags().Bool("spa", false, "client-side-routing fallback for --static mounts (serve index.html for unmatched paths)")
	upCmd.Flags().Bool("json", false, "print the final card as ONE JSON object on stdout (progress → stderr)")
	upCmd.Flags().Bool("yes", false, "no questions: accept every default (implied by --json / non-TTY)")
	upCmd.Flags().String("admin-email", "", "first admin email (default admin@<name>.local)")
	upCmd.Flags().String("admin-password", "", "first admin password (default: generated, printed once)")
	rootCmd.AddCommand(upCmd)
}

func runUp(opts upOptions) error {
	opts.progress = os.Stdout
	if opts.JSON {
		opts.progress = os.Stderr
		// stdout must carry EXACTLY one JSON object (the C1 rule): the engine's
		// structured request logs — stdout by 12-factor default — move to stderr
		// for this run. appximo.New re-runs logging.Init, which honors this.
		logging.SetDefaultWriter(os.Stderr)
	}
	step := func(format string, a ...any) { fmt.Fprintf(opts.progress, "  ✓ "+format+"\n", a...) }
	note := func(format string, a ...any) { fmt.Fprintf(opts.progress, "    %s\n", fmt.Sprintf(format, a...)) }

	interactive := !opts.JSON && !opts.Yes && stdinIsTTY()

	// ── The single question block (§13: everything the human answers, ONCE,
	// at the start — never a question mid-flow) ──────────────────────────────
	askDocker := os.Getenv("DATABASE_URL") == "" && !opts.NoDocker
	askName := opts.Name == ""
	derived := deriveAppName()
	if interactive && (askDocker || askName) {
		fmt.Fprintln(opts.progress, "appximo up — two questions, then no more:")
		if askDocker {
			fmt.Fprintf(opts.progress, "  1) Postgres: no DATABASE_URL set. Start %s in Docker (container %q, port %d, data kept in a volume)? [Y/n] ",
				opts.PGImage, opts.PGContainer, opts.PGPort)
			if !readYes() {
				return fmt.Errorf("no Postgres, no app. Three ways forward:\n" +
					"  - set DATABASE_URL to a Postgres you already have (postgres://user:pass@host:5432/db)\n" +
					"  - re-run and answer Y to start one in Docker\n" +
					"  - use any hosted Postgres (Neon, Supabase, RDS…) and set DATABASE_URL to it")
			}
		}
		if askName {
			fmt.Fprintf(opts.progress, "  2) App name [%s]: ", derived)
			if v := readLine(); v != "" {
				opts.Name = v
			}
		}
	}

	// ── Name (tenant id): the flag/answer must satisfy THE rule; a derived
	// name is auto-sanitized through the same single source (T1) ─────────────
	if opts.Name == "" {
		opts.Name = derived
		step("app name: %q (from the directory name; override with --name)", opts.Name)
	}
	if !controlplane.ValidTenantID(opts.Name) {
		msg := fmt.Sprintf("app name %q is not a valid tenant id: must match ^[a-z][a-z0-9]{1,29}$ — it becomes BOTH the database schema (no hyphens) and the subdomain (no underscores), so only lowercase letters and digits work", opts.Name)
		if s := controlplane.SuggestTenantID(opts.Name); s != "" {
			msg += fmt.Sprintf("\n  try: appximo up --name %s", s)
		}
		return fmt.Errorf("%s", msg)
	}

	// ── Schema: --schema > ./schema.json > write the starter ─────────────────
	schemaPath, schemaRaw, wroteStarter, err := resolveUpSchema(opts.SchemaPath)
	if err != nil {
		return err
	}
	if wroteStarter {
		step("schema: wrote the starter to %s (todo-api — edit it, or pass --schema)", schemaPath)
	} else {
		step("schema: using %s", schemaPath)
	}
	rep := schema.ValidateReport(schemaRaw)
	if !rep.Valid {
		var b strings.Builder
		fmt.Fprintf(&b, "schema %s is invalid (%d error(s)):\n", schemaPath, len(rep.Errors))
		for _, e := range rep.Errors {
			fmt.Fprintf(&b, "  - %s: %s", e.Path, e.Message)
			if e.Fix != "" {
				fmt.Fprintf(&b, " → %s", e.Fix)
			}
			b.WriteString("\n")
		}
		b.WriteString("fix it (appximo validate --json " + schemaPath + " is the detailed oracle) and re-run")
		return fmt.Errorf("%s", b.String())
	}
	for _, w := range rep.Warnings {
		note("⚠ schema warning: %s: %s", w.Path, w.Message)
	}
	parsed, err := schema.LoadFromBytes(schemaRaw)
	if err != nil {
		return fmt.Errorf("schema %s: %w", schemaPath, err)
	}
	resources := sortedKeys(parsed.Resources)
	tokenRole := pickRole(parsed)
	if tokenRole == "" {
		note("⚠ the schema declares NO rbac roles — every API request will be denied (403). Add an rbac block; the starter shows the shape.")
	}

	// ── Ports, checked FIRST (fail fastest): a bind failure must be a named
	// error before Docker moves or the engine boot log starts ────────────────
	controlPortEarly := resolveControlPort(opts.ControlPort)
	for _, p := range []struct {
		port int
		what string
		fix  string
	}{
		{opts.Port, "app port", "--port"},
		{controlPortEarly, "control-plane port", "--control-port"},
	} {
		if err := portFree(p.port); err != nil {
			return fmt.Errorf("%s %d is already in use — another server (a previous `appximo up`?) owns it.\n"+
				"  find it:  ss -ltnp | grep :%d   (or lsof -i :%d on macOS)\n"+
				"  then stop it, or pick another port: appximo up %s %d",
				p.what, p.port, p.port, p.port, p.fix, p.port+1)
		}
	}

	// ── Postgres ─────────────────────────────────────────────────────────────
	pg, err := resolvePostgres(opts, step, note)
	if err != nil {
		return err
	}

	// ── Secrets + .env (written AND loaded — F1/F1-bis died here) ────────────
	jwtSecret, adminKey, envWrites, err := ensureEnvFile(pg.DSN)
	if err != nil {
		return err
	}
	if len(envWrites) > 0 {
		step("secrets: wrote %s to ./.env (0600, no BOM) — and loaded them into this process", strings.Join(envWrites, ", "))
	} else {
		step("secrets: reusing ./.env (DATABASE_URL, JWT_SECRET, ADMIN_KEY all present)")
	}

	// ── Boot the engine (this same process; the orchestration below drives its
	// REAL http surface — no second implementation of any step) ──────────────
	staticMounts, err := appximo.ParseStaticSpecs(opts.Static, opts.SPA)
	if err != nil {
		return err
	}
	app, err := appximo.New(appximo.Config{
		SchemaPath:      schemaPath,
		Port:            opts.Port,
		ControlPort:     opts.ControlPort,
		Version:         version,
		DebugTracesHTML: debugTracesHTML,
		Static:          staticMounts,
		BannerWriter:    io.Discard, // the card below replaces the banner; --json owns stdout
	})
	if err != nil {
		return err
	}
	controlPort := controlPortEarly

	admEmail := opts.AdminEmail
	if admEmail == "" {
		admEmail = "admin@" + opts.Name + ".local"
	}
	admPassword := opts.AdminPassword
	passwordGenerated := false
	if admPassword == "" {
		admPassword = randHex(10) // 20 chars ≥ the 12-char platform minimum
		passwordGenerated = true
	}

	// The setup goroutine waits for the listener, then drives the server's own
	// HTTP seams: register tenant → bootstrap admin → tenant user → token →
	// smoke → card. Start() blocks in the main goroutine (signals, drain and
	// self-restart behave exactly like `appximo serve`).
	go func() {
		fail := func(err error) {
			fmt.Fprintln(os.Stderr, "appximo up:", err)
			os.Exit(1)
		}
		if err := waitReady(opts.Port, 30*time.Second); err != nil {
			fail(err)
		}

		res := upResult{
			OK: true, Name: opts.Name, Host: opts.Name + ".localhost",
			Port: opts.Port, ControlPort: controlPort,
			Resources: resources, TokenRole: tokenRole,
			Files:    map[string]string{"env": ".env (0600)", "schema": schemaPath},
			Postgres: pg.card, Tenant: upTenant{ID: opts.Name},
		}
		base := fmt.Sprintf("http://%s:%d", res.Host, opts.Port)
		res.URLs = map[string]string{
			"app": base + "/app", "docs": base + "/docs", "admin": base + "/admin",
			"editor": base + "/editor", "api": base + "/api", "graphql": base + "/graphql",
		}
		for _, m := range staticMounts {
			key := "site"
			if m.Path != "/" {
				key = "site " + m.Path
			}
			res.URLs[key] = base + strings.TrimSuffix(m.Path, "/") + "/"
		}

		// 1. Tenant, with the schema in the body (T2 — the same POST /tenants
		// install.sh prints; a duplicate is the idempotent re-run, not an error).
		created, err := registerTenant(controlPort, adminKey, opts.Name, admEmail, schemaRaw)
		if err != nil {
			fail(err)
		}
		res.Tenant.Created = created
		if created {
			res.Tenant.Schema = "registered"
			step("tenant %q registered with the schema — its tables were just created", opts.Name)
		} else {
			step("tenant %q already registered — reusing it", opts.Name)
			// PUBLIC-SURFACE-S1 Part C: an existing tenant's REGISTERED schema is
			// the surface the engine serves (deployed wins over boot), so a changed
			// schema.json silently kept meant `ok: true` over the OLD rules — the
			// accepted-and-continues class, in the newest command. Reconcile: same
			// schema → say so; changed → migrate through the same additive-with-
			// gated-drops path `appximo migrate` uses; failure → loud, never ok.
			rec, rerr := reconcileSchema(fmt.Sprintf("http://127.0.0.1:%d", controlPort), adminKey, opts.Name, schemaPath, schemaRaw)
			if rerr != nil {
				fail(rerr)
			}
			res.Tenant.Schema = rec.state
			res.Tenant.GatedDrops = rec.gated
			switch {
			case rec.state == "unchanged":
				step("schema.json matches the registered schema — nothing to migrate")
			case len(rec.gated) > 0:
				step("schema.json changed — additive changes migrated live")
				note("⚠ %d destructive drop(s) stay GATED (no data was lost): %s", len(rec.gated), strings.Join(rec.gated, ", "))
				note("  review impact: appximo migrate --tenant %s --schema %s --dry-run", opts.Name, schemaPath)
				note("  then approve:  appximo migrate --tenant %s --schema %s --approve-drops '%s'", opts.Name, schemaPath, strings.Join(rec.gated, ","))
			default:
				step("schema.json changed — migrated to the registered tenant (tables + live surface updated)")
			}
		}

		// 2. First admin (B8): the /admin bootstrap route, key-gated. 409 = an
		// admin exists from an earlier run — reuse, password not reprintable.
		bootstrappedNow, err := bootstrapAdmin(opts.Port, adminKey, admEmail, admPassword)
		if err != nil {
			fail(err)
		}
		// 3. A tenant user with the SAME credentials (so /app and /admin open
		// with one remembered pair). 409 = it exists from an earlier run.
		userCreatedNow := false
		if tokenRole != "" {
			userCreatedNow, err = createTenantUser(opts.Port, adminKey, opts.Name, admEmail, admPassword, tokenRole)
			if err != nil {
				note("⚠ tenant user not created: %v (create one in /admin → Users)", err)
			}
		}
		worksIn := ""
		switch {
		case bootstrappedNow && userCreatedNow:
			step("first admin created: %s — password printed ONCE below (works in /app and /admin)", admEmail)
			worksIn = "works in /app and /admin"
		case userCreatedNow:
			step("app user created: %s — password printed ONCE below (for /app; /admin keeps the credentials from the run that created it)", admEmail)
			worksIn = "works in /app (this tenant); /admin keeps its original credentials"
		case bootstrappedNow:
			step("platform admin created: %s (for /admin)", admEmail)
			worksIn = "works in /admin"
		default:
			step("admin and app user already exist from an earlier run — sign in with the credentials it printed then")
		}
		showPassword := bootstrappedNow || userCreatedNow || !passwordGenerated
		res.Credentials = &upCredentials{Email: admEmail, PrintedOnce: showPassword, Note: worksIn}
		if showPassword {
			res.Credentials.Password = admPassword
		} else {
			res.Credentials.Note = "created in an earlier run; that run printed the password once"
		}

		// 4. Dev token (the same mint `appximo token` does).
		if tokenRole != "" {
			tok, terr := auth.GenerateToken(auth.Claims{UserID: "dev", Role: tokenRole, TenantID: opts.Name}, jwtSecret)
			if terr != nil {
				fail(fmt.Errorf("mint dev token: %w", terr))
			}
			res.Token = tok
			step("dev token minted (role %s, 24 h)", tokenRole)
		}

		// 5. Smoke: one real request through the whole chain (Host → JWT → RBAC
		// → SQL), so the card's claims are verified, not assumed.
		if len(resources) > 0 && res.Token != "" {
			smoke := runSmoke(opts.Port, res.Host, res.Token, resources[0])
			res.Smoke = &smoke
			if smoke.Passed {
				step("verified: %s answered 200 through the full chain", smoke.Request)
			} else {
				note("⚠ smoke check failed: %s → %s", smoke.Request, smoke.Detail)
			}
			res.ExampleCurl = exampleCurl(parsed, resources[0], res.Host, opts.Port)
		}

		if opts.JSON {
			enc := json.NewEncoder(os.Stdout)
			enc.SetIndent("", "  ")
			_ = enc.Encode(res)
		} else {
			printCard(opts.progress, res, pg, wroteStarter)
		}
	}()

	return app.Start()
}

// ── steps ────────────────────────────────────────────────────────────────────

// resolveSchema picks the schema file: the explicit flag, an existing
// ./schema.json, or the embedded starter written to ./schema.json.
func resolveUpSchema(flag string) (path string, raw []byte, wroteStarter bool, err error) {
	if flag != "" {
		raw, err = os.ReadFile(flag)
		if err != nil {
			return "", nil, false, fmt.Errorf("cannot read --schema %s: %v\n  (the path is relative to the current directory)", flag, err)
		}
		return flag, raw, false, nil
	}
	if raw, err = os.ReadFile("schema.json"); err == nil {
		return "schema.json", raw, false, nil
	}
	raw = appximo.StarterSchema()
	if err = os.WriteFile("schema.json", raw, 0o644); err != nil {
		return "", nil, false, fmt.Errorf("cannot write ./schema.json: %w", err)
	}
	return "schema.json", raw, true, nil
}

type pgResolved struct {
	DSN  string
	card upPostgres
}

// resolvePostgres returns a CONNECTABLE DSN: the environment's DATABASE_URL if
// set, else a Docker postgres it starts (or reuses — the password is recovered
// from the container env, so a lost .env is not a dead end).
func resolvePostgres(opts upOptions, step func(string, ...any), note func(string, ...any)) (pgResolved, error) {
	if dsn := os.Getenv("DATABASE_URL"); dsn != "" {
		if err := waitPostgres(dsn, 5*time.Second); err != nil {
			return pgResolved{}, fmt.Errorf("DATABASE_URL is set but not connectable: %v\n"+
				"  - is that Postgres running? (the value came from the environment or ./.env)\n"+
				"  - fix the value, or unset DATABASE_URL to let `up` start one in Docker", err)
		}
		step("postgres: using DATABASE_URL from the environment")
		return pgResolved{DSN: dsn, card: upPostgres{Mode: "external"}}, nil
	}
	if opts.NoDocker {
		return pgResolved{}, fmt.Errorf("--no-docker is set and DATABASE_URL is not. Three ways forward:\n" +
			"  - set DATABASE_URL to a Postgres you already have (postgres://user:pass@host:5432/db)\n" +
			"  - drop --no-docker and let `up` start postgres:16 in Docker\n" +
			"  - use any hosted Postgres (Neon, Supabase, RDS…) and set DATABASE_URL to it")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		return pgResolved{}, fmt.Errorf("no DATABASE_URL and no `docker` on the PATH — `up` needs one of the two.\n" +
			"  - install Docker (https://docs.docker.com/get-docker/) and re-run, or\n" +
			"  - set DATABASE_URL to any Postgres (local install or hosted: Neon, Supabase, RDS…)")
	}
	if out, err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").CombinedOutput(); err != nil {
		s := string(out)
		switch {
		case strings.Contains(s, "permission denied"):
			return pgResolved{}, fmt.Errorf("docker is installed but this user may not use it (permission denied on the socket).\n"+
				"  - add yourself to the docker group: sudo usermod -aG docker $USER  (then log out and back in)\n"+
				"  - or run this command with sudo\n  - or set DATABASE_URL to skip Docker entirely\n  docker said: %s", firstLine(s))
		case strings.Contains(s, "Cannot connect") || strings.Contains(s, "daemon"):
			return pgResolved{}, fmt.Errorf("docker is installed but the daemon is not running.\n"+
				"  - start it (Docker Desktop, or: sudo systemctl start docker)\n"+
				"  - or set DATABASE_URL to skip Docker entirely\n  docker said: %s", firstLine(s))
		default:
			return pgResolved{}, fmt.Errorf("docker is not usable: %s\n  - fix Docker, or set DATABASE_URL to skip it", firstLine(s))
		}
	}

	name := opts.PGContainer
	volume := name + "-data"
	card := upPostgres{Mode: "docker", Container: name, Volume: volume}

	// A container from a previous run? Reuse it — recovering the password and
	// the published port from the container itself (docker access can read the
	// env anyway, so this adds no exposure and removes a dead end).
	if state, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", name).Output(); err == nil {
		running := strings.TrimSpace(string(state)) == "true"
		if !running {
			if out, serr := exec.Command("docker", "start", name).CombinedOutput(); serr != nil {
				return pgResolved{}, fmt.Errorf("container %q exists but cannot start: %s\n"+
					"  - inspect it: docker logs --tail 20 %s\n  - or remove it and its data and re-run: appximo down --pg-container %s --destroy-data",
					name, firstLine(string(out)), name, name)
			}
		}
		dsn, port, rerr := recoverContainerDSN(name)
		if rerr != nil {
			// Label-aware way out: `down` only touches containers `up` created,
			// so suggesting it for a FOREIGN container would dead-end (its
			// refusal is the next error the user would see).
			lbl, _ := exec.Command("docker", "inspect", "-f", `{{index .Config.Labels "com.appximo.up"}}`, name).Output()
			if strings.TrimSpace(string(lbl)) == "1" {
				return pgResolved{}, fmt.Errorf("container %q was created by `appximo up` but its connection cannot be recovered (%v).\n"+
					"  - remove it and its data and re-run: appximo down --pg-container %s --destroy-data\n"+
					"  - or set DATABASE_URL yourself", name, rerr, name)
			}
			return pgResolved{}, fmt.Errorf("a container named %q exists but was NOT created by `appximo up` (%v).\n"+
				"  - pick a different name: appximo up --pg-container %s2\n"+
				"  - or, if it IS a Postgres you own, set DATABASE_URL to point at it", name, rerr, name)
		}
		card.HostPort = port
		if err := waitPostgres(dsn, 60*time.Second); err != nil {
			return pgResolved{}, fmt.Errorf("container %q is up but Postgres is not answering: %v\n  docker logs --tail 20 %s", name, err, name)
		}
		if running {
			step("postgres: reusing running container %q (port %d, volume %s)", name, port, volume)
		} else {
			step("postgres: restarted container %q (port %d, volume %s — your data survived)", name, port, volume)
		}
		return pgResolved{DSN: dsn, card: card}, nil
	}

	// Fresh container. Loopback-published (field report I1: Docker bypasses
	// ufw — 127.0.0.1 keeps the dev database off the network).
	if err := portFree(opts.PGPort); err != nil {
		return pgResolved{}, fmt.Errorf("host port %d for Postgres is already in use.\n"+
			"  - find it:  ss -ltnp | grep :%d\n  - or pick another: appximo up --pg-port %d", opts.PGPort, opts.PGPort, opts.PGPort+1)
	}
	if err := exec.Command("docker", "image", "inspect", opts.PGImage).Run(); err != nil {
		note("pulling %s (first time only, ~150 MB — this is Docker downloading, not appximo)…", opts.PGImage)
		pull := exec.Command("docker", "pull", opts.PGImage)
		pull.Stdout, pull.Stderr = os.Stderr, os.Stderr
		if err := pull.Run(); err != nil {
			return pgResolved{}, fmt.Errorf("docker pull %s failed (network?) — re-run when it can download, or set DATABASE_URL", opts.PGImage)
		}
	}
	password := randHex(16)
	args := []string{"run", "-d", "--name", name, "--label", "com.appximo.up=1",
		"-v", volume + ":/var/lib/postgresql/data",
		"-p", fmt.Sprintf("127.0.0.1:%d:5432", opts.PGPort),
		"-e", "POSTGRES_USER=appximo", "-e", "POSTGRES_PASSWORD=" + password, "-e", "POSTGRES_DB=appximo",
		opts.PGImage}
	if out, err := exec.Command("docker", args...).CombinedOutput(); err != nil {
		return pgResolved{}, fmt.Errorf("docker run failed: %s\n  - inspect: docker logs %s\n  - clean up and retry: appximo down --pg-container %s --destroy-data", firstLine(string(out)), name, name)
	}
	dsn := fmt.Sprintf("postgres://appximo:%s@127.0.0.1:%d/appximo", password, opts.PGPort)
	if err := waitPostgres(dsn, 90*time.Second); err != nil {
		logs, _ := exec.Command("docker", "logs", "--tail", "10", name).CombinedOutput()
		return pgResolved{}, fmt.Errorf("started container %q but Postgres never became ready: %v\n  last log lines:\n%s", name, err, indent(string(logs)))
	}
	card.HostPort = opts.PGPort
	step("postgres: started %s in Docker — container %q, port 127.0.0.1:%d, data in volume %s", opts.PGImage, name, opts.PGPort, volume)
	return pgResolved{DSN: dsn, card: card}, nil
}

// recoverContainerDSN rebuilds the DSN of an `up`-created container from the
// container itself: POSTGRES_* from its env, the host port from its bindings.
func recoverContainerDSN(name string) (string, int, error) {
	envOut, err := exec.Command("docker", "inspect", "-f", "{{range .Config.Env}}{{println .}}{{end}}", name).Output()
	if err != nil {
		return "", 0, fmt.Errorf("docker inspect: %v", err)
	}
	vars := map[string]string{}
	for _, line := range strings.Split(string(envOut), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			vars[k] = v
		}
	}
	user, pass, db := vars["POSTGRES_USER"], vars["POSTGRES_PASSWORD"], vars["POSTGRES_DB"]
	if user == "" || pass == "" || db == "" {
		return "", 0, fmt.Errorf("container has no POSTGRES_USER/PASSWORD/DB env (not created by `appximo up`?)")
	}
	portOut, err := exec.Command("docker", "inspect", "-f",
		`{{(index (index .NetworkSettings.Ports "5432/tcp") 0).HostPort}}`, name).Output()
	if err != nil {
		return "", 0, fmt.Errorf("container publishes no host port for 5432 (docker inspect: %v)", err)
	}
	port := 0
	if _, err := fmt.Sscanf(strings.TrimSpace(string(portOut)), "%d", &port); err != nil || port == 0 {
		return "", 0, fmt.Errorf("cannot read the published port %q", strings.TrimSpace(string(portOut)))
	}
	return fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s", user, pass, port, db), port, nil
}

// ensureEnvFile guarantees DATABASE_URL, JWT_SECRET and ADMIN_KEY exist in the
// process env AND in ./.env (0600, created or appended — existing lines are
// never rewritten). Returns the effective secret pair and which keys it wrote.
func ensureEnvFile(dsn string) (jwtSecret, adminKey string, wrote []string, err error) {
	jwtSecret = os.Getenv("JWT_SECRET")
	if jwtSecret == "" {
		jwtSecret = randHex(32)
	}
	adminKey = os.Getenv("ADMIN_KEY")
	if adminKey == "" {
		adminKey = randHex(16)
	}

	existing := map[string]bool{}
	var current []byte
	if current, err = os.ReadFile(".env"); err == nil {
		for _, line := range strings.Split(string(current), "\n") {
			if k, _, ok := strings.Cut(strings.TrimSpace(strings.TrimPrefix(line, "\uFEFF")), "="); ok && !strings.HasPrefix(k, "#") {
				existing[strings.TrimSpace(k)] = true
			}
		}
	}

	var add strings.Builder
	if len(current) == 0 {
		add.WriteString("# Written by `appximo up` — the three variables the engine requires.\n")
		add.WriteString("# The real environment wins over this file on conflict.\n")
	}
	for _, kv := range [][2]string{{"DATABASE_URL", dsn}, {"JWT_SECRET", jwtSecret}, {"ADMIN_KEY", adminKey}} {
		if !existing[kv[0]] {
			fmt.Fprintf(&add, "%s=%s\n", kv[0], kv[1])
			wrote = append(wrote, kv[0])
		}
	}
	if add.Len() > 0 {
		if len(current) > 0 && !bytes.HasSuffix(current, []byte("\n")) {
			current = append(current, '\n')
		}
		if err := os.WriteFile(".env", append(current, []byte(add.String())...), 0o600); err != nil {
			return "", "", nil, fmt.Errorf("cannot write ./.env: %w", err)
		}
	}
	// Loaded into THIS process (F1's whole point): the engine boot below reads
	// the same values a later plain `appximo serve` will read from the file.
	_ = os.Setenv("DATABASE_URL", dsn)
	_ = os.Setenv("JWT_SECRET", jwtSecret)
	_ = os.Setenv("ADMIN_KEY", adminKey)
	return jwtSecret, adminKey, wrote, nil
}

// ── the in-process orchestration calls (the server's own HTTP seams) ─────────

func waitReady(port int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	url := fmt.Sprintf("http://127.0.0.1:%d/readyz", port)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(url); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("the engine did not become ready on :%d within %s — its log above says why", port, timeout)
}

func registerTenant(controlPort int, adminKey, name, email string, schemaRaw []byte) (created bool, err error) {
	body, _ := json.Marshal(map[string]any{
		"tenant_id": name, "display_name": name, "email": email, "plan": "dev",
		"schema": json.RawMessage(schemaRaw),
	})
	status, respBody, err := doJSON(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/tenants", controlPort),
		map[string]string{"X-Admin-Key": adminKey}, body)
	if err != nil {
		return false, fmt.Errorf("register tenant: %v", err)
	}
	switch status {
	case http.StatusCreated, http.StatusOK:
		return true, nil
	case http.StatusConflict:
		return false, nil // idempotent re-run
	default:
		return false, fmt.Errorf("register tenant %q failed (%d): %s", name, status, firstLine(string(respBody)))
	}
}

// schemaReconcile is the verdict of comparing schema.json against the tenant's
// REGISTERED schema on a re-run: "unchanged" (nothing to do) or "migrated" (the
// PUT ran; gated lists any destructive drops that stayed gated — no data lost).
type schemaReconcile struct {
	state string
	gated []string
}

// schemasEquivalent compares two schema documents structurally (key order and
// whitespace are serialization noise, not schema changes).
func schemasEquivalent(a, b []byte) bool {
	var av, bv any
	if json.Unmarshal(a, &av) != nil || json.Unmarshal(b, &bv) != nil {
		return false
	}
	return reflect.DeepEqual(av, bv)
}

// reconcileSchema makes a re-run honest about the schema (PUBLIC-SURFACE-S1
// Part C): it reads the tenant's registered schema back from the control plane
// and, when schema.json differs, applies it through the SAME PUT
// /tenants/{id}/schema migration path `appximo migrate` drives (additive
// applies live + notifies the running engine; destructive drops stay gated and
// are reported, never auto-approved). Any failure is an error the caller must
// surface — a changed schema silently kept behind `ok: true` is the bug this
// closes.
func reconcileSchema(baseURL, adminKey, name, schemaPath string, schemaRaw []byte) (schemaReconcile, error) {
	hdr := map[string]string{"X-Admin-Key": adminKey}
	status, stored, err := doJSON(http.MethodGet, fmt.Sprintf("%s/tenants/%s/schema", baseURL, name), hdr, nil)
	if err != nil {
		return schemaReconcile{}, fmt.Errorf("read the tenant's registered schema: %v", err)
	}
	if status == http.StatusOK && schemasEquivalent(stored, schemaRaw) {
		return schemaReconcile{state: "unchanged"}, nil
	}

	putBody, _ := json.Marshal(map[string]any{"schema": json.RawMessage(schemaRaw)})
	status, resp, err := doJSON(http.MethodPut, fmt.Sprintf("%s/tenants/%s/schema", baseURL, name), hdr, putBody)
	if err != nil {
		return schemaReconcile{}, fmt.Errorf("migrate the changed schema: %v", err)
	}
	if status != http.StatusOK {
		return schemaReconcile{}, fmt.Errorf(
			"schema.json differs from the registered schema and the migration FAILED (%d): %s — the tenant keeps its previous schema; fix the schema, or inspect with: appximo migrate --tenant %s --schema %s --dry-run",
			status, firstLine(string(resp)), name, schemaPath)
	}
	var out struct {
		Gated []string `json:"gated_drops"`
	}
	_ = json.Unmarshal(resp, &out)
	return schemaReconcile{state: "migrated", gated: out.Gated}, nil
}

func bootstrapAdmin(port int, adminKey, email, password string) (createdNow bool, err error) {
	body, _ := json.Marshal(map[string]string{"email": email, "password": password})
	status, respBody, err := doJSON(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/admin/auth/bootstrap", port),
		map[string]string{"X-Admin-Key": adminKey}, body)
	if err != nil {
		return false, fmt.Errorf("bootstrap admin: %v", err)
	}
	switch status {
	case http.StatusCreated:
		return true, nil
	case http.StatusConflict:
		return false, nil // an admin exists from an earlier run
	default:
		return false, fmt.Errorf("bootstrap admin failed (%d): %s", status, firstLine(string(respBody)))
	}
}

func createTenantUser(port int, adminKey, tenant, email, password, role string) (createdNow bool, err error) {
	body, _ := json.Marshal(map[string]string{"email": email, "password": password, "role": role})
	status, respBody, err := doJSON(http.MethodPost, fmt.Sprintf("http://127.0.0.1:%d/admin/tenants/%s/users", port, tenant),
		map[string]string{"X-Admin-Key": adminKey}, body)
	if err != nil {
		return false, err
	}
	switch status {
	case http.StatusCreated:
		return true, nil
	case http.StatusConflict:
		return false, nil // same email from an earlier run — password unchanged
	default:
		return false, fmt.Errorf("HTTP %d: %s", status, firstLine(string(respBody)))
	}
}

func runSmoke(port int, host, token, resource string) upSmoke {
	req, _ := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/%s?per_page=1", port, resource), nil)
	req.Host = host
	req.Header.Set("Authorization", "Bearer "+token)
	smoke := upSmoke{Request: fmt.Sprintf("GET /api/%s", resource)}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		smoke.Detail = err.Error()
		return smoke
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		smoke.Passed = true
		return smoke
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	smoke.Detail = fmt.Sprintf("HTTP %d: %s", resp.StatusCode, firstLine(string(b)))
	return smoke
}

func doJSON(method, url string, headers map[string]string, body []byte) (int, []byte, error) {
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, b, nil
}

// ── the card ─────────────────────────────────────────────────────────────────

func printCard(w io.Writer, res upResult, pg pgResolved, wroteStarter bool) {
	line := strings.Repeat("─", 62)
	fmt.Fprintf(w, "\n%s\n  Your app is running.\n\n", line)
	fmt.Fprintf(w, "  App      %s      ← create & edit records\n", res.URLs["app"])
	fmt.Fprintf(w, "  Docs     %s     (interactive API explorer)\n", res.URLs["docs"])
	fmt.Fprintf(w, "  Admin    %s    (tenants, users, observability)\n", res.URLs["admin"])
	fmt.Fprintf(w, "  Editor   %s   (visual schema editor)\n", res.URLs["editor"])
	if res.Credentials != nil {
		fmt.Fprintf(w, "\n  Sign in (%s)", res.Credentials.Note)
		if res.Credentials.Password != "" {
			fmt.Fprintf(w, " — printed ONCE, save it now:\n")
			fmt.Fprintf(w, "    email     %s\n    password  %s\n", res.Credentials.Email, res.Credentials.Password)
		} else {
			fmt.Fprintf(w, ":\n    email     %s\n", res.Credentials.Email)
		}
	}
	if res.Token != "" {
		fmt.Fprintf(w, "\n  Dev API token (role %s, 24 h):\n    %s\n", res.TokenRole, res.Token)
	}
	if res.ExampleCurl != "" {
		fmt.Fprintf(w, "\n  Try it from a second terminal:\n    %s\n", res.ExampleCurl)
	}
	fmt.Fprintf(w, "\n  Wrote  ./.env (secrets, 0600)")
	if wroteStarter {
		fmt.Fprintf(w, "  ·  ./schema.json (the starter — make it YOURS, then\n         appximo migrate --tenant %s --schema schema.json)", res.Name)
	}
	fmt.Fprintln(w)
	if pg.card.Mode == "docker" {
		fmt.Fprintf(w, "  Postgres  docker container %q (127.0.0.1:%d, volume %s)\n", pg.card.Container, pg.card.HostPort, pg.card.Volume)
	}
	fmt.Fprintf(w, "  Stop the server: Ctrl+C · Stop the Docker Postgres too: appximo down\n%s\n", line)
}

// exampleCurl builds one COPY-PASTE request that will actually succeed: a POST
// when every required field of the resource can be fabricated from its type, a
// GET otherwise. The Host form works on every OS (RFC 6761 aside, curl on some
// Linux resolvers won't resolve *.localhost — the explicit header always works).
func exampleCurl(s *schema.APISchema, resource, host string, port int) string {
	res := s.Resources[resource]
	sample := map[string]any{}
	ok := true
	for name, f := range res.Fields {
		if !f.Required || f.Auto {
			continue
		}
		switch {
		case len(f.Enum) > 0:
			sample[name] = f.Enum[0]
		case f.Type == "string" || f.Type == "text":
			v := "hello appximo"
			if f.Format == "email" {
				v = "a@example.com"
			}
			if f.Format == "uuid" || f.Format == "url" || f.Format == "date" || f.Pattern != "" {
				ok = false
			}
			sample[name] = v
		case f.Type == "int" || f.Type == "int64":
			sample[name] = 1
		case f.Type == "float64":
			sample[name] = 1.5
		case f.Type == "bool":
			sample[name] = true
		case f.Type == "time":
			sample[name] = time.Now().UTC().Format(time.RFC3339)
		default: // uuid / file / relation / json — nothing safe to invent
			ok = false
		}
	}
	authPart := fmt.Sprintf("-H 'Authorization: Bearer TOKEN' -H 'Host: %s'", host)
	if !ok || len(sample) == 0 {
		return fmt.Sprintf("curl %s http://localhost:%d/api/%s   (replace TOKEN with the dev token above)", authPart, port, resource)
	}
	body, _ := json.Marshal(sample)
	return fmt.Sprintf("curl %s -H 'Content-Type: application/json' -d '%s' http://localhost:%d/api/%s   (replace TOKEN with the dev token above)",
		authPart, string(body), port, resource)
}

// ── small helpers ────────────────────────────────────────────────────────────

func stdinIsTTY() bool {
	st, err := os.Stdin.Stat()
	return err == nil && st.Mode()&os.ModeCharDevice != 0
}

func readLine() string {
	var s string
	_, _ = fmt.Scanln(&s)
	return strings.TrimSpace(s)
}

func readYes() bool {
	s := strings.ToLower(readLine())
	return s == "" || s == "y" || s == "yes" || s == "s" || s == "si" || s == "sí"
}

// deriveAppName sanitizes the working directory's basename through the SAME
// rule the control plane enforces (T1 single source); "app" when nothing
// salvageable remains.
func deriveAppName() string {
	wd, err := os.Getwd()
	if err != nil {
		return "app"
	}
	if s := controlplane.SuggestTenantID(filepath.Base(wd)); s != "" {
		return s
	}
	return "app"
}

// pickRole chooses the role for the dev token + the first tenant user. These
// are the OPERATOR's credentials — the person who just ran `up` and has to be
// able to manage their own app — so the choice is the MOST PRIVILEGED role the
// schema declares, never an arbitrary one.
//
// It used to be "admin" by name, else the ALPHABETICALLY FIRST role. That
// silently handed the operator the least-privileged identity whenever the
// schema named its roles differently: a library schema with {member, staff}
// got `member`, so the printed token could not write the app's own main
// resource and the printed credentials could not manage anything (found by a
// third-party agent following the master prompt, LAUNCHPAD-S1).
//
// Order: "admin" by name → a role with full access (resources "*" + actions
// "*") → the widest grant surface → alphabetical, as the last deterministic
// tie-break.
func pickRole(s *schema.APISchema) string {
	if _, ok := s.RBAC.Roles["admin"]; ok {
		return "admin"
	}
	roles := make([]string, 0, len(s.RBAC.Roles))
	for r := range s.RBAC.Roles {
		roles = append(roles, r)
	}
	if len(roles) == 0 {
		return ""
	}
	sort.Strings(roles) // alphabetical first, so every tie below breaks deterministically
	best, bestScore := roles[0], -1
	for _, name := range roles {
		if sc := roleBreadth(s.RBAC.Roles[name], s); sc > bestScore {
			best, bestScore = name, sc
		}
	}
	return best
}

// roleBreadth scores how much of the app a role can reach, for pickRole's
// "most privileged wins". It is a heuristic for choosing a DEV credential —
// never an authorization decision (the engine's RBAC evaluator is the only
// authority for that).
func roleBreadth(r schema.RolePolicy, s *schema.APISchema) int {
	wildcardActions := func(actions []string) bool {
		for _, a := range actions {
			if a == "*" {
				return true
			}
		}
		return false
	}
	// A row condition or a field allowlist means the role sees only part of
	// its resources — a worse operator credential than an unrestricted one.
	const fullAccess = 1 << 20
	if len(r.Permissions) == 0 {
		var wildcardRes string
		if json.Unmarshal(r.Resources, &wildcardRes) == nil && wildcardRes == "*" {
			if wildcardActions(r.Actions) && r.Conditions == nil && len(r.Fields) == 0 {
				return fullAccess
			}
		}
		var named []string
		_ = json.Unmarshal(r.Resources, &named)
		if wildcardRes == "*" {
			named = sortedResourceNames(s)
		}
		score := len(named) * actionWeight(r.Actions)
		if r.Conditions != nil {
			score /= 2
		}
		if len(r.Fields) > 0 {
			score /= 2
		}
		return score
	}
	score := 0
	for _, p := range r.Permissions {
		s := actionWeight(p.Actions)
		if p.Conditions != nil {
			s /= 2
		}
		if len(p.Fields) > 0 {
			s /= 2
		}
		score += s
	}
	return score
}

// actionWeight counts what a grant may do; "*" counts as all four actions.
func actionWeight(actions []string) int {
	w := 0
	for _, a := range actions {
		if a == "*" {
			return 4
		}
		w++
	}
	return w
}

func sortedResourceNames(s *schema.APISchema) []string {
	names := make([]string, 0, len(s.Resources))
	for n := range s.Resources {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func resolveControlPort(flag int) int {
	if flag != 0 {
		return flag
	}
	if v := os.Getenv("APPXIMO_CONTROL_PORT"); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil && n > 0 {
			return n
		}
	}
	return 9090
}

func portFree(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
	if err != nil {
		return err
	}
	return ln.Close()
}

func waitPostgres(dsn string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		conn, err := pgx.Connect(ctx, dsn)
		if err == nil {
			err = conn.Ping(ctx)
			_ = conn.Close(ctx)
		}
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("not reachable after %s (last error: %v)", timeout, lastErr)
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func indent(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := range lines {
		lines[i] = "    " + lines[i]
	}
	return strings.Join(lines, "\n")
}
