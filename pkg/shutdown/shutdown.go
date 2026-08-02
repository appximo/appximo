// Package shutdown provides graceful HTTP server shutdown with readiness tracking.
package shutdown

import (
	"context"
	"net"
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

// Run binds srv.Addr and serves on it, blocking until ctx is cancelled, then
// executes the shutdown sequence in the mandated order:
//
//	ready=0  →  sleep drainDelay  →  srv.Shutdown(10s)  →  each onClose()
//
// drainDelay should be ~5 s in production (LB drain time) and 0 in tests.
//
// Callers that want to ANNOUNCE the server ("serving on :PORT") must bind
// first with Listen and pass the live listener to Serve — printing before the
// bind is ENG-34: the old single call forced the announcement before
// ListenAndServe ever ran, so a process could declare itself serving and then
// die on `bind: address already in use` while a draining predecessor still
// answered on the port (a client then talks to the STALE binary believing it
// is the new one — measured costing a fresh agent 10 minutes).
func (s *State) Run(ctx context.Context, srv *http.Server, drainDelay time.Duration, onClose ...func()) error {
	ln, err := Listen(srv.Addr)
	if err != nil {
		return err
	}
	return s.Serve(ctx, srv, ln, drainDelay, onClose...)
}

// Listen binds addr (":8080", "127.0.0.1:9090") and returns the live listener.
// A failed bind returns the error UNANNOUNCED — nothing may claim to serve yet.
func Listen(addr string) (net.Listener, error) {
	return net.Listen("tcp", addr)
}

// Serve runs srv on an ALREADY-BOUND listener and blocks until ctx is
// cancelled, then executes the same shutdown sequence as Run. The split exists
// so the boot can bind → announce → serve, in that order: once Listen has
// returned, the port is truly held and the kernel queues connections, so a
// "serving on" line printed between Listen and Serve is a statement of fact.
func (s *State) Serve(ctx context.Context, srv *http.Server, ln net.Listener, drainDelay time.Duration, onClose ...func()) error {
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

	if err := srv.Serve(ln); err != http.ErrServerClosed {
		return err
	}
	return nil
}
