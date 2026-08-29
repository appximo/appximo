package observability

import (
	"encoding/json"
	"net/http"
	"runtime"
	"strconv"
	"time"
)

// The HTTP face of the resource collector. Two handlers, served on two doors
// with the auth of each door (never their own):
//   - GET /debug/resources            (X-Admin-Key, the curl door — ObsServer.DebugRouter)
//   - GET /admin/resources            (platform token OR admin key — the /admin panel)
//   - GET /admin/resources/snapshot   (same auth) — the exportable JSON of a run
//
// Query parameters:
//
//	?series=N   the last N ticks in the correlation series (default 120, ≤ ring)
//	?since=MS   keep only ticks at or after this Unix-millisecond instant, so
//	            the window verdict covers EXACTLY one load run and not the
//	            history behind it. Without it the caller has to guess a tick
//	            count from the collector's cadence, and guessing wrong reads a
//	            previous, heavier run as if it were this one — measured in
//	            CAPACIDAD-USL-S1, where a 42-tick request at the 10 s
//	            background cadence covered seven minutes and reported
//	            `pool_exhausted` over a 25 rps run that never queued for
//	            anything. Combines with ?series= (the count is applied first).
//	?live=1     switch the collector to the live cadence for LiveWindow (the
//	            panel sends it on every poll; a curl can too)
//	?ticks=N    (snapshot) how many ticks to export (default: the whole ring)
//	?since=MS   (snapshot) the same filter, so an exported run is only the run
//
// The resources are the PROCESS's, not a tenant's, so there is no tenant
// scope here and no tenant-admin path: only the platform operator sees them
// (a tenant must not learn the box's RAM, cgroup path or pool size).

// ResourcesVersion is set by the engine so the exported snapshot names the
// build it came from (an attachment to a report must say which binary).
var ResourcesVersion = "dev"

// SetResources wires the collector into the obs server (nil = not enabled).
func (s *ObsServer) SetResources(rc *ResourceCollector) { s.resources = rc }

// Resources returns the wired collector (nil when self-monitoring is off).
func (s *ObsServer) Resources() *ResourceCollector { return s.resources }

func (s *ObsServer) resourcesUnavailable(w http.ResponseWriter) bool {
	if s.resources == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{
			"error": "self-monitoring is disabled (APPXIMO_SELFMON=off) — nothing to report",
		})
		return true
	}
	if !s.resources.Started() || s.resources.Latest() == nil {
		writeJSONStatus(w, http.StatusServiceUnavailable, map[string]any{
			"error":    "self-monitoring has not produced a tick yet — retry in a second",
			"interval": s.resources.Interval().String(),
		})
		return true
	}
	return false
}

// ServeResources answers the live board + the correlation series.
func (s *ObsServer) ServeResources(w http.ResponseWriter, r *http.Request) {
	if s.resourcesUnavailable(w) {
		return
	}
	rc := s.resources
	q := r.URL.Query()
	if q.Get("live") == "1" || q.Get("live") == "true" {
		rc.Touch()
	}
	n := 120
	if v := q.Get("series"); v != "" {
		if k, err := strconv.Atoi(v); err == nil && k > 0 {
			n = k
		}
	}
	series := sinceFilter(rc.Series(n), q.Get("since"))
	cfg := rc.Config()
	payload := map[string]any{
		"collector": map[string]any{
			"live":                   rc.Live(),
			"mode":                   modeOf(rc.Live()),
			"interval_ms":            rc.Interval().Milliseconds(),
			"background_interval_ms": cfg.BackgroundInterval.Milliseconds(),
			"live_interval_ms":       cfg.LiveInterval.Milliseconds(),
			"live_window_ms":         cfg.LiveWindow.Milliseconds(),
			"ring_size":              ResourceRingSize,
			"ticks":                  rc.Count(),
			"db_server_local":        cfg.DBServerLocal,
			"thresholds":             cfg.Thresholds,
		},
		"host": hostFacts(),
		// latest is the newest tick; window is the verdict over the series
		// (the load-test view), computed deterministically from the ticks.
		"latest": rc.Latest(),
		"window": Summarize(series),
		"series": series,
	}
	writeJSONStatus(w, http.StatusOK, payload)
}

// sinceFilter keeps the ticks at or after a Unix-millisecond instant. An
// absent or unparseable value keeps everything: a bad query parameter must
// never silently narrow a verdict.
func sinceFilter(series []ResourceSnapshot, since string) []ResourceSnapshot {
	if since == "" {
		return series
	}
	ms, err := strconv.ParseInt(since, 10, 64)
	if err != nil || ms <= 0 {
		return series
	}
	for i := range series {
		if series[i].TS >= ms {
			return series[i:]
		}
	}
	return nil
}

// ServeResourcesSnapshot is the exportable document of a run (spec §5, §7):
// engine + host identity, the window verdict, every tick. Served as an
// attachment so the browser saves it.
func (s *ObsServer) ServeResourcesSnapshot(w http.ResponseWriter, r *http.Request) {
	if s.resourcesUnavailable(w) {
		return
	}
	rc := s.resources
	n := 0
	if v := r.URL.Query().Get("ticks"); v != "" {
		if k, err := strconv.Atoi(v); err == nil && k > 0 {
			n = k
		}
	}
	series := sinceFilter(rc.Series(n), r.URL.Query().Get("since"))
	now := time.Now()
	doc := map[string]any{
		"schema":      "appximo.selfmon.snapshot/v1",
		"exported_at": now.UTC().Format(time.RFC3339),
		"engine": map[string]any{
			"version":    ResourcesVersion,
			"go":         runtime.Version(),
			"gomaxprocs": runtime.GOMAXPROCS(0),
		},
		"host":         hostFacts(),
		"collector":    map[string]any{"background_interval_ms": rc.Config().BackgroundInterval.Milliseconds(), "live_interval_ms": rc.Config().LiveInterval.Milliseconds(), "thresholds": rc.Config().Thresholds, "db_server_local": rc.Config().DBServerLocal},
		"window":       Summarize(series),
		"series":       series,
		"attributions": Attributions,
		"note":         "attribution is deterministic: fixed rules over the tick's numbers, ranked so the cause furthest from the operator's code wins; the 'signals' list under each verdict is the evidence. No language model produced any number or causal claim in this document.",
	}
	w.Header().Set("Content-Disposition", "attachment; filename=\"appximo-resources-"+now.UTC().Format("20060102-150405")+".json\"")
	writeJSONStatus(w, http.StatusOK, doc)
}

func modeOf(live bool) string {
	if live {
		return "live"
	}
	return "background"
}

// hostFacts is the cheap, static identity of the box the snapshot describes.
func hostFacts() map[string]any {
	return map[string]any{
		"os":         runtime.GOOS,
		"arch":       runtime.GOARCH,
		"num_cpu":    runtime.NumCPU(),
		"gomaxprocs": runtime.GOMAXPROCS(0),
	}
}

func writeJSONStatus(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v) //nolint:errcheck
}
