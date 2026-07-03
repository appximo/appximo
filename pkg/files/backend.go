package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Backend is the thin, swappable blob-storage contract under the Store
// (FILES-V2, the PocketBase pattern: one owned interface, interchangeable
// drivers). Two drivers exist: LocalBackend (direct disk — os.File +
// http.ServeContent, the measured optimum for a VPS) and S3Backend
// (gocloud.dev s3blob — any S3-compatible provider via config: R2, Spaces,
// MinIO, AWS).
//
// A Backend stores BYTES under validated CAS keys; everything above it —
// metadata (authoritative in the tenant's Postgres files table), tenancy
// checks, upload validation, hashing, dedup refcounts — is the Store's job
// and identical across drivers. That split is what makes the drivers truly
// interchangeable: the same conformance test suite passes on both.
//
// Keys are always "<tenant>/<aa>/<bb>/<sha256-hex>" (see blobKey): every
// component is either a validated tenant id or content-hash hex, so no key
// ever carries client input — path traversal is structurally impossible.
type Backend interface {
	// Put streams r to storage under key. opts carries the object headers a
	// storage service can persist (content type / disposition / cache-control);
	// the local driver ignores them (headers come from the DB row at serve time).
	Put(ctx context.Context, key string, r io.Reader, opts PutOptions) error

	// Get opens the blob for reading. The reader supports Seek (Range serving).
	// ErrNotFound if the key does not exist.
	Get(ctx context.Context, key string) (io.ReadSeekCloser, error)

	// Delete removes the blob. Deleting an absent key is a no-op (the Store has
	// already committed the metadata delete; a torn state must not resurrect it).
	Delete(ctx context.Context, key string) error

	// Stat describes the blob, or ErrNotFound. The Store uses it for dedup
	// (does this content already exist?).
	Stat(ctx context.Context, key string) (ObjectInfo, error)

	// List enumerates the blobs under a key prefix (an operational/GC surface,
	// not a request-path one — listing for clients is a metadata query).
	List(ctx context.Context, prefix string) ([]ObjectInfo, error)

	// Serve writes the blob as an HTTP response honoring Range / conditional
	// headers. The local driver proxies via http.ServeContent (206, ETag,
	// sendfile); the S3 driver redirects to a short-lived presigned URL by
	// default (the FILES-V1 contract: authorize, never proxy the bytes) or
	// proxies through ServeContent in "proxy" mode. info carries the
	// authoritative metadata from the Store's DB row.
	Serve(w http.ResponseWriter, r *http.Request, key string, info ServeInfo) error

	// SignedURL returns a URL that grants access to the blob for expiry, minted
	// by the STORAGE (S3 presigned, Signature v4). The local driver returns
	// ErrSignedURLUnsupported: its signed access is an engine-minted HMAC token
	// URL (pkg/files/token.go), built at the HTTP layer because only a request
	// knows the tenant's public origin.
	SignedURL(ctx context.Context, key string, expiry time.Duration) (string, error)
}

// ErrSignedURLUnsupported means the backend cannot mint storage-level signed
// URLs; the caller falls back to the engine's own signed-token URL.
var ErrSignedURLUnsupported = errors.New("files: backend does not mint signed URLs")

// PutOptions are object attributes a storage service persists with the blob
// and replays on direct (presigned) GETs.
type PutOptions struct {
	ContentType        string
	ContentDisposition string
	CacheControl       string
	// Size is the exact content length when known (uploads are staged and
	// hashed before the backend Put, so it always is). -1 means unknown.
	Size int64
}

// ObjectInfo describes one stored blob.
type ObjectInfo struct {
	Key         string
	Size        int64
	ContentType string
	ModTime     time.Time
	ETag        string
}

// ServeInfo is the authoritative response metadata for Backend.Serve, taken
// from the Store's DB row (never from storage or client input).
type ServeInfo struct {
	ContentType string
	ETag        string // strong ETag: the content hash, quoted
	ModTime     time.Time
	Filename    string // already header-sanitized (safeFilename)
	Size        int64
}

// filePutter is an optional Backend fast path: adopt an already-staged temp
// FILE as the blob without a second copy. The local driver implements it with
// an atomic os.Rename (staging happens on the same filesystem), preserving
// FILES-V1's write path exactly.
type filePutter interface {
	PutFile(ctx context.Context, key, srcPath string) error
}

// stagingDirer is an optional Backend hint for where the Store should stage
// uploads. The local driver stages inside the tenant's directory so the final
// rename is same-filesystem atomic; other backends use the OS temp dir.
type stagingDirer interface {
	StagingDir(tenant string) (string, error)
}

// blobKey is the canonical CAS key: <tenant>/<aa>/<bb>/<sha256-hex>, the same
// fan-out layout FILES-V1 used on disk, now backend-agnostic. tenant has been
// validated (validTenant) and sha is lowercase hex — no component is client
// input.
func blobKey(tenant, sha string) string {
	return tenant + "/" + sha[0:2] + "/" + sha[2:4] + "/" + sha
}

// validKey rejects anything that is not a well-formed CAS key. It is defense
// in depth: keys are only ever built by blobKey, but every driver re-checks at
// its boundary so a future caller bug cannot become a path traversal.
func validKey(key string) error {
	parts := strings.Split(key, "/")
	if len(parts) != 4 {
		return fmt.Errorf("files: malformed blob key %q", key)
	}
	if err := validTenant(parts[0]); err != nil {
		return err
	}
	sha := parts[3]
	if len(sha) != 64 || !isLowerHex(sha) || parts[1] != sha[0:2] || parts[2] != sha[2:4] {
		return fmt.Errorf("files: malformed blob key %q", key)
	}
	return nil
}

// validKeyPrefix accepts a List prefix: a bare tenant, or a tenant plus hex
// fan-out segments (no traversal characters possible).
func validKeyPrefix(prefix string) error {
	parts := strings.Split(strings.TrimSuffix(prefix, "/"), "/")
	if len(parts) < 1 || len(parts) > 4 {
		return fmt.Errorf("files: malformed list prefix %q", prefix)
	}
	if err := validTenant(parts[0]); err != nil {
		return err
	}
	for _, p := range parts[1:] {
		if !isLowerHex(p) {
			return fmt.Errorf("files: malformed list prefix %q", prefix)
		}
	}
	return nil
}

func isLowerHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return len(s) > 0
}
