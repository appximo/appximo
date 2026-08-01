package appitools

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/miguelangel/appitools/pkg/aigen"
)

// EDITOR-BOOT-SYNC — GET /editor/current-schema: the SOURCE schema of the
// surface this engine serves, for Studio's boot sync (the canvas must open on
// what the engine serves, never a frontend sample).
//
// The source of truth is the boot schema FILE (cfg.SchemaPath): every path
// that changes the served surface persists it there FIRST — the single-engine
// self-restart (UI-F4-S2 persistBootSchema + re-exec) and the fleet's per-app
// hot-swap (MT-STRUCT-S4 persists, then rebuilds from that file) — so the file
// is always the source of the currently-served surface, byte-faithful (no
// struct re-marshal that could reorder/normalize the author's document). Read
// per request: this is a rare, editor-only route, and reading the file (rather
// than caching boot bytes) keeps it correct across hot-swaps for free.
func (a *App) serveCurrentSchema(w http.ResponseWriter, _ *http.Request) {
	raw, err := os.ReadFile(a.cfg.SchemaPath)
	if err != nil || !json.Valid(raw) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"error":"boot schema unavailable"}`)) //nolint:errcheck
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// Never cache: a deploy + restart/hot-swap changes it, and the editor
	// must see the new reality on the next open.
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write(raw) //nolint:errcheck
}

// serveAIContext — GET /editor/ai-context: the exact text to paste into your own
// AI assistant so it can change THIS app (DOC-2).
//
// `appitools spec` is the cheapest, most effective piece of the product: an agent
// with no repository access, given only that document plus the current schema,
// produced a correct schema change in one round (docs/AUTHORING_JOURNEY.md 5-4).
// And nothing in the product said it existed — it was documented only in a file the
// owner has no reason to read, so the whole "my assistant edits my app" flow hinged
// on a step nobody was told about. This route is that step, one click away in
// Studio.
//
// Plain text on purpose: it goes to a clipboard and then into a chat box.
func (a *App) serveAIContext(w http.ResponseWriter, _ *http.Request) {
	var b strings.Builder
	b.WriteString(aigen.Spec())
	b.WriteString("\n\n---\n\n## The schema of the app you are changing\n\n")
	b.WriteString("This is the CURRENT schema. Apply the change the user asks for and return the\n")
	b.WriteString("COMPLETE schema as one JSON object — the whole document, not a fragment.\n\n")
	b.WriteString("```json\n")
	if raw, err := os.ReadFile(a.cfg.SchemaPath); err == nil && json.Valid(raw) {
		b.Write(raw)
	} else {
		b.WriteString("{}")
	}
	b.WriteString("\n```\n")
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Write([]byte(b.String())) //nolint:errcheck
}
