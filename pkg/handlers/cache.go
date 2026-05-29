package handlers

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"net/http"
)

// CachedGet wraps a GET handler to add ETag and Cache-Control headers.
// If the request includes an If-None-Match header that matches the computed ETag,
// it returns 304 Not Modified with no body. Only 200 responses are cached.
func CachedGet(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec := &bufferedWriter{header: make(http.Header), code: http.StatusOK}
		next.ServeHTTP(rec, r)

		// Pass non-200 responses through unchanged — don't cache errors
		if rec.code != http.StatusOK {
			for k, v := range rec.header {
				w.Header()[k] = v
			}
			w.WriteHeader(rec.code)
			w.Write(rec.buf.Bytes()) //nolint:errcheck
			return
		}

		body := rec.buf.Bytes()
		sum := md5.Sum(body)
		etag := fmt.Sprintf(`"%x"`, sum)

		for k, v := range rec.header {
			w.Header()[k] = v
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "private, max-age=0, must-revalidate")

		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		w.WriteHeader(http.StatusOK)
		w.Write(body) //nolint:errcheck
	}
}

// bufferedWriter captures a handler's response into memory.
type bufferedWriter struct {
	header http.Header
	code   int
	buf    bytes.Buffer
}

func (b *bufferedWriter) Header() http.Header        { return b.header }
func (b *bufferedWriter) WriteHeader(code int)       { b.code = code }
func (b *bufferedWriter) Write(p []byte) (int, error) { return b.buf.Write(p) }
