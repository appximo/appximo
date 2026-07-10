package flowtest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/miguelangel/appitools/pkg/auth"
)

// fakeApp is a mini app for runner tests: a login that issues a token, a
// create that requires it and returns an id, and a get that echoes the record.
// It lets the tests pin the runner's CORE contract — state (token, created id)
// flowing between steps, assertions with exact detail, fail-stops — without a
// database.
func fakeApp(t *testing.T) http.Handler {
	t.Helper()
	r := chi.NewRouter()
	r.Post("/auth/login", func(w http.ResponseWriter, req *http.Request) {
		var body struct{ Email, Password string }
		json.NewDecoder(req.Body).Decode(&body) //nolint:errcheck
		if body.Password != "secreta123" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":"invalid credentials"}`)
			return
		}
		fmt.Fprint(w, `{"user":{"email":"opto@x.co"},"token":"tok-123"}`)
	})
	r.Post("/api/citas", func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer tok-123" {
			w.WriteHeader(401)
			fmt.Fprint(w, `{"error":"invalid token"}`)
			return
		}
		var body map[string]any
		json.NewDecoder(req.Body).Decode(&body) //nolint:errcheck
		w.WriteHeader(201)
		fmt.Fprintf(w, `{"id":"cita-9","motivo":%q}`, body["motivo"])
	})
	r.Get("/api/citas/{id}", func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprintf(w, `{"id":%q,"estado":"pendiente","items":[{"n":1},{"n":2}]}`, chi.URLParam(req, "id"))
	})
	return r
}

func TestRunnerStateFlowsBetweenSteps(t *testing.T) {
	flow := &Flow{
		Name: "optica",
		Steps: []Step{
			{
				Name: "login", Method: "POST", Path: "/auth/login",
				Body:    `{"email":"opto@x.co","password":"secreta123"}`,
				Expect:  Expect{Status: 200, Asserts: []Assert{{Path: "token", Op: "exists"}}},
				Capture: map[string]string{"token": "token"},
			},
			{
				Name: "crear cita", Method: "POST", Path: "/api/citas",
				Body:    `{"motivo":"control-{{run_id}}"}`,
				Expect:  Expect{Status: 201},
				Capture: map[string]string{"cita_id": "id"},
			},
			{
				Name: "verificar", Method: "GET", Path: "/api/citas/{{cita_id}}",
				Expect: Expect{Status: 200, Asserts: []Assert{
					{Path: "estado", Op: "eq", Value: "pendiente"},
					{Path: "id", Op: "eq", Value: "{{cita_id}}"},
					{Path: "items.1.n", Op: "eq", Value: "2"},
				}},
			},
		},
	}
	if err := flow.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	var events []string
	r := &Runner{Handler: fakeApp(t)}
	res := r.Run(context.Background(), "acme", flow, func(ev string, _ any) { events = append(events, ev) })
	if !res.Pass || res.StepsFail != 0 {
		t.Fatalf("flow should pass: %+v", res)
	}
	// The token captured in step 1 authenticated step 2 (the fake 401s without
	// it); the id captured in step 2 built step 3's path.
	if res.Steps[2].Path != "/api/citas/cita-9" {
		t.Fatalf("captured id did not flow into the path: %q", res.Steps[2].Path)
	}
	if len(events) != 3 {
		t.Fatalf("expected 3 live step events, got %d", len(events))
	}
}

func TestRunnerFailureIsActionableAndStops(t *testing.T) {
	flow := &Flow{
		Name: "regresion",
		Steps: []Step{
			{Name: "login", Method: "POST", Path: "/auth/login",
				Body:    `{"email":"opto@x.co","password":"secreta123"}`,
				Expect:  Expect{Status: 200},
				Capture: map[string]string{"token": "token"}},
			{Name: "crear", Method: "POST", Path: "/api/citas",
				Body:   `{"motivo":"x"}`,
				Expect: Expect{Status: 201, Asserts: []Assert{{Path: "prioridad", Op: "exists"}}}},
			{Name: "nunca corre", Method: "GET", Path: "/api/citas/x", Expect: Expect{Status: 200}},
		},
	}
	res := (&Runner{Handler: fakeApp(t)}).Run(context.Background(), "acme", flow, nil)
	if res.Pass {
		t.Fatal("flow must fail")
	}
	// The exact failing step, with expected-vs-real detail.
	st := res.Steps[1]
	if st.Pass || len(st.Failures) != 1 || !strings.Contains(st.Failures[0], "prioridad exists: field not found") {
		t.Fatalf("failure detail not actionable: %+v", st)
	}
	if st.BodySample == "" {
		t.Fatal("failing step must carry a body sample")
	}
	// Later steps are skipped (their state depends on the failed one).
	if !res.Steps[2].Skipped {
		t.Fatalf("step after a failure must be skipped: %+v", res.Steps[2])
	}
}

func TestRunnerWrongStatusDetail(t *testing.T) {
	flow := &Flow{Name: "login-roto", Steps: []Step{
		{Name: "login mal", Method: "POST", Path: "/auth/login",
			Body: `{"email":"x","password":"MALA"}`, Expect: Expect{Status: 200}},
	}}
	res := (&Runner{Handler: fakeApp(t)}).Run(context.Background(), "acme", flow, nil)
	if res.Pass || !strings.Contains(res.Steps[0].Failures[0], "expected 200, got 401") {
		t.Fatalf("status failure must name expected vs got: %+v", res.Steps[0])
	}
}

// A flow-level Role pre-mints a real tenant JWT into {{token}} — the
// convenience path when the flow doesn't exercise the login itself.
func TestRunnerRolePremintsTenantToken(t *testing.T) {
	secret := "unit-secret-of-32-chars-minimum!!"
	var gotAuth, gotHost string
	h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotAuth = req.Header.Get("Authorization")
		gotHost = req.Host
		fmt.Fprint(w, `{}`)
	})
	flow := &Flow{Name: "role", Role: "optometra", Steps: []Step{
		{Name: "get", Method: "GET", Path: "/api/citas", Expect: Expect{Status: 200}},
	}}
	res := (&Runner{Handler: h, JWTSecret: secret}).Run(context.Background(), "vopt", flow, nil)
	if !res.Pass {
		t.Fatalf("should pass: %+v", res)
	}
	if !strings.HasPrefix(gotAuth, "Bearer ") {
		t.Fatalf("role flow must send a Bearer token, got %q", gotAuth)
	}
	claims, err := auth.ValidateToken(strings.TrimPrefix(gotAuth, "Bearer "), secret)
	if err != nil || claims.Role != "optometra" || claims.TenantID != "vopt" {
		t.Fatalf("minted token must be a REAL tenant JWT for the role: %+v err=%v", claims, err)
	}
	if !strings.HasPrefix(gotHost, "vopt.") {
		t.Fatalf("request Host must resolve the tenant: %q", gotHost)
	}
}

// The enriched assertion vocabulary (FLOWTEST-POWER-S1): not_exists (the
// GraphQL success check), ne, numeric comparisons, array length.
func TestRunnerEnrichedAsserts(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		fmt.Fprint(w, `{"data":[{"id":"a","total":42.5},{"id":"b","total":10}],"meta":{"total":2}}`)
	})
	flow := &Flow{Name: "asserts", Steps: []Step{
		{Name: "rich", Method: "GET", Path: "/api/x", Expect: Expect{Status: 200, Asserts: []Assert{
			{Path: "errors", Op: "not_exists"},
			{Path: "data", Op: "len", Value: "2"},
			{Path: "data.0.id", Op: "ne", Value: "b"},
			{Path: "data.0.total", Op: "gt", Value: "42"},
			{Path: "data.0.total", Op: "gte", Value: "42.5"},
			{Path: "data.1.total", Op: "lt", Value: "11"},
			{Path: "meta.total", Op: "lte", Value: "2"},
		}}},
	}}
	if err := flow.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	res := (&Runner{Handler: h}).Run(context.Background(), "acme", flow, nil)
	if !res.Pass {
		t.Fatalf("all enriched asserts should pass: %+v", res.Steps[0].Failures)
	}
	// Every step now carries its response, pass or fail (the "see what came
	// back" contract).
	if res.Steps[0].BodySample == "" {
		t.Fatal("a passing step must carry its response body")
	}

	failing := &Flow{Name: "asserts-fail", Steps: []Step{
		{Name: "rich", Method: "GET", Path: "/api/x", Expect: Expect{Status: 200, Asserts: []Assert{
			{Path: "meta", Op: "not_exists"},
			{Path: "data", Op: "len", Value: "3"},
			{Path: "data.0.id", Op: "gt", Value: "1"},
		}}},
	}}
	res = (&Runner{Handler: h}).Run(context.Background(), "acme", failing, nil)
	st := res.Steps[0]
	if res.Pass || len(st.Failures) != 3 {
		t.Fatalf("expected 3 failures: %+v", st.Failures)
	}
	for want, frag := range map[string]string{
		"not_exists": "field is present", "len": "expected length 3, got 2", "gt": "not numeric",
	} {
		found := false
		for _, f := range st.Failures {
			if strings.Contains(f, frag) {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s failure must be actionable (%q): %+v", want, frag, st.Failures)
		}
	}
}

func TestFlowValidateRejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		f    Flow
		want string
	}{
		{"no steps", Flow{Name: "x"}, "at least one step"},
		{"bad method", Flow{Name: "x", Steps: []Step{{Name: "s", Method: "FETCH", Path: "/a", Expect: Expect{Status: 200}}}}, "method"},
		{"bad path", Flow{Name: "x", Steps: []Step{{Name: "s", Method: "GET", Path: "api/a", Expect: Expect{Status: 200}}}}, "start with /"},
		{"bad status", Flow{Name: "x", Steps: []Step{{Name: "s", Method: "GET", Path: "/a", Expect: Expect{Status: 9}}}}, "status"},
		{"bad op", Flow{Name: "x", Steps: []Step{{Name: "s", Method: "GET", Path: "/a",
			Expect: Expect{Status: 200, Asserts: []Assert{{Path: "id", Op: "regex"}}}}}}, "exists/not_exists/eq/ne/contains"},
		{"gt needs value", Flow{Name: "x", Steps: []Step{{Name: "s", Method: "GET", Path: "/a",
			Expect: Expect{Status: 200, Asserts: []Assert{{Path: "n", Op: "gt"}}}}}}, "needs a value"},
	}
	for _, tc := range cases {
		err := tc.f.Validate()
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: err=%v, want containing %q", tc.name, err, tc.want)
		}
	}
}

func TestLookupPathAndStringify(t *testing.T) {
	var doc any
	json.Unmarshal([]byte(`{"data":[{"id":"a1","n":3,"ok":true,"price":9.5}],"meta":{"total":2}}`), &doc) //nolint:errcheck
	for path, want := range map[string]string{
		"data.0.id": "a1", "data.0.n": "3", "data.0.ok": "true",
		"data.0.price": "9.5", "meta.total": "2",
	} {
		v, found := lookupPath(doc, path)
		if !found || stringify(v) != want {
			t.Fatalf("lookup %s = %q (found=%v), want %q", path, stringify(v), found, want)
		}
	}
	if _, found := lookupPath(doc, "data.5.id"); found {
		t.Fatal("out-of-range index must not be found")
	}
	if _, found := lookupPath(doc, "meta.missing"); found {
		t.Fatal("missing key must not be found")
	}
}

func TestSubstituteLeavesUnknownVarsVisible(t *testing.T) {
	vars := map[string]string{"token": "t1", "cita_id": "c9"}
	got := substitute("/api/citas/{{cita_id}}?t={{token}}&x={{desconocida}}", vars)
	if got != "/api/citas/c9?t=t1&x={{desconocida}}" {
		t.Fatalf("substitute: %q", got)
	}
}
