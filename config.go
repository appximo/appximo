// Package appitools is the public library surface of the Appitools engine
// (ADR-016 "Appitools as a Go library"). A developer imports this package,
// builds the engine with New, registers custom Class-1 handlers with
// (*App).Register, and runs it with (*App).Start — compiling a single static
// CGO-free binary. The pure binary that ships is exactly this with zero
// registered handlers.
//
// EXPERIMENTAL: per ADR-016 Decision 5 the extension surface — Ctx, Claims,
// Route, Config, Handler, New, Register — will be frozen at the v1 major
// boundary. Until that promotion the interface may change between minor
// versions. Treat `grep UnsafeTx` as the complete audit of RBAC-bypass sites.
package appitools

// Config configures a New engine. SchemaPath and DSN are the only required
// fields; everything else falls back to the same defaults and environment
// variables the `appitools serve` command has always used, so the pure binary
// and a custom binary boot identically.
type Config struct {
	// SchemaPath is the path to the schema JSON compiled at boot.
	SchemaPath string
	// DSN is the PostgreSQL connection string. Empty falls back to DATABASE_URL.
	DSN string

	// Port is the data-plane HTTP port. 0 falls back to 8080.
	Port int

	// JWTSecret signs/validates HS256 tokens. Empty falls back to JWT_SECRET.
	JWTSecret string
	// AdminKey gates the control plane (:9090) and /metrics, /debug, /admin on
	// the data plane. Empty falls back to ADMIN_KEY.
	AdminKey string

	// Env mirrors APPITOOLS_ENV ("development" enables GraphiQL + pprof).
	// Empty falls back to the APPITOOLS_ENV environment variable.
	Env string

	// Version is reported by /health and the synthetic monitor. Empty reports
	// "dev"; the cmd binary passes its ldflags-injected build version.
	Version string

	// DebugTracesHTML is the embedded /debug/traces explorer page. The cmd
	// binary injects its go:embed'd asset here; when nil the visual route is
	// not mounted (the JSON debug APIs are unaffected). Optional engine wiring,
	// not part of the day-to-day user surface.
	DebugTracesHTML []byte
}
