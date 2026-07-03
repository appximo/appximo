// Package files is the engine's content-addressable file store (FILES-V1 core,
// FILES-V2 backends): the real source of the file_ref the XLSX consumer reads.
//
// Layout: a blob is keyed by the SHA-256 of its content as
//
//	<tenant>/<aa>/<bb>/<sha256-hex>
//
// where aa/bb are the first two bytes of the hash (a fan-out so one directory
// never holds millions of entries). Identical content hashes to the same key, so
// dedup is free WITHIN a tenant; the tenant prefix gives physical isolation (one
// tenant's bytes never share a directory with another's). File METADATA (id,
// sha256, size, content_type, original_name) lives in a per-tenant Postgres table
// — the blob in storage, the row in the tenant schema, the same isolation model
// the rest of the engine uses. Metadata is AUTHORITATIVE in the DB; storage only
// moves bytes.
//
// FILES-V2 splits the Store (tenancy + metadata + OWASP upload validation +
// hashing + dedup — identical everywhere) from the Backend (byte storage): the
// LocalBackend serves from disk inside the binary (http.ServeContent —
// Range/ETag/sendfile; signed access via engine-minted short-lived HMAC tokens),
// and the S3Backend targets any S3-compatible provider by config (R2, Spaces,
// MinIO, AWS), serving via short-lived presigned URL + 302 by default (the
// engine authorizes but never proxies the bytes) or via proxy mode.
package files

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound is returned by Get/Delete when no file with the given id exists for
// the tenant (a missing metadata row, a tenant with no files table yet, or a
// metadata row whose blob is absent). HTTP handlers map it to 404.
var ErrNotFound = errors.New("files: not found")

// PutMeta carries the client-supplied descriptors of an upload. NEITHER field is
// ever used to build a path on disk — the blob path is the content hash, so a
// hostile OriginalName like "../../etc/passwd" is inert metadata (see the path
// traversal guarantee in Local.Put).
type PutMeta struct {
	ContentType  string
	OriginalName string
}

// Meta is the stored record of one file: its id (the tenant-scoped handle clients
// and the file_ref use), the content hash, size, and the client descriptors.
type Meta struct {
	ID           string    `json:"id"`
	SHA256       string    `json:"sha256"`
	Size         int64     `json:"size"`
	ContentType  string    `json:"content_type"`
	OriginalName string    `json:"original_name"`
	CreatedAt    time.Time `json:"created_at"`
}

// VFS is the file-store contract shared by the Local (disk CAS) and S3 backends.
// Every method is tenant-scoped — a file id is only meaningful within its tenant,
// so there is no cross-tenant handle.
type VFS interface {
	// Put streams r to storage (hashing as it streams — never buffering the whole
	// body), records the metadata, and returns the stored Meta with its assigned
	// id. Identical content is de-duplicated: the blob is written once, but each
	// Put yields a distinct id (a new metadata row referencing the shared blob).
	Put(ctx context.Context, tenantID string, r io.Reader, meta PutMeta) (Meta, error)

	// Get opens the file's content for reading and returns its Meta. The caller
	// MUST Close the returned reader. ErrNotFound if the id is unknown to tenant.
	Get(ctx context.Context, tenantID, fileID string) (io.ReadCloser, Meta, error)

	// Delete removes the metadata row and, when no other row references the same
	// blob (dedup), the blob itself. ErrNotFound if the id is unknown.
	Delete(ctx context.Context, tenantID, fileID string) error

	// Exists reports whether the tenant already has a file with this content hash
	// (a metadata-level check used to short-circuit a re-upload).
	Exists(ctx context.Context, tenantID, sha256 string) (bool, error)

	// URL returns a handle to fetch the file. Local returns the internal blob path
	// (engine-only); S3 returns a short-lived presigned URL the client fetches
	// directly (the engine never proxies the bytes).
	URL(ctx context.Context, tenantID, fileID string) (string, error)
}
