package observability

import (
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
)

// ObsServer exposes internal observability data over HTTP.
type ObsServer struct {
	hist     *TenantHistogram
	errors   *ErrorStore
	anomaly  *AnomalyDetector
	synthmon *SyntheticMonitor
	rings    *Rings
	slo      *SLOEngine
}

func NewObsServer(
	hist *TenantHistogram,
	errors *ErrorStore,
	anomaly *AnomalyDetector,
	synthmon *SyntheticMonitor,
	rings *Rings,
	slo *SLOEngine,
) *ObsServer {
	return &ObsServer{hist: hist, errors: errors, anomaly: anomaly, synthmon: synthmon, rings: rings, slo: slo}
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
	r.Use(func(next http.Handler) http.Handler {
		return AdminAuth(adminKey, next)
	})
	r.Get("/tenant/{id}", s.handleTenant)
	r.Get("/synthetic", s.handleSynthetic)
	return r
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
	payload := map[string]any{
		"tenant_id":       id,
		"latency":         snap,
		"errors":          errs,
		"anomaly_count":   s.anomaly.GetCount(id),
		"recent_requests": recent,
	}
	if s.slo != nil {
		payload["slo"] = s.slo.Snapshot(id)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(payload) //nolint:errcheck
}

func (s *ObsServer) handleSynthetic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.synthmon.Results()) //nolint:errcheck
}
