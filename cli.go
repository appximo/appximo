package appximo

import (
	"flag"
	"fmt"
	"os"
)

// This file is the library half of the DEPLOYABLE-BINARY CONTRACT (ADR-023,
// CONSUMER-PATH-S1). The official production tooling (scripts/install.sh,
// deploy-update.sh, the generated systemd unit) drives any Appximo binary —
// the engine or a consumer app — through two entry points:
//
//	<bin> version                      → identity, exit 0
//	<bin> serve --schema P --port N    → run the server
//
// A consumer main that hand-rolls flag parsing falls into a silent trap: Go's
// flag package STOPS at the first non-flag argument, so `myapp serve --schema x
// --port 8090` silently discards every flag and boots with defaults — the
// failure surfaces later as "schema.json not found" with no hint (measured in
// PROD-JOURNEY-1B). ParseServeArgs implements the contract once, fail-LOUD:
// an unrecognized subcommand or a leftover argument aborts with an actionable
// message, never a silent default.

// ServeArgs is the parsed serve configuration a consumer main feeds into
// Config (SchemaPath/Port/ControlPort). Zero values in the defaults you pass
// are replaced by the engine's conventions (schema.json / 8080 / 9090).
type ServeArgs struct {
	SchemaPath  string
	Port        int
	ControlPort int

	// Static holds "[urlpath=]dir" mount specs from --static (repeatable), and
	// SPA the --spa flag (PUBLIC-SURFACE-S1 Part A) — feed them through
	// ParseStaticSpecs into Config.Static:
	//
	//	mounts, err := appximo.ParseStaticSpecs(args.Static, args.SPA)
	//	… Config{Static: mounts}
	//
	// A binary that wires its frontend with go:embed simply ignores them.
	Static []string
	SPA    bool
}

// stringArrayFlag collects a repeatable string flag ("--static a --static b").
type stringArrayFlag []string

func (f *stringArrayFlag) String() string { return fmt.Sprint([]string(*f)) }
func (f *stringArrayFlag) Set(v string) error {
	*f = append(*f, v)
	return nil
}

// ParseServeArgs processes os.Args for a consumer binary per the deployable
// contract (ADR-023):
//
//   - `version` (also -v / --version) prints
//     "<name> <version> (commit <revision>) — built on the appximo framework"
//     and exits 0 — install.sh identity-checks this, deploy-update.sh
//     sanity-checks it, and /health should report the same string's version
//     (pass it in Config.Version).
//   - a leading `serve` is accepted and skipped (the systemd unit the installer
//     writes runs `<bin> serve --schema … --port …`).
//   - any OTHER leading word, and any argument left over after the flags, is a
//     HARD error (exit 2) naming the offending argument — never a silent boot
//     with defaults.
//
// Typical consumer main:
//
//	var version, revision = "dev", "unknown"   // ldflags -X main.version=…
//
//	func main() {
//		args := appximo.ParseServeArgs("myapp", version, revision,
//			appximo.ServeArgs{Port: 8099, ControlPort: 9099})
//		app, err := appximo.New(appximo.Config{
//			SchemaPath: args.SchemaPath, Port: args.Port,
//			ControlPort: args.ControlPort, Version: version,
//		})
//		…
//	}
func ParseServeArgs(name, version, revision string, defaults ServeArgs) ServeArgs {
	// F1: a consumer binary gets the same .env fill-in as the engine CLI —
	// a `.env` in the working directory is loaded, the real environment wins.
	LoadDotEnv()
	out, versionLine, err := parseServeArgs(name, version, revision, defaults, os.Args[1:])
	if versionLine != "" {
		fmt.Println(versionLine)
		os.Exit(0)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", name, err)
		os.Exit(2)
	}
	return out
}

// parseServeArgs is the pure core (unit-tested; the wrapper owns os.Exit).
// versionLine != "" means "print it and exit 0".
func parseServeArgs(name, version, revision string, defaults ServeArgs, args []string) (ServeArgs, string, error) {
	if defaults.SchemaPath == "" {
		defaults.SchemaPath = "schema.json"
	}
	if defaults.Port == 0 {
		defaults.Port = 8080
	}
	if defaults.ControlPort == 0 {
		defaults.ControlPort = 9090
	}

	if len(args) > 0 {
		switch args[0] {
		case "version", "-v", "--version":
			return ServeArgs{}, fmt.Sprintf("%s %s (commit %s) — built on the appximo framework", name, version, revision), nil
		case "serve":
			args = args[1:]
		default:
			if len(args[0]) > 0 && args[0][0] != '-' {
				return ServeArgs{}, "", fmt.Errorf(
					"unknown subcommand %q — this binary serves one app; it accepts `version`, or `serve --schema <path> --port <n> [--control-port <n>]` (ADR-023). Operational commands (tenant, migrate, token, admin) live in the engine CLI (appximo-cli)",
					args[0])
			}
		}
	}

	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	schemaPath := fs.String("schema", defaults.SchemaPath, "path to the schema JSON")
	port := fs.Int("port", defaults.Port, "data-plane port")
	controlPort := fs.Int("control-port", defaults.ControlPort, "control-plane port (keep it loopback-only)")
	static := stringArrayFlag(defaults.Static)
	fs.Var(&static, "static", "serve a frontend from this binary: [urlpath=]dir (repeatable)")
	spa := fs.Bool("spa", defaults.SPA, "SPA fallback for --static mounts (serve index.html for unmatched paths)")
	if err := fs.Parse(args); err != nil {
		return ServeArgs{}, "", err
	}
	// The trap this contract exists to close: an argument the flag package left
	// over means SOMETHING was misplaced — booting with silent defaults here is
	// how a unit "starts fine" pointing at the wrong schema.
	if fs.NArg() > 0 {
		return ServeArgs{}, "", fmt.Errorf(
			"unexpected argument %q after the flags — every option must be a --flag (Go's flag parsing stops at the first bare word, so a misplaced argument would silently discard the rest). Got: %v",
			fs.Arg(0), args)
	}
	return ServeArgs{SchemaPath: *schemaPath, Port: *port, ControlPort: *controlPort, Static: static, SPA: *spa}, "", nil
}
