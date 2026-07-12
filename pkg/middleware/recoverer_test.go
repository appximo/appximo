package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRecoverer_PanicBecomes500(t *testing.T) {
	var (
		hookCalled bool
		gotStack   []byte
		gotVal     any
	)
	h := Recoverer(func(r *http.Request, recovered any, stack []byte) {
		hookCalled, gotVal, gotStack = true, recovered, stack
	})(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("kaboom")
	}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/x", nil))

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
	if body := strings.TrimSpace(rr.Body.String()); body != `{"error":"internal error"}` {
		t.Fatalf("body = %q, want masked JSON error", body)
	}
	if !hookCalled {
		t.Fatal("onPanic hook was not invoked")
	}
	if gotVal != "kaboom" {
		t.Fatalf("recovered value = %v, want kaboom", gotVal)
	}
	if len(gotStack) == 0 {
		t.Fatal("expected a non-empty stack trace")
	}
}

func TestRecoverer_NoPanic_PassesThrough(t *testing.T) {
	var hookCalled bool
	h := Recoverer(func(r *http.Request, recovered any, stack []byte) { hookCalled = true })(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTeapot)
			_, _ = w.Write([]byte("ok"))
		}))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/x", nil))

	if rr.Code != http.StatusTeapot {
		t.Fatalf("status = %d, want 418 (handler untouched)", rr.Code)
	}
	if rr.Body.String() != "ok" {
		t.Fatalf("body = %q, want ok", rr.Body.String())
	}
	if hookCalled {
		t.Fatal("onPanic must not fire when there is no panic")
	}
}

func TestRecoverer_NilHookDoesNotCrash(t *testing.T) {
	h := Recoverer(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("kaboom")
	}))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest("GET", "/api/x", nil))
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rr.Code)
	}
}

func TestRecoverer_AbortHandlerRepanics(t *testing.T) {
	h := Recoverer(nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		if rvr := recover(); rvr != http.ErrAbortHandler { //nolint:errorlint // sentinel
			t.Fatalf("expected ErrAbortHandler to propagate, got %v", rvr)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/x", nil))
	t.Fatal("expected ErrAbortHandler to re-panic")
}
