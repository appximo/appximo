package logging_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/miguelangel/appitools/pkg/logging"
)

func TestRedactWriter_RedactsToken(t *testing.T) {
	var buf bytes.Buffer
	rw := logging.NewRedactWriter(&buf)
	input := []byte(`{"level":"info","token":"supersecret","msg":"ok"}`)
	rw.Write(input) //nolint:errcheck

	out := buf.String()
	if strings.Contains(out, "supersecret") {
		t.Errorf("token value must be redacted; got: %s", out)
	}
	if !strings.Contains(out, "[REDACTED]") {
		t.Errorf("expected [REDACTED] in output; got: %s", out)
	}
}

func TestRedactWriter_RedactsPassword(t *testing.T) {
	var buf bytes.Buffer
	rw := logging.NewRedactWriter(&buf)
	rw.Write([]byte(`{"password":"hunter2","user":"alice"}`)) //nolint:errcheck

	out := buf.String()
	if strings.Contains(out, "hunter2") {
		t.Errorf("password must be redacted; got: %s", out)
	}
}

func TestRedactWriter_RedactsSecret(t *testing.T) {
	var buf bytes.Buffer
	rw := logging.NewRedactWriter(&buf)
	rw.Write([]byte(`{"secret":"my-hmac-key","event":"hook"}`)) //nolint:errcheck

	out := buf.String()
	if strings.Contains(out, "my-hmac-key") {
		t.Errorf("secret must be redacted; got: %s", out)
	}
}

func TestRedactWriter_RedactsAuthorization(t *testing.T) {
	var buf bytes.Buffer
	rw := logging.NewRedactWriter(&buf)
	rw.Write([]byte(`{"authorization":"Bearer eyJhbGciOiJIUzI1NiJ9.x.y"}`)) //nolint:errcheck

	out := buf.String()
	if strings.Contains(out, "eyJhbGciOiJIUzI1NiJ9") {
		t.Errorf("authorization value must be redacted; got: %s", out)
	}
}

func TestRedactWriter_PreservesNonSensitiveFields(t *testing.T) {
	var buf bytes.Buffer
	rw := logging.NewRedactWriter(&buf)
	input := `{"tenant_id":"acme","method":"GET","status":200}`
	rw.Write([]byte(input)) //nolint:errcheck

	out := buf.String()
	if !strings.Contains(out, "acme") {
		t.Errorf("non-sensitive field 'tenant_id' must not be redacted; got: %s", out)
	}
	if !strings.Contains(out, "200") {
		t.Errorf("non-sensitive field 'status' must not be redacted; got: %s", out)
	}
}

func TestRedactWriter_ReportsOriginalLength(t *testing.T) {
	var buf bytes.Buffer
	rw := logging.NewRedactWriter(&buf)
	input := []byte(`{"token":"abc","msg":"test"}`)
	n, err := rw.Write(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n != len(input) {
		t.Errorf("Write must report original len=%d, got %d", len(input), n)
	}
}
