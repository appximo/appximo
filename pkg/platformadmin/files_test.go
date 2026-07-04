package platformadmin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/files"
	"github.com/miguelangel/appitools/pkg/migration"
	"github.com/miguelangel/appitools/pkg/schema"
)

// cpStub satisfies controlplane.Service with a fixed tenant set — the files
// routes only call GetByID.
type cpStub struct{ tenants map[string]bool }

func (c cpStub) Register(context.Context, controlplane.RegisterRequest) (*controlplane.Tenant, error) {
	return nil, errors.New("not implemented")
}
func (c cpStub) GetByID(_ context.Context, id string) (*controlplane.Tenant, error) {
	if c.tenants[id] {
		return &controlplane.Tenant{}, nil
	}
	return nil, controlplane.ErrNotFound
}
func (c cpStub) UpdateSchema(context.Context, string, *schema.APISchema) error {
	return errors.New("not implemented")
}
func (c cpStub) UpdateSchemaApproved(context.Context, string, *schema.APISchema, []string) (*migration.ApplyOutcome, error) {
	return nil, errors.New("not implemented")
}
func (c cpStub) PreviewSchema(context.Context, string, *schema.APISchema, []string) (*migration.Preview, error) {
	return nil, errors.New("not implemented")
}
func (c cpStub) GetSchema(context.Context, string) (*schema.APISchema, error) {
	return nil, errors.New("not implemented")
}

// newFilesTestServer wires the admin routes over a REAL files.Store (local
// backend on a temp dir, in-memory metadata) — the same Store the tenant API
// uses, which is the point: the manager routes must surface ITS behavior.
func newFilesTestServer(t *testing.T, tenants ...string) (*httptest.Server, *files.Store) {
	t.Helper()
	set := map[string]bool{}
	for _, id := range tenants {
		set[id] = true
	}
	store := files.NewStore(files.NewLocalBackend(t.TempDir()), files.NewMemStore())
	s := NewService(nil, nil, cpStub{tenants: set}, nil, Config{JWTSecret: unitSecret})
	s.SetFileStore(store, files.DefaultMaxUploadBytes, 2*time.Second, []byte(unitSecret))
	r := chi.NewRouter()
	s.Register(r, nil, "unit-key")
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv, store
}

func adminMultipart(t *testing.T, srvURL, tenant, filename, contentType string, content []byte, authed bool) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
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
	mw.Close() //nolint:errcheck
	req, _ := http.NewRequest(http.MethodPost, srvURL+"/admin/tenants/"+tenant+"/files", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if authed {
		req.Header.Set("X-Admin-Key", "unit-key")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	return resp
}

func adminGET(t *testing.T, url string, authed bool) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, url, nil)
	if authed {
		req.Header.Set("X-Admin-Key", "unit-key")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get %s: %v", url, err)
	}
	return resp
}

func TestAdminFiles_RequirePlatformAuth(t *testing.T) {
	srv, _ := newFilesTestServer(t, "acme")
	// Every management route (not the token-authenticated download) → 403 bare.
	resp := adminGET(t, srv.URL+"/admin/tenants/acme/files", false)
	resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("list unauthenticated = %d, want 403", resp.StatusCode)
	}
	up := adminMultipart(t, srv.URL, "acme", "a.txt", "text/plain", []byte("x"), false)
	up.Body.Close() //nolint:errcheck
	if up.StatusCode != http.StatusForbidden {
		t.Fatalf("upload unauthenticated = %d, want 403", up.StatusCode)
	}
}

func TestAdminFiles_FullCycle_SameEngineBehavior(t *testing.T) {
	srv, _ := newFilesTestServer(t, "acme")
	content := []byte("managed from Studio via the real Store")

	// Upload → 201 with the engine's handle shape.
	up := adminMultipart(t, srv.URL, "acme", "informe.pdf", "", pdfBytes(content), true)
	var handle struct {
		FileID string `json:"file_id"`
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	}
	json.NewDecoder(up.Body).Decode(&handle) //nolint:errcheck
	up.Body.Close()                          //nolint:errcheck
	if up.StatusCode != http.StatusCreated || handle.FileID == "" {
		t.Fatalf("upload = %d %+v", up.StatusCode, handle)
	}

	// List → metadata page with total + backend name.
	lr := adminGET(t, srv.URL+"/admin/tenants/acme/files", true)
	var list struct {
		Files []struct {
			ID           string `json:"id"`
			OriginalName string `json:"original_name"`
			ContentType  string `json:"content_type"`
			Size         int64  `json:"size"`
		} `json:"files"`
		Total   int    `json:"total"`
		Backend string `json:"backend"`
	}
	json.NewDecoder(lr.Body).Decode(&list) //nolint:errcheck
	lr.Body.Close()                        //nolint:errcheck
	if lr.StatusCode != http.StatusOK || list.Total != 1 || len(list.Files) != 1 {
		t.Fatalf("list = %d %+v", lr.StatusCode, list)
	}
	if list.Files[0].OriginalName != "informe.pdf" || list.Backend != "local" {
		t.Fatalf("metadata mismatch: %+v", list)
	}

	// Signed URL → the admin download leg; fetching it needs NO auth header and
	// serves the exact bytes through Store.Serve (Range honored).
	ur := adminGET(t, srv.URL+"/admin/tenants/acme/files/"+handle.FileID+"/url", true)
	var signed struct {
		URL       string `json:"url"`
		ExpiresIn int    `json:"expires_in"`
	}
	json.NewDecoder(ur.Body).Decode(&signed) //nolint:errcheck
	ur.Body.Close()                          //nolint:errcheck
	if ur.StatusCode != http.StatusOK || signed.URL == "" || signed.ExpiresIn != 2 {
		t.Fatalf("url = %d %+v", ur.StatusCode, signed)
	}
	dl, err := http.Get(signed.URL)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	got, _ := io.ReadAll(dl.Body)
	dl.Body.Close() //nolint:errcheck
	if dl.StatusCode != http.StatusOK || !bytes.Equal(got, pdfBytes(content)) {
		t.Fatalf("download = %d (bytes match %t)", dl.StatusCode, bytes.Equal(got, pdfBytes(content)))
	}
	rreq, _ := http.NewRequest(http.MethodGet, signed.URL, nil)
	rreq.Header.Set("Range", "bytes=0-3")
	rres, _ := http.DefaultClient.Do(rreq)
	part, _ := io.ReadAll(rres.Body)
	rres.Body.Close() //nolint:errcheck
	if rres.StatusCode != http.StatusPartialContent || string(part) != "%PDF" {
		t.Fatalf("range = %d %q", rres.StatusCode, part)
	}

	// Delete → 204; the listing empties; a second delete → 404.
	dreq, _ := http.NewRequest(http.MethodDelete, srv.URL+"/admin/tenants/acme/files/"+handle.FileID, nil)
	dreq.Header.Set("X-Admin-Key", "unit-key")
	dres, _ := http.DefaultClient.Do(dreq)
	dres.Body.Close() //nolint:errcheck
	if dres.StatusCode != http.StatusNoContent {
		t.Fatalf("delete = %d, want 204", dres.StatusCode)
	}
	lr2 := adminGET(t, srv.URL+"/admin/tenants/acme/files", true)
	var list2 struct {
		Total int `json:"total"`
	}
	json.NewDecoder(lr2.Body).Decode(&list2) //nolint:errcheck
	lr2.Body.Close()                         //nolint:errcheck
	if list2.Total != 0 {
		t.Fatalf("total after delete = %d, want 0", list2.Total)
	}
	dres2, _ := http.DefaultClient.Do(dreq)
	dres2.Body.Close() //nolint:errcheck
	if dres2.StatusCode != http.StatusNotFound {
		t.Fatalf("second delete = %d, want 404", dres2.StatusCode)
	}
}

// pdfBytes prefixes content with the PDF magic so the .pdf upload passes the
// magic-byte check (the test exercises the REAL validation, not a bypass).
func pdfBytes(content []byte) []byte { return append([]byte("%PDF-1.4\n"), content...) }

func TestAdminFiles_OWASPRejectionsSurface(t *testing.T) {
	srv, _ := newFilesTestServer(t, "acme")

	// Disallowed extension → the engine's 422 with its actionable reason.
	up := adminMultipart(t, srv.URL, "acme", "shell.php", "application/x-php", []byte("<?php"), true)
	body, _ := io.ReadAll(up.Body)
	up.Body.Close() //nolint:errcheck
	if up.StatusCode != http.StatusUnprocessableEntity || !strings.Contains(string(body), ".php") {
		t.Fatalf("php upload = %d %s", up.StatusCode, body)
	}
	// Spoofed content (php inside a .jpg declared image/jpeg) → 422 by magic bytes.
	up2 := adminMultipart(t, srv.URL, "acme", "photo.jpg", "image/jpeg", []byte("<?php system('id');"), true)
	body2, _ := io.ReadAll(up2.Body)
	up2.Body.Close() //nolint:errcheck
	if up2.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("spoofed jpg = %d %s", up2.StatusCode, body2)
	}
}

func TestAdminFiles_TenantIsolationAndUnknownTenant(t *testing.T) {
	srv, _ := newFilesTestServer(t, "acme", "beta")

	up := adminMultipart(t, srv.URL, "acme", "a.txt", "text/plain", []byte("tenant a only"), true)
	var handle struct {
		FileID string `json:"file_id"`
	}
	json.NewDecoder(up.Body).Decode(&handle) //nolint:errcheck
	up.Body.Close()                          //nolint:errcheck

	// beta's listing does NOT contain acme's file.
	lr := adminGET(t, srv.URL+"/admin/tenants/beta/files", true)
	var list struct {
		Total int `json:"total"`
	}
	json.NewDecoder(lr.Body).Decode(&list) //nolint:errcheck
	lr.Body.Close()                        //nolint:errcheck
	if list.Total != 0 {
		t.Fatalf("beta sees %d files, want 0", list.Total)
	}
	// acme's file id is meaningless under beta (metadata is tenant-scoped).
	ur := adminGET(t, srv.URL+"/admin/tenants/beta/files/"+handle.FileID+"/url", true)
	ur.Body.Close() //nolint:errcheck
	if ur.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant url = %d, want 404", ur.StatusCode)
	}
	// An unregistered tenant is 404 — never a lazily-created metadata table.
	lr2 := adminGET(t, srv.URL+"/admin/tenants/ghost/files", true)
	lr2.Body.Close() //nolint:errcheck
	if lr2.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown tenant list = %d, want 404", lr2.StatusCode)
	}
}

func TestAdminFiles_DownloadTokenFailuresUniform404(t *testing.T) {
	srv, _ := newFilesTestServer(t, "acme", "beta")
	up := adminMultipart(t, srv.URL, "acme", "s.txt", "text/plain", []byte("secret"), true)
	var handle struct {
		FileID string `json:"file_id"`
	}
	json.NewDecoder(up.Body).Decode(&handle) //nolint:errcheck
	up.Body.Close()                          //nolint:errcheck

	get := func(u string) int {
		resp, err := http.Get(u)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		resp.Body.Close() //nolint:errcheck
		return resp.StatusCode
	}
	base := srv.URL + "/admin/tenants/acme/files/" + handle.FileID + "/download?token="

	// Garbage / missing token.
	if c := get(base + "garbage"); c != http.StatusNotFound {
		t.Fatalf("garbage token = %d, want 404", c)
	}
	// Expired token.
	expired, _ := files.MintDownloadToken([]byte(unitSecret), "acme", handle.FileID, platformFilesRole, time.Nanosecond)
	time.Sleep(5 * time.Millisecond)
	if c := get(base + url.QueryEscape(expired)); c != http.StatusNotFound {
		t.Fatalf("expired token = %d, want 404", c)
	}
	// A token minted for ANOTHER tenant cannot serve this path (and vice versa).
	foreign, _ := files.MintDownloadToken([]byte(unitSecret), "beta", handle.FileID, platformFilesRole, time.Minute)
	if c := get(base + url.QueryEscape(foreign)); c != http.StatusNotFound {
		t.Fatalf("cross-tenant token = %d, want 404", c)
	}
	// A TENANT-minted token (a real RBAC role, not the platform sentinel) is
	// useless on the admin route — the two token worlds do not mix.
	tenantTok, _ := files.MintDownloadToken([]byte(unitSecret), "acme", handle.FileID, "admin", time.Minute)
	if c := get(base + url.QueryEscape(tenantTok)); c != http.StatusNotFound {
		t.Fatalf("tenant-role token = %d, want 404", c)
	}
	// The genuine capability still works.
	ok, _ := files.MintDownloadToken([]byte(unitSecret), "acme", handle.FileID, platformFilesRole, time.Minute)
	if c := get(base + url.QueryEscape(ok)); c != http.StatusOK {
		t.Fatalf("valid token = %d, want 200", c)
	}
}
