package platformadmin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/miguelangel/appitools/pkg/controlplane"
	"github.com/miguelangel/appitools/pkg/flowtest"
	"github.com/miguelangel/appitools/pkg/schemahistory"
)

// Flow tests + post-deploy regression (FLOWTEST-S1) — the Studio "Flows" view's
// backend. Thin delegates into pkg/flowtest: CRUD over the persisted flows,
// and the RUN endpoints that execute against the app's LIVE router (the full
// tenant→JWT→RBAC chain — flows act as tenant users, never the super-admin)
// streaming each step's PASS/FAIL live over SSE (the acceptance/DevHub
// pattern) and persisting the verdict anchored to the tenant's current schema
// version (the VERSION-S1 link: "ran the suite after deploying v5 — all green").

func (s *Service) tenantExists(ctx context.Context, id string) (bool, error) {
	_, err := s.cp.GetByID(ctx, id)
	if errors.Is(err, controlplane.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// handleListFlows is GET /admin/tenants/{id}/flows.
func (s *Service) handleListFlows(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if ok, err := s.tenantExists(r.Context(), id); !ok {
		writeTenantLookup(w, err)
		return
	}
	flows, err := flowtest.ListFlows(r.Context(), s.pool, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list flows failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"flows": flows})
}

// handleSaveFlow is POST /admin/tenants/{id}/flows (create) and
// PUT /admin/tenants/{id}/flows/{fid} (update): body {flow: {...}}.
func (s *Service) handleSaveFlow(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	fid := chi.URLParam(r, "fid") // "" on create
	if ok, err := s.tenantExists(r.Context(), id); !ok {
		writeTenantLookup(w, err)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAdminBody)
	var body struct {
		Flow *flowtest.Flow `json:"flow"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Flow == nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "body must be {\"flow\": {...}}"})
		return
	}
	if err := body.Flow.Validate(); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": err.Error()})
		return
	}
	saved, err := flowtest.SaveFlow(r.Context(), s.pool, id, fid, body.Flow)
	switch {
	case err == nil:
		code := http.StatusOK
		if fid == "" {
			code = http.StatusCreated
		}
		writeJSON(w, code, saved)
	case errors.Is(err, flowtest.ErrFlowNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "flow not found"})
	case errors.Is(err, flowtest.ErrDuplicateName):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "save flow failed"})
	}
}

// handleGetFlow is GET /admin/tenants/{id}/flows/{fid}.
func (s *Service) handleGetFlow(w http.ResponseWriter, r *http.Request) {
	id, fid := chi.URLParam(r, "id"), chi.URLParam(r, "fid")
	f, err := flowtest.GetFlow(r.Context(), s.pool, id, fid)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, f)
	case errors.Is(err, flowtest.ErrFlowNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "flow not found"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "get flow failed"})
	}
}

// handleDeleteFlow is DELETE /admin/tenants/{id}/flows/{fid}. The flow's past
// runs stay (the regression trail is append-only, like the schema history).
func (s *Service) handleDeleteFlow(w http.ResponseWriter, r *http.Request) {
	id, fid := chi.URLParam(r, "id"), chi.URLParam(r, "fid")
	err := flowtest.DeleteFlow(r.Context(), s.pool, id, fid)
	switch {
	case err == nil:
		w.WriteHeader(http.StatusNoContent)
	case errors.Is(err, flowtest.ErrFlowNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "flow not found"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete flow failed"})
	}
}

// handleRunFlow is POST /admin/tenants/{id}/flows/{fid}/run — execute ONE flow,
// streaming live SSE events; the run is persisted like a suite run (scope =
// the flow's name).
func (s *Service) handleRunFlow(w http.ResponseWriter, r *http.Request) {
	id, fid := chi.URLParam(r, "id"), chi.URLParam(r, "fid")
	f, err := flowtest.GetFlow(r.Context(), s.pool, id, fid)
	if err != nil {
		if errors.Is(err, flowtest.ErrFlowNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "flow not found"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "get flow failed"})
		return
	}
	s.streamRun(w, r, id, f.Name, []*flowtest.StoredFlow{f})
}

// handleRunSuite is POST /admin/tenants/{id}/flows/run — the regression run:
// every flow of the tenant, in name order, one SSE stream, one persisted
// verdict anchored to the current schema version.
func (s *Service) handleRunSuite(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if ok, err := s.tenantExists(r.Context(), id); !ok {
		writeTenantLookup(w, err)
		return
	}
	metas, err := flowtest.ListFlows(r.Context(), s.pool, id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list flows failed"})
		return
	}
	if len(metas) == 0 {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "this tenant has no flows to run"})
		return
	}
	flows := make([]*flowtest.StoredFlow, 0, len(metas))
	for _, m := range metas {
		f, err := flowtest.GetFlow(r.Context(), s.pool, id, m.ID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "load flow " + m.Name + " failed"})
			return
		}
		flows = append(flows, f)
	}
	s.streamRun(w, r, id, "suite", flows)
}

// streamRun executes flows in order against the app's live router, emitting
// SSE events per flow/step, then persists + emits the final verdict.
func (s *Service) streamRun(w http.ResponseWriter, r *http.Request, tenantID, scope string, flows []*flowtest.StoredFlow) {
	var handler http.Handler
	if s.cfg.FlowHandlerFn != nil {
		handler = s.cfg.FlowHandlerFn()
	}
	if handler == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "flow runner unavailable (engine still booting)"})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	emit := func(event string, payload any) {
		b, _ := json.Marshal(payload)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b) //nolint:errcheck
		flusher.Flush()
	}

	runner := &flowtest.Runner{Handler: handler, JWTSecret: s.cfg.JWTSecret}
	run := &flowtest.Run{TenantID: tenantID, Scope: scope, Pass: true}
	if v, err := schemahistory.Latest(r.Context(), s.pool, tenantID); err == nil {
		run.SchemaVersion = v
	}

	results := make([]*flowtest.FlowResult, 0, len(flows))
	for i, f := range flows {
		emit("flow", map[string]any{"name": f.Name, "index": i, "total": len(flows)})
		fr := runner.Run(r.Context(), tenantID, f.Flow, emit)
		results = append(results, fr)
		emit("flow_done", map[string]any{
			"name": fr.Name, "pass": fr.Pass,
			"steps_total": fr.StepsTotal, "steps_failed": fr.StepsFail,
			"duration_ms": fr.DurationMS,
		})
		run.FlowsTotal++
		run.StepsTotal += fr.StepsTotal
		run.StepsFailed += fr.StepsFail
		if !fr.Pass {
			run.FlowsFailed++
			run.Pass = false
		}
	}

	run.Results, _ = json.Marshal(results)
	// Persist with a fresh context: the client may disconnect mid-stream and
	// the verdict must still land in the trail.
	if err := flowtest.SaveRun(context.WithoutCancel(r.Context()), s.pool, run); err != nil {
		emit("error", map[string]string{"error": "run executed but could not be persisted: " + err.Error()})
	}
	emit("done", map[string]any{
		"run_id": run.ID, "pass": run.Pass, "schema_version": run.SchemaVersion,
		"flows_total": run.FlowsTotal, "flows_failed": run.FlowsFailed,
		"steps_total": run.StepsTotal, "steps_failed": run.StepsFailed,
	})
}

// handleListRuns is GET /admin/tenants/{id}/flows/runs — the regression trail.
func (s *Service) handleListRuns(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if ok, err := s.tenantExists(r.Context(), id); !ok {
		writeTenantLookup(w, err)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	runs, err := flowtest.ListRuns(r.Context(), s.pool, id, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "list runs failed"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs})
}

// handleGetRun is GET /admin/tenants/{id}/flows/runs/{rid} — one run's full
// per-step results (the "what exactly failed" drill-down).
func (s *Service) handleGetRun(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	rid, err := strconv.ParseInt(chi.URLParam(r, "rid"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "run id must be an integer"})
		return
	}
	run, gerr := flowtest.GetRun(r.Context(), s.pool, id, rid)
	switch {
	case gerr == nil:
		writeJSON(w, http.StatusOK, run)
	case errors.Is(gerr, flowtest.ErrFlowNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "run not found"})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "get run failed"})
	}
}

// writeTenantLookup maps a tenantExists miss: nil err ⇒ clean 404, else 500.
func writeTenantLookup(w http.ResponseWriter, err error) {
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "tenant lookup failed"})
		return
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "tenant not found"})
}
