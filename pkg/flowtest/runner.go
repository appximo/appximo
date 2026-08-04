package flowtest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/appximo/appximo/pkg/auth"
)

// Runner executes flows against the app's REAL router (the same handler the
// listener serves — tenant middleware, rate limit, cache, JWT, RBAC, generated
// routes), in-process: no loopback socket, no parallel path, and after a
// hot-swap it runs against the CURRENT surface. The acceptance-test pattern,
// server-side.
type Runner struct {
	// Handler is the live data-plane router (App.currentRouter).
	Handler http.Handler
	// JWTSecret mints the flow-level Role token — the SAME tenant-JWT contract
	// the engine validates (never a super-admin credential).
	JWTSecret string
	// HostSuffix builds the request Host (<tenant><HostSuffix>) the tenant
	// middleware resolves. Default ".flows.internal".
	HostSuffix string
}

// StepResult is one executed step's outcome — always with the exact detail
// (expected vs got) so a red step is actionable.
type StepResult struct {
	Index      int               `json:"index"`
	Name       string            `json:"name"`
	Method     string            `json:"method"`
	Path       string            `json:"path"` // after variable substitution
	Skipped    bool              `json:"skipped,omitempty"`
	Pass       bool              `json:"pass"`
	Status     int               `json:"status"`
	Expected   int               `json:"expected"`
	Failures   []string          `json:"failures,omitempty"`
	BodySample string            `json:"body_sample,omitempty"` // the response body (capped ~2 KB) — every step, pass or fail
	Captured   map[string]string `json:"captured,omitempty"`
	DurationMS int64             `json:"duration_ms"`
}

// FlowResult is one flow's verdict with every step's outcome.
type FlowResult struct {
	Name       string       `json:"name"`
	Pass       bool         `json:"pass"`
	Steps      []StepResult `json:"steps"`
	StepsTotal int          `json:"steps_total"`
	StepsFail  int          `json:"steps_failed"`
	DurationMS int64        `json:"duration_ms"`
}

// Emit receives live events while a flow runs: ("step", StepResult) after each
// step, so a UI can stream the run (the DevHub SSE pattern). nil is fine.
type Emit func(event string, payload any)

// Run executes flow for tenantID. State flows between steps via captured
// variables; the first failing step stops the flow (later steps depend on its
// state) and the remaining steps are reported as skipped.
func (r *Runner) Run(ctx context.Context, tenantID string, flow *Flow, emit Emit) *FlowResult {
	if emit == nil {
		emit = func(string, any) {}
	}
	start := time.Now()
	res := &FlowResult{Name: flow.Name, Pass: true, StepsTotal: len(flow.Steps)}

	now := time.Now()
	tag := strconv.FormatInt(now.UnixMicro(), 36)
	if len(tag) > 8 {
		tag = tag[len(tag)-8:]
	}
	vars := map[string]string{
		// A unique run id for titles/uniques so re-running a flow never
		// collides with its previous data (the acceptance RUN_ID pattern).
		"run_id": fmt.Sprintf("%d", now.UnixNano()),
		// run_tag: the SHORT unique salt (8 base36 chars) — fits unique fields
		// with a tight maxLength where the 19-digit run_id would overflow.
		"run_tag": tag,
	}
	if flow.Role != "" {
		if tok, err := auth.GenerateToken(auth.Claims{
			UserID: "flowtest", Role: flow.Role, TenantID: tenantID,
		}, r.JWTSecret); err == nil {
			vars["token"] = tok
		}
	}

	failed := false
	for i, st := range flow.Steps {
		if failed {
			sr := StepResult{Index: i, Name: st.Name, Method: st.Method, Path: st.Path, Skipped: true}
			res.Steps = append(res.Steps, sr)
			emit("step", sr)
			continue
		}
		sr := r.runStep(ctx, tenantID, i, st, vars)
		res.Steps = append(res.Steps, sr)
		emit("step", sr)
		if !sr.Pass {
			failed = true
			res.Pass = false
			res.StepsFail++
		}
	}
	res.DurationMS = time.Since(start).Milliseconds()
	return res
}

func (r *Runner) runStep(ctx context.Context, tenantID string, idx int, st Step, vars map[string]string) StepResult {
	start := time.Now()
	sr := StepResult{
		Index: idx, Name: st.Name, Method: strings.ToUpper(st.Method),
		Path: substitute(st.Path, vars), Expected: st.Expect.Status,
	}

	var body *bytes.Buffer
	contentType := ""
	switch {
	case st.Upload != nil:
		body = &bytes.Buffer{}
		mw := multipart.NewWriter(body)
		field := st.Upload.Field
		if field == "" {
			field = "file"
		}
		part, err := mw.CreateFormFile(field, substitute(st.Upload.Filename, vars))
		if err == nil {
			_, err = part.Write([]byte(substitute(st.Upload.Content, vars)))
		}
		if cerr := mw.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			sr.Failures = append(sr.Failures, "build multipart: "+err.Error())
			sr.DurationMS = time.Since(start).Milliseconds()
			return sr
		}
		contentType = mw.FormDataContentType()
	case st.Body != "":
		body = bytes.NewBufferString(substitute(st.Body, vars))
		contentType = "application/json"
	default:
		body = &bytes.Buffer{}
	}

	suffix := r.HostSuffix
	if suffix == "" {
		suffix = ".flows.internal"
	}
	// The runner executes INSIDE an admin request; its context carries that
	// request's chi RouteContext. The inner router would REUSE it and route the
	// synthetic request as if it were the admin one (live finding: /health
	// answered 405) — a fresh RouteContext isolates the re-entrant routing.
	ctx = context.WithValue(ctx, chi.RouteCtxKey, chi.NewRouteContext())
	req := httptest.NewRequest(sr.Method, "http://placeholder"+sr.Path, body).WithContext(ctx)
	req.Host = tenantID + suffix
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	// The captured (or pre-minted) {{token}} authenticates by default; an
	// explicit Authorization header on the step wins.
	if tok, ok := vars["token"]; ok {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	for k, v := range st.Headers {
		req.Header.Set(k, substitute(v, vars))
	}

	rec := httptest.NewRecorder()
	r.Handler.ServeHTTP(rec, req)
	sr.Status = rec.Code
	respBody := rec.Body.Bytes()

	if sr.Status != st.Expect.Status {
		sr.Failures = append(sr.Failures,
			fmt.Sprintf("status: expected %d, got %d", st.Expect.Status, sr.Status))
	}

	var doc any
	docOK := json.Unmarshal(respBody, &doc) == nil
	for _, a := range st.Expect.Asserts {
		if fail := evalAssert(a, doc, docOK, vars); fail != "" {
			sr.Failures = append(sr.Failures, fail)
		}
	}

	// Captures run only on a passing step (a failed step's response is not the
	// state later steps were designed against).
	if len(sr.Failures) == 0 && len(st.Capture) > 0 {
		sr.Captured = map[string]string{}
		for name, path := range st.Capture {
			if val, found := lookupPath(doc, path); found {
				s := stringify(val)
				vars[name] = s
				sr.Captured[name] = s
			} else {
				sr.Failures = append(sr.Failures, fmt.Sprintf("capture %s from %q: field not found", name, path))
			}
		}
	}

	sr.Pass = len(sr.Failures) == 0
	// The response travels with EVERY step (FLOWTEST-POWER-S1 — "see what came
	// back", not just PASS/FAIL), capped so a 50-step persisted run stays lean.
	sample := string(respBody)
	if len(sample) > 2000 {
		sample = sample[:2000] + "…"
	}
	sr.BodySample = sample
	sr.DurationMS = time.Since(start).Milliseconds()
	return sr
}

// evalAssert evaluates one assertion against the parsed response; "" = pass,
// anything else is the actionable failure text (expected vs got, always).
func evalAssert(a Assert, doc any, docOK bool, vars map[string]string) string {
	val, found := lookupPath(doc, a.Path)
	want := substitute(a.Value, vars)
	switch a.Op {
	case "exists":
		if !docOK || !found {
			return fmt.Sprintf("assert %s exists: field not found", a.Path)
		}
	case "not_exists":
		if docOK && found {
			return fmt.Sprintf("assert %s not_exists: field is present (value %q)", a.Path, stringify(val))
		}
	case "eq":
		if !docOK || !found {
			return fmt.Sprintf("assert %s == %q: field not found", a.Path, want)
		}
		if got := stringify(val); got != want {
			return fmt.Sprintf("assert %s: expected %q, got %q", a.Path, want, got)
		}
	case "ne":
		if !docOK || !found {
			return fmt.Sprintf("assert %s != %q: field not found", a.Path, want)
		}
		if got := stringify(val); got == want {
			return fmt.Sprintf("assert %s: expected anything but %q, got exactly that", a.Path, want)
		}
	case "contains":
		if !docOK || !found {
			return fmt.Sprintf("assert %s contains %q: field not found", a.Path, want)
		}
		if got := stringify(val); !strings.Contains(got, want) {
			return fmt.Sprintf("assert %s: expected to contain %q, got %q", a.Path, want, got)
		}
	case "gt", "gte", "lt", "lte":
		if !docOK || !found {
			return fmt.Sprintf("assert %s %s %q: field not found", a.Path, a.Op, want)
		}
		got := stringify(val)
		gotN, err1 := strconv.ParseFloat(got, 64)
		wantN, err2 := strconv.ParseFloat(want, 64)
		if err1 != nil || err2 != nil {
			return fmt.Sprintf("assert %s %s %q: not numeric (got %q)", a.Path, a.Op, want, got)
		}
		ok := false
		switch a.Op {
		case "gt":
			ok = gotN > wantN
		case "gte":
			ok = gotN >= wantN
		case "lt":
			ok = gotN < wantN
		case "lte":
			ok = gotN <= wantN
		}
		if !ok {
			return fmt.Sprintf("assert %s: expected %s %s, got %s", a.Path, a.Op, want, got)
		}
	case "len":
		if !docOK || !found {
			return fmt.Sprintf("assert %s len %q: field not found", a.Path, want)
		}
		var n int
		switch x := val.(type) {
		case []any:
			n = len(x)
		case string:
			n = len(x)
		default:
			return fmt.Sprintf("assert %s len: value is not an array or string", a.Path)
		}
		wantN, err := strconv.Atoi(want)
		if err != nil {
			return fmt.Sprintf("assert %s len %q: expected length is not an integer", a.Path, want)
		}
		if n != wantN {
			return fmt.Sprintf("assert %s: expected length %d, got %d", a.Path, wantN, n)
		}
	}
	return ""
}
