package userauth

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// NIGHT-SWEEP-S1 — the /auth body-decode contract (the F-8 naming axis applied
// here, mirroring platformadmin.decode): an unknown key is named, a sent body
// must parse even on the empty-tolerant helper, and only the caller's own key
// is ever echoed.

func TestDecodeBody_NamesUnknownKey(t *testing.T) {
	req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{"emial":"a@b.c","password":"x"}`))
	w := httptest.NewRecorder()
	var dst struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if decodeBody(w, req, &dst) {
		t.Fatal("unknown key must fail the decode")
	}
	body := w.Body.String()
	if !strings.Contains(body, `unknown field \"emial\"`) && !strings.Contains(body, "unknown field") {
		t.Fatalf("400 must name the unknown key, got: %s", body)
	}
	if !strings.Contains(body, "emial") {
		t.Fatalf("the caller's own key must be echoed, got: %s", body)
	}
}

func TestDecodeBody_OtherErrorsStayTerse(t *testing.T) {
	// A type error's Go-internal message (struct field names/types) must NOT
	// reach the pre-auth caller — only the unknown-field form is surfaced.
	req := httptest.NewRequest("POST", "/auth/login", strings.NewReader(`{"email":5}`))
	w := httptest.NewRecorder()
	var dst struct {
		Email string `json:"email"`
	}
	if decodeBody(w, req, &dst) {
		t.Fatal("type error must fail the decode")
	}
	body := w.Body.String()
	if strings.Contains(body, "Go struct") || strings.Contains(body, "cannot unmarshal") {
		t.Fatalf("Go internals leaked to a pre-auth caller: %s", body)
	}
	if !strings.Contains(body, "invalid JSON body") {
		t.Fatalf("expected the terse form, got: %s", body)
	}
}

func TestDecodeBodyAllowEmpty_EmptyTolerated_SentBodyMustParse(t *testing.T) {
	// Genuinely empty body → tolerated (refresh's header path).
	req := httptest.NewRequest("POST", "/auth/refresh", strings.NewReader(""))
	w := httptest.NewRecorder()
	var dst struct {
		Token string `json:"token"`
	}
	if !decodeBodyAllowEmpty(w, req, &dst) {
		t.Fatalf("empty body must be tolerated, got: %s", w.Body.String())
	}

	// A body that IS sent must parse: a misspelled key used to be swallowed and
	// fall through to the header in silence, nullifying DisallowUnknownFields.
	req = httptest.NewRequest("POST", "/auth/refresh", strings.NewReader(`{"tok":"abc"}`))
	w = httptest.NewRecorder()
	if decodeBodyAllowEmpty(w, req, &dst) {
		t.Fatal("a sent body with an unknown key must be rejected")
	}
	if !strings.Contains(w.Body.String(), "tok") {
		t.Fatalf("the unknown key must be named, got: %s", w.Body.String())
	}

	// Malformed JSON is a named rejection too, not a silent fall-through.
	req = httptest.NewRequest("POST", "/auth/refresh", strings.NewReader(`{broken`))
	w = httptest.NewRecorder()
	if decodeBodyAllowEmpty(w, req, &dst) {
		t.Fatal("malformed JSON must be rejected")
	}
}
