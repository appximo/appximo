package workflows

import (
	"testing"
	"time"

	"github.com/appximo/appximo/pkg/schema"
)

func compileOne(t *testing.T, raw string) *Workflow {
	t.Helper()
	s, err := schema.LoadFromBytes([]byte(raw))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if errs := schema.Validate(s); len(errs) != 0 {
		t.Fatalf("schema invalid: %v", errs)
	}
	wfs, err := Compile(s)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if len(wfs) != 1 {
		t.Fatalf("want 1 workflow, got %d", len(wfs))
	}
	return wfs[0]
}

const eventWF = `{
	"$schema": "s", "version": "1", "name": "t",
	"resources": {"tasks": {"fields": {"status": {"type": "string"}}, "events": ["create"]}},
	"rbac": {"roles": {"admin": {"resources": "*", "actions": ["*"]}}},
	"workflows": {"w": {
		"trigger": {"type": "event", "event": "create", "resource": "tasks"},
		"steps": [
			{"name": "gate", "type": "condition", "config": {"expr": "record.status == 'open'"}},
			{"name": "mark", "type": "update", "config": {"resource": "tasks", "id": "event.id", "data": {"status": "seen", "at": "=string(now)"}}}
		]}}}`

func TestCompile_EventWorkflow(t *testing.T) {
	wf := compileOne(t, eventWF)
	if wf.TriggerType != "event" || wf.Topic != "tasks.created" {
		t.Fatalf("trigger compiled wrong: %+v", wf)
	}
	if len(wf.Steps) != 2 || wf.Steps[0].Cond == nil || wf.Steps[1].ID == nil {
		t.Fatalf("steps compiled wrong: %+v", wf.Steps)
	}
	// Literal vs "=" expression in data.
	env := map[string]any{
		"record": map[string]any{"status": "open"},
		"event":  map[string]any{"id": "abc"},
		"now":    time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC),
	}
	gate, err := wf.Steps[0].Cond.Eval(env)
	if err != nil || gate != true {
		t.Fatalf("condition eval = %v, %v", gate, err)
	}
	id, err := wf.Steps[1].ID.Eval(env)
	if err != nil || id != "abc" {
		t.Fatalf("id eval = %v, %v", id, err)
	}
	lit, err := wf.Steps[1].Data["status"].Eval(env)
	if err != nil || lit != "seen" {
		t.Fatalf("literal eval = %v, %v", lit, err)
	}
	dyn, err := wf.Steps[1].Data["at"].Eval(env)
	if err != nil || dyn == "" || dyn == "=string(now)" {
		t.Fatalf("expression value must evaluate, got %v, %v", dyn, err)
	}
}

const cronWF = `{
	"$schema": "s", "version": "1", "name": "t",
	"resources": {"tasks": {"fields": {"status": {"type": "string"}}}},
	"rbac": {"roles": {"admin": {"resources": "*", "actions": ["*"]}}},
	"workflows": {"nightly": {
		"trigger": {"type": "cron", "cron": "30 2 * * *", "timezone": "America/New_York"},
		"steps": [{"name": "emit", "type": "enqueue", "config": {"topic": "reports.nightly"}}]}}}`

// TestNextAfter_DST pins the written DST policy (ADR-031): the 02:30 schedule in
// America/New_York on 2026-03-08 (spring forward — 02:30 does not exist) resolves
// to a valid LATER instant and never goes backwards; on 2026-11-01 (fall back —
// 01:30 exists twice) each NextAfter is strictly increasing, so the repeated
// wall-clock hour cannot fire twice.
func TestNextAfter_DST(t *testing.T) {
	wf := compileOne(t, cronWF)
	ny, _ := time.LoadLocation("America/New_York")

	// Spring forward: from 01:00 on 2026-03-08, the 02:30 occurrence does not
	// exist; the next firing must be a real instant strictly after.
	from := time.Date(2026, 3, 8, 1, 0, 0, 0, ny)
	next := wf.NextAfter(from)
	if !next.After(from) {
		t.Fatalf("spring forward: next %v not after %v", next, from)
	}
	// And the one after that must also be strictly later (no stuck schedule).
	next2 := wf.NextAfter(next)
	if !next2.After(next) {
		t.Fatalf("spring forward: schedule stuck at %v", next)
	}

	// Fall back (2026-11-01): strictly increasing instants — advancing from each
	// firing can never revisit the repeated hour.
	from = time.Date(2026, 10, 31, 12, 0, 0, 0, ny)
	seen := map[int64]bool{}
	cur := from
	for i := 0; i < 4; i++ {
		cur = wf.NextAfter(cur)
		u := cur.Unix()
		if seen[u] {
			t.Fatalf("fall back: instant %v fired twice", cur)
		}
		seen[u] = true
	}
}

func TestCompile_OverlapAndRole(t *testing.T) {
	raw := `{
		"$schema": "s", "version": "1", "name": "t",
		"resources": {"tasks": {"fields": {"status": {"type": "string"}}, "events": ["create"]}},
		"rbac": {"roles": {"bot": {"resources": ["tasks"], "actions": ["read", "update"]}}},
		"workflows": {"w": {
			"trigger": {"type": "event", "event": "create", "resource": "tasks"},
			"overlap": "allow", "role": "bot",
			"steps": [{"name": "s", "type": "create", "config": {"resource": "tasks", "data": {"status": "x"}}}]}}}`
	wf := compileOne(t, raw)
	if !wf.OverlapAllow || wf.Role != "bot" {
		t.Fatalf("overlap/role compiled wrong: %+v", wf)
	}
}
