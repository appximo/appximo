package handlers

import (
	"crypto/md5"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func echoHandler(body string, code int) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		w.Write([]byte(body)) //nolint:errcheck
	}
}

func TestCachedGet_SetsETagHeader(t *testing.T) {
	h := CachedGet(echoHandler(`{"data":[]}`, http.StatusOK))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	etag := rec.Header().Get("ETag")
	if etag == "" {
		t.Fatal("expected ETag header")
	}
	// ETag must be the MD5 of the body wrapped in quotes
	body := rec.Body.Bytes()
	sum := md5.Sum(body)
	want := fmt.Sprintf(`"%x"`, sum)
	if etag != want {
		t.Errorf("ETag: got %q, want %q", etag, want)
	}
}

func TestCachedGet_SetsCacheControlHeader(t *testing.T) {
	h := CachedGet(echoHandler(`{}`, http.StatusOK))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	cc := rec.Header().Get("Cache-Control")
	if !strings.Contains(cc, "private") {
		t.Errorf("Cache-Control should contain 'private': %q", cc)
	}
}

func TestCachedGet_304WhenIfNoneMatchMatches(t *testing.T) {
	body := `{"data":[]}`
	sum := md5.Sum([]byte(body))
	etag := fmt.Sprintf(`"%x"`, sum)

	h := CachedGet(echoHandler(body, http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("If-None-Match", etag)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotModified {
		t.Errorf("expected 304, got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("expected empty body on 304, got %d bytes", rec.Body.Len())
	}
}

func TestCachedGet_200WhenIfNoneMatchDoesNotMatch(t *testing.T) {
	h := CachedGet(echoHandler(`{"data":[1,2,3]}`, http.StatusOK))
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("If-None-Match", `"stale-etag"`)

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 with stale ETag, got %d", rec.Code)
	}
}

func TestCachedGet_DifferentBodyDifferentETag(t *testing.T) {
	getETag := func(body string) string {
		h := CachedGet(echoHandler(body, http.StatusOK))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
		return rec.Header().Get("ETag")
	}

	e1 := getETag(`{"data":[]}`)
	e2 := getETag(`{"data":[{"id":"1"}]}`)
	if e1 == e2 {
		t.Error("different bodies must produce different ETags")
	}
}

func TestCachedGet_NonOKResponsePassedThrough(t *testing.T) {
	// 400 responses should NOT get ETag headers
	h := CachedGet(echoHandler(`{"error":"bad"}`, http.StatusBadRequest))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 passthrough, got %d", rec.Code)
	}
	if rec.Header().Get("ETag") != "" {
		t.Error("ETag must not be set on non-200 response")
	}
}
