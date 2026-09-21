package workflows

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/appximo/appximo/pkg/schema"
)

// fakeEngine answers GET /api/eventos?filter[inicio][gt]=..&filter[inicio][lte]=..
// over an in-memory row set, exactly as the engine's list endpoint would
// (RFC 3339 values, {data, meta} envelope, has_next=false — the test set is
// below one page). It is the sweeper's only door to rows.
type fakeEngine struct {
	mu   sync.Mutex
	rows []map[string]any
	hits int
}

func (f *fakeEngine) Do(ctx context.Context, tenant, method, path string, body any) (int, []byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hits++
	if !strings.HasPrefix(path, "/api/eventos?") {
		return 404, []byte(`{"error":"no"}`), nil
	}
	q := path[strings.Index(path, "?")+1:]
	var gt, lte time.Time
	for _, kv := range strings.Split(q, "&") {
		k, v, _ := strings.Cut(kv, "=")
		v = strings.ReplaceAll(v, "%3A", ":")
		v = strings.ReplaceAll(v, "%2B", "+")
		switch {
		case strings.Contains(k, "%5Bgt%5D"):
			gt, _ = time.Parse(time.RFC3339Nano, v)
		case strings.Contains(k, "%5Blte%5D"):
			lte, _ = time.Parse(time.RFC3339Nano, v)
		}
	}
	var out []map[string]any
	for _, r := range f.rows {
		at, _ := time.Parse(time.RFC3339, r["inicio"].(string))
		if at.After(gt) && !at.After(lte) {
			out = append(out, r)
		}
	}
	if out == nil {
		out = []map[string]any{}
	}
	b, _ := json.Marshal(map[string]any{"data": out, "meta": map[string]any{"has_next": false}})
	return http.StatusOK, b, nil
}

func timeTestWorkflow(t *testing.T) *Workflow {
	t.Helper()
	return compileOne(t, timeWF)
}

type fixedTimeLister struct{ entries []TimeEntry }

func (f fixedTimeLister) TimeEntries() []TimeEntry { return f.entries }

func outboxRows(t *testing.T, topic string) []string {
	t.Helper()
	rows, err := testPool.Query(context.Background(), `SELECT payload::text FROM public.outbox WHERE topic=$1 ORDER BY id`, topic)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		_ = rows.Scan(&p)
		out = append(out, p)
	}
	return out
}

// TestReminders_ExactlyOnceFollowsTheRowAndRespectsGrace is the central proof
// of the time trigger (MOTOR-AGENDA-S1, provocations F7/F8/F9):
//
//	a row due now                → ONE message enqueued, ONE ledger claim
//	the same sweep run again     → nothing (a restart inside the window)
//	two sweeps racing            → still one
//	the row moved later          → no firing at the old time; ONE at the new
//	the row cancelled            → never fires
//	a row past grace             → skipped (noise, not a reminder)
//	the enqueue is atomic with the claim (same tx: the outbox row exists iff the claim does)
func TestReminders_ExactlyOnceFollowsTheRowAndRespectsGrace(t *testing.T) {
	pool := requirePG(t)
	ctx := context.Background()
	wf := timeTestWorkflow(t)
	eng := &fakeEngine{}
	store := NewStore(pool)
	exec := &Executor{Clients: func(string) EngineDoer { return eng }, Store: store, Log: zerolog.Nop()}
	now := time.Date(2026, 9, 22, 15, 50, 0, 0, time.UTC) // 15 min before a 16:05 event = due 15:50
	sw := &Sweeper{Src: fixedTimeLister{[]TimeEntry{{Tenant: "t1", Workflow: wf}}}, Exec: exec, Store: store, Now: func() time.Time { return now }}

	row := func(id, at, estado string) map[string]any {
		return map[string]any{"id": id, "titulo": "reunión " + id, "inicio": at, "estado": estado}
	}
	eng.rows = []map[string]any{
		row("a", "2026-09-22T16:05:00Z", "ok"),        // due 15:50 = now → fires
		row("b", "2026-09-22T16:05:00Z", "cancelada"), // cancelled → never
		row("c", "2026-09-22T15:55:00Z", "ok"),        // due 15:40, grace 15m → 10 min late → still fires
		row("d", "2026-09-22T15:45:00Z", "ok"),        // due 15:30, 20 min late > grace → skipped
		row("e", "2026-09-22T17:00:00Z", "ok"),        // due 16:45 → not yet
	}

	sw.Sweep(ctx)
	got := outboxRows(t, "message.telegram")
	if len(got) != 2 {
		t.Fatalf("want 2 messages (a, c), got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "En 15 min: reunión a") && !strings.Contains(got[1], "En 15 min: reunión a") {
		t.Fatalf("message text not rendered from the row: %v", got)
	}
	var claims int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM public.workflow_reminders`).Scan(&claims)
	if claims != 2 {
		t.Fatalf("want 2 claims, got %d", claims)
	}

	// A restart inside the window: the same sweep again → nothing new.
	sw.Sweep(ctx)
	if n := len(outboxRows(t, "message.telegram")); n != 2 {
		t.Fatalf("second sweep must add nothing, got %d", n)
	}
	// Two sweeps racing (two leaders is impossible by the lock, but the claim
	// itself must hold even if it were not): still exactly two.
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); sw.Sweep(ctx) }()
	}
	wg.Wait()
	if n := len(outboxRows(t, "message.telegram")); n != 2 {
		t.Fatalf("racing sweeps must add nothing, got %d", n)
	}

	// The row moves to 17:00 BEFORE its (old) reminder: e is not due yet, so
	// move e earlier to be due now instead — and move a (already fired) to
	// 17:20: a new appointment gets a new reminder at ITS time, not now.
	eng.rows[4]["inicio"] = "2026-09-22T16:05:00Z" // e now due 15:50 → fires once
	eng.rows[0]["inicio"] = "2026-09-22T17:20:00Z" // a moved after firing → due 17:05, not now
	sw.Sweep(ctx)
	got = outboxRows(t, "message.telegram")
	if len(got) != 3 || !strings.Contains(got[2], "reunión e") {
		t.Fatalf("moved rows: want a third message for e only, got %v", got)
	}
	// Advance the clock to 17:05: a fires again for its NEW time (once), and
	// nothing fires twice.
	now = time.Date(2026, 9, 22, 17, 5, 0, 0, time.UTC)
	sw.Sweep(ctx)
	sw.Sweep(ctx)
	got = outboxRows(t, "message.telegram")
	if len(got) != 4 || !strings.Contains(got[3], "reunión a") {
		t.Fatalf("rescheduled row must fire once at its new time, got %v", got)
	}
	// Cancel e's twin scenario: a row that becomes cancelled before its due
	// never fires.
	eng.rows = append(eng.rows, row("f", "2026-09-22T17:20:00Z", "ok"))
	eng.rows[5]["estado"] = "cancelada"
	sw.Sweep(ctx)
	if n := len(outboxRows(t, "message.telegram")); n != 4 {
		t.Fatalf("a cancelled row must not fire, got %d", n)
	}
	// The ledger and the outbox agree: every claim has its message.
	var runsOK int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM public.workflow_runs WHERE status='ok' AND trigger LIKE 'time:%'`).Scan(&runsOK)
	if runsOK != 4 {
		t.Fatalf("want 4 ok reminder runs, got %d", runsOK)
	}
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM public.workflow_reminders`).Scan(&claims)
	if claims != 4 {
		t.Fatalf("want 4 claims, got %d", claims)
	}
}

// TestReminders_FailedRunReleasesTheClaim: a step that fails rolls the claim
// back, so the next sweep retries (at-least-once for a FAILED run), and the
// outbox never carries a row from a failed run.
func TestReminders_FailedRunReleasesTheClaim(t *testing.T) {
	pool := requirePG(t)
	ctx := context.Background()
	wf := timeTestWorkflow(t)
	// Break the step: an expression over a missing field fails at eval time.
	broken := *wf
	broken.Steps = []Step{{Name: "boom", Type: "enqueue", Topic: "message.telegram", Data: map[string]*Value{"text": mustValue(t, "=record.titulo + missing.x")}}}
	eng := &fakeEngine{rows: []map[string]any{{"id": "a", "titulo": "x", "inicio": "2026-09-22T16:05:00Z", "estado": "ok"}}}
	store := NewStore(pool)
	exec := &Executor{Clients: func(string) EngineDoer { return eng }, Store: store, Log: zerolog.Nop()}
	now := time.Date(2026, 9, 22, 15, 50, 0, 0, time.UTC)
	sw := &Sweeper{Src: fixedTimeLister{[]TimeEntry{{Tenant: "t1", Workflow: &broken}}}, Exec: exec, Store: store, Now: func() time.Time { return now }}
	sw.Sweep(ctx)
	var claims, failed int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM public.workflow_reminders`).Scan(&claims)
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM public.workflow_runs WHERE status='failed'`).Scan(&failed)
	if claims != 0 || failed != 1 || len(outboxRows(t, "message.telegram")) != 0 {
		t.Fatalf("failed run: claims=%d failed_runs=%d outbox=%d", claims, failed, len(outboxRows(t, "message.telegram")))
	}
	// Fixed workflow, next sweep: fires once.
	sw.Src = fixedTimeLister{[]TimeEntry{{Tenant: "t1", Workflow: wf}}}
	sw.Sweep(ctx)
	if n := len(outboxRows(t, "message.telegram")); n != 1 {
		t.Fatalf("after the fix the reminder fires once, got %d", n)
	}
}

func mustValue(t *testing.T, raw string) *Value {
	t.Helper()
	v, err := compileValue(raw)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

var _ = fmt.Sprint
var _ = schema.WhenHolds
