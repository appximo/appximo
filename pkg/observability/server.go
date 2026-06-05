package observability

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

// ObsServer exposes internal observability data over HTTP.
type ObsServer struct {
	hist       *TenantHistogram
	errors     *ErrorStore
	anomaly    *AnomalyDetector
	synthmon   *SyntheticMonitor
	rings      *Rings
	slo        *SLOEngine
	store         *ObsStore
	tracesHandler http.Handler // serves /debug/traces; performs its own auth
	geo           *GeoLookup   // country lookup for enriching recent_traces
}

// SetGeo installs the GeoLite2 lookup used to enrich recent_traces with country.
func (s *ObsServer) SetGeo(g *GeoLookup) { s.geo = g }

// SetTracesHandler installs the handler served at /debug/traces. It is injected
// from package main (where the HTML is go:embed'd and the admin key lives) so the
// route applies ITS OWN auth — query param (?key=) OR X-Admin-Key header — which
// is intentionally looser than the header-only gate on the JSON debug APIs, so
// the page can be opened directly in a browser.
func (s *ObsServer) SetTracesHandler(h http.Handler) { s.tracesHandler = h }

func NewObsServer(
	hist *TenantHistogram,
	errors *ErrorStore,
	anomaly *AnomalyDetector,
	synthmon *SyntheticMonitor,
	rings *Rings,
	slo *SLOEngine,
	store *ObsStore,
) *ObsServer {
	return &ObsServer{hist: hist, errors: errors, anomaly: anomaly, synthmon: synthmon, rings: rings, slo: slo, store: store}
}

// AdminAuth wraps next so it is reachable only with a matching X-Admin-Key header.
// This is the single gate shared by the /debug/* endpoints and /metrics.
func AdminAuth(adminKey string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("X-Admin-Key") != adminKey {
			w.Header().Set("Content-Type", "application/json")
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, req)
	})
}

// DebugRouter returns an admin-gated sub-router serving /tenant/{id} and /synthetic.
// Mount it at "/debug" on any router (the main :8080 server or the control plane).
func (s *ObsServer) DebugRouter(adminKey string) http.Handler {
	r := chi.NewRouter()
	// JSON debug APIs: header-only admin gate (X-Admin-Key). Never query param.
	r.Group(func(gr chi.Router) {
		gr.Use(func(next http.Handler) http.Handler {
			return AdminAuth(adminKey, next)
		})
		gr.Get("/tenant/{id}", s.handleTenant)
		gr.Get("/synthetic", s.handleSynthetic)
	})
	// HTML trace explorer: NOT in the header-only group. The injected handler does
	// its own auth (?key= OR X-Admin-Key) so it opens directly in a browser.
	r.Get("/traces", s.serveTraces)
	return r
}

// serveTraces delegates to the injected /debug/traces handler (which performs its
// own query-param-or-header auth). Returns 404 when no handler was injected.
func (s *ObsServer) serveTraces(w http.ResponseWriter, r *http.Request) {
	if s.tracesHandler == nil {
		http.Error(w, "traces UI not available", http.StatusNotFound)
		return
	}
	s.tracesHandler.ServeHTTP(w, r)
}

// Router returns a chi.Mux exposing the /debug/... paths, admin-protected.
// Mount at "/" on an existing router (used by the control plane on :9090).
func (s *ObsServer) Router(adminKey string) *chi.Mux {
	r := chi.NewRouter()
	r.Mount("/debug", s.DebugRouter(adminKey))
	return r
}

func (s *ObsServer) handleTenant(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	snap := s.hist.FullSnapshot(id)
	errs := s.errors.TopN(id, 10)
	recent := []RecentRequest{}
	if s.rings != nil {
		recent = s.rings.Recent(id)
	}
	recentTraces := []TraceView{}
	if s.rings != nil {
		recentTraces = s.rings.RecentTraces(id, 10)
		// Enrich on-demand (admin query): parse the UA and geo-resolve the country,
		// so the hot path only had to store the raw IP + UA.
		for i := range recentTraces {
			recentTraces[i].Browser, recentTraces[i].OS = ParseUserAgent(recentTraces[i].UserAgent)
			if s.geo != nil {
				recentTraces[i].Country = s.geo.Country(recentTraces[i].IP)
			}
		}
	}
	payload := map[string]any{
		"tenant_id":       id,
		"latency":         snap,
		"errors":          errs,
		"anomaly_count":   s.anomaly.GetCount(id),
		"recent_requests": recent,
		"recent_traces":   recentTraces,
	}
	if s.slo != nil {
		payload["slo"] = s.slo.Snapshot(id)
	}
	// ?history=<hours> adds persisted snapshots; absent → current behavior unchanged.
	if h := r.URL.Query().Get("history"); h != "" && s.store != nil {
		if hours, err := strconv.Atoi(h); err == nil && hours > 0 {
			payload["history"] = s.historyPoints(id, hours)
		}
	}
	// ?traces=slow adds the tenant's persisted slow traces from the last 24h.
	if r.URL.Query().Get("traces") == "slow" && s.store != nil {
		traces, err := s.store.SlowTraces(id, 24)
		if err != nil {
			traces = []TraceView{}
		}
		payload["slow_traces"] = traces
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(payload) //nolint:errcheck
}

// HistoryPoint is the trimmed per-snapshot projection returned under "history".
type HistoryPoint struct {
	TS        int64  `json:"ts"`
	P50US     int64  `json:"p50_us"`
	P95US     int64  `json:"p95_us"`
	SLOStatus string `json:"slo_status"`
}

// historyPoints loads persisted snapshots for the tenant and projects them for the API.
// On a store error it returns an empty slice rather than failing the whole response.
func (s *ObsServer) historyPoints(id string, hours int) []HistoryPoint {
	snaps, err := s.store.History(id, hours)
	if err != nil {
		return []HistoryPoint{}
	}
	pts := make([]HistoryPoint, 0, len(snaps))
	for _, snap := range snaps {
		pts = append(pts, HistoryPoint{
			TS:        snap.TS,
			P50US:     snap.P50US,
			P95US:     snap.P95US,
			SLOStatus: snap.SLOStatus,
		})
	}
	return pts
}

func (s *ObsServer) handleSynthetic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.synthmon.Results()) //nolint:errcheck
}
