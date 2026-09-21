// Package workflows is the executor for the schema's `workflows` block
// (AUTOMATIZACION-S1, ADR-031 — the block was reserved by the internal ADR-012
// until its written trigger fired: three apps re-implementing trigger→condition→
// action by hand, and a client asking three times).
//
// The design, decided by prior research (decision A-70) and not re-litigated:
//
//   - THE SCHEMA IS THE SINGLE SOURCE OF TRUTH. Workflows live in the tenant's
//     deployed schema (public.tenants.json_schema), the same document Studio
//     edits and `validate` guards. There is no second store: every tool that
//     mixed git with a UI store broke (Hasura disables its served console,
//     Windmill's "Git always wins", n8n documents data loss on import) — one
//     side wins, always, so here only one side EXISTS.
//   - EXPRESSIONS ARE expr-lang/expr: sandboxed, typed, non-Turing-complete,
//     guaranteed termination, ~70ns/op. Never JavaScript.
//   - LEADER ELECTION IS pg_try_advisory_lock: the Postgres every deployment
//     already has. No Redis, no etcd, no extra infrastructure.
//   - EXECUTION LIVES IN appximo-worker (the async subsystem), NEVER on the
//     engine's request path. Steps act through the engine HTTP API with a scoped
//     service JWT, so every write inherits validation + RBAC (the worker
//     doctrine since SERVICE-JWT-V1).
//   - OBSERVABLE FROM DAY ONE: every run is a row in public.workflow_runs
//     (status, error, per-step detail, duration); cron schedules persist
//     next_run/last_run in public.workflow_cron; GET /admin/workflows and the
//     appximo_workflow_* gauges read them. The outbox shipped blind and stayed
//     blind for months — this executor does not repeat that.
package workflows

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/robfig/cron/v3"

	"github.com/appximo/appximo/pkg/schema"
)

// Value is one compiled config value: a literal, or (for strings starting with
// "=") a compiled expr-lang expression over the run environment.
type Value struct {
	Literal any
	Prog    *vm.Program
	Src     string // the expression source, for error messages
}

// Eval resolves the value against env.
func (v *Value) Eval(env map[string]any) (any, error) {
	if v.Prog == nil {
		return v.Literal, nil
	}
	out, err := expr.Run(v.Prog, env)
	if err != nil {
		return nil, fmt.Errorf("expression %q: %w", v.Src, err)
	}
	return out, nil
}

// Step is one compiled sequential step.
type Step struct {
	Name string
	Type string // condition | update | create | webhook | enqueue

	Cond *Value // condition: must evaluate truthy to continue

	Resource string            // update / create
	ID       *Value            // update
	Data     map[string]*Value // update / create / webhook / enqueue

	URL       string // webhook
	SecretEnv string // webhook

	Topic string // enqueue
}

// Workflow is one compiled workflow of one schema.
type Workflow struct {
	Name string

	TriggerType string // "event" | "cron" | "time"
	Topic       string // event: the outbox topic consumed
	CronSpec    string // cron: the raw spec (for display)
	Schedule    cron.Schedule
	Location    *time.Location

	// Time trigger (MOTOR-AGENDA-S1): per-row, relative to Resource.Field.
	Resource string
	Field    string
	Offset   time.Duration // the declared before/after distance
	Before   bool          // true = before the moment, false = after
	Grace    time.Duration // how late a firing may still happen
	When     *schema.WhenDef

	OverlapAllow bool   // overlap: "allow" (default false = skip)
	Role         string // "" = the worker's default service role

	Steps []Step
}

// compileValue builds a Value from a raw config value ("=" prefix ⇒ expression).
func compileValue(raw any) (*Value, error) {
	if s, ok := raw.(string); ok && strings.HasPrefix(s, "=") {
		src := strings.TrimPrefix(s, "=")
		prog, err := expr.Compile(src, expr.AllowUndefinedVariables())
		if err != nil {
			return nil, fmt.Errorf("expression %q: %w", src, err)
		}
		return &Value{Prog: prog, Src: src}, nil
	}
	return &Value{Literal: raw}, nil
}

// compileData compiles a step's data map (may be absent → nil).
func compileData(cfg map[string]any) (map[string]*Value, error) {
	raw, ok := cfg["data"]
	if !ok {
		return nil, nil
	}
	m, ok := raw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf(`"data" must be an object`)
	}
	out := make(map[string]*Value, len(m))
	for k, v := range m {
		cv, err := compileValue(v)
		if err != nil {
			return nil, fmt.Errorf("data.%s: %w", k, err)
		}
		out[k] = cv
	}
	return out, nil
}

func cfgString(cfg map[string]any, key string) string {
	if v, ok := cfg[key].(string); ok {
		return v
	}
	return ""
}

// Compile builds the executable workflows of a VALIDATED schema. It assumes
// schema.Validate passed (the deploy gate guarantees it); a residual
// inconsistency is returned as an error, never a panic.
func Compile(s *schema.APISchema) ([]*Workflow, error) {
	names := make([]string, 0, len(s.Workflows))
	for name := range s.Workflows {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []*Workflow
	for _, name := range names {
		ws := s.Workflows[name]
		wf := &Workflow{
			Name:         name,
			TriggerType:  ws.Trigger.Type,
			OverlapAllow: ws.Overlap == "allow",
			Role:         ws.Role,
		}
		switch ws.Trigger.Type {
		case "event":
			wf.Topic = schema.WorkflowEventTopic(ws.Trigger)
			if wf.Topic == "" {
				return nil, fmt.Errorf("workflow %q: unresolvable event trigger (%s/%s)", name, ws.Trigger.Resource, ws.Trigger.Event)
			}
		case "cron":
			sched, err := schema.WorkflowCronParser.Parse(ws.Trigger.Cron)
			if err != nil {
				return nil, fmt.Errorf("workflow %q: cron %q: %w", name, ws.Trigger.Cron, err)
			}
			wf.Schedule = sched
			wf.CronSpec = ws.Trigger.Cron
			wf.Location = time.UTC
			if ws.Trigger.Timezone != "" {
				loc, err := time.LoadLocation(ws.Trigger.Timezone)
				if err != nil {
					return nil, fmt.Errorf("workflow %q: timezone %q: %w", name, ws.Trigger.Timezone, err)
				}
				wf.Location = loc
			}
		case "time":
			wf.Resource, wf.Field, wf.When = ws.Trigger.Resource, ws.Trigger.Field, ws.Trigger.When
			src := ws.Trigger.Before
			wf.Before = src != ""
			if !wf.Before {
				src = ws.Trigger.After
			}
			off, err := schema.ParseWorkflowDuration(src)
			if err != nil {
				return nil, fmt.Errorf("workflow %q: offset %q: %w", name, src, err)
			}
			wf.Offset = off
			// Default grace: a "before" reminder is useful until the moment itself
			// (never fires after it); an "after" follow-up keeps an hour.
			wf.Grace = off
			if !wf.Before {
				wf.Grace = time.Hour
			}
			if ws.Trigger.Grace != "" {
				g, err := schema.ParseWorkflowDuration(ws.Trigger.Grace)
				if err != nil {
					return nil, fmt.Errorf("workflow %q: grace %q: %w", name, ws.Trigger.Grace, err)
				}
				wf.Grace = g
			}
		default:
			return nil, fmt.Errorf("workflow %q: unsupported trigger type %q", name, ws.Trigger.Type)
		}

		for i, st := range ws.Steps {
			cs := Step{Name: st.Name, Type: st.Type}
			var err error
			switch st.Type {
			case "condition":
				cs.Cond, err = compileValue("=" + cfgString(st.Config, "expr"))
			case "update":
				cs.Resource = cfgString(st.Config, "resource")
				idSrc := cfgString(st.Config, "id")
				if !strings.HasPrefix(idSrc, "=") {
					idSrc = "=" + idSrc // the id config is always an expression
				}
				if cs.ID, err = compileValue(idSrc); err == nil {
					cs.Data, err = compileData(st.Config)
				}
			case "create":
				cs.Resource = cfgString(st.Config, "resource")
				cs.Data, err = compileData(st.Config)
			case "webhook":
				cs.URL = cfgString(st.Config, "url")
				cs.SecretEnv = cfgString(st.Config, "hmac_secret_env")
				cs.Data, err = compileData(st.Config)
			case "enqueue":
				cs.Topic = cfgString(st.Config, "topic")
				cs.Data, err = compileData(st.Config)
			default:
				err = fmt.Errorf("unsupported step type %q", st.Type)
			}
			if err != nil {
				return nil, fmt.Errorf("workflow %q step[%d] %q: %w", name, i, st.Name, err)
			}
			wf.Steps = append(wf.Steps, cs)
		}
		out = append(out, wf)
	}
	return out, nil
}

// NextAfter computes a cron workflow's next firing instant strictly after t,
// evaluated in the workflow's location. THE DST POLICY (ADR-031, written down
// because Temporal documents this hazard without ornament and every scheduler
// hits it): the schedule is computed in the DECLARED timezone (default UTC,
// where DST does not exist). On spring-forward, a firing that falls in the
// nonexistent hour resolves to the next valid instant — combined with the
// catch-up rule ("a due schedule runs ONCE, however late") the run happens once,
// late, never lost. On fall-back, next_run is an absolute instant already in the
// past exactly once — the run fires once, and the NEXT occurrence is computed
// strictly after "now", so the repeated wall-clock hour can never fire twice.
func (w *Workflow) NextAfter(t time.Time) time.Time {
	return w.Schedule.Next(t.In(w.Location))
}
