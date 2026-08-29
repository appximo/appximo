package main

// The leash, proven against illegal attempts. Every refusal here must happen
// with ZERO network calls — the recording transport fails the test otherwise.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// recordingTransport counts every request and serves canned responses. It is
// the whole proof apparatus: if a guarded call slips through to the network,
// the count says so.
type recordingTransport struct {
	mu      sync.Mutex
	calls   []string // "METHOD path"
	respond func(req *http.Request) (*http.Response, error)
}

func (t *recordingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	t.calls = append(t.calls, req.Method+" "+req.URL.Path)
	t.mu.Unlock()
	if t.respond != nil {
		return t.respond(req)
	}
	return jsonResp(200, `{}`), nil
}

func (t *recordingTransport) count() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.calls)
}

func (t *recordingTransport) mutations() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var m []string
	for _, c := range t.calls {
		if !strings.HasPrefix(c, "GET ") {
			m = append(m, c)
		}
	}
	return m
}

func jsonResp(code int, body string) *http.Response {
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func testClient(apply bool, rt *recordingTransport) *Client {
	return &Client{
		base:  "https://api.invalid",
		token: "test-token-never-real",
		http:  &http.Client{Transport: rt},
		apply: apply,
		out:   io.Discard,
	}
}

func labDroplet(id int64, name string, tags []string, publicIP string, age time.Duration) Droplet {
	d := Droplet{ID: id, Name: name, Tags: tags, Status: "active", CreatedAt: time.Now().Add(-age)}
	if publicIP != "" {
		d.PublicIPs = []string{publicIP}
	}
	return d
}

// ── refusals, with zero network ────────────────────────────────────────────

func TestDestroyRefusesForeignName(t *testing.T) {
	rt := &recordingTransport{}
	c := testClient(true, rt) // apply=true: the guard, not the dry-run, must refuse
	err := c.Destroy(context.Background(), labDroplet(7, "web-1", []string{LabTag}, "203.0.113.9", time.Hour))
	if !errors.Is(err, ErrGuard) {
		t.Fatalf("want guard refusal, got %v", err)
	}
	if n := rt.count(); n != 0 {
		t.Fatalf("guard refusal must not touch the network; %d calls made: %v", n, rt.calls)
	}
}

func TestDestroyRefusesMissingTag(t *testing.T) {
	rt := &recordingTransport{}
	c := testClient(true, rt)
	err := c.Destroy(context.Background(), labDroplet(8, "applab-gen", nil, "203.0.113.9", time.Hour))
	if !errors.Is(err, ErrGuard) {
		t.Fatalf("prefix alone must not authorize; got %v", err)
	}
	if n := rt.count(); n != 0 {
		t.Fatalf("guard refusal must not touch the network; calls: %v", rt.calls)
	}
}

func TestDestroyRefusesProtectedIPs(t *testing.T) {
	// The belt loads from operator config; seed it here the way the file would.
	protectedIPs["203.0.113.105"] = "the orchestrator box (test)"
	protectedIPs["203.0.113.58"] = "the production demos box (test)"
	defer func() { delete(protectedIPs, "203.0.113.105"); delete(protectedIPs, "203.0.113.58") }()
	for ip, who := range protectedIPs {
		rt := &recordingTransport{}
		c := testClient(true, rt)
		// Even a droplet wearing the full lab identity is refused on a
		// protected address.
		err := c.Destroy(context.Background(), labDroplet(9, "applab-evil", []string{LabTag}, ip, time.Hour))
		if !errors.Is(err, ErrGuard) {
			t.Fatalf("droplet on %s (%s) must be refused; got %v", ip, who, err)
		}
		if n := rt.count(); n != 0 {
			t.Fatalf("refusal for %s must not touch the network; calls: %v", ip, rt.calls)
		}
	}
}

func TestDestroyByNameRefusesForeignNameBeforeLookup(t *testing.T) {
	rt := &recordingTransport{}
	c := testClient(true, rt)
	err := c.DestroyByName(context.Background(), "reto-tr-prod")
	if !errors.Is(err, ErrGuard) {
		t.Fatalf("want guard refusal, got %v", err)
	}
	if n := rt.count(); n != 0 {
		t.Fatalf("name refusal must come before the listing; calls: %v", rt.calls)
	}
}

func TestCreateRefusesForeignName(t *testing.T) {
	rt := &recordingTransport{}
	c := testClient(true, rt)
	_, err := c.Create(context.Background(), CreateRequest{Name: "my-server", Size: "s-1vcpu-1gb", Region: "nyc3", Image: "ubuntu-24-04-x64"})
	if !errors.Is(err, ErrGuard) {
		t.Fatalf("want guard refusal, got %v", err)
	}
	if n := rt.count(); n != 0 {
		t.Fatalf("name refusal must not touch the network; calls: %v", rt.calls)
	}
}

// ── the cap ────────────────────────────────────────────────────────────────

func TestCreateRefusesOverCap(t *testing.T) {
	full := `{"droplets":[` + strings.TrimSuffix(strings.Repeat(
		`{"id":1,"name":"applab-x","tags":["applab"],"status":"active"},`, MaxDroplets), ",") + `]}`
	rt := &recordingTransport{respond: func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			return jsonResp(200, full), nil
		}
		return jsonResp(202, `{}`), nil
	}}
	c := testClient(true, rt)
	_, err := c.Create(context.Background(), CreateRequest{Name: "applab-extra", Size: "c-2", Region: "nyc3", Image: "ubuntu-24-04-x64"})
	if !errors.Is(err, ErrGuard) {
		t.Fatalf("want cap refusal, got %v", err)
	}
	if m := rt.mutations(); len(m) != 0 {
		t.Fatalf("cap refusal must not POST; mutations: %v", m)
	}
}

// ── dry-run is the default and mutates nothing ─────────────────────────────

func TestDryRunMutatesNothing(t *testing.T) {
	rt := &recordingTransport{respond: func(req *http.Request) (*http.Response, error) {
		return jsonResp(200, `{"droplets":[]}`), nil
	}}
	c := testClient(false, rt) // dry-run
	d, err := c.Create(context.Background(), CreateRequest{Name: "applab-gen", Size: "c-4", Region: "nyc3", Image: "ubuntu-24-04-x64"})
	if err != nil {
		t.Fatalf("dry-run create: %v", err)
	}
	if d.Status != "dry-run" {
		t.Fatalf("dry-run create must return a synthetic droplet, got %+v", d)
	}
	if err := c.Destroy(context.Background(), labDroplet(3, "applab-target-basic", []string{LabTag}, "203.0.113.4", time.Hour)); err != nil {
		t.Fatalf("dry-run destroy: %v", err)
	}
	if err := c.Snapshot(context.Background(), labDroplet(3, "applab-target-basic", []string{LabTag}, "203.0.113.4", time.Hour), "applab-snap"); err != nil {
		t.Fatalf("dry-run snapshot: %v", err)
	}
	if m := rt.mutations(); len(m) != 0 {
		t.Fatalf("dry-run made mutating calls: %v", m)
	}
}

func TestDryRunPlanNeverContainsToken(t *testing.T) {
	rt := &recordingTransport{respond: func(req *http.Request) (*http.Response, error) {
		return jsonResp(200, `{"droplets":[]}`), nil
	}}
	var buf strings.Builder
	c := testClient(false, rt)
	c.out = &buf
	_, _ = c.Create(context.Background(), CreateRequest{Name: "applab-gen", Size: "c-4", Region: "nyc3", Image: "ubuntu-24-04-x64"})
	_ = c.Destroy(context.Background(), labDroplet(3, "applab-gen", []string{LabTag}, "", time.Hour))
	if strings.Contains(buf.String(), c.token) {
		t.Fatalf("plan output leaked the token")
	}
}

// ── the reaper ─────────────────────────────────────────────────────────────

func TestReapSelectsOnlyOldLabDroplets(t *testing.T) {
	now := time.Now()
	ds := []Droplet{
		labDroplet(1, "applab-gen", []string{LabTag}, "203.0.113.1", 9*time.Hour),             // old lab → reap
		labDroplet(2, "applab-target-basic", []string{LabTag}, "203.0.113.2", 30*time.Minute), // young lab → keep
		labDroplet(3, "applab-imposter", nil, "203.0.113.3", 40*time.Hour),                    // no tag → skip, never destroy
		labDroplet(4, "prod-api", []string{LabTag}, "203.0.113.4", 40*time.Hour),              // wrong name → skip
	}
	old, skipped := ReapSelect(ds, 6*time.Hour, now)
	if len(old) != 1 || old[0].ID != 1 {
		t.Fatalf("want exactly droplet 1 reaped, got %+v", old)
	}
	if len(skipped) != 2 {
		t.Fatalf("want 2 guarded-out droplets reported, got %+v", skipped)
	}
}

// ── down keeps going and verifies ──────────────────────────────────────────

// TestDownSurvivesPartialFailure pins the "infallible down" property at the
// unit level: one droplet's DELETE failing must not stop the others, and the
// verdict must come from a final re-listing, not from the loop's bookkeeping.
func TestDownSurvivesPartialFailure(t *testing.T) {
	var deletes int32
	listCalls := 0
	rt := &recordingTransport{}
	rt.respond = func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodGet:
			listCalls++
			if listCalls == 1 { // initial listing: two lab droplets
				return jsonResp(200, `{"droplets":[
					{"id":11,"name":"applab-gen","tags":["applab"],"status":"active"},
					{"id":12,"name":"applab-target-basic","tags":["applab"],"status":"active"}]}`), nil
			}
			// final verification: droplet 11 still alive
			return jsonResp(200, `{"droplets":[{"id":11,"name":"applab-gen","tags":["applab"],"status":"active"}]}`), nil
		case req.Method == http.MethodDelete && strings.HasSuffix(req.URL.Path, "/11"):
			deletes++
			return jsonResp(500, `{"message":"boom"}`), nil
		case req.Method == http.MethodDelete:
			deletes++
			return jsonResp(204, ``), nil
		}
		return jsonResp(200, `{}`), nil
	}
	c := testClient(true, rt)
	survivors, err := downAll(context.Background(), c, 2, 0)
	if err == nil {
		t.Fatalf("down with a survivor must report failure")
	}
	if len(survivors) != 1 || survivors[0].ID != 11 {
		t.Fatalf("want droplet 11 reported as survivor, got %+v", survivors)
	}
	if deletes < 3 { // 12 once + 11 tried twice (retry)
		t.Fatalf("want retries on the failing droplet, got %d deletes: %v", deletes, rt.calls)
	}
}

func TestDownCleanExitsClean(t *testing.T) {
	listCalls := 0
	rt := &recordingTransport{}
	rt.respond = func(req *http.Request) (*http.Response, error) {
		if req.Method == http.MethodGet {
			listCalls++
			if listCalls == 1 {
				return jsonResp(200, `{"droplets":[{"id":21,"name":"applab-gen","tags":["applab"],"status":"active"}]}`), nil
			}
			return jsonResp(200, `{"droplets":[]}`), nil
		}
		return jsonResp(204, ``), nil
	}
	c := testClient(true, rt)
	survivors, err := downAll(context.Background(), c, 2, 0)
	if err != nil || len(survivors) != 0 {
		t.Fatalf("clean down failed: err=%v survivors=%+v", err, survivors)
	}
}

func TestValidLabName(t *testing.T) {
	for name, want := range map[string]bool{
		"applab-gen": true, "applab-": false, "applab": false,
		"web-applab-1": false, "": false, "APPLAB-GEN": false,
	} {
		if got := ValidLabName(name); got != want {
			t.Fatalf("ValidLabName(%q) = %v, want %v", name, got, want)
		}
	}
}

func TestGuardErrIsError(t *testing.T) {
	err := guardErr("x %d", 1)
	if !errors.Is(err, ErrGuard) || err.Error() != fmt.Sprintf("%s: x 1", ErrGuard.Error()) {
		t.Fatalf("guardErr shape: %v", err)
	}
}
