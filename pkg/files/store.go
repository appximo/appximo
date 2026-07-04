package files

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// copyBufSize is the streaming copy buffer (ADR / storage investigation: io.Copy
// with a fixed buffer, never io.ReadAll). 64 KiB balances syscall count against
// per-upload RAM: a 50 MiB upload still costs one 64 KiB buffer, not 50 MiB.
const copyBufSize = 64 << 10

// sniffLen is how much of the upload head feeds http.DetectContentType (its
// documented maximum useful window).
const sniffLen = 512

// Store is the engine's file store (FILES-V2): the tenancy, metadata, upload
// validation, hashing and dedup logic — everything that must be IDENTICAL
// across storage drivers — over a swappable Backend that only moves bytes.
// It implements the FILES-V1 VFS contract, so every existing consumer (the
// upload/download routes, the XLSX worker) works unchanged on any backend.
type Store struct {
	backend Backend
	store   metaStore
	policy  UploadPolicy
	bufSize int
}

// StoreOption configures NewStore.
type StoreOption func(*Store)

// WithUploadPolicy replaces the default upload-validation policy.
func WithUploadPolicy(p UploadPolicy) StoreOption {
	return func(s *Store) { s.policy = p }
}

// NewStore builds the file store over an explicit backend.
func NewStore(b Backend, ms metaStore, opts ...StoreOption) *Store {
	s := &Store{backend: b, store: ms, policy: NewUploadPolicy(nil), bufSize: copyBufSize}
	for _, o := range opts {
		o(s)
	}
	return s
}

// NewLocal builds the disk-backed store (the FILES-V1 constructor, kept as the
// back-compat spelling: same signature, same on-disk layout, now via the
// Backend seam). root is created lazily on the first upload.
func NewLocal(root string, store metaStore) *Store {
	return NewStore(NewLocalBackend(root), store)
}

var _ VFS = (*Store)(nil)

// Backend exposes the underlying driver (wiring/logging introspection).
func (s *Store) Backend() Backend { return s.backend }

// Put streams r through the OWASP upload checks (extension allowlist + magic
// bytes on the first 512 bytes — the client Content-Type is never trusted),
// hashes as it streams to a staging file (never buffering the body in RAM),
// then commits the blob to the backend under its content-hash key — or reuses
// an existing identical blob (dedup). The client name is stored SANITIZED, as
// metadata only. Every Put yields a fresh metadata row (a new id), even over a
// deduplicated blob.
func (s *Store) Put(ctx context.Context, tenant string, r io.Reader, pm PutMeta) (Meta, error) {
	if err := validTenant(tenant); err != nil {
		return Meta{}, err
	}
	if err := s.store.ensure(ctx, tenant); err != nil {
		return Meta{}, err
	}

	name := SanitizeName(pm.OriginalName)

	// Sniff window: read up to 512 bytes ahead of the stream.
	head := make([]byte, sniffLen)
	n, rerr := io.ReadFull(r, head)
	if rerr != nil && !errors.Is(rerr, io.EOF) && !errors.Is(rerr, io.ErrUnexpectedEOF) {
		return Meta{}, fmt.Errorf("files: read upload head: %w", rerr)
	}
	head = head[:n]

	storedCT, err := s.policy.Validate(name, pm.ContentType, head)
	if err != nil {
		return Meta{}, err
	}

	// Stage to disk (backend-hinted dir so the local driver's final rename is
	// same-filesystem atomic), hashing as it streams.
	stagingDir := os.TempDir()
	if sd, ok := s.backend.(stagingDirer); ok {
		if stagingDir, err = sd.StagingDir(tenant); err != nil {
			return Meta{}, err
		}
	}
	tmp, err := os.CreateTemp(stagingDir, "up-*")
	if err != nil {
		return Meta{}, fmt.Errorf("files: create temp: %w", err)
	}
	tmpName := tmp.Name()
	// settled means the temp file no longer needs cleanup (adopted by the
	// backend, or removed on the dedup path). Until then ANY early return
	// removes it — an interrupted upload leaves no garbage.
	settled := false
	defer func() {
		_ = tmp.Close()
		if !settled {
			_ = os.Remove(tmpName)
		}
	}()

	h := sha256.New()
	src := io.MultiReader(bytes.NewReader(head), r)
	size, err := io.CopyBuffer(tmp, io.TeeReader(src, h), make([]byte, s.bufSize))
	if err != nil {
		return Meta{}, fmt.Errorf("files: stream upload: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return Meta{}, fmt.Errorf("files: fsync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return Meta{}, fmt.Errorf("files: close temp: %w", err)
	}

	sum := hex.EncodeToString(h.Sum(nil))
	key := blobKey(tenant, sum)

	if _, statErr := s.backend.Stat(ctx, key); statErr == nil {
		// Dedup: identical content already stored — drop the staging copy.
		_ = os.Remove(tmpName)
		settled = true
	} else if !errors.Is(statErr, ErrNotFound) {
		return Meta{}, statErr
	} else {
		opts := PutOptions{
			ContentType:        storedCT,
			ContentDisposition: `attachment; filename="` + safeFilename(name) + `"`,
			Size:               size,
		}
		if fp, ok := s.backend.(filePutter); ok {
			// Zero-copy adopt (local: atomic rename).
			if err := fp.PutFile(ctx, key, tmpName); err != nil {
				return Meta{}, err
			}
			settled = true
		} else {
			f, oerr := os.Open(tmpName)
			if oerr != nil {
				return Meta{}, fmt.Errorf("files: reopen temp: %w", oerr)
			}
			err := s.backend.Put(ctx, key, f, opts)
			_ = f.Close()
			if err != nil {
				return Meta{}, err
			}
			_ = os.Remove(tmpName)
			settled = true
		}
	}

	m := Meta{SHA256: sum, Size: size, ContentType: storedCT, OriginalName: name}
	id, err := s.store.insert(ctx, tenant, m)
	if err != nil {
		// The blob may be left orphaned, which is harmless: it is content-addressed,
		// dedups a future identical upload, and is never reachable without a row.
		return Meta{}, err
	}
	m.ID = id
	return m, nil
}

// Get opens the file's content and returns it with its metadata. The reader
// supports Seek. ErrNotFound if the id is unknown to the tenant OR the
// metadata row exists but its blob is gone (a torn state, surfaced as
// not-found, never a 500).
func (s *Store) Get(ctx context.Context, tenant, id string) (io.ReadCloser, Meta, error) {
	m, key, err := s.lookup(ctx, tenant, id)
	if err != nil {
		return nil, Meta{}, err
	}
	rc, err := s.backend.Get(ctx, key)
	if err != nil {
		return nil, Meta{}, err
	}
	return rc, m, nil
}

// Delete removes the metadata row and, when no other row references the same
// blob (dedup), the blob itself. ErrNotFound if the id is unknown.
func (s *Store) Delete(ctx context.Context, tenant, id string) error {
	if err := validTenant(tenant); err != nil {
		return err
	}
	sha, stillRef, err := s.store.del(ctx, tenant, id)
	if err != nil {
		return err
	}
	if !stillRef {
		return s.backend.Delete(ctx, blobKey(tenant, sha))
	}
	return nil
}

// Exists reports whether the tenant already stored content with this hash.
func (s *Store) Exists(ctx context.Context, tenant, sha string) (bool, error) {
	if err := validTenant(tenant); err != nil {
		return false, err
	}
	return s.store.existsSHA(ctx, tenant, sha)
}

// URL returns an engine-internal handle: the on-disk blob path for the local
// backend (FILES-V1 behavior, kept), or a storage presigned URL (default TTL)
// for backends that mint them. Client-facing signed access goes through
// SignedURL / the /api/files/{id}/url route instead.
func (s *Store) URL(ctx context.Context, tenant, id string) (string, error) {
	_, key, err := s.lookup(ctx, tenant, id)
	if err != nil {
		return "", err
	}
	if lb, ok := s.backend.(*LocalBackend); ok {
		return filepath.Join(lb.root, filepath.FromSlash(key)), nil
	}
	return s.backend.SignedURL(ctx, key, DefaultSignedURLTTL)
}

// SignedURL asks the BACKEND for a storage-minted URL valid for ttl (S3
// presigned). ErrSignedURLUnsupported means the caller must mint the engine's
// own token URL instead (local backend).
func (s *Store) SignedURL(ctx context.Context, tenant, id string, ttl time.Duration) (string, error) {
	_, key, err := s.lookup(ctx, tenant, id)
	if err != nil {
		return "", err
	}
	return s.backend.SignedURL(ctx, key, ttl)
}

// Serve writes the file as an HTTP response through the backend's serving
// strategy (local: ServeContent proxy — Range/ETag/sendfile; S3: presigned
// 302 redirect, or proxy mode). Headers come from the DB row (authoritative),
// never from storage or the client.
func (s *Store) Serve(w http.ResponseWriter, r *http.Request, tenant, id string) error {
	m, key, err := s.lookup(r.Context(), tenant, id)
	if err != nil {
		return err
	}
	return s.backend.Serve(w, r, key, ServeInfo{
		ContentType: m.ContentType,
		ETag:        `"` + m.SHA256 + `"`,
		ModTime:     m.CreatedAt,
		Filename:    safeFilename(m.OriginalName),
		Size:        m.Size,
	})
}

// Stat returns the metadata row (the authoritative record) for id.
func (s *Store) Stat(ctx context.Context, tenant, id string) (Meta, error) {
	m, _, err := s.lookup(ctx, tenant, id)
	return m, err
}

// ListMeta returns a page of the tenant's file metadata (newest first) and the
// total count — a METADATA query (the authoritative Postgres table), never a
// storage List call. It is the browse surface for operational UIs (the Studio
// files manager); a tenant with no files yet lists as empty.
func (s *Store) ListMeta(ctx context.Context, tenant string, limit, offset int) ([]Meta, int, error) {
	if err := validTenant(tenant); err != nil {
		return nil, 0, err
	}
	if limit <= 0 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return s.store.list(ctx, tenant, limit, offset)
}

// BackendName names the driver behind b ("local", "s3", or "custom") — an
// informational label for operational UIs, never a behavioral switch.
func BackendName(b Backend) string {
	switch b.(type) {
	case *LocalBackend:
		return "local"
	case *S3Backend:
		return "s3"
	default:
		return "custom"
	}
}

// lookup resolves id → (metadata row, blob key) under tenant validation.
func (s *Store) lookup(ctx context.Context, tenant, id string) (Meta, string, error) {
	if err := validTenant(tenant); err != nil {
		return Meta{}, "", err
	}
	m, err := s.store.get(ctx, tenant, id)
	if err != nil {
		return Meta{}, "", err
	}
	return m, blobKey(tenant, m.SHA256), nil
}
