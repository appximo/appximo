//go:build integration

// FILES-V1 integration tests: the content-addressable file store against a real
// Postgres (the per-tenant files table) and real disk (the CAS blobs), plus the
// full e2e chain that ties the store to the XLSX consumer:
//
//	upload .xlsx → POST /api/files → file_id → POST filejob{file_ref:file_id}
//	  → filejobs.created → worker VFS.Get(tenant, file_id) → stream + aggregate
//	  → writeback → job completed
//
// Reuses the shared Postgres container + control plane from TestMain
// (library_integration_test.go).
package appximo

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rs/zerolog"
	"github.com/xuri/excelize/v2"

	"github.com/appximo/appximo/pkg/consumers"
	"github.com/appximo/appximo/pkg/files"
	"github.com/appximo/appximo/pkg/worker"
	"github.com/appximo/appximo/tests/helpers"
)

// newFileJobAppWithFiles builds the filejob app with an explicit CAS root so the
// engine and the worker share the same blob directory on this host.
func newFileJobAppWithFiles(t *testing.T, filesDir string) *httptest.Server {
	t.Helper()
	app, err := New(Config{
		SchemaPath: fileJobFixturePath(),
		DSN:        itConnStr,
		JWTSecret:  helpers.JWTSecret,
		AdminKey:   helpers.AdminKey,
		Env:        "test",
		FilesDir:   filesDir,
	})
	if err != nil {
		t.Fatalf("New(filejob+files): %v", err)
	}
	t.Cleanup(func() { app.pool.Close() })
	srv := httptest.NewServer(app.buildRouter(app.bootSurface()))
	t.Cleanup(srv.Close)
	return srv
}

// uploadMultipart POSTs content as a multipart "file" part and returns the parsed
// {file_id, sha256, size} response.
func uploadMultipart(t *testing.T, srv *httptest.Server, tenant, filename, contentType string, content []byte) (fileID, sha string, size int64) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	pw, err := mw.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := pw.Write(content); err != nil {
		t.Fatalf("write content: %v", err)
	}
	mw.Close() //nolint:errcheck

	adminTok := helpers.GenToken(t, "admin", "admin-user", tenant)
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/files", &buf)
	req.Host = tenant + ".localhost"
	req.Header.Set("Authorization", "Bearer "+adminTok)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("upload status = %d: %s", resp.StatusCode, b)
	}
	var out struct {
		FileID string `json:"file_id"`
		SHA256 string `json:"sha256"`
		Size   int64  `json:"size"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode upload: %v", err)
	}
	if out.FileID == "" {
		t.Fatal("upload returned empty file_id")
	}
	return out.FileID, out.SHA256, out.Size
}

func TestFiles_UploadDownload_RoundTrip(t *testing.T) {
	helpers.RegisterTenant(t, itPool, "filesrt", loadFileJobSchema(t))
	srv := newFileJobAppWithFiles(t, t.TempDir())

	content := []byte("integration round-trip payload \x00\x01\x02 binary safe")
	fileID, _, size := uploadMultipart(t, srv, "filesrt", "payload.bin", "application/octet-stream", content)
	if size != int64(len(content)) {
		t.Fatalf("size = %d, want %d", size, len(content))
	}

	adminTok := helpers.GenToken(t, "admin", "admin-user", "filesrt")
	resp := do(t, srv, "GET", "/api/files/"+fileID, "filesrt.localhost", adminTok, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download status = %d, want 200", resp.StatusCode)
	}
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, content) {
		t.Fatal("download body mismatch")
	}
}

func TestFiles_TenantIsolation(t *testing.T) {
	helpers.RegisterTenant(t, itPool, "fisoa", loadFileJobSchema(t))
	helpers.RegisterTenant(t, itPool, "fisob", loadFileJobSchema(t))
	srv := newFileJobAppWithFiles(t, t.TempDir())

	fileID, _, _ := uploadMultipart(t, srv, "fisoa", "a.txt", "text/plain", []byte("tenant a only"))

	// Tenant B (valid token + Host) cannot read tenant A's file id → 404 (B's files
	// table has no such row; an absent files table also reads as not-found).
	tokB := helpers.GenToken(t, "admin", "u", "fisob")
	resp := do(t, srv, "GET", "/api/files/"+fileID, "fisob.localhost", tokB, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant download status = %d, want 404", resp.StatusCode)
	}
}

// TestFiles_XLSX_EndToEnd is the full chain: upload an .xlsx to the file store,
// reference its file_id from a filejob, and let the worker resolve it via VFS.Get
// and process it to completion.
func TestFiles_XLSX_EndToEnd(t *testing.T) {
	helpers.RegisterTenant(t, itPool, "filesxlsx", loadFileJobSchema(t))
	filesDir := t.TempDir()
	srv := newFileJobAppWithFiles(t, filesDir)

	// Build the .xlsx in memory and upload it to the store.
	f := excelize.NewFile()
	sheet := f.GetSheetName(0)
	rows := [][]any{{"tipo", "monto"}, {"ingreso", 100}, {"ingreso", 50}, {"egreso", 25}}
	for i, row := range rows {
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		if err := f.SetSheetRow(sheet, cell, &row); err != nil {
			t.Fatalf("set row: %v", err)
		}
	}
	var xbuf bytes.Buffer
	if err := f.Write(&xbuf); err != nil {
		t.Fatalf("write xlsx: %v", err)
	}
	f.Close() //nolint:errcheck

	fileID, _, _ := uploadMultipart(t, srv, "filesxlsx",
		"report.xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", xbuf.Bytes())

	// The filejob's file_ref is the VFS file_id (NOT a raw path).
	id := createFileJob(t, srv, "filesxlsx", fileID)

	// The worker resolves file_id → blob via VFS.Get over the SAME CAS root.
	vfs := files.NewLocal(filesDir, files.NewPGStore(itPool))
	client := worker.NewEngineClient(srv.URL, "localhost", helpers.JWTSecret, "service_worker", worker.DefaultServiceTokenTTL)
	proc := consumers.NewXLSXProcessor(client, "filejobs", zerolog.Nop()).
		WithFileOpener(func(ctx context.Context, tenant, ref string) (io.ReadCloser, error) {
			rc, _, err := vfs.Get(ctx, tenant, ref)
			return rc, err
		})

	nop := zerolog.Nop()
	res, err := worker.Drain(context.Background(), itPool, proc, 50, 5, &nop)
	if err != nil {
		t.Fatalf("Drain: %v", err)
	}
	if res.Processed < 1 {
		t.Fatalf("processed %d, want >=1", res.Processed)
	}

	job := getFileJob(t, srv, "filesxlsx", id)
	if job["status"] != "completed" {
		t.Fatalf("status = %v, want completed", job["status"])
	}
	resultStr, _ := job["result"].(string)
	var result consumers.XLSXResult
	if err := json.Unmarshal([]byte(resultStr), &result); err != nil {
		t.Fatalf("decode result %q: %v", resultStr, err)
	}
	if result.Rows != 3 || result.Total != 175 || result.ByType["ingreso"] != 150 || result.ByType["egreso"] != 25 {
		t.Fatalf("unexpected aggregate: %+v", result)
	}
}

// TestFiles_DownloadBypassesCompression pins the FILES-FIX-SENDFILE routing on
// the FULL engine router: a download requested with Accept-Encoding: gzip must
// come back UNCOMPRESSED (the byte-serving routes are routed around the
// Compress wrapper so http.ServeContent keeps the io.ReaderFrom/sendfile fast
// path) with its serve headers intact — while a JSON route through the same
// chain still gets gzipped (compression itself is not disabled).
func TestFiles_DownloadBypassesCompression(t *testing.T) {
	helpers.RegisterTenant(t, itPool, "filesgz", loadFileJobSchema(t))
	srv := newFileJobAppWithFiles(t, t.TempDir())

	content := bytes.Repeat([]byte("compress-me-if-you-can "), 500) // ~11 KB, compressible on purpose
	fileID, sha, _ := uploadMultipart(t, srv, "filesgz", "notes.txt", "text/plain", content)

	adminTok := helpers.GenToken(t, "admin", "admin-user", "filesgz")
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/files/"+fileID, nil)
	req.Host = "filesgz.localhost"
	req.Header.Set("Authorization", "Bearer "+adminTok)
	req.Header.Set("Accept-Encoding", "gzip")
	tr := &http.Transport{DisableCompression: true} // see the wire encoding, no auto-gunzip
	resp, err := (&http.Client{Transport: tr}).Do(req)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download status = %d, want 200", resp.StatusCode)
	}
	if ce := resp.Header.Get("Content-Encoding"); ce != "" {
		t.Fatalf("download Content-Encoding = %q, want none (bypass broken — sendfile lost)", ce)
	}
	if et := resp.Header.Get("Etag"); et != `"`+sha+`"` {
		t.Fatalf("download ETag = %q, want content hash", et)
	}
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, content) {
		t.Fatal("download body mismatch")
	}

	// Same chain, JSON route: gzip still applies (compression not disabled).
	jreq, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/files/"+fileID+"/url", nil)
	jreq.Host = "filesgz.localhost"
	jreq.Header.Set("Authorization", "Bearer "+adminTok)
	jreq.Header.Set("Accept-Encoding", "gzip")
	jresp, err := (&http.Client{Transport: tr}).Do(jreq)
	if err != nil {
		t.Fatalf("json route: %v", err)
	}
	defer jresp.Body.Close()
	if jresp.StatusCode != http.StatusOK {
		t.Fatalf("json route status = %d, want 200", jresp.StatusCode)
	}
	if ce := jresp.Header.Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("json route Content-Encoding = %q, want gzip (Compress must stay on for JSON)", ce)
	}
}
