package files

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/miguelangel/appitools/pkg/tenant"
)

// filesTestServer mounts the upload/download handlers behind a middleware that
// injects a fixed tenant — standing in for the engine's TenantMiddleware so the
// handlers run exactly as they do in the chain.
func filesTestServer(t *testing.T, vfs *Store, maxBytes int64, tenantID string) *httptest.Server {
	t.Helper()
	r := chi.NewMux()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			tc := &tenant.TenantCtx{ID: tenantID, PGSchema: "tenant_" + tenantID}
			next.ServeHTTP(w, req.WithContext(tenant.WithContext(req.Context(), tc)))
		})
	})
	r.Post("/api/files", UploadHandler(vfs, maxBytes))
	r.Get("/api/files/{id}", DownloadHandler(vfs))
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func multipartUpload(t *testing.T, url, field, filename, contentType string, content []byte) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="`+field+`"; filename="`+filename+`"`)
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	pw, err := mw.CreatePart(h)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := pw.Write(content); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close mw: %v", err)
	}
	req, _ := http.NewRequest(http.MethodPost, url, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do upload: %v", err)
	}
	return resp
}

func TestHTTP_UploadDownload_RoundTrip(t *testing.T) {
	l := NewLocal(t.TempDir(), newMemStore())
	srv := filesTestServer(t, l, DefaultMaxUploadBytes, "acme")

	content := []byte("the quick brown fox jumps over the lazy dog")
	resp := multipartUpload(t, srv.URL+"/api/files", "file", "fox.txt", "text/plain", content)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("upload status = %d, want 201", resp.StatusCode)
	}
	var up struct {
		FileID string `json:"file_id"`
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	}
	json.NewDecoder(resp.Body).Decode(&up) //nolint:errcheck
	resp.Body.Close()
	if up.FileID == "" || up.Size != int64(len(content)) {
		t.Fatalf("unexpected upload response: %+v", up)
	}

	dl, err := http.Get(srv.URL + "/api/files/" + up.FileID)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer dl.Body.Close()
	if dl.StatusCode != http.StatusOK {
		t.Fatalf("download status = %d, want 200", dl.StatusCode)
	}
	if ct := dl.Header.Get("Content-Type"); ct != "text/plain" {
		t.Fatalf("content-type = %q, want text/plain", ct)
	}
	if cd := dl.Header.Get("Content-Disposition"); cd != `attachment; filename="fox.txt"` {
		t.Fatalf("content-disposition = %q", cd)
	}
	got, _ := io.ReadAll(dl.Body)
	if !bytes.Equal(got, content) {
		t.Fatalf("download body mismatch")
	}
}

func TestHTTP_Upload_TooLarge(t *testing.T) {
	l := NewLocal(t.TempDir(), newMemStore())
	srv := filesTestServer(t, l, 16, "acme") // 16-byte cap

	resp := multipartUpload(t, srv.URL+"/api/files", "file", "big.txt", "text/plain",
		bytes.Repeat([]byte("A"), 1024))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
}

func TestHTTP_Upload_NoFilePart(t *testing.T) {
	l := NewLocal(t.TempDir(), newMemStore())
	srv := filesTestServer(t, l, DefaultMaxUploadBytes, "acme")

	resp := multipartUpload(t, srv.URL+"/api/files", "notfile", "x.txt", "text/plain", []byte("x"))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestHTTP_Download_NotFound(t *testing.T) {
	l := NewLocal(t.TempDir(), newMemStore())
	srv := filesTestServer(t, l, DefaultMaxUploadBytes, "acme")

	resp, err := http.Get(srv.URL + "/api/files/" + uuid.NewString())
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestHTTP_Download_InvalidID(t *testing.T) {
	l := NewLocal(t.TempDir(), newMemStore())
	srv := filesTestServer(t, l, DefaultMaxUploadBytes, "acme")

	resp, err := http.Get(srv.URL + "/api/files/not-a-uuid")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
}

func TestSafeFilename(t *testing.T) {
	cases := map[string]string{
		"plain.txt":               "plain.txt",
		"../../etc/passwd":        "passwd",
		`a"b.txt`:                 "ab.txt",
		"with\nnewline":           "withnewline",
		`C:\windows\system32.dll`: "system32.dll",
		"":                        "download",
	}
	for in, want := range cases {
		if got := safeFilename(in); got != want {
			t.Errorf("safeFilename(%q) = %q, want %q", in, got, want)
		}
	}
}
