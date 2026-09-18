package schema

import "testing"

// The workflows block used to validate clean while executing NOTHING. Now it is
// real surface: these tests pin that DECLARED == EXECUTABLE at load time.

func wfSchema(t *testing.T, workflows string) []ValidationError {
	t.Helper()
	raw := `{
		"$schema": "s", "version": "1", "name": "t",
		"resources": {
			"tasks": {
				"fields": {"title": {"type": "string"}, "status": {"type": "string"}},
				"events": ["create", "update"]
			},
			"silent": {"fields": {"x": {"type": "string"}}}
		},
		"rbac": {"roles": {"admin": {"resources": "*", "actions": ["*"]}}},
		"workflows": ` + workflows + `}`
	s, err := LoadFromBytes([]byte(raw))
	if err != nil {
		return []ValidationError{{Field: "$", Message: err.Error()}}
	}
	return Validate(s)
}

func TestWorkflows_ValidEventAndCronPass(t *testing.T) {
	errs := wfSchema(t, `{
		"al_crear": {
			"trigger": {"type": "event", "event": "create", "resource": "tasks"},
			"steps": [
				{"name": "solo_abiertas", "type": "condition", "config": {"expr": "record.status == 'open'"}},
				{"name": "marcar", "type": "update", "config": {"resource": "tasks", "id": "event.id", "data": {"status": "seen"}}},
				{"name": "avisar", "type": "webhook", "config": {"url": "https://erp.example.com/hook", "hmac_secret_env": "WF_SECRET"}},
				{"name": "emitir", "type": "enqueue", "config": {"topic": "informes.daily", "data": {"cuando": "=now"}}}
			],
			"overlap": "allow",
			"role": "admin"
		},
		"diario": {
			"trigger": {"type": "cron", "cron": "0 8 * * *", "timezone": "America/Bogota"},
			"steps": [{"name": "emitir", "type": "enqueue", "config": {"topic": "resumen.diario"}}]
		}
	}`)
	if len(errs) != 0 {
		t.Fatalf("valid workflows rejected: %v", errs)
	}
}

func TestWorkflows_EventNotEmittedIsLoadError(t *testing.T) {
	// `silent` declares no events: the workflow could never fire — the exact
	// dead-promise shape, now a load error with the fix spelled out.
	errs := wfSchema(t, `{
		"w": {"trigger": {"type": "event", "event": "create", "resource": "silent"},
		      "steps": [{"name": "s", "type": "enqueue", "config": {"topic": "x.y"}}]}}`)
	if !hasErr(errs, "workflows.w.trigger", "does not emit") {
		t.Fatalf("want trigger_event_not_emitted error, got %v", errs)
	}
}

func TestWorkflows_BadCronAndTimezoneRejected(t *testing.T) {
	errs := wfSchema(t, `{
		"w": {"trigger": {"type": "cron", "cron": "99 99 * * *", "timezone": "Marte/Colonia"},
		      "steps": [{"name": "s", "type": "enqueue", "config": {"topic": "x.y"}}]}}`)
	if !hasErr(errs, "trigger.cron", "invalid cron") {
		t.Fatalf("want invalid cron error, got %v", errs)
	}
	if !hasErr(errs, "trigger.timezone", "IANA") {
		t.Fatalf("want invalid timezone error, got %v", errs)
	}
}

func TestWorkflows_BadExprRejected(t *testing.T) {
	errs := wfSchema(t, `{
		"w": {"trigger": {"type": "event", "event": "create", "resource": "tasks"},
		      "steps": [{"name": "s", "type": "condition", "config": {"expr": "record.status =="}}]}}`)
	if !hasErr(errs, "config.expr", "does not compile") {
		t.Fatalf("want expr compile error, got %v", errs)
	}
}

func TestWorkflows_HTTPTriggerNamedUnsupported(t *testing.T) {
	errs := wfSchema(t, `{
		"w": {"trigger": {"type": "http"},
		      "steps": [{"name": "s", "type": "enqueue", "config": {"topic": "x.y"}}]}}`)
	if !hasErr(errs, "trigger.type", "not part of workflows v1") {
		t.Fatalf("want unsupported http trigger error, got %v", errs)
	}
}

func TestWorkflows_EnqueueLoopRejected(t *testing.T) {
	// The workflow enqueues the very topic that triggers it: a declared
	// infinite loop, rejected at load.
	errs := wfSchema(t, `{
		"w": {"trigger": {"type": "event", "event": "create", "resource": "tasks"},
		      "steps": [{"name": "s", "type": "enqueue", "config": {"topic": "tasks.created"}}]}}`)
	if !hasErr(errs, "config.topic", "infinite loop") {
		t.Fatalf("want enqueue_loop error, got %v", errs)
	}
}

func TestWorkflows_StepShapeErrors(t *testing.T) {
	errs := wfSchema(t, `{
		"w": {"trigger": {"type": "event", "event": "create", "resource": "tasks"},
		      "overlap": "maybe",
		      "role": "ghost",
		      "steps": [
		        {"name": "a", "type": "teleport", "config": {}},
		        {"name": "a", "type": "update", "config": {"resource": "nope", "extra": 1}},
		        {"name": "c", "type": "webhook", "config": {"url": "http://insecure.example"}}
		      ]}}`)
	for frag, msg := range map[string]string{
		"steps[0].type":         "invalid step type",
		"steps[1].name":         "duplicate step name",
		"steps[1].config.extra": "unknown config key",
		"config.resource":       "not declared",
		"steps[1].config.id":    `requires an "id"`,
		"steps[2].config.url":   "must be HTTPS",
		".overlap":              "invalid overlap",
		".role":                 "not declared in rbac.roles",
	} {
		if !hasErr(errs, frag, msg) {
			t.Errorf("want error %q at %q, got %v", msg, frag, errs)
		}
	}
}

func TestWorkflows_EmptyStepsRejected(t *testing.T) {
	errs := wfSchema(t, `{
		"w": {"trigger": {"type": "event", "event": "create", "resource": "tasks"}, "steps": []}}`)
	if !hasErr(errs, "workflows.w.steps", "at least one step") {
		t.Fatalf("want empty_steps error, got %v", errs)
	}
}
