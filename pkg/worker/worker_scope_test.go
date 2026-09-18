package worker

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

// ── TopicSet: pure unit tests (run in -short) ──

func TestTopicSet_Matches(t *testing.T) {
	set := TopicSet{
		Exact:    []string{"email.send"},
		Prefixes: []string{"echo."},
		Suffixes: []string{".created"},
	}
	for topic, want := range map[string]bool{
		"email.send":     true,
		"email.send2":    false,
		"echo.test":      true,
		"orders.created": true,
		"orders.updated": false,
		"factura.emitir": false,
		"created":        false, // suffix requires the dot
		"tasks.created":  true,
		"echo":           false,
	} {
		if got := set.Matches(topic); got != want {
			t.Errorf("Matches(%q) = %v, want %v", topic, got, want)
		}
	}
	if !(TopicSet{}).Empty() {
		t.Error("empty set must report Empty")
	}
}

func TestTopicSet_LikePatternsEscapeMetachars(t *testing.T) {
	// A literal "_" or "%" in a declared prefix must not widen the SQL claim.
	set := TopicSet{Prefixes: []string{"a_b."}, Suffixes: []string{".100%"}}
	pats := set.likePatterns()
	want := []string{`a\_b.%`, `%.100\%`}
	if len(pats) != 2 || pats[0] != want[0] || pats[1] != want[1] {
		t.Fatalf("likePatterns = %v, want %v", pats, want)
	}
}

// ── DB-backed tests (full lane) ──

type scopedProc struct {
	set  TopicSet
	fn   ProcessorFunc
	seen []string
}

func (p *scopedProc) Topics() TopicSet { return p.set }
func (p *scopedProc) Process(ctx context.Context, row Row) error {
	p.seen = append(p.seen, row.Topic)
	if p.fn != nil {
		return p.fn(ctx, row)
	}
	return nil
}

// TestDrain_TopicScopedClaim is THE AUTO-1 death certificate at the SQL layer:
// a worker whose Processor owns only its topics never claims — never marks sent,
// never burns attempts on — a foreign topic. The foreign row stays byte-intact
// 'pending', exactly like the 34 factura.emitir this trap nearly destroyed.
func TestDrain_TopicScopedClaim(t *testing.T) {
	pool := requirePG(t)
	ctx := context.Background()

	mine := enqueue(t, pool, "acme", "echo.test", map[string]any{"n": 1})
	foreign := enqueue(t, pool, "acme", "factura.emitir", map[string]any{"factura": "F-001"})
	suffixed := enqueue(t, pool, "acme", "orders.created", map[string]any{"id": "x"})

	proc := &scopedProc{set: TopicSet{Prefixes: []string{"echo."}, Suffixes: []string{".created"}}}
	res, err := Drain(ctx, pool, proc, 50, 5, nil)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if res.Processed != 2 || res.Failed != 0 {
		t.Fatalf("drain = %+v, want 2 processed (echo.test + orders.created)", res)
	}

	if st, sent, att := rowState(t, pool, mine); st != "sent" || sent == nil {
		t.Fatalf("owned row: state=%s sent=%v attempts=%d", st, sent, att)
	}
	if st, sent, att := rowState(t, pool, suffixed); st != "sent" || sent == nil {
		t.Fatalf("suffix-owned row: state=%s sent=%v attempts=%d", st, sent, att)
	}
	// The foreign row is UNTOUCHED: pending, zero attempts, never seen.
	if st, sent, att := rowState(t, pool, foreign); st != "pending" || sent != nil || att != 0 {
		t.Fatalf("foreign row must stay pristine pending, got state=%s sent=%v attempts=%d", st, sent, att)
	}
	for _, topic := range proc.seen {
		if topic == "factura.emitir" {
			t.Fatal("the processor must never even SEE the foreign topic")
		}
	}
}

// TestDrain_EmptyTopicSetClaimsNothing: a consumer that owns no topics (an auto
// worker on a database with no workflows) claims zero rows.
func TestDrain_EmptyTopicSetClaimsNothing(t *testing.T) {
	pool := requirePG(t)
	id := enqueue(t, pool, "acme", "anything.at.all", map[string]any{})
	proc := &scopedProc{} // empty set
	res, err := Drain(context.Background(), pool, proc, 50, 5, nil)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	if res.Claimed() != 0 {
		t.Fatalf("empty owner claimed %d rows", res.Claimed())
	}
	if st, _, att := rowState(t, pool, id); st != "pending" || att != 0 {
		t.Fatalf("row must stay pending untouched, got %s/%d", st, att)
	}
}

// TestDrain_RecordsLastError pins AUTO-3's second half: a failing row carries
// WHY in last_error (and a later success clears it).
func TestDrain_RecordsLastError(t *testing.T) {
	pool := requirePG(t)
	ctx := context.Background()
	id := enqueue(t, pool, "acme", "jobs.created", map[string]any{})

	failing := &scopedProc{
		set: TopicSet{Exact: []string{"jobs.created"}},
		fn: func(context.Context, Row) error {
			return errors.New("DIAN adapter unreachable: connection refused")
		},
	}
	// Exhaust: maxAttempts=2 → second drain parks it failed.
	for i := 0; i < 2; i++ {
		if _, err := Drain(ctx, pool, failing, 10, 2, nil); err != nil {
			t.Fatalf("drain %d: %v", i, err)
		}
	}
	var state string
	var lastErr *string
	if err := pool.QueryRow(ctx, `SELECT state, last_error FROM public.outbox WHERE id=$1`, id).Scan(&state, &lastErr); err != nil {
		t.Fatalf("read: %v", err)
	}
	if state != "failed" || lastErr == nil || !strings.Contains(*lastErr, "DIAN adapter unreachable") {
		t.Fatalf("failed row must carry its error: state=%s last_error=%v", state, lastErr)
	}

	// Un-park it by hand (the documented recovery: reset state) and succeed —
	// last_error clears with the delivery.
	if _, err := pool.Exec(ctx, `UPDATE public.outbox SET state='pending', attempts=0 WHERE id=$1`, id); err != nil {
		t.Fatalf("reset: %v", err)
	}
	ok := &scopedProc{set: TopicSet{Exact: []string{"jobs.created"}}}
	if _, err := Drain(ctx, pool, ok, 10, 2, nil); err != nil {
		t.Fatalf("drain ok: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT state, last_error FROM public.outbox WHERE id=$1`, id).Scan(&state, &lastErr); err != nil {
		t.Fatalf("read: %v", err)
	}
	if state != "sent" || lastErr != nil {
		t.Fatalf("delivered row must clear last_error: state=%s last_error=%v", state, lastErr)
	}
}

// TestWorker_ForeignPendingWarning: the worker names what it deliberately leaves
// behind (once a minute) — the loud half of not claiming foreign topics.
func TestWorker_ForeignPendingWarning(t *testing.T) {
	pool := requirePG(t)
	ctx := context.Background()
	enqueue(t, pool, "acme", "factura.emitir", map[string]any{"f": 1})

	var buf syncBuffer
	log := zerolog.New(&buf)
	wk := &Worker{log: log} // in-package: exercise reportForeign directly (the loop needs a live LISTEN conn)
	wk.reportForeign(ctx, pool, TopicSet{Prefixes: []string{"echo."}})

	out := buf.String()
	if !strings.Contains(out, "factura.emitir(1)") || !strings.Contains(out, "NO consumer") {
		t.Fatalf("foreign warning must name the topic and the count, got: %s", out)
	}
	// Rate-limited: an immediate second call logs nothing new.
	before := len(buf.String())
	wk.reportForeign(ctx, pool, TopicSet{Prefixes: []string{"echo."}})
	if len(buf.String()) != before {
		t.Fatal("foreign warning must be rate-limited to once a minute")
	}
}

// syncBuffer is a minimal threadsafe buffer for zerolog output in tests.
type syncBuffer struct {
	b strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) { return s.b.Write(p) }
func (s *syncBuffer) String() string              { return s.b.String() }
