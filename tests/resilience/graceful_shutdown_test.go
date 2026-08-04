//go:build resilience

package resilience

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/appximo/appximo/pkg/shutdown"
)

// TestGracefulShutdownUnderLoad drives the SAME drain path the binary uses on
// SIGTERM: cmd_serve.go runs `shutdown.State.Run(signalCtx, srv, 5s, cleanup)` and
// the OS signal simply cancels that context. Here we cancel the context directly
// (the exact trigger) while the server is under continuous load, and assert the
// three graceful-shutdown guarantees:
//
//	a. in-flight requests complete (no cut/truncated responses),
//	b. the server closes well within the 10s Shutdown budget (< 15s), and
//	c. new requests after shutdown are refused (the listener is closed).
func TestGracefulShutdownUnderLoad(t *testing.T) {
	ss := shutdown.New()
	addr := freeAddr(t) // 127.0.0.1:<free port>
	baseURL := "http://" + addr

	const slowBody = "ok-complete-body"
	mux := http.NewServeMux()
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(700 * time.Millisecond) // long enough to be in-flight at SIGTERM
		_, _ = w.Write([]byte(slowBody))
	})
	mux.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("pong"))
	})

	srv := &http.Server{Addr: addr, Handler: mux}
	ctx, cancel := context.WithCancel(context.Background())

	runErr := make(chan error, 1)
	go func() {
		// drainDelay 200ms (prod uses 5s); cleanup runs after Shutdown returns.
		runErr <- ss.Run(ctx, srv, 200*time.Millisecond)
	}()

	// Wait for the listener to be up.
	waitForServer(t, baseURL+"/ping")

	// Continuous background load. Each worker classifies every response:
	//   good     = 200 + full expected body
	//   refused  = connection error (expected once the listener closes)
	//   bad      = a connected response that was non-200 or truncated (must stay 0)
	var good, refused, bad int64
	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			client := &http.Client{}
			for {
				select {
				case <-stop:
					return
				default:
				}
				resp, err := client.Get(baseURL + "/slow")
				if err != nil {
					atomic.AddInt64(&refused, 1)
					continue
				}
				body, rerr := io.ReadAll(resp.Body)
				resp.Body.Close()
				if rerr == nil && resp.StatusCode == http.StatusOK && string(body) == slowBody {
					atomic.AddInt64(&good, 1)
				} else {
					atomic.AddInt64(&bad, 1)
				}
			}
		}()
	}

	// An explicit in-flight request that STARTS before SIGTERM and is mid-handler
	// when it fires — it must still complete with 200.
	inflight := make(chan inflightResult, 1)
	go func() {
		resp, err := http.Get(baseURL + "/slow")
		if err != nil {
			inflight <- inflightResult{err: err}
			return
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		inflight <- inflightResult{status: resp.StatusCode, body: string(body)}
	}()

	// Let the explicit request reach the handler's 700ms sleep, then send "SIGTERM".
	time.Sleep(150 * time.Millisecond)
	cancelAt := time.Now()
	cancel()

	// Run returns only after srv.Shutdown completes (in-flight drained).
	var rErr error
	select {
	case rErr = <-runErr:
	case <-time.After(15 * time.Second):
		t.Fatal("shutdown did not complete within 15s (drain budget exceeded)")
	}
	closeDur := time.Since(cancelAt)

	// Stop the load and collect counts.
	close(stop)
	wg.Wait()

	// (b) bounded close.
	if closeDur >= 15*time.Second {
		t.Fatalf("server took %v to close, want < 15s", closeDur)
	}
	if rErr != nil {
		t.Fatalf("Run returned error (expected clean ErrServerClosed → nil): %v", rErr)
	}

	// (a) the explicit in-flight request completed cleanly.
	select {
	case r := <-inflight:
		if r.err != nil {
			t.Fatalf("in-flight request failed instead of completing: %v", r.err)
		}
		if r.status != http.StatusOK || r.body != slowBody {
			t.Fatalf("in-flight request was cut: status=%d body=%q (want 200 / %q)", r.status, r.body, slowBody)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("in-flight request never returned after shutdown")
	}

	// (a, cont.) under load, no connected request was ever truncated/non-200.
	if bad != 0 {
		t.Fatalf("%d load requests got a cut/non-200 response during shutdown (want 0)", bad)
	}
	if good == 0 {
		t.Fatal("load generated zero successful requests — the test did not exercise real traffic")
	}

	// (c) new requests after shutdown are refused (listener closed).
	if _, err := http.Get(baseURL + "/ping"); err == nil {
		t.Fatal("a new request after shutdown succeeded; the listener should be closed (connection refused)")
	}

	t.Log(fmt.Sprintf("graceful shutdown OK: closed in %v · in-flight completed · load good=%d refused=%d bad=0",
		closeDur.Round(time.Millisecond), good, refused))
}

type inflightResult struct {
	status int
	body   string
	err    error
}

// waitForServer polls url until it responds 200 or a 5s deadline passes.
func waitForServer(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("server did not come up within 5s")
}
