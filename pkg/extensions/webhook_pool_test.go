package extensions_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/miguelangel/appitools/pkg/extensions"
	"github.com/miguelangel/appitools/pkg/schema"
)

// FireAfterHook must bound the number of concurrent in-flight dispatches. With a
// cap of 2, firing 5 hooks against a webhook server that blocks admits exactly 2
// (returns true) and drops 3 (returns false) instead of spawning 5 goroutines.
// This is the remediation for the "webhook dispatch has no goroutine limit"
// finding from V3.
func TestFireAfterHook_BoundsConcurrency(t *testing.T) {
	var hits int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&hits, 1)
		<-release // hold the dispatch goroutine (and its semaphore slot) open
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	disp := extensions.NewWebhookDispatcherOpts(
		extensions.WithInsecureTransport(&http.Client{Timeout: 5 * time.Second}),
	)
	hr := extensions.NewHookRunnerWithDispatcher(extensions.NewJSSandbox(), disp, 2)

	hook := &schema.HookConfig{Type: "webhook", URL: srv.URL}
	admitted, dropped := 0, 0
	for i := 0; i < 5; i++ {
		if hr.FireAfterHook(hook, map[string]any{"id": i}, "t") {
			admitted++
		} else {
			dropped++
		}
	}
	if admitted != 2 || dropped != 3 {
		t.Fatalf("expected 2 admitted / 3 dropped with cap=2, got %d / %d", admitted, dropped)
	}

	// Exactly the 2 admitted dispatches reach the (blocked) server.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && atomic.LoadInt32(&hits) < 2 {
		time.Sleep(5 * time.Millisecond)
	}
	// Give any erroneously-spawned goroutine a chance to also reach the server.
	time.Sleep(100 * time.Millisecond)
	if got := atomic.LoadInt32(&hits); got != 2 {
		t.Fatalf("expected exactly 2 webhook hits (bounded), got %d", got)
	}
	close(release)
}

// FireAfterHook must return immediately even when the webhook is slow: the
// request goroutine is never blocked on dispatch (the basis of the "201 <100ms"
// guarantee).
func TestFireAfterHook_NonBlocking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(750 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	disp := extensions.NewWebhookDispatcherOpts(
		extensions.WithInsecureTransport(&http.Client{Timeout: 5 * time.Second}),
	)
	hr := extensions.NewHookRunnerWithDispatcher(extensions.NewJSSandbox(), disp, 0)
	hook := &schema.HookConfig{Type: "webhook", URL: srv.URL}

	start := time.Now()
	if !hr.FireAfterHook(hook, map[string]any{"id": "x"}, "t") {
		t.Fatal("expected FireAfterHook to admit the dispatch")
	}
	if elapsed := time.Since(start); elapsed > 20*time.Millisecond {
		t.Fatalf("FireAfterHook blocked for %v; expected immediate return", elapsed)
	}
}

// A nil hook is a no-op that reports success (true) and dispatches nothing.
func TestFireAfterHook_NilHook(t *testing.T) {
	hr := extensions.NewHookRunner(extensions.NewJSSandbox())
	if !hr.FireAfterHook(nil, map[string]any{"id": "x"}, "t") {
		t.Fatal("nil hook should return true (no-op)")
	}
}
