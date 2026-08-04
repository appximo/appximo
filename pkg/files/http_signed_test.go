package files

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/appximo/appximo/pkg/tenant"
)

// signedTestServer wires the FULL file route surface (upload, download, signed
// URL mint, signed serve, delete) behind a fixed-tenant middleware — the same
// shape app.go mounts, minus JWT/RBAC (represented by the allowRead func and
// the X-User-Role header, exactly how the RBAC middleware reads identity in
// tests).
func signedTestServer(t *testing.T, store *Store, tenantID string, allowRead func(string) bool) *httptest.Server {
	t.Helper()
	r := chi.NewMux()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			tc := &tenant.TenantCtx{ID: tenantID, PGSchema: "tenant_" + tenantID}
			next.ServeHTTP(w, req.WithContext(tenant.WithContext(req.Context(), tc)))
		})
	})
	r.Post("/api/files", UploadHandler(store, DefaultMaxUploadBytes))
	r.Get("/api/files/{id}", DownloadHandler(store))
	r.Get("/api/files/{id}/url", SignedURLHandler(store, tokSecret, 2*time.Second))
	r.Delete("/api/files/{id}", DeleteHandler(store))
	r.Get(SignedPathPrefix+"/{token}", SignedServeHandler(store, tokSecret, allowRead))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func uploadOne(t *testing.T, srv *httptest.Server, name, ct string, content []byte) string {
	t.Helper()
	resp := multipartUpload(t, srv.URL+"/api/files", "file", name, ct, content)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload status = %d: %s", resp.StatusCode, b)
	}
	var up struct {
		FileID string `json:"file_id"`
	}
	json.NewDecoder(resp.Body).Decode(&up) //nolint:errcheck
	return up.FileID
}

func TestHTTP_SignedURL_MintAndServe(t *testing.T) {
	store := NewLocal(t.TempDir(), newMemStore())
	srv := signedTestServer(t, store, "acme", func(role string) bool { return role == "viewer" })

	content := []byte("private but shareable for 2 seconds")
	id := uploadOne(t, srv, "share.txt", "text/plain", content)

	// Mint (the chain would have enforced RBAC read; the role rides the token).
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/files/"+id+"/url", nil)
	req.Header.Set("X-User-Role", "viewer")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	var out struct {
		URL       string `json:"url"`
		ExpiresIn int    `json:"expires_in"`
	}
	json.NewDecoder(resp.Body).Decode(&out) //nolint:errcheck
	resp.Body.Close()                       //nolint:errcheck
	if resp.StatusCode != http.StatusOK || out.URL == "" || out.ExpiresIn != 2 {
		t.Fatalf("mint status=%d url=%q expires_in=%d", resp.StatusCode, out.URL, out.ExpiresIn)
	}
	if !strings.Contains(out.URL, SignedPathPrefix+"/") {
		t.Fatalf("local signed URL must go through the engine: %q", out.URL)
	}

	// The signed URL serves WITHOUT any Authorization (that is its purpose) —
	// and supports Range like the normal download.
	got, err := http.Get(out.URL)
	if err != nil {
		t.Fatalf("signed get: %v", err)
	}
	body, _ := io.ReadAll(got.Body)
	got.Body.Close() //nolint:errcheck
	if got.StatusCode != http.StatusOK || !bytes.Equal(body, content) {
		t.Fatalf("signed get status=%d", got.StatusCode)
	}

	rreq, _ := http.NewRequest(http.MethodGet, out.URL, nil)
	rreq.Header.Set("Range", "bytes=0-6")
	rgot, err := http.DefaultClient.Do(rreq)
	if err != nil {
		t.Fatalf("signed range get: %v", err)
	}
	rbody, _ := io.ReadAll(rgot.Body)
	rgot.Body.Close() //nolint:errcheck
	if rgot.StatusCode != http.StatusPartialContent || !bytes.Equal(rbody, content[:7]) {
		t.Fatalf("signed range: status=%d body=%q", rgot.StatusCode, rbody)
	}
}

func TestHTTP_SignedURL_FailuresAreUniform404(t *testing.T) {
	store := NewLocal(t.TempDir(), newMemStore())
	srv := signedTestServer(t, store, "acme", func(role string) bool { return role == "viewer" })
	id := uploadOne(t, srv, "s.txt", "text/plain", []byte("secret"))

	get := func(url string) int {
		t.Helper()
		resp, err := http.Get(url)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		resp.Body.Close() //nolint:errcheck
		return resp.StatusCode
	}

	// No token / garbage token → 404 (never 401/403 — anti-fingerprinting).
	if code := get(srv.URL + SignedPathPrefix + "/garbage"); code != http.StatusNotFound {
		t.Fatalf("garbage token status = %d, want 404", code)
	}

	// Expired token → 404.
	expired, _ := MintDownloadToken(tokSecret, "acme", id, "viewer", time.Nanosecond)
	time.Sleep(5 * time.Millisecond)
	if code := get(srv.URL + SignedPathPrefix + "/" + expired); code != http.StatusNotFound {
		t.Fatalf("expired token status = %d, want 404", code)
	}

	// A token minted for ANOTHER tenant is useless on this host → 404.
	foreign, _ := MintDownloadToken(tokSecret, "otherco", id, "viewer", time.Minute)
	if code := get(srv.URL + SignedPathPrefix + "/" + foreign); code != http.StatusNotFound {
		t.Fatalf("cross-tenant token status = %d, want 404", code)
	}

	// A role that lost (or never had) read on files → 404.
	norole, _ := MintDownloadToken(tokSecret, "acme", id, "banned", time.Minute)
	if code := get(srv.URL + SignedPathPrefix + "/" + norole); code != http.StatusNotFound {
		t.Fatalf("revoked-role token status = %d, want 404", code)
	}

	// The happy token still works (the failures above were the token's fault).
	ok, _ := MintDownloadToken(tokSecret, "acme", id, "viewer", time.Minute)
	if code := get(srv.URL + SignedPathPrefix + "/" + ok); code != http.StatusOK {
		t.Fatalf("valid token status = %d, want 200", code)
	}
}

func TestHTTP_Delete(t *testing.T) {
	store := NewLocal(t.TempDir(), newMemStore())
	srv := signedTestServer(t, store, "acme", func(string) bool { return true })
	id := uploadOne(t, srv, "gone.txt", "text/plain", []byte("delete me"))

	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/files/"+id, nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d, want 204", resp.StatusCode)
	}

	// Gone.
	g, _ := http.Get(srv.URL + "/api/files/" + id)
	g.Body.Close() //nolint:errcheck
	if g.StatusCode != http.StatusNotFound {
		t.Fatalf("get after delete = %d, want 404", g.StatusCode)
	}
	// Deleting again → 404.
	req2, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/files/"+id, nil)
	resp2, _ := srv.Client().Do(req2)
	resp2.Body.Close() //nolint:errcheck
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete = %d, want 404", resp2.StatusCode)
	}
}

func TestHTTP_Download_RangeAndETag(t *testing.T) {
	store := NewLocal(t.TempDir(), newMemStore())
	srv := signedTestServer(t, store, "acme", func(string) bool { return true })
	content := []byte("0123456789abcdef")
	id := uploadOne(t, srv, "r.txt", "text/plain", content)

	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/files/"+id, nil)
	req.Header.Set("Range", "bytes=4-7")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("range get: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusPartialContent || string(body) != "4567" {
		t.Fatalf("range: status=%d body=%q", resp.StatusCode, body)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}

	// Conditional revalidation → 304.
	req2, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/files/"+id, nil)
	req2.Header.Set("If-None-Match", etag)
	resp2, err := srv.Client().Do(req2)
	if err != nil {
		t.Fatalf("conditional get: %v", err)
	}
	resp2.Body.Close() //nolint:errcheck
	if resp2.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want 304", resp2.StatusCode)
	}
}

func TestHTTP_Upload_RejectedByPolicy(t *testing.T) {
	store := NewLocal(t.TempDir(), newMemStore())
	srv := signedTestServer(t, store, "acme", func(string) bool { return true })

	// Disallowed extension → 422.
	resp := multipartUpload(t, srv.URL+"/api/files", "file", "shell.php", "image/jpeg", []byte("<?php"))
	resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("php upload status = %d, want 422", resp.StatusCode)
	}
	// Spoofed content (php in a .jpg) → 422 by magic bytes.
	resp2 := multipartUpload(t, srv.URL+"/api/files", "file", "photo.jpg", "image/jpeg", []byte("<?php system('id'); ?>"))
	resp2.Body.Close() //nolint:errcheck
	if resp2.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("spoofed jpg status = %d, want 422", resp2.StatusCode)
	}
}
