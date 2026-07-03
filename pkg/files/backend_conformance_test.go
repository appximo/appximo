package files

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// backendConformance is THE driver contract: every Backend must pass this
// exact suite. It runs against LocalBackend here and against S3Backend (MinIO)
// in the integration lane (files_s3_integration_test.go) — passing both is
// what "interchangeable" means.
func backendConformance(t *testing.T, ctx context.Context, b Backend) {
	t.Helper()
	content := []byte("conformance payload — swappable drivers, same behavior")
	sum := sha256.Sum256(content)
	sha := hex.EncodeToString(sum[:])
	key := blobKey("acme", sha)

	// Put then Stat.
	if err := b.Put(ctx, key, bytes.NewReader(content), PutOptions{ContentType: "text/plain", Size: int64(len(content))}); err != nil {
		t.Fatalf("Put: %v", err)
	}
	info, err := b.Stat(ctx, key)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if info.Size != int64(len(content)) {
		t.Fatalf("Stat size = %d, want %d", info.Size, len(content))
	}

	// Stat of an absent key is ErrNotFound.
	absent := blobKey("acme", strings.Repeat("0", 64))
	if _, err := b.Stat(ctx, absent); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Stat absent err = %v, want ErrNotFound", err)
	}

	// Get returns identical bytes and a working Seeker.
	rc, err := b.Get(ctx, key)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, content) {
		t.Fatal("Get content mismatch")
	}
	if _, err := rc.Seek(10, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	tail, _ := io.ReadAll(rc)
	if !bytes.Equal(tail, content[10:]) {
		t.Fatal("Seek+Read mismatch")
	}
	rc.Close() //nolint:errcheck

	// Get of an absent key is ErrNotFound (drivers may defer the check to the
	// first read — normalize by attempting one).
	if rc, err := b.Get(ctx, absent); err == nil {
		_, rerr := rc.Read(make([]byte, 1))
		rc.Close() //nolint:errcheck
		if rerr == nil {
			t.Fatal("Get absent: expected an error")
		}
	} else if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get absent err = %v, want ErrNotFound", err)
	}

	// List sees the blob under the tenant prefix.
	objs, err := b.List(ctx, "acme")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, o := range objs {
		if strings.HasSuffix(o.Key, sha) {
			found = true
		}
	}
	if !found {
		t.Fatalf("List did not return the blob (got %d objects)", len(objs))
	}

	// Malformed keys are rejected at the driver boundary (traversal-proof).
	for _, bad := range []string{"../../etc/passwd", "acme/../../x", "acme/zz/xx/nothex", "acme"} {
		if err := b.Put(ctx, bad, bytes.NewReader([]byte("x")), PutOptions{}); err == nil {
			t.Fatalf("Put with malformed key %q must fail", bad)
		}
		if _, err := b.Get(ctx, bad); err == nil {
			t.Fatalf("Get with malformed key %q must fail", bad)
		}
	}

	// Serve honors Range and the conditional ETag.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveErr := b.Serve(w, r, key, ServeInfo{
			ContentType: "text/plain", ETag: `"` + sha + `"`,
			ModTime: time.Now().Add(-time.Hour), Filename: "c.txt", Size: int64(len(content)),
		})
		if serveErr != nil {
			t.Errorf("Serve: %v", serveErr)
		}
	}))
	defer srv.Close()

	// Follow redirects (the S3 redirect mode 302s to a presigned URL).
	client := srv.Client()

	full, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	body, _ := io.ReadAll(full.Body)
	full.Body.Close() //nolint:errcheck
	if full.StatusCode != http.StatusOK || !bytes.Equal(body, content) {
		t.Fatalf("full GET: status %d, body match %t", full.StatusCode, bytes.Equal(body, content))
	}

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("Range", "bytes=5-9")
	partial, err := client.Do(req)
	if err != nil {
		t.Fatalf("range GET: %v", err)
	}
	part, _ := io.ReadAll(partial.Body)
	partial.Body.Close() //nolint:errcheck
	if partial.StatusCode != http.StatusPartialContent {
		t.Fatalf("range GET status = %d, want 206", partial.StatusCode)
	}
	if !bytes.Equal(part, content[5:10]) {
		t.Fatalf("range GET body = %q, want %q", part, content[5:10])
	}

	// Delete removes it; deleting again is a no-op (idempotent).
	if err := b.Delete(ctx, key); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := b.Stat(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Stat after delete err = %v, want ErrNotFound", err)
	}
	if err := b.Delete(ctx, key); err != nil {
		t.Fatalf("second Delete must be a no-op, got %v", err)
	}
}

func TestBackendConformance_Local(t *testing.T) {
	backendConformance(t, context.Background(), NewLocalBackend(t.TempDir()))
}

func TestLocalBackend_ServeSetsETagAndDisposition(t *testing.T) {
	b := NewLocalBackend(t.TempDir())
	content := []byte("etag material")
	sum := sha256.Sum256(content)
	sha := hex.EncodeToString(sum[:])
	key := blobKey("acme", sha)
	if err := b.Put(context.Background(), key, bytes.NewReader(content), PutOptions{}); err != nil {
		t.Fatalf("Put: %v", err)
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	if err := b.Serve(rr, req, key, ServeInfo{ContentType: "text/plain", ETag: `"` + sha + `"`, ModTime: time.Now(), Filename: "e.txt"}); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if got := rr.Header().Get("ETag"); got != `"`+sha+`"` {
		t.Fatalf("ETag = %q", got)
	}
	if got := rr.Header().Get("Content-Disposition"); got != `attachment; filename="e.txt"` {
		t.Fatalf("Content-Disposition = %q", got)
	}

	// Conditional GET on the strong ETag → 304, no body.
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/x", nil)
	req2.Header.Set("If-None-Match", `"`+sha+`"`)
	if err := b.Serve(rr2, req2, key, ServeInfo{ContentType: "text/plain", ETag: `"` + sha + `"`, ModTime: time.Now(), Filename: "e.txt"}); err != nil {
		t.Fatalf("Serve conditional: %v", err)
	}
	if rr2.Code != http.StatusNotModified {
		t.Fatalf("conditional GET status = %d, want 304", rr2.Code)
	}
}
