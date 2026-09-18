package schema

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"github.com/robfig/cron/v3"
)

// Workflow semantic validation (AUTOMATIZACION-S1, ADR-031). Until the executor
// existed, `workflows` was strict-keyed but semantically unchecked — a schema
// could declare a trigger on an event the resource never emits, a cron spec that
// never parses, an expression that never compiles, and `validate` said clean.
// Now the block is real surface, so it gets the same load-time discipline as
// everything else: DECLARED == EXECUTABLE, or the schema is rejected with the
// exact path and the fix.

// workflowStepTypes is the closed set of step types the executor runs, mapped to
// each type's allowed config keys.
var workflowStepTypes = map[string][]string{
	"condition": {"expr"},
	"update":    {"resource", "id", "data"},
	"create":    {"resource", "data"},
	"webhook":   {"url", "hmac_secret_env", "data"},
	"enqueue":   {"topic", "data"},
}

// WorkflowEventTopic returns the outbox topic an event trigger consumes
// ("orders.created" for {resource: orders, event: create}) — EmitTopic, the same
// single source the engine's CRUD emission uses, so the validator, the executor
// and the producer can never disagree on which topic a workflow listens to.
func WorkflowEventTopic(t WorkflowTrigger) string {
	return EmitTopic(t.Resource, t.Event)
}

// WorkflowCronParser is the ONE cron grammar the validator and the scheduler
// share: standard 5-field (minute hour dom month dow) plus the @daily/@hourly/
// @every descriptors. No seconds field — a multi-tenant scheduler has no
// business firing sub-minute.
var WorkflowCronParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

// CompileWorkflowExpr compiles one expr-lang expression the way the executor
// evaluates it (undefined variables allowed at compile — the run environment is
// dynamic; a genuinely missing variable fails the RUN, which is recorded).
// Shared by the validator and pkg/workflows so "validates" implies "compiles".
func CompileWorkflowExpr(src string) error {
	_, err := expr.Compile(src, expr.AllowUndefinedVariables())
	return err
}

// validateWorkflows checks every declared workflow: trigger coherence (an event
// trigger must name a resource that EMITS that event — otherwise the workflow
// could never fire, the exact dead-promise shape this block used to be), cron +
// timezone parseability, step shapes against the closed type set, and every
// expression compiled. Enqueue topics may not collide with a topic that triggers
// a workflow (a declared infinite loop is a load error, not a surprise at 3 a.m.).
func validateWorkflows(s *APISchema) []ValidationError {
	var errs []ValidationError
	add := func(field, msg string, rule string, extra ...func(*ValidationError)) {
		e := ValidationError{Field: field, Message: msg, Rule: rule}
		for _, f := range extra {
			f(&e)
		}
		errs = append(errs, e)
	}

	// Topics consumed by event triggers — for the enqueue-loop check.
	triggerTopics := map[string]string{} // topic → workflow name
	for name, wf := range s.Workflows {
		if wf.Trigger.Type == "event" {
			if _, ok := emitTopicSuffix[wf.Trigger.Event]; ok && wf.Trigger.Resource != "" {
				triggerTopics[WorkflowEventTopic(wf.Trigger)] = name
			}
		}
	}

	names := make([]string, 0, len(s.Workflows))
	for name := range s.Workflows {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		wf := s.Workflows[name]
		p := "workflows." + name

		if !resourceNameRe.MatchString(name) {
			add(p, fmt.Sprintf("invalid workflow name %q: must match ^[a-z][a-z0-9_]*$", name), "invalid_name")
		}

		// ── trigger ──
		switch wf.Trigger.Type {
		case "event":
			suffix, ok := emitTopicSuffix[wf.Trigger.Event]
			if !ok {
				add(p+".trigger.event",
					fmt.Sprintf("invalid trigger event %q: must be one of create, update, delete (the events vocabulary)", wf.Trigger.Event),
					"invalid_trigger_event",
					func(e *ValidationError) {
						e.Expected = []string{"create", "update", "delete"}
						e.Got = wf.Trigger.Event
					})
			}
			res, resOK := s.Resources[wf.Trigger.Resource]
			if !resOK {
				add(p+".trigger.resource",
					fmt.Sprintf("trigger resource %q is not a declared resource", wf.Trigger.Resource),
					"unknown_resource", func(e *ValidationError) { e.Got = wf.Trigger.Resource })
			} else if ok {
				emits := false
				for _, ev := range res.Events {
					if ev == wf.Trigger.Event {
						emits = true
					}
				}
				if !emits {
					add(p+".trigger",
						fmt.Sprintf("resource %q does not emit %q events, so this workflow could never fire — add %q to resources.%s.events (the workflow consumes the outbox topic %q)",
							wf.Trigger.Resource, wf.Trigger.Event, wf.Trigger.Event, wf.Trigger.Resource, wf.Trigger.Resource+"."+suffix),
						"trigger_event_not_emitted",
						func(e *ValidationError) {
							e.Fix = fmt.Sprintf("add \"events\": [%q] to resources.%s", wf.Trigger.Event, wf.Trigger.Resource)
						})
				}
			}
			if wf.Trigger.Cron != "" || wf.Trigger.Timezone != "" {
				add(p+".trigger", `an "event" trigger takes no cron/timezone`, "trigger_key_conflict")
			}
		case "cron":
			if wf.Trigger.Cron == "" {
				add(p+".trigger.cron", `a "cron" trigger requires a cron spec (5-field, e.g. "0 8 * * *", or @daily / @every 1h)`, "missing_cron")
			} else if _, err := WorkflowCronParser.Parse(wf.Trigger.Cron); err != nil {
				add(p+".trigger.cron",
					fmt.Sprintf("invalid cron spec %q: %v (5-field minute hour dom month dow, or @daily / @every 1h)", wf.Trigger.Cron, err),
					"invalid_cron", func(e *ValidationError) { e.Got = wf.Trigger.Cron })
			}
			if wf.Trigger.Timezone != "" {
				if _, err := time.LoadLocation(wf.Trigger.Timezone); err != nil {
					add(p+".trigger.timezone",
						fmt.Sprintf("invalid timezone %q: must be an IANA name (e.g. America/Bogota); default is UTC", wf.Trigger.Timezone),
						"invalid_timezone", func(e *ValidationError) { e.Got = wf.Trigger.Timezone })
				}
			}
			if wf.Trigger.Event != "" || wf.Trigger.Resource != "" {
				add(p+".trigger", `a "cron" trigger takes no event/resource`, "trigger_key_conflict")
			}
		case "http":
			add(p+".trigger.type",
				`trigger type "http" is not part of workflows v1 (an HTTP-triggered pipeline is a custom route whose handler enqueues an outbox event; an "event" workflow consumes it) — supported: event, cron`,
				"unsupported_trigger", func(e *ValidationError) { e.Expected = []string{"event", "cron"}; e.Got = "http" })
		default:
			add(p+".trigger.type",
				fmt.Sprintf("invalid trigger type %q: must be \"event\" or \"cron\"", wf.Trigger.Type),
				"invalid_trigger_type", func(e *ValidationError) { e.Expected = []string{"event", "cron"}; e.Got = wf.Trigger.Type })
		}

		// ── overlap / role ──
		if wf.Overlap != "" && wf.Overlap != "skip" && wf.Overlap != "allow" {
			add(p+".overlap",
				fmt.Sprintf("invalid overlap %q: \"skip\" (default — a run that would overlap the previous one is recorded as skipped) or \"allow\"", wf.Overlap),
				"invalid_overlap", func(e *ValidationError) { e.Expected = []string{"skip", "allow"}; e.Got = wf.Overlap })
		}
		if wf.Role != "" {
			if _, ok := s.RBAC.Roles[wf.Role]; !ok {
				declared := make([]string, 0, len(s.RBAC.Roles))
				for r := range s.RBAC.Roles {
					declared = append(declared, r)
				}
				sort.Strings(declared)
				add(p+".role",
					fmt.Sprintf("role %q is not declared in rbac.roles (declared: %s) — the steps act through the engine API as this role", wf.Role, strings.Join(declared, ", ")),
					"unknown_role", func(e *ValidationError) { e.Got = wf.Role; e.Expected = declared })
			}
		}

		// ── steps ──
		if len(wf.Steps) == 0 {
			add(p+".steps", "a workflow needs at least one step (a trigger with no steps is a promise that does nothing)", "empty_steps")
		}
		seen := map[string]bool{}
		for i, st := range wf.Steps {
			sp := fmt.Sprintf("%s.steps[%d]", p, i)
			if st.Name == "" {
				add(sp+".name", "step name is required", "missing_step_name")
			} else if seen[st.Name] {
				add(sp+".name", fmt.Sprintf("duplicate step name %q", st.Name), "duplicate_step_name")
			}
			seen[st.Name] = true

			allowed, ok := workflowStepTypes[st.Type]
			if !ok {
				types := make([]string, 0, len(workflowStepTypes))
				for t := range workflowStepTypes {
					types = append(types, t)
				}
				sort.Strings(types)
				add(sp+".type",
					fmt.Sprintf("invalid step type %q: must be one of %s", st.Type, strings.Join(types, ", ")),
					"invalid_step_type", func(e *ValidationError) { e.Expected = types; e.Got = st.Type })
				continue
			}
			// Strict config keys, like every other level of the schema.
			for k := range st.Config {
				valid := false
				for _, a := range allowed {
					if k == a {
						valid = true
					}
				}
				if !valid {
					add(sp+".config."+k,
						fmt.Sprintf("unknown config key %q for a %q step (valid: %s)", k, st.Type, strings.Join(allowed, ", ")),
						"unknown_key", func(e *ValidationError) { e.Expected = allowed; e.Got = k })
				}
			}
			cfgStr := func(key string) (string, bool) {
				v, ok := st.Config[key]
				if !ok {
					return "", false
				}
				sv, isStr := v.(string)
				return sv, isStr
			}
			compileExpr := func(field, src string) {
				if err := CompileWorkflowExpr(src); err != nil {
					add(field, fmt.Sprintf("expression does not compile: %v", err), "invalid_expr",
						func(e *ValidationError) { e.Got = src })
				}
			}
			// Values in data maps: "=" prefix means an expression — compile it.
			checkData := func(field string) {
				raw, ok := st.Config["data"]
				if !ok {
					return
				}
				m, isMap := raw.(map[string]any)
				if !isMap {
					add(field, `"data" must be an object`, "invalid_type")
					return
				}
				for k, v := range m {
					if sv, isStr := v.(string); isStr && strings.HasPrefix(sv, "=") {
						compileExpr(field+"."+k, strings.TrimPrefix(sv, "="))
					}
				}
			}

			switch st.Type {
			case "condition":
				src, isStr := cfgStr("expr")
				if !isStr || src == "" {
					add(sp+".config.expr", `a "condition" step requires an "expr" string (expr-lang; false stops the run)`, "missing_expr")
				} else {
					compileExpr(sp+".config.expr", src)
				}
			case "update", "create":
				resName, _ := cfgStr("resource")
				if resName == "" {
					add(sp+".config.resource", fmt.Sprintf(`a %q step requires a "resource"`, st.Type), "missing_resource")
				} else if _, ok := s.Resources[resName]; !ok {
					add(sp+".config.resource", fmt.Sprintf("resource %q is not declared", resName), "unknown_resource",
						func(e *ValidationError) { e.Got = resName })
				}
				if st.Type == "update" {
					idExpr, isStr := cfgStr("id")
					if !isStr || idExpr == "" {
						add(sp+".config.id", `an "update" step requires an "id" expression (e.g. "event.id")`, "missing_id")
					} else {
						compileExpr(sp+".config.id", strings.TrimPrefix(idExpr, "="))
					}
				}
				if _, ok := st.Config["data"]; !ok {
					add(sp+".config.data", fmt.Sprintf(`a %q step requires a "data" object`, st.Type), "missing_data")
				}
				checkData(sp + ".config.data")
			case "webhook":
				url, _ := cfgStr("url")
				if url == "" {
					add(sp+".config.url", `a "webhook" step requires a "url"`, "missing_url")
				} else if !strings.HasPrefix(url, "https://") {
					add(sp+".config.url",
						fmt.Sprintf("webhook url %q must be HTTPS (the dispatcher is HTTPS-only and SSRF-guarded, same as hooks)", url),
						"insecure_url", func(e *ValidationError) { e.Got = url })
				}
				checkData(sp + ".config.data")
			case "enqueue":
				topic, _ := cfgStr("topic")
				if topic == "" {
					add(sp+".config.topic", `an "enqueue" step requires a "topic"`, "missing_topic")
				} else if other, clash := triggerTopics[topic]; clash {
					add(sp+".config.topic",
						fmt.Sprintf("enqueue topic %q is consumed by workflow %q's event trigger — a workflow that enqueues its own trigger is a declared infinite loop", topic, other),
						"enqueue_loop", func(e *ValidationError) { e.Got = topic })
				}
				checkData(sp + ".config.data")
			}
		}
	}
	return errs
}
