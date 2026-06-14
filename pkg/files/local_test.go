package files

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func newLocal(t *testing.T) *Local {
	t.Helper()
	return NewLocal(t.TempDir(), newMemStore())
}

// countFiles returns the number of regular files under root (recursively).
func countFiles(t *testing.T, root string) int {
	t.Helper()
	n := 0
	err := filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return n
}

func TestLocalPut_CASLayout_And_RoundTrip(t *testing.T) {
	l := newLocal(t)
	content := []byte("hello content-addressable world")
	want := sha256.Sum256(content)
	wantHex := hex.EncodeToString(want[:])

	m, err := l.Put(context.Background(), "acme", bytes.NewReader(content), PutMeta{
		ContentType: "text/plain", OriginalName: "greeting.txt",
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if m.SHA256 != wantHex {
		t.Fatalf("sha = %s, want %s", m.SHA256, wantHex)
	}
	if m.Size != int64(len(content)) {
		t.Fatalf("size = %d, want %d", m.Size, len(content))
	}
	if m.ID == "" {
		t.Fatal("empty file id")
	}

	// Blob lands at <root>/acme/aa/bb/<sha>.
	blob := filepath.Join(l.root, "acme", wantHex[0:2], wantHex[2:4], wantHex)
	if _, err := os.Stat(blob); err != nil {
		t.Fatalf("blob not at expected CAS path %s: %v", blob, err)
	}

	// Get returns identical bytes + metadata.
	rc, gm, err := l.Get(context.Background(), "acme", m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, content) {
		t.Fatalf("Get content mismatch")
	}
	if gm.OriginalName != "greeting.txt" || gm.ContentType != "text/plain" {
		t.Fatalf("metadata mismatch: %+v", gm)
	}
}

func TestLocalPut_Dedup(t *testing.T) {
	l := newLocal(t)
	content := []byte("same bytes twice")

	m1, err := l.Put(context.Background(), "acme", bytes.NewReader(content), PutMeta{OriginalName: "a.bin"})
	if err != nil {
		t.Fatalf("Put 1: %v", err)
	}
	m2, err := l.Put(context.Background(), "acme", bytes.NewReader(content), PutMeta{OriginalName: "b.bin"})
	if err != nil {
		t.Fatalf("Put 2: %v", err)
	}
	if m1.ID == m2.ID {
		t.Fatal("dedup must still mint a distinct id per upload")
	}
	if m1.SHA256 != m2.SHA256 {
		t.Fatal("same content must hash equal")
	}
	// Exactly one blob on disk (tmp dir is empty after both Puts).
	if n := countFiles(t, filepath.Join(l.root, "acme")); n != 1 {
		t.Fatalf("dedup: %d files on disk, want 1 (shared blob)", n)
	}
	// Both ids resolve to the same content.
	for _, id := range []string{m1.ID, m2.ID} {
		rc, _, err := l.Get(context.Background(), "acme", id)
		if err != nil {
			t.Fatalf("Get %s: %v", id, err)
		}
		got, _ := io.ReadAll(rc)
		rc.Close()
		if !bytes.Equal(got, content) {
			t.Fatalf("Get %s content mismatch", id)
		}
	}
}

// maxChunkReader records the largest buffer length a consumer asks for. A
// CopyBuffer-based (streaming) Put never asks for more than its 64 KiB buffer; an
// io.ReadAll-based one would request megabyte-sized buffers as it grows.
type maxChunkReader struct {
	src io.Reader
	max int
}

func (m *maxChunkReader) Read(p []byte) (int, error) {
	if len(p) > m.max {
		m.max = len(p)
	}
	return m.src.Read(p)
}

// patternSrc is a deterministic infinite byte stream (0,1,2,…,255,0,…). Two fresh
// instances produce identical sequences, so the expected hash can be computed
// independently of the Put.
type patternSrc struct{ i byte }

func (p *patternSrc) Read(b []byte) (int, error) {
	for j := range b {
		b[j] = p.i
		p.i++
	}
	return len(b), nil
}

func patternReader(n int64) io.Reader { return io.LimitReader(&patternSrc{}, n) }

func TestLocalPut_Streaming50MB_BoundedBuffer(t *testing.T) {
	if testing.Short() {
		// Still runs under `-short` (make test): 50 MiB stream is ~sub-second and the
		// whole point is to prove memory stays bounded, so we do NOT skip it.
	}
	const size = int64(50 << 20) // 50 MiB

	// Expected hash from an independent identical stream.
	h := sha256.New()
	if _, err := io.Copy(h, patternReader(size)); err != nil {
		t.Fatalf("hash stream: %v", err)
	}
	wantHex := hex.EncodeToString(h.Sum(nil))

	l := newLocal(t)
	mc := &maxChunkReader{src: patternReader(size)}
	m, err := l.Put(context.Background(), "big", mc, PutMeta{OriginalName: "big.bin"})
	if err != nil {
		t.Fatalf("Put 50MB: %v", err)
	}
	if m.Size != size {
		t.Fatalf("size = %d, want %d", m.Size, size)
	}
	if m.SHA256 != wantHex {
		t.Fatalf("sha mismatch on 50MB streaming put")
	}
	// Bounded buffering: the source was never asked for more than the 64 KiB copy
	// buffer — i.e. the file was streamed, not read whole into memory.
	if mc.max > copyBufSize {
		t.Fatalf("max read chunk = %d bytes, want <= %d (Put must stream, not ReadAll)", mc.max, copyBufSize)
	}

	// And it round-trips byte-exactly.
	rc, _, err := l.Get(context.Background(), "big", m.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer rc.Close()
	gh := sha256.New()
	if _, err := io.CopyBuffer(gh, rc, make([]byte, copyBufSize)); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if hex.EncodeToString(gh.Sum(nil)) != wantHex {
		t.Fatal("50MB round-trip hash mismatch")
	}
}

// errAfter yields n good bytes, then fails — modelling a client that aborts an
// upload mid-stream.
type errAfter struct {
	remaining int
}

func (e *errAfter) Read(p []byte) (int, error) {
	if e.remaining <= 0 {
		return 0, errors.New("connection reset mid-upload")
	}
	k := len(p)
	if k > e.remaining {
		k = e.remaining
	}
	for i := 0; i < k; i++ {
		p[i] = 'x'
	}
	e.remaining -= k
	return k, nil
}

func TestLocalPut_InterruptedUpload_NoGarbage(t *testing.T) {
	l := newLocal(t)
	_, err := l.Put(context.Background(), "acme", &errAfter{remaining: 1 << 20}, PutMeta{OriginalName: "partial.bin"})
	if err == nil {
		t.Fatal("expected Put to fail on an interrupted upload")
	}
	// No blob, no leftover temp file — the partial upload left nothing on disk.
	if n := countFiles(t, l.root); n != 0 {
		t.Fatalf("interrupted upload left %d files on disk, want 0", n)
	}
}

func TestLocalGet_NotFound(t *testing.T) {
	l := newLocal(t)
	if _, _, err := l.Get(context.Background(), "acme", uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestLocalDelete_DedupAware(t *testing.T) {
	l := newLocal(t)
	content := []byte("shared blob")
	m1, _ := l.Put(context.Background(), "acme", bytes.NewReader(content), PutMeta{})
	m2, _ := l.Put(context.Background(), "acme", bytes.NewReader(content), PutMeta{})

	// Deleting one id keeps the blob (still referenced by the other).
	if err := l.Delete(context.Background(), "acme", m1.ID); err != nil {
		t.Fatalf("Delete m1: %v", err)
	}
	if n := countFiles(t, filepath.Join(l.root, "acme")); n != 1 {
		t.Fatalf("after first delete: %d blobs, want 1 (still referenced)", n)
	}
	if _, _, err := l.Get(context.Background(), "acme", m2.ID); err != nil {
		t.Fatalf("m2 must still resolve after m1 delete: %v", err)
	}

	// Deleting the last reference removes the blob.
	if err := l.Delete(context.Background(), "acme", m2.ID); err != nil {
		t.Fatalf("Delete m2: %v", err)
	}
	if n := countFiles(t, filepath.Join(l.root, "acme")); n != 0 {
		t.Fatalf("after last delete: %d blobs, want 0 (orphaned blob removed)", n)
	}
	// Deleting an unknown id is ErrNotFound.
	if err := l.Delete(context.Background(), "acme", uuid.NewString()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("delete unknown: err = %v, want ErrNotFound", err)
	}
}

func TestLocalTenantIsolation(t *testing.T) {
	l := newLocal(t)
	m, err := l.Put(context.Background(), "tenanta", bytes.NewReader([]byte("a's secret")), PutMeta{})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	// Tenant B cannot read tenant A's file by id — the id is tenant-scoped.
	if _, _, err := l.Get(context.Background(), "tenantb", m.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant Get err = %v, want ErrNotFound", err)
	}
	// And the blob is physically under tenant A's directory, not a shared root.
	if n := countFiles(t, filepath.Join(l.root, "tenanta")); n != 1 {
		t.Fatalf("tenant A blob count = %d, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(l.root, "tenantb")); !os.IsNotExist(err) {
		t.Fatalf("tenant B directory should not exist, stat err = %v", err)
	}
}

func TestLocalPut_PathTraversal_OriginalNameIsInert(t *testing.T) {
	l := newLocal(t)
	content := []byte("traversal attempt")
	sum := sha256.Sum256(content)
	wantHex := hex.EncodeToString(sum[:])

	m, err := l.Put(context.Background(), "acme", bytes.NewReader(content), PutMeta{
		OriginalName: "../../../../etc/passwd",
	})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	// The blob is at the hash path — never at a path derived from original_name.
	blob := filepath.Join(l.root, "acme", wantHex[0:2], wantHex[2:4], wantHex)
	if _, err := os.Stat(blob); err != nil {
		t.Fatalf("blob not at hash path: %v", err)
	}
	// Nothing escaped the CAS root: exactly one file under root, named by the hash.
	if n := countFiles(t, l.root); n != 1 {
		t.Fatalf("path traversal: %d files under root, want 1 (the hashed blob)", n)
	}
	// original_name survives ONLY as metadata.
	if m.OriginalName != "../../../../etc/passwd" {
		t.Fatalf("original_name metadata = %q, want it preserved verbatim", m.OriginalName)
	}
}

func TestLocalPut_InvalidTenant(t *testing.T) {
	l := newLocal(t)
	for _, bad := range []string{"", "../etc", "a/b", "UPPER"} {
		if _, err := l.Put(context.Background(), bad, bytes.NewReader([]byte("x")), PutMeta{}); err == nil {
			t.Fatalf("Put with invalid tenant %q should fail", bad)
		}
	}
}
