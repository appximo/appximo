//go:build integration

// FRONTEND-SPEC-S1 integration: Ctx.ServeFile + Route.ByteServing against a
// real Postgres + real disk — the public-product-image seam, end to end:
//
//	upload → custom GET route authorizes → ctx.ServeFile streams the blob
//
// Pins the parts a unit test cannot: real bytes + stored Content-Type, the
// strong content-hash ETag (If-None-Match → 304), Range → 206, the uniform 404
// for unknown AND foreign-tenant ids, the compression bypass (no
// Content-Encoding on the stream), and the response-cache bypass (repeat
// authenticated GETs keep Content-Disposition — a cache hit would strip it).
// Reuses the shared Postgres container from TestMain (library_integration_test.go).
package appitools

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/miguelangel/appitools/pkg/schema"
	"github.com/miguelangel/appitools/tests/helpers"
)

// newByteServingApp builds the filejob-fixture app with two custom ServeFile
// routes: a Public one (the storefront-image shape) and an authenticated one
// (the protected-download shape, cache-eligible role → exercises the bypass).
func newByteServingApp(t *testing.T, filesDir string) *httptest.Server {
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
		t.Fatalf("New: %v", err)
	}
	serve := func(ctx Ctx) error { return ctx.ServeFile(ctx.Request().URL.Query().Get("id")) }
	if err := app.Register(Route{Method: "GET", Path: "/api/blob", Public: true, ByteServing: true, Handler: serve}); err != nil {
		t.Fatalf("register public blob route: %v", err)
	}
	if err := app.Register(Route{Method: "GET", Path: "/api/blob-auth", ByteServing: true, Handler: serve}); err != nil {
		t.Fatalf("register auth blob route: %v", err)
	}
	// FILES-2: same shape, immutable cache policy declared by the handler.
	serveCached := func(ctx Ctx) error {
		return ctx.ServeFile(ctx.Request().URL.Query().Get("id"), WithCacheControl(CacheControlImmutable))
	}
	if err := app.Register(Route{Method: "GET", Path: "/api/blob-cached", Public: true, ByteServing: true, Handler: serveCached}); err != nil {
		t.Fatalf("register cached blob route: %v", err)
	}
	t.Cleanup(func() { app.pool.Close() })
	srv := httptest.NewServer(app.buildRouter(app.bootSurface()))
	t.Cleanup(srv.Close)
	return srv
}

func getBlob(t *testing.T, srv *httptest.Server, path, host, token string, hdr map[string]string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+path, nil)
	req.Host = host
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	// DisableCompression so Accept-Encoding is OURS: the bypass assertion needs
	// to offer gzip explicitly and see the server decline it on the stream.
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	return resp
}

func TestServeFile_PublicRoute_StreamsWithETagRangeAnd404s(t *testing.T) {
	helpers.RegisterTenant(t, itPool, "bsrv", loadFileJobSchema(t))
	srv := newByteServingApp(t, t.TempDir())

	content := []byte("byte-serving seam \x00\x01 binary payload for range+etag checks")
	fileID, sha, _ := uploadMultipart(t, srv, "bsrv", "img.bin", "application/octet-stream", content)

	// Anonymous full read — the storefront <img> case: bytes, type, strong ETag,
	// and NO Content-Encoding even though we offered gzip (compression bypass).
	resp := getBlob(t, srv, "/api/blob?id="+fileID, "bsrv.localhost", "", map[string]string{"Accept-Encoding": "gzip"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("anonymous stream status = %d: %s", resp.StatusCode, b)
	}
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, content) {
		t.Fatal("streamed body mismatch")
	}
	if enc := resp.Header.Get("Content-Encoding"); enc != "" {
		t.Fatalf("stream was compressed (Content-Encoding=%q) — the ByteServing bypass did not engage", enc)
	}
	etag := resp.Header.Get("ETag")
	if etag != `"`+sha+`"` {
		t.Fatalf("ETag = %q, want the content hash %q", etag, `"`+sha+`"`)
	}

	// Conditional revalidation: the browser-cache path a stable image URL relies on.
	resp304 := getBlob(t, srv, "/api/blob?id="+fileID, "bsrv.localhost", "", map[string]string{"If-None-Match": etag})
	defer resp304.Body.Close()
	if resp304.StatusCode != http.StatusNotModified {
		t.Fatalf("If-None-Match status = %d, want 304", resp304.StatusCode)
	}

	// Range → 206 with exactly the requested slice.
	respR := getBlob(t, srv, "/api/blob?id="+fileID, "bsrv.localhost", "", map[string]string{"Range": "bytes=0-9"})
	defer respR.Body.Close()
	if respR.StatusCode != http.StatusPartialContent {
		t.Fatalf("Range status = %d, want 206", respR.StatusCode)
	}
	slice, _ := io.ReadAll(respR.Body)
	if !bytes.Equal(slice, content[:10]) {
		t.Fatalf("Range body = %q, want first 10 bytes", slice)
	}

	// Uniform miss: unknown uuid and malformed id read identically (404).
	for _, id := range []string{"2f0c8a4e-8f7f-4f0e-9f0a-1a2b3c4d5e6f", "not-a-uuid"} {
		r := getBlob(t, srv, "/api/blob?id="+id, "bsrv.localhost", "", nil)
		if r.StatusCode != http.StatusNotFound {
			t.Fatalf("miss %q status = %d, want 404", id, r.StatusCode)
		}
		r.Body.Close()
	}

	// ENG-32: HEAD on the custom GET route answers 200 with the GET's headers
	// and no body (http.ServeContent handles HEAD natively; the public-route
	// auth skip covers the HEAD alias).
	headReq, _ := http.NewRequest(http.MethodHead, srv.URL+"/api/blob?id="+fileID, nil)
	headReq.Host = "bsrv.localhost"
	headResp, err := (&http.Client{}).Do(headReq)
	if err != nil {
		t.Fatalf("HEAD: %v", err)
	}
	defer headResp.Body.Close()
	if headResp.StatusCode != http.StatusOK {
		t.Fatalf("HEAD status = %d, want 200 (ENG-32)", headResp.StatusCode)
	}
	if hb, _ := io.ReadAll(headResp.Body); len(hb) != 0 {
		t.Fatalf("HEAD carried a %d-byte body", len(hb))
	}
	if headResp.Header.Get("ETag") != etag {
		t.Fatalf("HEAD ETag = %q, want %q", headResp.Header.Get("ETag"), etag)
	}

	// FILES-2: the handler-declared cache policy rides the stream…
	respC := getBlob(t, srv, "/api/blob-cached?id="+fileID, "bsrv.localhost", "", nil)
	defer respC.Body.Close()
	if cc := respC.Header.Get("Cache-Control"); cc != CacheControlImmutable {
		t.Fatalf("Cache-Control = %q, want %q", cc, CacheControlImmutable)
	}
	// …and is NOT sent on the 404 path (a cached miss would pin forever).
	respMiss := getBlob(t, srv, "/api/blob-cached?id=2f0c8a4e-8f7f-4f0e-9f0a-1a2b3c4d5e6f", "bsrv.localhost", "", nil)
	defer respMiss.Body.Close()
	if respMiss.StatusCode != http.StatusNotFound {
		t.Fatalf("cached-route miss status = %d, want 404", respMiss.StatusCode)
	}
	if cc := respMiss.Header.Get("Cache-Control"); cc != "" {
		t.Fatalf("404 carried Cache-Control %q — a cacheable miss", cc)
	}
}

func TestServeFile_ForeignTenantIdIsTheSame404(t *testing.T) {
	helpers.RegisterTenant(t, itPool, "bsrva", loadFileJobSchema(t))
	helpers.RegisterTenant(t, itPool, "bsrvb", loadFileJobSchema(t))
	srv := newByteServingApp(t, t.TempDir())

	fileID, _, _ := uploadMultipart(t, srv, "bsrva", "a.txt", "text/plain", []byte("tenant a's bytes"))

	// Tenant B's Host + tenant A's file id: the metadata lives in tenant A's
	// schema, so B's lookup finds nothing — isolation is structural, and the
	// answer is indistinguishable from "no such file".
	resp := getBlob(t, srv, "/api/blob?id="+fileID, "bsrvb.localhost", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("cross-tenant stream status = %d, want 404", resp.StatusCode)
	}
}

func TestServeFile_AuthedRoute_BypassesResponseCache(t *testing.T) {
	helpers.RegisterTenant(t, itPool, "bsrvc", loadFileJobSchema(t))
	srv := newByteServingApp(t, t.TempDir())

	content := []byte("cache-bypass probe payload")
	fileID, _, _ := uploadMultipart(t, srv, "bsrvc", "doc.txt", "text/plain", content)
	tok := helpers.GenToken(t, "admin", "admin-user", "bsrvc")

	// Two consecutive authenticated GETs with a cache-eligible role (admin,
	// wildcard, no row condition). WITHOUT the ByteServing cache bypass the
	// second would be an LRU hit that replays only the JSON-shaped header
	// allowlist — losing Content-Disposition. Both must carry it, with the
	// full body.
	for i := 0; i < 2; i++ {
		resp := getBlob(t, srv, "/api/blob-auth?id="+fileID, "bsrvc.localhost", tok, nil)
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("authed stream #%d status = %d", i+1, resp.StatusCode)
		}
		if !bytes.Equal(body, content) {
			t.Fatalf("authed stream #%d body mismatch", i+1)
		}
		if cd := resp.Header.Get("Content-Disposition"); cd == "" {
			t.Fatalf("authed stream #%d lost Content-Disposition — response served from the cache?", i+1)
		}
	}

	// And without a token the authenticated route stays closed (401) — the
	// ByteServing flag never widens auth.
	resp := getBlob(t, srv, "/api/blob-auth?id="+fileID, "bsrvc.localhost", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous on authed route = %d, want 401", resp.StatusCode)
	}
}

// The per-resource RBAC form can grant the BUILT-IN files store (validator
// special case, this session) — and the grant is honored at RUNTIME: a scoped
// role uploads (create) and reads, and an action it does not declare (delete)
// stays deny-by-default. Before this, only role-global/wildcard roles could
// reach /api/files.
func TestFilesGrant_PermissionsFormWorksAtRuntime(t *testing.T) {
	schemaJSON := `{
	  "$schema": "https://appitools.dev/schema/v1", "version": "1", "name": "files-grant",
	  "resources": { "docs": { "fields": { "title": { "type": "string", "required": true } } } },
	  "rbac": { "roles": {
	    "admin": { "resources": "*", "actions": ["*"] },
	    "staff": { "permissions": {
	      "docs":  { "actions": ["read", "create"] },
	      "files": { "actions": ["read", "create"] }
	    } }
	  } }
	}`
	dir := t.TempDir()
	schemaPath := dir + "/schema.json"
	if err := os.WriteFile(schemaPath, []byte(schemaJSON), 0o600); err != nil {
		t.Fatalf("write schema: %v", err)
	}
	var parsed schema.APISchema
	if err := json.Unmarshal([]byte(schemaJSON), &parsed); err != nil {
		t.Fatalf("parse schema: %v", err)
	}
	helpers.RegisterTenant(t, itPool, "fgrant", &parsed)

	app, err := New(Config{
		SchemaPath: schemaPath, DSN: itConnStr,
		JWTSecret: helpers.JWTSecret, AdminKey: helpers.AdminKey, Env: "test",
		FilesDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { app.pool.Close() })
	srv := httptest.NewServer(app.buildRouter(app.bootSurface()))
	t.Cleanup(srv.Close)

	// staff (permissions form) CAN upload…
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	pw, _ := mw.CreateFormFile("file", "nota.txt")
	pw.Write([]byte("scoped upload")) //nolint:errcheck
	mw.Close()                        //nolint:errcheck
	staffTok := helpers.GenToken(t, "staff", "staff-user", "fgrant")
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/files", &buf)
	req.Host = "fgrant.localhost"
	req.Header.Set("Authorization", "Bearer "+staffTok)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("staff upload: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("staff upload status = %d, want 201: %s", resp.StatusCode, b)
	}
	var up struct {
		FileID string `json:"file_id"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&up)

	// …and read…
	rd := do(t, srv, "GET", "/api/files/"+up.FileID, "fgrant.localhost", staffTok, "")
	defer rd.Body.Close()
	if rd.StatusCode != http.StatusOK {
		t.Fatalf("staff download status = %d, want 200", rd.StatusCode)
	}

	// …but DELETE stays deny-by-default (not in the grant).
	del := do(t, srv, "DELETE", "/api/files/"+up.FileID, "fgrant.localhost", staffTok, "")
	defer del.Body.Close()
	if del.StatusCode != http.StatusForbidden {
		t.Fatalf("staff delete status = %d, want 403 (delete not granted)", del.StatusCode)
	}
}
