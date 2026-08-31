package resilience

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeMeminfo(t *testing.T, dir string, availKB, swapFreeKB int64) string {
	t.Helper()
	p := filepath.Join(dir, "meminfo")
	body := "MemTotal:        1963000 kB\nMemFree:          85000 kB\nMemAvailable:    " +
		itoa(availKB) + " kB\nBuffers:           1000 kB\nCached:          886000 kB\nSwapTotal:       2047000 kB\nSwapFree:        " +
		itoa(swapFreeKB) + " kB\n"
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func itoa(n int64) string { return strings.TrimSpace(strings.Repeat(" ", 0) + fmtInt(n)) }

func fmtInt(n int64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestMemoryGuard_MeasuresAvailablePlusSwap pins the D-ter decision: the signal is
// MemAvailable + SwapFree, never MemAvailable alone (on a Postgres box the latter
// sits at tens of MiB at rest — shared_buffers is Cached but not reclaimable).
func TestMemoryGuard_MeasuresAvailablePlusSwap(t *testing.T) {
	dir := t.TempDir()
	p := writeMeminfo(t, dir, 50*1024, 1400*1024) // 50 MiB available, 1.4 GiB swap free
	g := NewMemoryGuard(64<<20, p, time.Second)
	ok, avail := g.Allow()
	if !ok {
		t.Fatalf("50 MiB available + 1400 MiB swap must pass a 64 MiB floor (got %d MiB)", avail>>20)
	}
	if avail>>20 != 1450 {
		t.Fatalf("available must be MemAvailable+SwapFree = 1450 MiB, got %d", avail>>20)
	}
}

// TestMemoryGuard_RefusesWritesOnlyBelowFloor: below the floor, a data-plane
// write is a 503 that names the numbers and the knob; reads and non-API paths
// keep flowing.
func TestMemoryGuard_RefusesWritesOnlyBelowFloor(t *testing.T) {
	dir := t.TempDir()
	p := writeMeminfo(t, dir, 20*1024, 0) // 20 MiB, no swap
	g := NewMemoryGuard(32<<20, p, time.Second)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	h := g.Middleware(next)
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{"POST", "/api/notes", 503},
		{"PATCH", "/api/notes/x", 503},
		{"DELETE", "/api/notes/x", 503},
		{"POST", "/api/transaction", 503},
		{"POST", "/graphql", 503},
		{"GET", "/api/notes", 200},
		{"GET", "/healthz", 200},
		{"POST", "/auth/login", 200},
		{"POST", "/admin/auth/login", 200},
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		if rec.Code != tc.want {
			t.Errorf("%s %s: want %d, got %d", tc.method, tc.path, tc.want, rec.Code)
		}
		if tc.want == 503 {
			if rec.Header().Get("Retry-After") == "" {
				t.Errorf("%s %s: 503 must carry Retry-After", tc.method, tc.path)
			}
			body := rec.Body.String()
			for _, want := range []string{"MemAvailable+SwapFree is 20 MiB", "32 MiB floor", MemoryGuardEnvVar, "reads continue"} {
				if !strings.Contains(body, want) {
					t.Errorf("%s %s: body must say %q: %s", tc.method, tc.path, want, body)
				}
			}
		}
	}
}

// TestMemoryGuard_UnreadableMeminfoNeverRefuses: a guard that cannot measure
// must not turn into a 503 generator.
func TestMemoryGuard_UnreadableMeminfoNeverRefuses(t *testing.T) {
	g := NewMemoryGuard(32<<20, filepath.Join(t.TempDir(), "missing"), time.Second)
	rec := httptest.NewRecorder()
	g.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(201) })).
		ServeHTTP(rec, httptest.NewRequest("POST", "/api/x", nil))
	if rec.Code != 201 {
		t.Fatalf("unreadable meminfo must pass writes through, got %d", rec.Code)
	}
}

// TestMemoryGuard_SamplesAtMostOncePerInterval: the hot path pays an atomic
// load; /proc/meminfo is read at most once per interval.
func TestMemoryGuard_SamplesAtMostOncePerInterval(t *testing.T) {
	dir := t.TempDir()
	p := writeMeminfo(t, dir, 20*1024, 0)
	g := NewMemoryGuard(32<<20, p, time.Hour)
	now := time.Unix(1000, 0)
	g.nowFn = func() time.Time { return now }
	if ok, _ := g.Allow(); ok {
		t.Fatal("first sample: 20 MiB < 32 MiB must refuse")
	}
	writeMeminfo(t, dir, 900*1024, 0) // the host recovered…
	if ok, _ := g.Allow(); ok {
		t.Fatal("…but within the interval the cached sample still rules")
	}
	now = now.Add(2 * time.Hour)
	if ok, _ := g.Allow(); !ok {
		t.Fatal("after the interval the fresh sample must admit writes")
	}
}

// TestMemoryGuard_FromEnv: the knob's contract — empty = default floor
// (max(32 MiB, 2 % of MemTotal)), 0 = disabled, garbage = an error (a safety knob
// never falls back silently).
func TestMemoryGuard_FromEnv(t *testing.T) {
	if _, err := os.Stat("/proc/meminfo"); err != nil {
		t.Skip("no /proc/meminfo on this host")
	}
	t.Setenv(MemoryGuardEnvVar, "")
	g, err := NewMemoryGuardFromEnv()
	if err != nil || g == nil {
		t.Fatalf("default must build a guard: g=%v err=%v", g, err)
	}
	if g.MinBytes() < 32<<20 {
		t.Fatalf("default floor must be at least 32 MiB, got %d", g.MinBytes())
	}
	t.Setenv(MemoryGuardEnvVar, "0")
	if g, err := NewMemoryGuardFromEnv(); err != nil || g != nil {
		t.Fatalf("0 must disable: g=%v err=%v", g, err)
	}
	t.Setenv(MemoryGuardEnvVar, "lots")
	if _, err := NewMemoryGuardFromEnv(); err == nil || !strings.Contains(err.Error(), MemoryGuardEnvVar) {
		t.Fatalf("garbage must be a named error, got %v", err)
	}
	t.Setenv(MemoryGuardEnvVar, "256")
	if g, err := NewMemoryGuardFromEnv(); err != nil || g == nil || g.MinBytes() != 256<<20 {
		t.Fatalf("explicit 256 → 256 MiB floor: g=%v err=%v", g, err)
	}
}

// TestMemoryGuard_GraphQLReadPassesUnderPressure pins ENG-60 (DEPLOY-FLOTA-S1):
// with the host under the floor, a GraphQL QUERY (a read that happens to travel
// as a POST) is served, a GraphQL MUTATION is refused, an unreadable body is
// refused (conservative), the body reaches the handler intact, and with no
// classifier wired every /graphql POST stays a write (the old contract).
func TestMemoryGuard_GraphQLReadPassesUnderPressure(t *testing.T) {
	dir := t.TempDir()
	p := writeMeminfo(t, dir, 10*1024, 0) // 10 MiB available, no swap → under a 64 MiB floor
	g := NewMemoryGuard(64<<20, p, time.Hour)
	g.GraphQLMutation = func(body []byte) bool { return strings.Contains(string(body), "mutation") }
	var seen string
	h := g.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seen = string(b)
		w.WriteHeader(http.StatusOK)
	}))
	do := func(path, body string) int {
		seen = ""
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
		return rec.Code
	}
	if c := do("/graphql", `{"query":"{ guides { id } }"}`); c != http.StatusOK {
		t.Fatalf("GraphQL query under pressure = %d, want 200 (a read is not a write)", c)
	}
	if seen != `{"query":"{ guides { id } }"}` {
		t.Fatalf("handler saw body %q — the guard must hand the body back untouched", seen)
	}
	if c := do("/graphql", `{"query":"mutation { x }"}`); c != http.StatusServiceUnavailable {
		t.Fatalf("GraphQL mutation under pressure = %d, want 503", c)
	}
	if c := do("/api/things", `{}`); c != http.StatusServiceUnavailable {
		t.Fatalf("REST write under pressure = %d, want 503", c)
	}
	// No classifier → the old contract: every /graphql POST is a write.
	g.GraphQLMutation = nil
	if c := do("/graphql", `{"query":"{ guides { id } }"}`); c != http.StatusServiceUnavailable {
		t.Fatalf("GraphQL POST with no classifier = %d, want 503 (old contract kept)", c)
	}
	// Oversized body → refused like a write (never parsed past the cap).
	g.GraphQLMutation = func([]byte) bool { return false }
	if c := do("/graphql", strings.Repeat("x", graphqlBodyCap+1)); c != http.StatusServiceUnavailable {
		t.Fatalf("oversized GraphQL body under pressure = %d, want 503", c)
	}
}
