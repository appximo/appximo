package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSelectiveCompress pins the FILES-FIX-SENDFILE contract:
//   - a SKIPPED request reaches the handler with the ResponseWriter UNWRAPPED
//     (pointer-identical to what the server passed in — the io.ReaderFrom /
//     sendfile fast path depends on exactly this), and its response carries no
//     Content-Encoding even when the client accepts gzip;
//   - every other request keeps the normal Compress behavior (matching content
//     types ARE gzipped).
//
// A future router refactor that re-wraps the byte-serving routes breaks this
// test, not just a benchmark.
func TestSelectiveCompress(t *testing.T) {
	blob := bytes.Repeat([]byte{0xa5, 0x5a, 0x01}, 1500)
	jsonBody := `{"data":"` + strings.Repeat("x", 4000) + `"}`

	var sawWriter http.ResponseWriter
	mw := SelectiveCompress(
		func(r *http.Request) bool { return strings.HasPrefix(r.URL.Path, "/blob") },
		5, "application/json",
	)
	h := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawWriter = w
		if strings.HasPrefix(r.URL.Path, "/blob") {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(blob) //nolint:errcheck
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, jsonBody) //nolint:errcheck
	}))

	// Skipped path: naked writer, no Content-Encoding, bytes verbatim.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/blob/abc", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec, req)
	if sawWriter != http.ResponseWriter(rec) {
		t.Fatalf("skipped path: handler got a WRAPPED writer (%T) — the sendfile fast path is broken", sawWriter)
	}
	if ce := rec.Header().Get("Content-Encoding"); ce != "" {
		t.Fatalf("skipped path: Content-Encoding = %q, want none", ce)
	}
	if !bytes.Equal(rec.Body.Bytes(), blob) {
		t.Fatal("skipped path: body altered")
	}

	// Non-skipped JSON path: Compress still does its job.
	rec2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "/api/things", nil)
	req2.Header.Set("Accept-Encoding", "gzip")
	h.ServeHTTP(rec2, req2)
	if sawWriter == http.ResponseWriter(rec2) {
		t.Fatal("json path: handler got the naked writer — Compress was skipped for a non-blob route")
	}
	if ce := rec2.Header().Get("Content-Encoding"); ce != "gzip" {
		t.Fatalf("json path: Content-Encoding = %q, want gzip", ce)
	}
	zr, err := gzip.NewReader(rec2.Body)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	got, _ := io.ReadAll(zr)
	if string(got) != jsonBody {
		t.Fatal("json path: gunzipped body mismatch")
	}
}
