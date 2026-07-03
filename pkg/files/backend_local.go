package files

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// LocalBackend is the direct-disk driver: blobs at <root>/<key> (the FILES-V1
// CAS layout, byte-compatible with existing deployments). It deliberately does
// NOT go through gocloud's fileblob: the storage-investigation benchmark showed
// the local ceiling IS the standard library (http.ServeContent over an *os.File
// → Range + ETag + sendfile zero-copy, ~24× less CPU than a manual copy path),
// and FILES-V1's atomic temp+rename write path is already the PocketBase
// end-state (they replaced gocloud with an owned implementation; we simply
// never take the detour for the driver that has nothing to gain from it).
type LocalBackend struct {
	root    string
	bufSize int
}

// NewLocalBackend builds the disk driver rooted at root (created lazily on the
// first write, so an engine that never stores a file touches no disk).
func NewLocalBackend(root string) *LocalBackend {
	return &LocalBackend{root: root, bufSize: copyBufSize}
}

var _ Backend = (*LocalBackend)(nil)
var _ filePutter = (*LocalBackend)(nil)
var _ stagingDirer = (*LocalBackend)(nil)

// Root returns the base directory (operational introspection/logging).
func (l *LocalBackend) Root() string { return l.root }

// path maps a validated CAS key to its on-disk location. validKey guarantees
// every segment is a validated tenant id or hash hex — never client input.
func (l *LocalBackend) path(key string) (string, error) {
	if err := validKey(key); err != nil {
		return "", err
	}
	return filepath.Join(l.root, filepath.FromSlash(key)), nil
}

// StagingDir places upload staging INSIDE the tenant's directory so the final
// rename in PutFile is same-filesystem atomic (the FILES-V1 guarantee).
func (l *LocalBackend) StagingDir(tenant string) (string, error) {
	if err := validTenant(tenant); err != nil {
		return "", err
	}
	dir := filepath.Join(l.root, tenant, "tmp")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("files: mkdir staging: %w", err)
	}
	return dir, nil
}

// PutFile adopts a staged temp file as the blob via atomic rename — the zero-
// copy fast path the Store prefers when the backend offers it.
func (l *LocalBackend) PutFile(_ context.Context, key, srcPath string) error {
	dst, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("files: mkdir blob dir: %w", err)
	}
	if err := os.Rename(srcPath, dst); err != nil {
		return fmt.Errorf("files: commit blob: %w", err)
	}
	return nil
}

// Put streams r into the blob path through a temp file + rename (never a
// partial blob at the final path, even on a mid-stream failure).
func (l *LocalBackend) Put(ctx context.Context, key string, r io.Reader, _ PutOptions) error {
	dst, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("files: mkdir blob dir: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), "put-*")
	if err != nil {
		return fmt.Errorf("files: create temp: %w", err)
	}
	tmpName := tmp.Name()
	settled := false
	defer func() {
		_ = tmp.Close()
		if !settled {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := io.CopyBuffer(tmp, r, make([]byte, l.bufSize)); err != nil {
		return fmt.Errorf("files: stream blob: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("files: fsync blob: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("files: close blob: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		return fmt.Errorf("files: commit blob: %w", err)
	}
	settled = true
	return nil
}

// Get opens the blob; *os.File is the ReadSeekCloser (Range serving native).
func (l *LocalBackend) Get(_ context.Context, key string) (io.ReadSeekCloser, error) {
	p, err := l.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("files: open blob: %w", err)
	}
	return f, nil
}

// Delete removes the blob; an absent key is a no-op (see Backend.Delete).
func (l *LocalBackend) Delete(_ context.Context, key string) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("files: remove blob: %w", err)
	}
	return nil
}

// Stat describes the blob. The ETag is the content hash (the key's last
// segment) — strong by construction in a CAS.
func (l *LocalBackend) Stat(_ context.Context, key string) (ObjectInfo, error) {
	p, err := l.path(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	fi, err := os.Stat(p)
	if err != nil {
		if os.IsNotExist(err) {
			return ObjectInfo{}, ErrNotFound
		}
		return ObjectInfo{}, fmt.Errorf("files: stat blob: %w", err)
	}
	return ObjectInfo{Key: key, Size: fi.Size(), ModTime: fi.ModTime(), ETag: shaOf(key)}, nil
}

// List walks the blobs under prefix (tenant or tenant/aa[/bb]).
func (l *LocalBackend) List(_ context.Context, prefix string) ([]ObjectInfo, error) {
	if err := validKeyPrefix(prefix); err != nil {
		return nil, err
	}
	base := filepath.Join(l.root, filepath.FromSlash(prefix))
	var out []ObjectInfo
	err := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipAll
			}
			return err
		}
		if d.IsDir() {
			if d.Name() == "tmp" {
				return filepath.SkipDir // staging area, not blobs
			}
			return nil
		}
		fi, ferr := d.Info()
		if ferr != nil {
			return ferr
		}
		rel, rerr := filepath.Rel(l.root, p)
		if rerr != nil {
			return rerr
		}
		key := filepath.ToSlash(rel)
		out = append(out, ObjectInfo{Key: key, Size: fi.Size(), ModTime: fi.ModTime(), ETag: shaOf(key)})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("files: list blobs: %w", err)
	}
	return out, nil
}

// Serve proxies the blob with http.ServeContent: Range (206, video seeking),
// If-None-Match/If-Modified-Since (via the strong content-hash ETag), and —
// because the ReadSeeker is a real *os.File — the kernel sendfile zero-copy
// path. This IS the local ceiling; nothing custom to build (measured).
func (l *LocalBackend) Serve(w http.ResponseWriter, r *http.Request, key string, info ServeInfo) error {
	p, err := l.path(key)
	if err != nil {
		return err
	}
	f, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return fmt.Errorf("files: open blob: %w", err)
	}
	defer f.Close() //nolint:errcheck

	setServeHeaders(w, info)
	// Name is empty on purpose: the Content-Type is already set from the DB row
	// (never re-sniffed from a client-controlled filename).
	http.ServeContent(w, r, "", info.ModTime, f)
	return nil
}

// SignedURL: local blobs are served BY the engine, so the signed handle is the
// engine's own short-lived token URL, minted at the HTTP layer (it needs the
// request's tenant origin). The backend signals that with this sentinel.
func (l *LocalBackend) SignedURL(context.Context, string, time.Duration) (string, error) {
	return "", ErrSignedURLUnsupported
}

// setServeHeaders applies the authoritative (DB-row) response metadata shared
// by every proxying driver: type, strong ETag, attachment disposition (a
// stored file is a download, never an executed document — with nosniff it is
// stored-XSS-inert even for HTML/SVG payloads).
func setServeHeaders(w http.ResponseWriter, info ServeInfo) {
	ct := info.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if info.ETag != "" {
		w.Header().Set("ETag", info.ETag)
	}
	name := info.Filename
	if name == "" {
		name = "download"
	}
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
}

// shaOf extracts the content hash from a CAS key (its last segment).
func shaOf(key string) string {
	if len(key) >= 64 {
		return key[len(key)-64:]
	}
	return ""
}
