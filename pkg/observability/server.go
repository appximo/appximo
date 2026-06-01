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
}

func NewObsServer(
	hist *TenantHistogram,
	errors *ErrorStore,
	anomaly *AnomalyDetector,
	synthmon *SyntheticMonitor,
) *ObsServer {
	return &ObsServer{hist: hist, errors: errors, anomaly: anomaly, synthmon: synthmon}
}

// Router returns a chi.Mux with admin-protected debug routes.
// Mount at "/" on an existing router to expose /debug/... paths.
func (s *ObsServer) Router(adminKey string) *chi.Mux {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if req.Header.Get("X-Admin-Key") != adminKey {
				w.Header().Set("Content-Type", "application/json")
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, req)
		})
	})
	r.Get("/debug/tenant/{id}", s.handleTenant)
	r.Get("/debug/synthetic", s.handleSynthetic)
	return r
}

func (s *ObsServer) handleTenant(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	snap := s.hist.Snapshot(id)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
		"tenant_id": id,
		"latency":   snap,
	})
}

func (s *ObsServer) handleSynthetic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.synthmon.Results()) //nolint:errcheck
}
