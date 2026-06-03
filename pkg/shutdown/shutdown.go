// Package shutdown provides graceful HTTP server shutdown with readiness tracking.
package shutdown

import (
	"context"
	"net/http"
	"sync/atomic"
	"time"
)

// State tracks server readiness and orchestrates the shutdown sequence.
type State struct {
	ready atomic.Int32
}

// New returns a State in the ready=1 (serving) condition.
func New() *State {
	s := &State{}
	s.ready.Store(1)
	return s
}

// MarkShuttingDown sets ready=0; /readyz will return 503 from this point on.
func (s *State) MarkShuttingDown() { s.ready.Store(0) }

// IsReady reports whether the server is currently ready to accept requests.
func (s *State) IsReady() bool { return s.ready.Load() == 1 }

// HealthzHandler is a liveness probe that never touches PostgreSQL.
func (s *State) HealthzHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"alive"}`)) //nolint:errcheck
}

// ReadyzHandler is a readiness probe. Returns 503 during and after shutdown.
func (s *State) ReadyzHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !s.IsReady() {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"status":"shutting_down"}`)) //nolint:errcheck
		return
	}
	w.Write([]byte(`{"status":"ready"}`)) //nolint:errcheck
}

// Run starts srv and blocks until ctx is cancelled, then executes the shutdown
// sequence in the mandated order:
//
//	ready=0  →  sleep drainDelay  →  srv.Shutdown(10s)  →  each onClose()
//
// drainDelay should be ~5 s in production (LB drain time) and 0 in tests.
func (s *State) Run(ctx context.Context, srv *http.Server, drainDelay time.Duration, onClose ...func()) error {
	go func() {
		<-ctx.Done()
		s.MarkShuttingDown()

		// Let the load-balancer observe /readyz→503 and stop routing before we
		// stop accepting connections.
		time.Sleep(drainDelay)

		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.Shutdown(shutCtx) //nolint:errcheck

		for _, fn := range onClose {
			fn()
		}
	}()

	if err := srv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}
