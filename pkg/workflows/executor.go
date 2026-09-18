package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/rs/zerolog"

	"github.com/appximo/appximo/pkg/extensions"
	"github.com/appximo/appximo/pkg/outbox"
)

// EngineDoer is the slice of worker.EngineClient the executor needs: an
// authenticated request through the engine API (tenant Host + scoped service
// JWT), so every step's write inherits the engine's validation and RBAC. Steps
// NEVER touch the tenant database directly — the worker doctrine.
type EngineDoer interface {
	Do(ctx context.Context, tenant, method, path string, body any) (int, []byte, error)
}

// ClientFactory returns the EngineDoer for a given RBAC role (workflows may
// declare their own role; "" means the worker's default service role). The
// factory caches per-role clients.
type ClientFactory func(role string) EngineDoer

// Executor runs one workflow instance: sequential steps, per-step detail, the
// run recorded in public.workflow_runs whatever happens.
type Executor struct {
	Clients    ClientFactory
	Store      *Store
	Dispatcher *extensions.WebhookDispatcher // SSRF-guarded, HTTPS-only — same as hooks
	Log        zerolog.Logger

	// StepTimeout bounds each step (default 30s).
	StepTimeout time.Duration
}

// stepResult is the per-step entry of a run's recorded detail.
type stepResult struct {
	Name   string `json:"name"`
	Status string `json:"status"` // ok | failed | stopped
	Note   string `json:"note,omitempty"`
}

// FetchRecord loads the triggering record through the engine API (GET
// /api/{resource}/{id}) as the role — so a workflow can only see what its role
// may read. A 404 returns nil (the row is gone; expressions over `record` will
// fail the run, visibly).
func (e *Executor) FetchRecord(ctx context.Context, tenant, role, resource, id string) (map[string]any, error) {
	status, body, err := e.Clients(role).Do(ctx, tenant, http.MethodGet, "/api/"+resource+"/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch %s/%s: %w", resource, id, err)
	}
	switch {
	case status == http.StatusNotFound:
		return nil, nil
	case status < 200 || status >= 300:
		return nil, fmt.Errorf("fetch %s/%s: engine answered %d: %s", resource, id, status, truncate(body))
	}
	// GET /api/{resource}/{id} answers the BARE object (the list endpoint is the
	// one with a {data, meta} envelope) — caught live in AUTOMATIZACION-S1: an
	// assumed envelope made `record` silently nil and every condition over it
	// silently false.
	var record map[string]any
	if err := json.Unmarshal(body, &record); err != nil {
		return nil, fmt.Errorf("fetch %s/%s: decode: %w", resource, id, err)
	}
	if len(record) == 0 {
		return nil, fmt.Errorf("fetch %s/%s: engine answered 2xx with an empty object", resource, id)
	}
	return record, nil
}

// Run executes wf for tenant with the given environment, recording the run.
// The returned error is non-nil only for a FAILED run (the event consumer
// propagates it so the outbox row retries; steps must therefore be idempotent —
// the same at-least-once doctrine as every consumer).
func (e *Executor) Run(ctx context.Context, tenant string, wf *Workflow, trigger string, env map[string]any) error {
	runID, err := e.Store.StartRun(ctx, tenant, wf.Name, trigger)
	if err != nil {
		return err
	}
	results, runErr := e.runSteps(ctx, tenant, wf, env)

	status := "ok"
	errMsg := ""
	if runErr != nil {
		status = "failed"
		errMsg = runErr.Error()
	}
	if ferr := e.Store.FinishRun(context.WithoutCancel(ctx), runID, status, errMsg, results); ferr != nil {
		e.Log.Warn().Err(ferr).Int64("run", runID).Msg("workflows: could not record run outcome")
	}
	evt := e.Log.Info()
	if runErr != nil {
		evt = e.Log.Warn().Err(runErr)
	}
	evt.Str("tenant", tenant).Str("workflow", wf.Name).Str("trigger", trigger).
		Str("status", status).Int64("run", runID).Msg("workflows: run finished")
	return runErr
}

func (e *Executor) runSteps(ctx context.Context, tenant string, wf *Workflow, env map[string]any) ([]stepResult, error) {
	timeout := e.StepTimeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	var results []stepResult
	for i := range wf.Steps {
		st := &wf.Steps[i]
		stepCtx, cancel := context.WithTimeout(ctx, timeout)
		res, stop, err := e.runStep(stepCtx, tenant, wf, st, env)
		cancel()
		results = append(results, res)
		if err != nil {
			return results, fmt.Errorf("step %q: %w", st.Name, err)
		}
		if stop {
			return results, nil
		}
	}
	return results, nil
}

// runStep executes one step. stop=true means a condition evaluated false — the
// run ends here with status ok.
func (e *Executor) runStep(ctx context.Context, tenant string, wf *Workflow, st *Step, env map[string]any) (stepResult, bool, error) {
	res := stepResult{Name: st.Name, Status: "ok"}

	evalData := func() (map[string]any, error) {
		out := make(map[string]any, len(st.Data))
		for k, v := range st.Data {
			val, err := v.Eval(env)
			if err != nil {
				return nil, fmt.Errorf("data.%s: %w", k, err)
			}
			out[k] = val
		}
		return out, nil
	}

	switch st.Type {
	case "condition":
		val, err := st.Cond.Eval(env)
		if err != nil {
			res.Status = "failed"
			return res, false, err
		}
		if truthy, ok := val.(bool); !ok {
			res.Status = "failed"
			return res, false, fmt.Errorf("condition %q evaluated to %T (%v), want a boolean", st.Cond.Src, val, val)
		} else if !truthy {
			res.Status = "stopped"
			res.Note = "condition false — run stopped"
			return res, true, nil
		}

	case "update", "create":
		data, err := evalData()
		if err != nil {
			res.Status = "failed"
			return res, false, err
		}
		method, path := http.MethodPost, "/api/"+st.Resource
		if st.Type == "update" {
			idVal, err := st.ID.Eval(env)
			if err != nil {
				res.Status = "failed"
				return res, false, fmt.Errorf("id: %w", err)
			}
			method, path = http.MethodPatch, fmt.Sprintf("/api/%s/%v", st.Resource, idVal)
		}
		status, body, err := e.Clients(wf.Role).Do(ctx, tenant, method, path, data)
		if err != nil {
			res.Status = "failed"
			return res, false, err
		}
		if status < 200 || status >= 300 {
			res.Status = "failed"
			return res, false, fmt.Errorf("%s %s: engine answered %d: %s", method, path, status, truncate(body))
		}
		res.Note = fmt.Sprintf("%s %s → %d", method, path, status)

	case "webhook":
		payload := map[string]any{"workflow": wf.Name, "tenant_id": tenant, "event": env["event"], "record": env["record"]}
		if len(st.Data) > 0 {
			data, err := evalData()
			if err != nil {
				res.Status = "failed"
				return res, false, err
			}
			payload["data"] = data
		}
		if err := e.Dispatcher.DispatchOnce(ctx, st.URL, st.SecretEnv, "workflow."+wf.Name, payload); err != nil {
			res.Status = "failed"
			return res, false, err
		}
		res.Note = "POST " + st.URL

	case "enqueue":
		data, err := evalData()
		if err != nil {
			res.Status = "failed"
			return res, false, err
		}
		if data == nil {
			data = map[string]any{}
		}
		data["workflow"] = wf.Name
		data["tenant_id"] = tenant
		if err := e.enqueue(ctx, tenant, st.Topic, data); err != nil {
			res.Status = "failed"
			return res, false, err
		}
		res.Note = "enqueued " + st.Topic

	default:
		res.Status = "failed"
		return res, false, fmt.Errorf("unsupported step type %q", st.Type)
	}
	return res, false, nil
}

// enqueue writes an outbox event in its own small tx on the store's pool.
func (e *Executor) enqueue(ctx context.Context, tenant, topic string, payload any) error {
	var tx pgx.Tx
	tx, err := e.Store.Pool().Begin(ctx)
	if err != nil {
		return fmt.Errorf("enqueue %s: %w", topic, err)
	}
	defer tx.Rollback(context.Background()) //nolint:errcheck
	if _, err := outbox.Enqueue(ctx, tx, tenant, topic, payload); err != nil {
		return fmt.Errorf("enqueue %s: %w", topic, err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("enqueue %s: %w", topic, err)
	}
	return nil
}

func truncate(b []byte) string {
	const cap = 300
	if len(b) > cap {
		return string(b[:cap]) + "…"
	}
	return string(b)
}
