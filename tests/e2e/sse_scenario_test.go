//go:build e2e

package e2e_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/miguelangel/appitools/tests/helpers"
)

// sseEvent is one parsed SSE message (event name + decoded data payload).
type sseEvent struct {
	Name string
	Data map[string]any
}

// sseSubscribe opens GET /api/{resource}/events as tenantID with token and
// returns a channel of parsed events plus a cancel func that closes the
// connection. It fails the test if the stream does not open with 200.
func sseSubscribe(t *testing.T, srv *httptest.Server, tenantID, token, resource string) (<-chan sseEvent, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/"+resource+"/events", nil)
	if err != nil {
		t.Fatalf("sse request: %v", err)
	}
	req.Host = tenantID + ".localhost"
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		cancel()
		t.Fatalf("sse connect: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		cancel()
		t.Fatalf("sse connect: status %d, want 200", resp.StatusCode)
	}

	out := make(chan sseEvent, 32)
	go func() {
		defer resp.Body.Close()
		defer close(out)
		sc := bufio.NewScanner(resp.Body)
		var name string
		var data strings.Builder
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event:"):
				name = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			case strings.HasPrefix(line, "data:"):
				data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
			case line == "" && name != "":
				var payload map[string]any
				_ = json.Unmarshal([]byte(data.String()), &payload)
				out <- sseEvent{Name: name, Data: payload}
				name = ""
				data.Reset()
			case line == "":
				// comment/heartbeat flush — ignore
				data.Reset()
			}
		}
	}()
	return out, cancel
}

// waitEvent receives one event or fails after the timeout.
func waitEvent(t *testing.T, ch <-chan sseEvent, timeout time.Duration, what string) sseEvent {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if !ok {
			t.Fatalf("%s: stream closed unexpectedly", what)
		}
		return ev
	case <-time.After(timeout):
		t.Fatalf("%s: no event within %s", what, timeout)
	}
	return sseEvent{} // unreachable
}

// assertSilence asserts no event arrives within the window.
func assertSilence(t *testing.T, ch <-chan sseEvent, window time.Duration, what string) {
	t.Helper()
	select {
	case ev, ok := <-ch:
		if ok {
			t.Fatalf("%s: unexpected event %+v", what, ev)
		}
	case <-time.After(window):
	}
}

// TestSSEScenario covers the four mandatory S45 security/functional cases:
// tenant isolation, RBAC field filtering on event payloads, auth before the
// stream opens, and the full create→update→delete lifecycle — plus the
// goroutine-leak check (every closed connection must clean its subscriber).
func TestSSEScenario(t *testing.T) {
	const tenantA = "ssetenanta"
	const tenantB = "ssetenantb"

	// ONE server, TWO tenants — isolation must hold inside a single hub.
	srv, _, s := newE2EServer(t, "logistics_schema.json", tenantA)
	helpers.RegisterTenant(t, testPool, tenantB, s)

	adminA := "Bearer " + mintJWT(t, tenantA, "super_admin")
	adminB := "Bearer " + mintJWT(t, tenantB, "super_admin")
	e := newHTTPExpect(t, srv, tenantA)

	// ── 3. Sin token / token inválido → 401 antes de abrir el stream ──────────
	t.Run("AuthRequired", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/guides/events", nil)
		req.Host = tenantA + ".localhost"
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("no token: status %d, want 401", resp.StatusCode)
		}
		req2, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/guides/events", nil)
		req2.Host = tenantA + ".localhost"
		req2.Header.Set("Authorization", "Bearer not.a.jwt")
		resp2, err := srv.Client().Do(req2)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		resp2.Body.Close()
		if resp2.StatusCode != http.StatusUnauthorized {
			t.Fatalf("bad token: status %d, want 401", resp2.StatusCode)
		}
	})

	// ── 1. Aislamiento multi-tenant ────────────────────────────────────────────
	t.Run("TenantIsolation", func(t *testing.T) {
		events, cancel := sseSubscribe(t, srv, tenantA, strings.TrimPrefix(adminA, "Bearer "), "guides")
		defer cancel()

		// Create in tenant B while A listens → A must stay silent.
		eb := newHTTPExpect(t, srv, tenantB)
		eb.POST("/api/guides").
			WithHeader("Authorization", adminB).
			WithJSON(map[string]any{"code": "B-ISO-1", "origin": "Cali", "destination": "Bogota"}).
			Expect().Status(http.StatusCreated)
		assertSilence(t, events, 1500*time.Millisecond, "tenant A subscriber after tenant B create")

		// Create in tenant A → the event arrives.
		e.POST("/api/guides").
			WithHeader("Authorization", adminA).
			WithJSON(map[string]any{"code": "A-ISO-1", "origin": "Bogota", "destination": "Cali"}).
			Expect().Status(http.StatusCreated)
		ev := waitEvent(t, events, 3*time.Second, "tenant A create")
		if ev.Name != "create" {
			t.Fatalf("event name = %q, want create", ev.Name)
		}
		rec, _ := ev.Data["record"].(map[string]any)
		if rec == nil || rec["code"] != "A-ISO-1" {
			t.Fatalf("event record = %v, want code A-ISO-1", ev.Data)
		}
	})

	// ── 2. RBAC de campos en el payload del evento ─────────────────────────────
	t.Run("FieldRBAC", func(t *testing.T) {
		// public role: read guides, fields [code,status,updated_at] only.
		public := mintJWT(t, tenantA, "public")
		events, cancel := sseSubscribe(t, srv, tenantA, public, "guides")
		defer cancel()

		e.POST("/api/guides").
			WithHeader("Authorization", adminA).
			WithJSON(map[string]any{
				"code": "A-RBAC-1", "origin": "SecretOrigin", "destination": "SecretDest",
				"weight_kg": 12.5,
			}).
			Expect().Status(http.StatusCreated)

		ev := waitEvent(t, events, 3*time.Second, "public-role create event")
		rec, _ := ev.Data["record"].(map[string]any)
		if rec == nil {
			t.Fatalf("no record in event: %v", ev.Data)
		}
		if rec["code"] != "A-RBAC-1" {
			t.Fatalf("allowed field missing: %v", rec)
		}
		for _, forbidden := range []string{"origin", "destination", "weight_kg", "operator_id"} {
			if _, leaked := rec[forbidden]; leaked {
				t.Fatalf("field %q leaked to restricted role: %v", forbidden, rec)
			}
		}
	})

	// ── 4. Flujo feliz create→update→delete + goroutine cleanup ───────────────
	t.Run("LifecycleAndGoroutines", func(t *testing.T) {
		runtime.GC()
		before := runtime.NumGoroutine()

		events, cancel := sseSubscribe(t, srv, tenantA, strings.TrimPrefix(adminA, "Bearer "), "guides")

		id := e.POST("/api/guides").
			WithHeader("Authorization", adminA).
			WithJSON(map[string]any{"code": "A-LIFE-1", "status": "pending", "origin": "Bogota", "destination": "Cali"}).
			Expect().Status(http.StatusCreated).
			JSON().Object().Value("id").String().NotEmpty().Raw()

		ev := waitEvent(t, events, 3*time.Second, "create event")
		if ev.Name != "create" {
			t.Fatalf("first event = %q, want create", ev.Name)
		}
		rec, _ := ev.Data["record"].(map[string]any)
		if rec["code"] != "A-LIFE-1" || ev.Data["id"] != id {
			t.Fatalf("create event mismatch: %v", ev.Data)
		}

		e.PATCH("/api/guides/{id}", id).
			WithHeader("Authorization", adminA).
			WithJSON(map[string]any{"status": "delivered"}).
			Expect().Status(http.StatusOK)
		ev = waitEvent(t, events, 3*time.Second, "update event")
		if ev.Name != "update" {
			t.Fatalf("second event = %q, want update", ev.Name)
		}
		rec, _ = ev.Data["record"].(map[string]any)
		if rec["status"] != "delivered" {
			t.Fatalf("update record: %v", rec)
		}

		e.DELETE("/api/guides/{id}", id).
			WithHeader("Authorization", adminA).
			Expect().Status(http.StatusNoContent)
		ev = waitEvent(t, events, 3*time.Second, "delete event")
		if ev.Name != "delete" {
			t.Fatalf("third event = %q, want delete", ev.Name)
		}
		if ev.Data["id"] != id || ev.Data["record"] != nil {
			t.Fatalf("delete event must carry id + null record: %v", ev.Data)
		}

		// Close the connection: the handler goroutine and the subscriber must go.
		cancel()
		deadline := time.Now().Add(5 * time.Second)
		for {
			runtime.GC()
			if g := runtime.NumGoroutine(); g <= before+2 || time.Now().After(deadline) {
				if g > before+2 {
					t.Fatalf("goroutine leak: before=%d after=%d", before, g)
				}
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
}
