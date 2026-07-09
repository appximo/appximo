package editorui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/miguelangel/appitools/pkg/schema"
)

func newTestRouter(t *testing.T) *chi.Mux {
	t.Helper()
	r := chi.NewRouter()
	if err := Register(r); err != nil {
		t.Fatalf("Register: %v", err)
	}
	return r
}

func TestValidateRouteGoodSchema(t *testing.T) {
	r := newTestRouter(t)
	body := `{
		"$schema": "https://appitools.dev/schema/v1",
		"version": "1",
		"name": "todo-api",
		"resources": {
			"tasks": { "fields": { "title": { "type": "string", "required": true } } }
		}
	}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/editor/validate", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
	var rep schema.ValidationReport
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("response is not a ValidationReport: %v", err)
	}
	if !rep.Valid || len(rep.Errors) != 0 {
		t.Fatalf("valid schema reported invalid: %+v", rep.Errors)
	}
}

func TestValidateRouteBadSchema(t *testing.T) {
	r := newTestRouter(t)
	// The JSON-AUDIT-V1 probe schema: an invented resource-level key, an invalid
	// field type, and a semantic error (m2m relation without through).
	body := `{
		"$schema": "https://appitools.dev/schema/v1",
		"version": "1",
		"name": "probe",
		"resources": {
			"tasks": {
				"fields": { "prio": { "type": "number" } },
				"bloque_inventado": true,
				"relations": {
					"tags": { "type": "many_to_many", "target": "tags", "fk": "task_id" }
				}
			},
			"tags": { "fields": { "name": { "type": "string" } } }
		}
	}`
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/editor/validate", strings.NewReader(body)))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (validation errors are the PAYLOAD, not an HTTP failure)", rec.Code)
	}
	var rep schema.ValidationReport
	if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
		t.Fatalf("response is not a ValidationReport: %v", err)
	}
	if rep.Valid {
		t.Fatal("broken schema reported valid")
	}
	var sawUnknownKey, sawBadType bool
	for _, e := range rep.Errors {
		if e.Path == "" {
			t.Errorf("error without a path: %+v", e)
		}
		if e.Rule == "unknown_key" && e.Got == "bloque_inventado" {
			sawUnknownKey = true
		}
		if strings.HasPrefix(e.Path, "resources.tasks.fields.prio.type") {
			sawBadType = true
		}
	}
	if !sawUnknownKey {
		t.Errorf("invented key not flagged as unknown_key: %+v", rep.Errors)
	}
	if !sawBadType {
		t.Errorf(`"type":"number" not flagged at its path: %+v`, rep.Errors)
	}
}

func TestValidateRouteBodyCap(t *testing.T) {
	r := newTestRouter(t)
	rec := httptest.NewRecorder()
	huge := strings.NewReader(`{"pad":"` + strings.Repeat("x", validateBodyCap+1) + `"}`)
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/editor/validate", huge))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", rec.Code)
	}
}

func TestMetaSchemaRoute(t *testing.T) {
	r := newTestRouter(t)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/editor/meta-schema", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatalf("meta-schema is not valid JSON: %v", err)
	}
	if _, ok := doc["$schema"]; !ok {
		t.Error("meta-schema has no $schema key")
	}
	// Exactly the embedded bytes — one runtime source of truth.
	if got, want := rec.Body.String(), string(schema.MetaSchemaJSON()); got != want {
		t.Error("served meta-schema differs from schema.MetaSchemaJSON()")
	}
}
