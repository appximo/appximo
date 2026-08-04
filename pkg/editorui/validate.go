// JSON-EDITOR-S1: the validation surface for Studio's assisted JSON editor.
// Two THIN, STATELESS routes in the /openapi class — no DB access, no deploy,
// no hot-path involvement, JWT-skipped like the rest of /editor:
//
//   - POST /editor/validate    — body = a raw candidate schema JSON (1 MiB cap,
//     same as the engine's body cap); response = schema.ValidateReport, the
//     AI-F0-S2 unified structural+semantic report (dot-path / rule / expected /
//     got / fix per error). This is the editor's SEMANTIC layer: the frontend
//     never reimplements the validator, it renders this report at the right
//     lines. Debounce-friendly: pure function of the body, callable per keystroke.
//
//   - GET /editor/meta-schema  — the embedded formal JSON Schema meta-schema
//     (Draft 2020-12, pkg/schema/appximo.schema.json), for the editor's
//     client-side STRUCTURAL layer (CodeMirror autocompletion/hover/diagnostics).
//     Same bytes as `appximo meta-schema`; cacheable.
package editorui

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/appximo/appximo/pkg/schema"
)

// validateBodyCap bounds the schema document accepted by POST /editor/validate —
// aligned with the engine's global 1 MiB request-body cap (a real schema is KBs).
const validateBodyCap = 1 << 20

func serveValidate(w http.ResponseWriter, r *http.Request) {
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, validateBodyCap))
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusRequestEntityTooLarge)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "schema document too large (1 MiB cap)"})
		return
	}
	report := schema.ValidateReport(raw)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(report)
}

func serveMetaSchema(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	// Immutable per binary (go:embed), but a new release may ship a new grammar —
	// cache briefly, not forever.
	w.Header().Set("Cache-Control", "public, max-age=3600")
	_, _ = w.Write(schema.MetaSchemaJSON())
}
