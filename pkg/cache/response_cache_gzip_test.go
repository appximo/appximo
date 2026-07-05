package cache

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/miguelangel/appitools/pkg/auth"
)

// bigBody is large and repetitive so gzip is guaranteed to shrink it.
var bigBody = func() string {
	var b strings.Builder
	b.WriteString(`{"data":[`)
	for i := 0; i < 60; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"id":"000045e7-4f4f-4d09-9133-22b6a22cb8ef","code":"GD-T10-93854","destination":"Dest-93854","status":"delivered","weight_kg":55}`)
	}
	b.WriteString(`]}`)
	return b.String()
}()

func gunzip(t *testing.T, b []byte) string {
	t.Helper()
	zr, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	out, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip read: %v", err)
	}
	return string(out)
}

func gzipHandler(rc *ResponseCache, body string, calls *int) http.Handler {
	return rc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			*calls++
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(body)) //nolint:errcheck
	}))
}

func validatedReq(token, accept string) *http.Request {
	r := withTenant(httptest.NewRequest(http.MethodGet, "/api/guides?filter[status]=delivered", nil), "acme")
	r.Header.Set("Authorization", "Bearer "+token)
	if accept != "" {
		r.Header.Set("Accept-Encoding", accept)
	}
	return r
}

// HIT for a gzip client: response is Content-Encoding: gzip, the body is valid
// gzip that decompresses to the original, Content-Length matches the wire bytes,
// and the wire bytes are smaller than the plain body.
func TestCacheHitGzipEncoded(t *testing.T) {
	const token = "tok-gzip-hit"
	auth.SetCachedClaims("", token, &auth.Claims{Role: "super_admin", TenantID: "acme"})

	rc := New(5 * time.Second)
	calls := 0
	h := gzipHandler(rc, bigBody, &calls)

	h.ServeHTTP(httptest.NewRecorder(), validatedReq(token, "gzip")) // miss → populate
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, validatedReq(token, "gzip")) // hit

	if calls != 1 {
		t.Fatalf("handler must run exactly once (miss only), got %d", calls)
	}
	if rec.Header().Get("X-Cache") != "HIT" {
		t.Errorf("expected X-Cache: HIT, got %q", rec.Header().Get("X-Cache"))
	}
	if enc := rec.Header().Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("expected Content-Encoding: gzip, got %q", enc)
	}
	if v := rec.Header().Get("Vary"); !strings.Contains(v, "Accept-Encoding") {
		t.Errorf("expected Vary: Accept-Encoding, got %q", v)
	}
	if cl := rec.Header().Get("Content-Length"); cl != strconv.Itoa(rec.Body.Len()) {
		t.Errorf("Content-Length %q != wire bytes %d", cl, rec.Body.Len())
	}
	if rec.Body.Len() >= len(bigBody) {
		t.Errorf("gzipped wire bytes (%d) not smaller than plain (%d)", rec.Body.Len(), len(bigBody))
	}
	if got := gunzip(t, rec.Body.Bytes()); got != bigBody {
		t.Errorf("decompressed body mismatch")
	}
}

// HIT for a client that does NOT accept gzip: plain body, no Content-Encoding,
// directly readable.
func TestCacheHitPlainForNonGzipClient(t *testing.T) {
	const token = "tok-plain-hit"
	auth.SetCachedClaims("", token, &auth.Claims{Role: "super_admin", TenantID: "acme"})

	rc := New(5 * time.Second)
	calls := 0
	h := gzipHandler(rc, bigBody, &calls)

	h.ServeHTTP(httptest.NewRecorder(), validatedReq(token, "")) // miss → populate
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, validatedReq(token, "")) // hit, no Accept-Encoding

	if calls != 1 {
		t.Fatalf("handler must run exactly once, got %d", calls)
	}
	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Errorf("non-gzip client must not get Content-Encoding, got %q", enc)
	}
	if rec.Body.String() != bigBody {
		t.Errorf("plain body mismatch")
	}
}

// A miss must store BOTH the plain bytes and a smaller gzip copy.
func TestCacheMissStoresCompressed(t *testing.T) {
	const token = "tok-miss-store"
	auth.SetCachedClaims("", token, &auth.Claims{Role: "super_admin", TenantID: "acme"})

	rc := New(5 * time.Second)
	h := gzipHandler(rc, bigBody, nil)
	h.ServeHTTP(httptest.NewRecorder(), validatedReq(token, "gzip"))

	item := rc.get("acme", "/api/guides?filter[status]=delivered")
	if item == nil {
		t.Fatal("miss did not populate the cache")
	}
	if string(item.plain) != bigBody {
		t.Error("stored plain bytes differ from handler output")
	}
	if item.gzipped == nil {
		t.Fatal("miss did not pre-compress the body")
	}
	if len(item.gzipped) >= len(item.plain) {
		t.Errorf("stored gzip (%d) not smaller than plain (%d)", len(item.gzipped), len(item.plain))
	}
	if got := gunzip(t, item.gzipped); got != bigBody {
		t.Error("stored gzip does not decompress to the original")
	}
}

// Tiny, incompressible bodies must fall back to plain (no broken gzip).
func TestCacheTinyBodyNotCompressed(t *testing.T) {
	const token = "tok-tiny"
	auth.SetCachedClaims("", token, &auth.Claims{Role: "super_admin", TenantID: "acme"})

	rc := New(5 * time.Second)
	h := gzipHandler(rc, `{}`, nil)
	h.ServeHTTP(httptest.NewRecorder(), validatedReq(token, "gzip")) // miss

	item := rc.get("acme", "/api/guides?filter[status]=delivered")
	if item == nil || item.gzipped != nil {
		t.Fatalf("tiny body should not be stored gzipped: %+v", item)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, validatedReq(token, "gzip")) // hit
	if enc := rec.Header().Get("Content-Encoding"); enc != "" {
		t.Errorf("tiny body must be served plain, got Content-Encoding %q", enc)
	}
	if rec.Body.String() != `{}` {
		t.Errorf("tiny body mismatch: %q", rec.Body.String())
	}
}

// ETag set by the handler survives in the cache, and conditional requests still
// reach the handler (which answers 304) because If-None-Match bypasses the cache.
func TestCacheETagAndConditional(t *testing.T) {
	const token = "tok-etag"
	const etag = `"v1-abc"`
	auth.SetCachedClaims("", token, &auth.Claims{Role: "super_admin", TenantID: "acme"})

	rc := New(5 * time.Second)
	conditionalHits := 0
	h := rc.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Etag", etag)
		if r.Header.Get("If-None-Match") == etag {
			conditionalHits++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(bigBody)) //nolint:errcheck
	}))

	// Normal GET — cached, ETag preserved on the HIT.
	h.ServeHTTP(httptest.NewRecorder(), validatedReq(token, "gzip")) // miss
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, validatedReq(token, "gzip")) // hit
	if rec.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("expected HIT")
	}
	if rec.Header().Get("Etag") != etag {
		t.Errorf("cached HIT lost ETag: %q", rec.Header().Get("Etag"))
	}

	// Conditional GET — must bypass the cache and let the handler answer 304.
	cond := validatedReq(token, "gzip")
	cond.Header.Set("If-None-Match", etag)
	crec := httptest.NewRecorder()
	h.ServeHTTP(crec, cond)
	if crec.Code != http.StatusNotModified {
		t.Errorf("expected 304 from handler, got %d", crec.Code)
	}
	if conditionalHits != 1 {
		t.Errorf("conditional request must reach the handler once, got %d", conditionalHits)
	}
}

// Concurrent HITs with a mix of gzip and plain clients: no data race (run with
// -race) and every caller gets a correct, correctly-encoded body.
func TestCacheConcurrentMixedEncoding(t *testing.T) {
	const token = "tok-mixed"
	auth.SetCachedClaims("", token, &auth.Claims{Role: "super_admin", TenantID: "acme"})

	rc := New(5 * time.Second)
	h := gzipHandler(rc, bigBody, nil)
	h.ServeHTTP(httptest.NewRecorder(), validatedReq(token, "gzip")) // populate

	const n = 40
	var wg sync.WaitGroup
	errs := make([]string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			wantGzip := idx%2 == 0
			accept := ""
			if wantGzip {
				accept = "gzip"
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, validatedReq(token, accept))
			if wantGzip {
				if rec.Header().Get("Content-Encoding") != "gzip" {
					errs[idx] = "expected gzip"
					return
				}
				if gunzip(t, rec.Body.Bytes()) != bigBody {
					errs[idx] = "gzip body mismatch"
				}
			} else {
				if rec.Header().Get("Content-Encoding") != "" {
					errs[idx] = "unexpected gzip"
					return
				}
				if rec.Body.String() != bigBody {
					errs[idx] = "plain body mismatch"
				}
			}
		}(i)
	}
	wg.Wait()
	for i, e := range errs {
		if e != "" {
			t.Errorf("caller %d: %s", i, e)
		}
	}
}

// BenchmarkCacheHit measures the new gzip-free HIT path: pre-compressed bytes are
// written verbatim, no per-hit compression.
func BenchmarkCacheHit(b *testing.B) {
	const token = "bench-tok"
	auth.SetCachedClaims("", token, &auth.Claims{Role: "super_admin", TenantID: "acme"})
	rc := New(5 * time.Minute)
	h := gzipHandler(rc, bigBody, nil)
	h.ServeHTTP(httptest.NewRecorder(), validatedReq(token, "gzip")) // populate once

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		h.ServeHTTP(httptest.NewRecorder(), validatedReq(token, "gzip"))
	}
}

// BenchmarkGzipPerHit isolates the per-hit compression the OLD path paid on every
// single HIT (chi Compress, pooled writer — modelled here by the same pooled
// gzipBytes the new code calls once per miss). Adding this to BenchmarkCacheHit's
// hit-serve cost approximates the old per-hit cost; the new path pays it zero
// times per hit. This is the CPU the change removes from the hot path.
func BenchmarkGzipPerHit(b *testing.B) {
	plain := []byte(bigBody)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if gzipBytes(plain) == nil {
			b.Fatal("expected compressible body")
		}
	}
}
