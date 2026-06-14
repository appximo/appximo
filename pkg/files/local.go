package files

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// copyBufSize is the streaming copy buffer (ADR / storage investigation: io.Copy
// with a fixed buffer, never io.ReadAll). 64 KiB balances syscall count against
// per-upload RAM: a 50 MiB upload still costs one 64 KiB buffer, not 50 MiB.
const copyBufSize = 64 << 10

// Local is the on-disk content-addressable VFS backend. Blobs live under root,
// keyed by content hash with a tenant prefix; metadata lives in store. It holds no
// per-file state, so one Local is shared across all requests.
type Local struct {
	root    string
	store   metaStore
	bufSize int
}

// NewLocal builds the disk CAS backend. root is the base directory (created
// lazily, per tenant, on first Put — NewLocal never touches the filesystem, so an
// engine that never serves an upload pays nothing). store persists metadata.
func NewLocal(root string, store metaStore) *Local {
	return &Local{root: root, store: store, bufSize: copyBufSize}
}

var _ VFS = (*Local)(nil)

// blobPath is the content-addressed path: <root>/<tenant>/<aa>/<bb>/<sha>. The
// tenant has been validated (validTenant) and sha is hex, so no component is
// attacker-controlled — original_name is NEVER part of the path.
func (l *Local) blobPath(tenant, sha string) string {
	return filepath.Join(l.root, tenant, sha[0:2], sha[2:4], sha)
}

// Put streams r to a temp file while hashing (io.TeeReader → sha256), then atomically
// renames it into its content-addressed path — or, if a blob with that hash already
// exists, drops the temp and reuses it (dedup). A read error mid-stream (the client
// aborting an upload) removes the temp file, so an interrupted upload never leaves a
// partial blob behind. Every Put records a fresh metadata row (a new id) regardless
// of dedup, so two uploads of the same bytes yield two ids over one blob.
func (l *Local) Put(ctx context.Context, tenant string, r io.Reader, pm PutMeta) (Meta, error) {
	if err := validTenant(tenant); err != nil {
		return Meta{}, err
	}
	if err := l.store.ensure(ctx, tenant); err != nil {
		return Meta{}, err
	}

	tmpDir := filepath.Join(l.root, tenant, "tmp")
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		return Meta{}, fmt.Errorf("files: mkdir tmp: %w", err)
	}
	tmp, err := os.CreateTemp(tmpDir, "up-*")
	if err != nil {
		return Meta{}, fmt.Errorf("files: create temp: %w", err)
	}
	tmpName := tmp.Name()
	// settled means the temp file no longer needs cleanup (it was renamed into the
	// CAS, or removed on the dedup path). Until then, ANY early return removes it —
	// this is what makes an interrupted upload leave no garbage.
	settled := false
	defer func() {
		_ = tmp.Close()
		if !settled {
			_ = os.Remove(tmpName)
		}
	}()

	h := sha256.New()
	n, err := io.CopyBuffer(tmp, io.TeeReader(r, h), make([]byte, l.bufSize))
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
	blob := l.blobPath(tenant, sum)

	if _, statErr := os.Stat(blob); statErr == nil {
		// Dedup: identical content already on disk — discard the temp, keep the blob.
		_ = os.Remove(tmpName)
		settled = true
	} else {
		if err := os.MkdirAll(filepath.Dir(blob), 0o755); err != nil {
			return Meta{}, fmt.Errorf("files: mkdir blob dir: %w", err)
		}
		if err := os.Rename(tmpName, blob); err != nil {
			return Meta{}, fmt.Errorf("files: commit blob: %w", err)
		}
		settled = true // temp is now the blob
	}

	m := Meta{SHA256: sum, Size: n, ContentType: pm.ContentType, OriginalName: pm.OriginalName}
	id, err := l.store.insert(ctx, tenant, m)
	if err != nil {
		// The blob may be left orphaned, which is harmless: it is content-addressed,
		// dedups a future identical upload, and is never reachable without a row.
		return Meta{}, err
	}
	m.ID = id
	return m, nil
}

// Get opens the blob for the file id and returns it with its metadata. The caller
// closes the reader. ErrNotFound if the id is unknown OR the metadata row exists
// but its blob is missing on disk (a torn state, surfaced as not-found, not 500).
func (l *Local) Get(ctx context.Context, tenant, id string) (io.ReadCloser, Meta, error) {
	if err := validTenant(tenant); err != nil {
		return nil, Meta{}, err
	}
	m, err := l.store.get(ctx, tenant, id)
	if err != nil {
		return nil, Meta{}, err
	}
	f, err := os.Open(l.blobPath(tenant, m.SHA256))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, Meta{}, ErrNotFound
		}
		return nil, Meta{}, fmt.Errorf("files: open blob: %w", err)
	}
	return f, m, nil
}

// Delete removes the metadata row and, if no other row references the same blob,
// the blob on disk (dedup-aware: a blob shared by two ids survives the first
// delete). ErrNotFound if the id is unknown.
func (l *Local) Delete(ctx context.Context, tenant, id string) error {
	if err := validTenant(tenant); err != nil {
		return err
	}
	sha, stillRef, err := l.store.del(ctx, tenant, id)
	if err != nil {
		return err
	}
	if !stillRef {
		if err := os.Remove(l.blobPath(tenant, sha)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("files: remove blob: %w", err)
		}
	}
	return nil
}

// Exists reports whether the tenant already stored content with this hash.
func (l *Local) Exists(ctx context.Context, tenant, sha string) (bool, error) {
	if err := validTenant(tenant); err != nil {
		return false, err
	}
	return l.store.existsSHA(ctx, tenant, sha)
}

// URL returns the internal blob path (Local is engine-only — the bytes are served
// by the engine's download handler, not a redirect). The S3 backend will instead
// return a short-lived presigned URL here.
func (l *Local) URL(ctx context.Context, tenant, id string) (string, error) {
	if err := validTenant(tenant); err != nil {
		return "", err
	}
	m, err := l.store.get(ctx, tenant, id)
	if err != nil {
		return "", err
	}
	return l.blobPath(tenant, m.SHA256), nil
}
