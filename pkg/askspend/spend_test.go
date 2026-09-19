package askspend

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/appximo/appximo/pkg/observability"
)

type fakeAlerter struct {
	mu   sync.Mutex
	sent []observability.Alert
}

func (f *fakeAlerter) Send(_ context.Context, a observability.Alert) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, a)
	return nil
}

func TestConfigFromEnv_FailFast(t *testing.T) {
	t.Setenv("APPXIMO_ASK_PER_MINUTE", "0")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("per-minute 0 must refuse to boot")
	}
	t.Setenv("APPXIMO_ASK_PER_MINUTE", "6")
	t.Setenv("APPXIMO_ASK_DAILY_USD", "abc")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("a non-number daily cap must refuse to boot")
	}
	t.Setenv("APPXIMO_ASK_DAILY_USD", "-1")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("a negative daily cap must refuse to boot")
	}
	t.Setenv("APPXIMO_ASK_DAILY_USD", "0.25")
	t.Setenv("APPXIMO_ASK_ALERT_PCT", "150")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("an alert percent over 100 must refuse to boot")
	}
	t.Setenv("APPXIMO_ASK_ALERT_PCT", "")
	c, err := ConfigFromEnv()
	if err != nil || c.DailyUSD != 0.25 || c.PerMinute != 6 || c.AlertPct != 80 {
		t.Fatalf("config: %+v %v", c, err)
	}
}

func TestLedger_DailyCapAlertsAndSwitchesTheModelOff(t *testing.T) {
	fa := &fakeAlerter{}
	l := New(Config{PerMinute: 100, DailyUSD: 0.010, AlertPct: 80}, nil, time.UTC, fa, "Tienda")
	ctx := context.Background()
	// 3 model questions at $0.003 → 0.009 (90 %): the warning fires ONCE.
	for i := 0; i < 3; i++ {
		if v := l.Allow(ctx, "t1"); !v.ModelOK {
			t.Fatalf("q%d must be allowed: %+v", i, v)
		}
		l.Record(ctx, "t1", "model", 1, 0.003)
	}
	if len(fa.sent) != 1 || fa.sent[0].Kind != "ask_spend_warning" || fa.sent[0].Fields["pct"] != "90" {
		t.Fatalf("one warning at 90%%: %+v", fa.sent)
	}
	// Still allowed (0.009 < 0.010); the 4th crosses the cap → capped alert.
	if v := l.Allow(ctx, "t1"); !v.ModelOK {
		t.Fatalf("under the cap must still allow: %+v", v)
	}
	d := l.Record(ctx, "t1", "model", 1, 0.003)
	if !d.Capped || len(fa.sent) != 2 || fa.sent[1].Kind != "ask_spend_capped" || fa.sent[1].Level != observability.LevelCritical {
		t.Fatalf("cap alert: %+v %+v", d, fa.sent)
	}
	if v := l.Allow(ctx, "t1"); v.ModelOK || v.Reason != "capped" {
		t.Fatalf("over the cap the model is off: %+v", v)
	}
	// Parser/cache answers keep being recorded and never alert again.
	l.Record(ctx, "t1", "parser", 0, 0)
	l.Record(ctx, "t1", "cache", 0, 0)
	if len(fa.sent) != 2 {
		t.Fatalf("no repeat alerts: %d", len(fa.sent))
	}
	// Another tenant has its own wallet.
	if v := l.Allow(ctx, "t2"); !v.ModelOK {
		t.Fatalf("t2 is not capped: %+v", v)
	}
	rows := l.Today()
	if len(rows) != 2 {
		t.Fatalf("today rows: %+v", rows)
	}
	for _, r := range rows {
		if r.Tenant == "t1" && (r.Questions != 6 || r.ModelCalls != 4 || r.Parser != 1 || r.Cache != 1) {
			t.Fatalf("t1 row: %+v", r)
		}
	}
}

func TestLedger_PerMinuteCapCountsModelCallsOnly(t *testing.T) {
	l := New(Config{PerMinute: 2, DailyUSD: 0, AlertPct: 80}, nil, time.UTC, nil, "")
	base := time.Date(2026, 9, 20, 10, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return base }
	ctx := context.Background()
	l.Record(ctx, "t", "model", 1, 0.003)
	l.Record(ctx, "t", "model", 1, 0.003)
	if v := l.Allow(ctx, "t"); v.ModelOK || v.Reason != "minute" {
		t.Fatalf("third model call in the minute must be refused: %+v", v)
	}
	for i := 0; i < 50; i++ {
		l.Record(ctx, "t", "parser", 0, 0) // free answers do not count
	}
	if v := l.Allow(ctx, "t"); v.Reason != "minute" {
		t.Fatalf("still the minute cap: %+v", v)
	}
	l.now = func() time.Time { return base.Add(61 * time.Second) }
	if v := l.Allow(ctx, "t"); !v.ModelOK {
		t.Fatalf("next minute allows again: %+v", v)
	}
	// Day rollover: a new day starts from zero.
	l.now = func() time.Time { return base.Add(24 * time.Hour) }
	if rows := l.Today(); len(rows) != 0 {
		t.Fatalf("yesterday's rows are not today: %+v", rows)
	}
}
