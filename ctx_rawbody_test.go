package appitools

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

// LIBRARY-GAPS-S1 — Ctx.RawBody: the webhook path.
//
// The bug it prevents is subtle and expensive: an http.Request body is a
// single-use stream, so the pre-RawBody idiom (io.ReadAll on ctx.Request().Body)
// left Bind with nothing to decode — and re-implementing the size cap by hand was
// the price of doing the SECURE thing on the single most security-sensitive
// handler most products ever write.

// newBodyCtx builds a requestCtx over a POST body — enough for the binders, which
// touch nothing else.
func newBodyCtx(body string) *requestCtx {
	req := httptest.NewRequest("POST", "/api/webhooks/x", strings.NewReader(body))
	return &requestCtx{w: httptest.NewRecorder(), r: req}
}

func TestRawBody_ExactBytes(t *testing.T) {
	// Deliberately ugly JSON: whitespace and key order are exactly what a
	// signature covers and exactly what a parse-then-reserialize destroys.
	const payload = `{ "b" : 2,   "a":1 }`
	raw, err := newBodyCtx(payload).RawBody()
	if err != nil {
		t.Fatalf("RawBody: %v", err)
	}
	if string(raw) != payload {
		t.Fatalf("RawBody must be byte-exact\n got: %q\nwant: %q", raw, payload)
	}
}

func TestRawBody_ThenBind(t *testing.T) {
	c := newBodyCtx(`{"event":"payment.approved","amount":1500}`)

	raw, err := c.RawBody()
	if err != nil {
		t.Fatalf("RawBody: %v", err)
	}
	// A real signature check over the raw bytes, exactly as a gateway adapter does.
	mac := hmac.New(sha256.New, []byte("secret"))
	mac.Write(raw)
	sig := hex.EncodeToString(mac.Sum(nil))

	var body struct {
		Event  string `json:"event"`
		Amount int    `json:"amount"`
	}
	if err := c.Bind(&body); err != nil {
		t.Fatalf("Bind after RawBody must still work: %v", err)
	}
	if body.Event != "payment.approved" || body.Amount != 1500 {
		t.Fatalf("Bind decoded the wrong values: %+v", body)
	}

	// And the signature is reproducible from the same buffer (RawBody is stable).
	raw2, _ := c.RawBody()
	mac2 := hmac.New(sha256.New, []byte("secret"))
	mac2.Write(raw2)
	if hex.EncodeToString(mac2.Sum(nil)) != sig {
		t.Fatal("a second RawBody must return the same bytes")
	}
}

func TestRawBody_AfterBind(t *testing.T) {
	// The reverse order must work too — the body is buffered once, not consumed.
	c := newBodyCtx(`{"event":"x"}`)
	var body map[string]any
	if err := c.Bind(&body); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	raw, err := c.RawBody()
	if err != nil {
		t.Fatalf("RawBody after Bind: %v", err)
	}
	if string(raw) != `{"event":"x"}` {
		t.Fatalf("RawBody after Bind lost the bytes: %q", raw)
	}
}

func TestRawBody_CapEnforcedWithoutHandlerCode(t *testing.T) {
	c := newBodyCtx(strings.Repeat("a", MaxBodyBytes+1))
	_, err := c.RawBody()
	if !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("an over-cap body must return ErrBodyTooLarge, got: %v", err)
	}
	// The same verdict is memoized for Bind (one read, one decision).
	if err := c.Bind(&struct{}{}); !errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("Bind must report the same cap error, got: %v", err)
	}
}

// A handler that simply returns the error gets a 413, not a masked 500.
func TestRawBody_TooLargeMapsTo413(t *testing.T) {
	app := &App{schema: tasksSchema()}
	rec := httptest.NewRecorder()
	rc := newBodyCtx("x")
	app.writeHandlerError(rec, rc, Route{Method: "POST", Path: "/api/hooks"}, ErrBodyTooLarge)
	if rec.Code != 413 {
		t.Fatalf("expected 413, got %d (%s)", rec.Code, rec.Body.String())
	}
}

func TestRawBody_EmptyBody(t *testing.T) {
	c := newBodyCtx("")
	raw, err := c.RawBody()
	if err != nil {
		t.Fatalf("an empty body is not an error: %v", err)
	}
	if len(raw) != 0 {
		t.Fatalf("expected no bytes, got %q", raw)
	}
}

func TestBind_SemanticsUnchanged(t *testing.T) {
	// Bind still decodes with a Decoder over the buffered bytes, so trailing
	// content is tolerated exactly as it was before the buffer existed.
	c := newBodyCtx(`{"a":1}` + "\n")
	var m map[string]int
	if err := c.Bind(&m); err != nil {
		t.Fatalf("Bind: %v", err)
	}
	if m["a"] != 1 {
		t.Fatalf("unexpected decode: %v", m)
	}
	// A malformed body still surfaces the json error (not a cap error).
	c2 := newBodyCtx(`{nope`)
	if err := c2.Bind(&m); err == nil || errors.Is(err, ErrBodyTooLarge) {
		t.Fatalf("expected a JSON syntax error, got: %v", err)
	}
}
