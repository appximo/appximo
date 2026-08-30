package resilience

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func admissionForTest(t *testing.T, limit string, exempt func(*http.Request) bool) *Admission {
	t.Helper()
	t.Setenv(AdmissionEnvVar, limit)
	a, err := NewAdmissionFromEnv(2, 10, exempt)
	if err != nil {
		t.Fatalf("NewAdmissionFromEnv: %v", err)
	}
	return a
}

func TestAdmissionAutoFormula(t *testing.T) {
	t.Setenv(AdmissionEnvVar, "")
	a, err := NewAdmissionFromEnv(2, 10, nil)
	if err != nil || a == nil {
		t.Fatalf("auto: %v %v", a, err)
	}
	if a.Limit() != 48 { // 4×(2+10)
		t.Fatalf("auto limit on 2 cores + 10 conns = %d, want 48", a.Limit())
	}
	a, err = NewAdmissionFromEnv(1, 4, nil)
	if err != nil || a.Limit() != 32 { // floor
		t.Fatalf("floor: %v %v", a, err)
	}
}

func TestAdmissionDisabledAndBadValue(t *testing.T) {
	t.Setenv(AdmissionEnvVar, "0")
	if a, err := NewAdmissionFromEnv(2, 10, nil); err != nil || a != nil {
		t.Fatalf("0 must disable: %v %v", a, err)
	}
	t.Setenv(AdmissionEnvVar, "many")
	if _, err := NewAdmissionFromEnv(2, 10, nil); err == nil {
		t.Fatal("a non-integer must refuse to boot (ENG-47 rule)")
	}
	t.Setenv(AdmissionEnvVar, "-3")
	if _, err := NewAdmissionFromEnv(2, 10, nil); err == nil {
		t.Fatal("a negative value must refuse to boot")
	}
}

// TestAdmissionShedsOverLimitCheaplyAndEarly pins the contract: over the cap
// the answer is an immediate 429 with Retry-After, the handler is NOT invoked,
// and the slot accounting recovers when the holders finish.
func TestAdmissionShedsOverLimit(t *testing.T) {
	a := admissionForTest(t, "2", nil)
	release := make(chan struct{})
	var entered sync.WaitGroup
	entered.Add(2)
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/fill" {
			entered.Done()
			<-release
		}
		w.WriteHeader(200)
	}))
	// Fill both slots.
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/fill", nil))
			if rec.Code != 200 {
				t.Errorf("admitted request got %d", rec.Code)
			}
		}()
	}
	entered.Wait()
	// The third is shed, immediately.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("POST", "/api/tasks", strings.NewReader("{}")))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("over-limit request: got %d, want 429", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "1" {
		t.Fatalf("429 without Retry-After: %q", rec.Header().Get("Retry-After"))
	}
	if !strings.Contains(rec.Body.String(), "server at capacity") || !strings.Contains(rec.Body.String(), AdmissionEnvVar) {
		t.Fatalf("shed body must name the condition and the knob: %s", rec.Body.String())
	}
	if got := a.Rejected(); got != 1 {
		t.Fatalf("rejected counter = %d, want 1", got)
	}
	close(release)
	wg.Wait()
	if got := a.InFlight(); got != 0 {
		t.Fatalf("in-flight after drain = %d, want 0", got)
	}
	// The door is open again.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/tasks", nil))
	if rec.Code != 200 {
		t.Fatalf("post-drain request got %d, want 200", rec.Code)
	}
}

func TestAdmissionScopeExclusions(t *testing.T) {
	a := admissionForTest(t, "1", func(r *http.Request) bool {
		return strings.HasPrefix(r.URL.Path, "/api/files/") // byte-serving stand-in
	})
	// Occupy the single slot.
	release := make(chan struct{})
	var entered sync.WaitGroup
	entered.Add(1)
	h := a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/busy" {
			entered.Done()
			<-release
		}
		w.WriteHeader(200)
	}))
	go func() {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/busy", nil))
	}()
	entered.Wait()
	defer close(release)

	// Everything out of scope passes even with the cap full.
	for _, tc := range []struct{ method, path string }{
		{"GET", "/healthz"}, {"GET", "/readyz"}, {"GET", "/health"},
		{"GET", "/metrics"}, {"GET", "/debug/vars"}, {"GET", "/admin/tenants"},
		{"GET", "/editor"}, {"GET", "/app/index.html"}, {"GET", "/docs"},
		{"GET", "/openapi.json"},
		{"GET", "/api/tasks/events"}, // SSE — held open by design
		{"GET", "/api/files/abc"},    // byte-serving download (exempt fn)
		{"OPTIONS", "/api/tasks"},    // CORS preflight
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != 200 {
			t.Fatalf("%s %s must bypass admission, got %d", tc.method, tc.path, rec.Code)
		}
	}
	// And a covered path is shed.
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/api/tasks", nil))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("covered path with a full cap: got %d, want 429", rec.Code)
	}
}

func TestAdmissionCoversTheDataPlane(t *testing.T) {
	a := admissionForTest(t, "5", nil)
	for _, p := range []string{"/api/tasks", "/api/transaction", "/graphql", "/auth/login", "/api/files"} {
		if !a.covered(httptest.NewRequest("POST", p, nil)) {
			t.Fatalf("%s must be admission-scoped", p)
		}
	}
}
