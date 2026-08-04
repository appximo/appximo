package shutdown_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/appximo/appximo/pkg/shutdown"
)

func TestReadyzHandler_Returns200WhenReady(t *testing.T) {
	s := shutdown.New()
	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	s.ReadyzHandler(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "ready") {
		t.Errorf("expected 'ready' in body, got %s", w.Body.String())
	}
}

func TestReadyzHandler_Returns503WhenShuttingDown(t *testing.T) {
	s := shutdown.New()
	s.MarkShuttingDown()

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	w := httptest.NewRecorder()
	s.ReadyzHandler(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("want 503, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "shutting_down") {
		t.Errorf("expected 'shutting_down' in body, got %s", w.Body.String())
	}
}

func TestHealthzHandler_AlwaysReturns200(t *testing.T) {
	s := shutdown.New()
	s.MarkShuttingDown() // even while shutting down

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	s.HealthzHandler(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("want 200, got %d; healthz must never return non-200", w.Code)
	}
	if !strings.Contains(w.Body.String(), "alive") {
		t.Errorf("expected 'alive' in body, got %s", w.Body.String())
	}
}

// TestGracefulShutdown_InFlightRequestsComplete verifies that:
//   - a request that started before shutdown completes with 200
//   - new requests receive 503 from /readyz while the server is shutting down
func TestGracefulShutdown_InFlightRequestsComplete(t *testing.T) {
	s := shutdown.New()

	slowStarted := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.HealthzHandler)
	mux.HandleFunc("/readyz", s.ReadyzHandler)
	mux.HandleFunc("/slow", func(w http.ResponseWriter, r *http.Request) {
		close(slowStarted)
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := "http://" + ln.Addr().String()

	srv := &http.Server{Handler: mux}
	ctx, cancel := context.WithCancel(context.Background())

	serverDone := make(chan error, 1)
	go func() {
		// Use Serve directly so we can control the listener.
		go func() {
			<-ctx.Done()
			s.MarkShuttingDown()
			// drainDelay = 0 in tests; shutdown after 0s drain
			shutCtx, cancelShut := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancelShut()
			srv.Shutdown(shutCtx) //nolint:errcheck
		}()
		err := srv.Serve(ln)
		if err != http.ErrServerClosed {
			serverDone <- err
		} else {
			serverDone <- nil
		}
	}()

	// Start in-flight slow request.
	inflight := make(chan int, 1)
	go func() {
		resp, err := http.Get(addr + "/slow")
		if err != nil {
			inflight <- 0
			return
		}
		defer resp.Body.Close()
		inflight <- resp.StatusCode
	}()

	<-slowStarted                     // request is actively being processed
	cancel()                          // signal shutdown
	time.Sleep(20 * time.Millisecond) // let MarkShuttingDown propagate

	// /readyz must now return 503
	resp, err := http.Get(addr + "/readyz")
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("readyz: want 503 during shutdown, got %d", resp.StatusCode)
		}
	}

	// In-flight request must complete successfully.
	status := <-inflight
	if status != http.StatusOK {
		t.Errorf("in-flight request: want 200, got %d", status)
	}

	if err := <-serverDone; err != nil {
		t.Errorf("server error: %v", err)
	}
}

// ── ENG-34: bind first, announce after ──────────────────────────────────────

// TestListen_BusyPortFails — half one of ENG-34's contract: the bind is a REAL
// operation that can fail (a draining predecessor still holds the port), which
// is exactly why "serving on :PORT" must come after it. A second Listen on a
// held port errors immediately, unannounced.
func TestListen_BusyPortFails(t *testing.T) {
	ln, err := shutdown.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("first listen: %v", err)
	}
	defer ln.Close()

	if _, err := shutdown.Listen(ln.Addr().String()); err == nil {
		t.Fatal("second Listen on a held port must fail — this is the failure the announcement used to precede")
	}
}

// TestServe_ServesOnBoundListener — half two: between Listen and Serve the
// port is truly held (the kernel queues connections), so an announcement
// printed there is a statement of fact; and Serve still runs the normal
// graceful drain on ctx cancellation.
func TestServe_ServesOnBoundListener(t *testing.T) {
	ln, err := shutdown.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	s := shutdown.New()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok")) //nolint:errcheck
	})}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, srv, ln, 0) }()

	resp, err := http.Get("http://" + ln.Addr().String() + "/")
	if err != nil {
		t.Fatalf("request against the bound listener: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned error on graceful shutdown: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Serve did not return after ctx cancellation")
	}
	if s.IsReady() {
		t.Fatal("state must be not-ready after shutdown")
	}
}

// TestRun_BusyPortReturnsBindError — Run (bind + Serve) surfaces the bind
// failure as its return value, so the caller's error path — not its success
// announcement — is what runs.
func TestRun_BusyPortReturnsBindError(t *testing.T) {
	ln, err := shutdown.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	s := shutdown.New()
	srv := &http.Server{Addr: ln.Addr().String(), Handler: http.NewServeMux()}
	if err := s.Run(context.Background(), srv, 0); err == nil {
		t.Fatal("Run on a busy port must return the bind error")
	}
}
