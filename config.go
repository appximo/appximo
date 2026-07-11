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

import "github.com/miguelangel/appitools/pkg/userauth"

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

	// ControlPort is the control-plane HTTP port (tenant registration,
	// X-Admin-Key-gated — keep it off the internet). 0 falls back to
	// APPITOOLS_CONTROL_PORT, then 9090 (the historical fixed value, so a
	// single-engine deployment boots byte-identically). Parameterized in
	// MT-STRUCT-S1 so N engines can coexist on one box (`appitools fleet`).
	ControlPort int

	// ObsDBPath is the observability SQLite path. Empty falls back to
	// OBS_DB_PATH, then the package default (/var/lib/appitools/obs.db).
	// A Config field (not env-only) since MT-STRUCT-S3: N in-process apps
	// share the process env, and each app needs its OWN obs store.
	ObsDBPath string

	// JWTSecret signs/validates HS256 tokens. Empty falls back to JWT_SECRET.
	JWTSecret string
	// AdminKey gates the control plane (:9090) and /metrics, /debug, /admin on
	// the data plane. Empty falls back to ADMIN_KEY.
	AdminKey string

	// Env mirrors APPITOOLS_ENV ("development" enables GraphiQL + introspection +
	// pprof). Empty falls back to the APPITOOLS_ENV environment variable.
	Env string

	// GraphQLPlayground explicitly enables GraphQL introspection and the
	// GraphiQL explorer (/graphiql) OUTSIDE development — the operator's opt-in
	// for exploring/testing GraphQL in production without flipping the broader
	// Env=development flag (which also enables pprof). False falls back to
	// APPITOOLS_GRAPHQL_PLAYGROUND (truthy); Env=="development" already implies
	// this regardless (GRAPHQL-EXPLORER-S1).
	GraphQLPlayground bool

	// FilesDir is the root directory of the content-addressable file store
	// (FILES-V1). Empty falls back to APPITOOLS_FILES_DIR, then to
	// /var/lib/appitools/files. The directory is created lazily on the first
	// upload, so an engine that never serves /api/files touches no disk.
	// Applies to the "local" files backend only.
	FilesDir string

	// --- File store backends (FILES-V2): BYOC storage, swappable by config ---
	//
	// FilesBackend selects where blobs live: "local" (default — the tenant's
	// files on this VPS's disk, served by the engine with Range/ETag/sendfile)
	// or "s3" (any S3-compatible provider: Cloudflare R2, DO Spaces, MinIO,
	// AWS — served via short-lived presigned URL + 302 by default). Empty falls
	// back to APPITOOLS_FILES_BACKEND, then "local". Tenancy, RBAC, metadata
	// and upload validation are IDENTICAL on both backends.
	FilesBackend string
	// FilesS3Bucket / FilesS3Endpoint / FilesS3Region / FilesS3AccessKey /
	// FilesS3SecretKey configure the S3 backend provider-agnostically. Each
	// falls back to its APPITOOLS_FILES_S3_* env var (BUCKET, ENDPOINT, REGION,
	// ACCESS_KEY, SECRET_KEY). Endpoint empty means AWS S3 proper; Region
	// empty defaults to "auto" (R2's spelling, harmless elsewhere). With
	// FilesBackend="s3", a missing bucket or credentials fails boot loudly.
	FilesS3Bucket    string
	FilesS3Endpoint  string
	FilesS3Region    string
	FilesS3AccessKey string
	FilesS3SecretKey string
	// FilesS3ForcePathStyle addresses the bucket as <endpoint>/<bucket>
	// (required by MinIO). Falls back to APPITOOLS_FILES_S3_FORCE_PATH_STYLE
	// (truthy).
	FilesS3ForcePathStyle bool
	// FilesS3Prefix namespaces keys inside the bucket. Empty falls back to
	// APPITOOLS_FILES_S3_PREFIX, then "tenants/".
	FilesS3Prefix string
	// FilesS3ServeMode is how GET /api/files/{id} delivers S3 bytes:
	// "redirect" (default — 302 to a short-lived presigned URL; the engine
	// authorizes, the bucket serves, zero engine bandwidth) or "proxy" (bytes
	// stream through the engine; bucket never exposed). Falls back to
	// APPITOOLS_FILES_S3_SERVE.
	FilesS3ServeMode string
	// FilesTokenTTLSeconds bounds signed download URLs (both the engine-minted
	// local tokens and S3 presigned URLs from /api/files/{id}/url). 0 falls
	// back to APPITOOLS_FILES_TOKEN_TTL (seconds), then 180.
	FilesTokenTTLSeconds int
	// FilesAllowedExt replaces the default upload extension ALLOWLIST
	// (OWASP: allowlist, never denylist). Entries with or without the dot;
	// the single value "*" disables the extension check (magic-byte checks
	// still apply). Empty falls back to APPITOOLS_FILES_ALLOWED_EXT
	// (comma-separated), then to files.DefaultAllowedExtensions.
	FilesAllowedExt []string

	// BareDomains are hostnames that are THIS APP ITSELF (the fleet manifest's
	// `domains`), not a tenant: a request whose Host equals one exactly carries
	// no tenant label, so the tenant middleware passes it through with no
	// TenantCtx instead of mis-reading the domain's first label as a tenant
	// (the observability phantom-tenant bug, FLEET-CONSOLE-S2). Empty (the
	// single-engine default) leaves the middleware byte-identical to before.
	BareDomains []string

	// Version is reported by /health and the synthetic monitor. Empty reports
	// "dev"; the cmd binary passes its ldflags-injected build version.
	Version string

	// DebugTracesHTML is the embedded /debug/traces explorer page. The cmd
	// binary injects its go:embed'd asset here; when nil the visual route is
	// not mounted (the JSON debug APIs are unaffected). Optional engine wiring,
	// not part of the day-to-day user surface.
	DebugTracesHTML []byte

	// AuthSignupRole is the RBAC role assigned to every PUBLIC signup
	// (POST /auth/signup), the auth-as-product core (AUTH-CORE-V1). Empty
	// DISABLES public signup (safe by default — no accidental self-service
	// accounts). It must name a role declared in the schema's RBAC; New rejects
	// an unknown role at boot. Empty falls back to APPITOOLS_AUTH_SIGNUP_ROLE.
	AuthSignupRole string
	// AuthMinPasswordLength is the minimum accepted signup password length.
	// 0 falls back to APPITOOLS_AUTH_MIN_PASSWORD, then to 8.
	AuthMinPasswordLength int

	// PublicRouteRPS / PublicRouteBurst tune the DEDICATED rate limit applied
	// to PUBLIC custom routes (Route.Public — LIBRARY-EXTEND-S1), per
	// (tenant, client IP), on top of the per-tenant limiter. Zero falls back to
	// APPITOOLS_PUBLIC_ROUTE_RPS / APPITOOLS_PUBLIC_ROUTE_BURST, then to the
	// deliberately conservative 5 rps / burst 10 — an anonymous endpoint is
	// abuse surface, so the default protects it without configuration.
	PublicRouteRPS   float64
	PublicRouteBurst int

	// AuthRequireVerified, when true, blocks login for a user whose email is not
	// yet verified (→ 403). Empty falls back to APPITOOLS_AUTH_REQUIRE_VERIFIED
	// ("true"/"1"/"on"). Default false (login works without verification).
	AuthRequireVerified bool
	// AuthBaseURL optionally overrides the origin used to build password-reset /
	// email-verification links. Empty falls back to APPITOOLS_AUTH_BASE_URL; if
	// still empty the link origin is derived from the request Host (the
	// multi-tenant-correct default). The link path is appended by the engine.
	AuthBaseURL string

	// OAuthCallbackURL is the FIXED public origin OAuth providers redirect back to
	// (AUTH-OAUTH-V1), e.g. "https://auth.example.com" — it must be the redirect
	// URI registered with each provider. Empty falls back to
	// APPITOOLS_OAUTH_CALLBACK_URL; if still empty it is derived from the request.
	OAuthCallbackURL string
	// OAuthDefaultRole is the role assigned to a user auto-created on first social
	// login. Empty falls back to APPITOOLS_OAUTH_DEFAULT_ROLE then to
	// AuthSignupRole; if all empty, a brand-new social email is rejected (existing
	// users still link/login). A configured role must exist in the schema RBAC.
	OAuthDefaultRole string
	// OAuthSuccessRedirect, when set, makes the OAuth callback 302 to
	// "<url>#token=<jwt>" instead of returning JSON. Empty falls back to
	// APPITOOLS_OAUTH_SUCCESS_REDIRECT.
	OAuthSuccessRedirect string
	// OAuthProviders optionally sets the social-login provider credentials
	// directly ("google"/"github"/"microsoft" → client id+secret). Nil falls
	// back to the APPITOOLS_OAUTH_{PROVIDER}_CLIENT_ID/_CLIENT_SECRET env vars.
	// A Config field so the in-process fleet can give EACH APP its own
	// providers (each app is a product with its own identity) instead of the
	// process-wide env.
	OAuthProviders map[string]userauth.OAuthProviderConfig

	// MFAKey is the key material that ENCRYPTS users' TOTP secrets at rest
	// (AUTH-MFA-V1, AES-256-GCM). Empty falls back to APPITOOLS_MFA_KEY, then to
	// the JWT secret. Set a dedicated key if you want to rotate it independently of
	// JWT_SECRET (rotating it invalidates existing TOTP enrollments).
	MFAKey string
	// MFAIssuer is the issuer label shown in authenticator apps. Empty falls back
	// to APPITOOLS_MFA_ISSUER, then to "Appitools".
	MFAIssuer string

	// --- CORS (API-PRODUCTIVA-V1): cross-origin access for browser clients ---
	// CORS is instance INFRASTRUCTURE config, not schema. An empty CORSAllowedOrigins
	// DISABLES CORS (the safe default — no Access-Control-* headers, no preflight
	// short-circuit); an operator opts in by listing browser origins. CORS applies
	// ONLY to the public data-plane routes (/api, /auth, /graphql, /openapi), never
	// to the control plane, /admin, /metrics or /debug.
	//
	// CORSAllowedOrigins is the exact origin allowlist, or the single literal "*"
	// for any origin. Empty falls back to APPITOOLS_CORS_ORIGINS (comma-separated).
	CORSAllowedOrigins []string
	// CORSAllowedMethods is echoed in preflight responses. Empty falls back to
	// APPITOOLS_CORS_METHODS, then to "GET,POST,PUT,PATCH,DELETE,OPTIONS".
	CORSAllowedMethods []string
	// CORSAllowedHeaders is echoed in preflight responses. Empty falls back to
	// APPITOOLS_CORS_HEADERS, then to "Authorization,Content-Type".
	CORSAllowedHeaders []string
	// CORSExposedHeaders lists response headers a browser script may read. Empty
	// falls back to APPITOOLS_CORS_EXPOSE_HEADERS, then to none.
	CORSExposedHeaders []string
	// CORSAllowCredentials sends Access-Control-Allow-Credentials: true (browser may
	// send cookies/Authorization). Falls back to APPITOOLS_CORS_CREDENTIALS (truthy).
	// With credentials a literal "*" origin is reflected (the Fetch spec forbids "*").
	CORSAllowCredentials bool
	// CORSMaxAge bounds preflight caching (seconds). 0 falls back to
	// APPITOOLS_CORS_MAX_AGE, then to 600.
	CORSMaxAge int
}
