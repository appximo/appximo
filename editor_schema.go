package appitools

import (
	"encoding/json"
	"net/http"
	"os"
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
