package appitools

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// FRONTEND-SPEC-S1: Route.ByteServing + Ctx.ServeFile — the seam that lets a
// custom route stream a tenant file (a public product image, an authorized
// download) around the response cache and the compression wrapper. These tests
// pin the boot validation, the matcher, the Ctx guards, and flush's behavior;
// the full round-trip (bytes, ETag, Range, cross-tenant 404, compression
// bypass) is serve_file_integration_test.go.

func TestValidateRoute_ByteServingRules(t *testing.T) {
	t.Parallel()
	s := tasksSchema()

	cases := []struct {
		name    string
		rt      Route
		wantErr string // "" = must be accepted
	}{
		{"GET literal path is accepted",
			Route{Method: "GET", Path: "/api/imagen", ByteServing: true, Handler: noopHandler}, ""},
		{"public + byte-serving composes (the storefront image shape)",
			Route{Method: "GET", Path: "/api/catalogo-imagen", Public: true, ByteServing: true, Handler: noopHandler}, ""},
		{"non-GET is rejected",
			Route{Method: "POST", Path: "/api/imagen", ByteServing: true, Handler: noopHandler}, "ByteServing is for GET"},
		{"a chi param would miss the exact-path bypass — rejected",
			Route{Method: "GET", Path: "/api/imagen/{id}", ByteServing: true, Handler: noopHandler}, "literal path"},
		{"a wildcard likewise",
			Route{Method: "GET", Path: "/api/imagen/*", ByteServing: true, Handler: noopHandler}, "literal path"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateRoute(tc.rt, s, map[string]bool{})
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("want accepted, got: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("want error containing %q, got: %v", tc.wantErr, err)
			}
		})
	}
}

func TestByteServingRoutePaths_NilWhenNoneAndExactWhenDeclared(t *testing.T) {
	t.Parallel()

	// No ByteServing routes → nil map, so the compress/cache predicates pay
	// exactly the pre-existing nil check (the common case must cost nothing).
	a := &App{routes: []Route{{Method: "POST", Path: "/api/checkout", Handler: noopHandler}}}
	if got := a.byteServingRoutePaths(); got != nil {
		t.Fatalf("no ByteServing routes: want nil, got %v", got)
	}

	a2 := &App{routes: []Route{
		{Method: "GET", Path: "/api/catalogo-imagen", ByteServing: true, Handler: noopHandler},
		{Method: "GET", Path: "/api/otra", Handler: noopHandler},
	}}
	got := a2.byteServingRoutePaths()
	if !got["/api/catalogo-imagen"] {
		t.Fatal("declared path missing from the matcher set")
	}
	if got["/api/otra"] {
		t.Fatal("non-ByteServing path leaked into the matcher set")
	}
}

func TestServeFile_GuardsAreLoud(t *testing.T) {
	t.Parallel()

	// Without ByteServing the stream would be cached/compressed/buffered —
	// a silent correctness bug, so the call itself must fail, naming the fix.
	c := &requestCtx{byteServing: false}
	if err := c.ServeFile("2f0c8a4e-8f7f-4f0e-9f0a-1a2b3c4d5e6f"); err == nil ||
		!strings.Contains(err.Error(), "ByteServing") {
		t.Fatalf("route without ByteServing: want a loud error naming the flag, got %v", err)
	}

	// After JSON/Error buffered a response there is exactly one response —
	// the file cannot silently replace it.
	c2 := &requestCtx{byteServing: true}
	_ = c2.JSON(200, map[string]any{"ok": true})
	if err := c2.ServeFile("2f0c8a4e-8f7f-4f0e-9f0a-1a2b3c4d5e6f"); err == nil ||
		!strings.Contains(err.Error(), "already buffered") {
		t.Fatalf("ServeFile after JSON: want error, got %v", err)
	}

	// A malformed id is the SAME uniform miss as an unknown one (no format
	// oracle on a download route) — the sentinel maps to 404 in the middleware.
	c3 := &requestCtx{byteServing: true}
	if err := c3.ServeFile("not-a-uuid"); !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("malformed id: want ErrFileNotFound, got %v", err)
	}

	// One response per request.
	c4 := &requestCtx{byteServing: true}
	if err := c4.ServeFile("2f0c8a4e-8f7f-4f0e-9f0a-1a2b3c4d5e6f"); err != nil {
		t.Fatalf("first ServeFile: %v", err)
	}
	if err := c4.ServeFile("2f0c8a4e-8f7f-4f0e-9f0a-1a2b3c4d5e6f"); err == nil ||
		!strings.Contains(err.Error(), "twice") {
		t.Fatalf("second ServeFile: want error, got %v", err)
	}
}

func TestFlush_ServeFileNever204AndBufferedResponseWins(t *testing.T) {
	t.Parallel()

	// A registered file must not fall into the "handler wrote nothing" 204
	// branch. With no engine wired (a test-built ctx) the serve degrades to a
	// masked 500 — never a silent 204, never a panic.
	c := &requestCtx{byteServing: true}
	if err := c.ServeFile("2f0c8a4e-8f7f-4f0e-9f0a-1a2b3c4d5e6f"); err != nil {
		t.Fatalf("ServeFile: %v", err)
	}
	rec := httptest.NewRecorder()
	c.flush(rec)
	if rec.Code == 204 {
		t.Fatal("flush answered 204 for a registered file")
	}
	if rec.Code != 500 {
		t.Fatalf("test-built ctx (no store): want masked 500, got %d", rec.Code)
	}

	// The buffered response (the error path) always wins over a registered
	// file: ctx.Error after ServeFile must flush the JSON error, not the blob.
	c2 := &requestCtx{byteServing: true}
	_ = c2.ServeFile("2f0c8a4e-8f7f-4f0e-9f0a-1a2b3c4d5e6f")
	_ = c2.Error(409, "conflicto", nil)
	rec2 := httptest.NewRecorder()
	c2.flush(rec2)
	if rec2.Code != 409 {
		t.Fatalf("buffered error should win: got %d, want 409", rec2.Code)
	}
	if ct := rec2.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("buffered error Content-Type = %q", ct)
	}
}
