package appximo

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestServeCurrentSchema pins the boot-sync contract (EDITOR-BOOT-SYNC): the
// route serves the boot schema file BYTE-FAITHFULLY (the editor round-trips
// the exact source document), never caches, and degrades to a clean JSON 500
// when the file is missing or corrupt.
func TestServeCurrentSchema(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "schema.json")
	doc := `{"$schema":"https://appximo.com/schema/v1","version":"1","name":"punto-gafas","resources":{"pacientes":{"fields":{"nombre":{"type":"string"}}}}}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	app := &App{cfg: Config{SchemaPath: path}}

	rec := httptest.NewRecorder()
	app.serveCurrentSchema(rec, httptest.NewRequest(http.MethodGet, "/editor/current-schema", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != doc {
		t.Fatalf("body is not the byte-faithful source:\n got %q\nwant %q", got, doc)
	}
	if cc := rec.Header().Get("Cache-Control"); cc == "" || cc == "public" {
		t.Fatalf("Cache-Control = %q — the route must never cache", cc)
	}

	// Corrupt file → clean JSON 500, never raw bytes.
	if err := os.WriteFile(path, []byte("{broken"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = httptest.NewRecorder()
	app.serveCurrentSchema(rec, httptest.NewRequest(http.MethodGet, "/editor/current-schema", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("corrupt file: status = %d, want 500", rec.Code)
	}

	// Missing file → same clean 500.
	app.cfg.SchemaPath = filepath.Join(dir, "gone.json")
	rec = httptest.NewRecorder()
	app.serveCurrentSchema(rec, httptest.NewRequest(http.MethodGet, "/editor/current-schema", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("missing file: status = %d, want 500", rec.Code)
	}
}
