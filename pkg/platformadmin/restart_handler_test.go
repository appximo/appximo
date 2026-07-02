package platformadmin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// newRestartTestRouter wires ONLY the routes Register mounts, with the given
// persist/trigger hooks, and returns the router (adminKey = "unit-key").
func newRestartTestRouter(t *testing.T, persist func(json.RawMessage) error, trigger func()) chi.Router {
	t.Helper()
	s := NewService(nil, nil, nil, nil, Config{
		JWTSecret:         unitSecret,
		PersistBootSchema: persist,
		TriggerRestart:    trigger,
	})
	r := chi.NewRouter()
	s.Register(r, nil, "unit-key")
	return r
}

func TestEngineSchema_RequiresPlatformAuth(t *testing.T) {
	r := newRestartTestRouter(t, func(json.RawMessage) error { t.Fatal("persist reached without auth"); return nil }, func() {})
	req := httptest.NewRequest(http.MethodPost, "/admin/engine/schema", strings.NewReader(`{"schema":{}}`))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("unauthenticated restart: want 403, got %d", w.Code)
	}
}

func TestEngineSchema_PersistsThenTriggers(t *testing.T) {
	var persisted json.RawMessage
	triggered := false
	r := newRestartTestRouter(t,
		func(raw json.RawMessage) error { persisted = raw; return nil },
		func() {
			if persisted == nil {
				t.Fatal("TriggerRestart called BEFORE the schema was persisted")
			}
			triggered = true
		})
	req := httptest.NewRequest(http.MethodPost, "/admin/engine/schema", strings.NewReader(`{"schema":{"$schema":"x","version":"1"}}`))
	req.Header.Set("X-Admin-Key", "unit-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	if !triggered || persisted == nil {
		t.Fatalf("persist/trigger not both invoked (persisted=%v triggered=%v)", persisted != nil, triggered)
	}
}

func TestEngineSchema_RejectedSchemaDoesNotRestart(t *testing.T) {
	triggered := false
	r := newRestartTestRouter(t,
		func(json.RawMessage) error { return fmt.Errorf("%w: field x is wrong", ErrSchemaRejected) },
		func() { triggered = true })
	req := httptest.NewRequest(http.MethodPost, "/admin/engine/schema", strings.NewReader(`{"schema":{"bad":true}}`))
	req.Header.Set("X-Admin-Key", "unit-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422 for a rejected schema, got %d: %s", w.Code, w.Body)
	}
	if triggered {
		t.Fatal("SAFETY: restart triggered for a rejected schema")
	}
}

func TestEngineSchema_UnavailableWithoutHooks(t *testing.T) {
	r := newRestartTestRouter(t, nil, nil)
	req := httptest.NewRequest(http.MethodPost, "/admin/engine/schema", strings.NewReader(`{"schema":{}}`))
	req.Header.Set("X-Admin-Key", "unit-key")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 when self-restart is unavailable, got %d", w.Code)
	}
}

func TestServedResources_ReportsSelfRestartAvailability(t *testing.T) {
	for _, tc := range []struct {
		persist func(json.RawMessage) error
		trigger func()
		want    bool
	}{
		{func(json.RawMessage) error { return nil }, func() {}, true},
		{nil, nil, false},
	} {
		r := newRestartTestRouter(t, tc.persist, tc.trigger)
		req := httptest.NewRequest(http.MethodGet, "/admin/served-resources", nil)
		req.Header.Set("X-Admin-Key", "unit-key")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var body struct {
			SelfRestart bool `json:"self_restart"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.SelfRestart != tc.want {
			t.Fatalf("self_restart: want %v, got %v", tc.want, body.SelfRestart)
		}
	}
}
