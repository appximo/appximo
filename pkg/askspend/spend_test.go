package askspend

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/appximo/appximo/pkg/observability"
	"github.com/appximo/appximo/pkg/summary"
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
		if v := l.Allow(ctx, "t1", ""); !v.ModelOK {
			t.Fatalf("q%d must be allowed: %+v", i, v)
		}
		l.Record(ctx, "t1", "", "model", 1, 0.003)
	}
	if len(fa.sent) != 1 || fa.sent[0].Kind != "ask_spend_warning" || fa.sent[0].Fields["pct"] != "90" {
		t.Fatalf("one warning at 90%%: %+v", fa.sent)
	}
	// Still allowed (0.009 < 0.010); the 4th crosses the cap → capped alert.
	if v := l.Allow(ctx, "t1", ""); !v.ModelOK {
		t.Fatalf("under the cap must still allow: %+v", v)
	}
	d := l.Record(ctx, "t1", "", "model", 1, 0.003)
	if !d.Capped || len(fa.sent) != 2 || fa.sent[1].Kind != "ask_spend_capped" || fa.sent[1].Level != observability.LevelCritical {
		t.Fatalf("cap alert: %+v %+v", d, fa.sent)
	}
	if v := l.Allow(ctx, "t1", ""); v.ModelOK || v.Reason != "capped" {
		t.Fatalf("over the cap the model is off: %+v", v)
	}
	// Parser/cache answers keep being recorded and never alert again.
	l.Record(ctx, "t1", "", "parser", 0, 0)
	l.Record(ctx, "t1", "", "cache", 0, 0)
	if len(fa.sent) != 2 {
		t.Fatalf("no repeat alerts: %d", len(fa.sent))
	}
	// Another tenant has its own wallet.
	if v := l.Allow(ctx, "t2", ""); !v.ModelOK {
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
	l.Record(ctx, "t", "", "model", 1, 0.003)
	l.Record(ctx, "t", "", "model", 1, 0.003)
	if v := l.Allow(ctx, "t", ""); v.ModelOK || v.Reason != "minute" {
		t.Fatalf("third model call in the minute must be refused: %+v", v)
	}
	for i := 0; i < 50; i++ {
		l.Record(ctx, "t", "", "parser", 0, 0) // free answers do not count
	}
	if v := l.Allow(ctx, "t", ""); v.Reason != "minute" {
		t.Fatalf("still the minute cap: %+v", v)
	}
	l.now = func() time.Time { return base.Add(61 * time.Second) }
	if v := l.Allow(ctx, "t", ""); !v.ModelOK {
		t.Fatalf("next minute allows again: %+v", v)
	}
	// Day rollover: a new day starts from zero.
	l.now = func() time.Time { return base.Add(24 * time.Hour) }
	if rows := l.Today(); len(rows) != 0 {
		t.Fatalf("yesterday's rows are not today: %+v", rows)
	}
}

func TestLedger_PerUserCapComposesWithTheTenantCap(t *testing.T) {
	fa := &fakeAlerter{}
	l := New(Config{PerMinute: 100, DailyUSD: 1, AlertPct: 80, UserDailyUSD: 0.005}, nil, time.UTC, fa, "")
	ctx := context.Background()
	// user A spends its cap; user B is untouched; the tenant is far from its own.
	for i := 0; i < 2; i++ {
		if v := l.Allow(ctx, "t", "A"); !v.ModelOK {
			t.Fatalf("A q%d allowed: %+v", i, v)
		}
		l.Record(ctx, "t", "A", "model", 1, 0.003)
	}
	if v := l.Allow(ctx, "t", "A"); v.ModelOK || v.Reason != "user_capped" {
		t.Fatalf("A must be user-capped: %+v", v)
	}
	if v := l.Allow(ctx, "t", "B"); !v.ModelOK {
		t.Fatalf("B goes on: %+v", v)
	}
	if v := l.Allow(ctx, "t", ""); !v.ModelOK {
		t.Fatalf("an identity-less caller meets only the tenant cap: %+v", v)
	}
	if len(fa.sent) != 1 || fa.sent[0].Kind != "ask_user_capped" || fa.sent[0].Fields["user"] != "A" {
		t.Fatalf("one admin alert for the capped user: %+v", fa.sent)
	}
	// The tenant cap still wins when reached first.
	l2 := New(Config{PerMinute: 100, DailyUSD: 0.004, AlertPct: 0, UserDailyUSD: 1}, nil, time.UTC, nil, "")
	l2.Record(ctx, "t", "A", "model", 1, 0.003)
	l2.Record(ctx, "t", "B", "model", 1, 0.003)
	if v := l2.Allow(ctx, "t", "C"); v.ModelOK || v.Reason != "capped" {
		t.Fatalf("tenant cap first: %+v", v)
	}
}

func TestConfigFromEnv_TraceHistoryUser(t *testing.T) {
	t.Setenv("APPXIMO_ASK_TRACE", "maybe")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("trace must be on/off")
	}
	t.Setenv("APPXIMO_ASK_TRACE", "on")
	t.Setenv("APPXIMO_ASK_HISTORY_TEXT", "everything")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("history text must be redacted/full/none")
	}
	t.Setenv("APPXIMO_ASK_HISTORY_TEXT", "none")
	t.Setenv("APPXIMO_ASK_HISTORY_DAYS", "7")
	t.Setenv("APPXIMO_ASK_DAILY_USD_PER_USER", "0.1")
	c, err := ConfigFromEnv()
	if err != nil || !c.Trace || c.HistoryText != "none" || c.HistoryDays != 7 || c.UserDailyUSD != 0.1 {
		t.Fatalf("%+v %v", c, err)
	}
	t.Setenv("APPXIMO_ASK_HISTORY_DAYS", "-1")
	if _, err := ConfigFromEnv(); err == nil {
		t.Fatal("negative retention must refuse to boot")
	}
}

func TestDigest_ComposeAndReport(t *testing.T) {
	l := New(Config{PerMinute: 6, DailyUSD: 0.5, AlertPct: 80, HistoryDays: 30, HistoryText: "redacted"}, nil, time.UTC, nil, "Tienda")
	ctx := context.Background()
	l.Record(ctx, "t", "u", "parser", 0, 0)
	l.Record(ctx, "t", "u", "cache", 0, 0)
	l.Record(ctx, "t", "u", "model", 1, 0.003)
	d := BuildDigest(ctx, l, "t", "Tienda")
	if d.Today.Questions != 3 || d.Today.USD != 0.003 || d.Remaining != 0.497 {
		t.Fatalf("digest today: %+v", d.Today)
	}
	for _, want := range []string{"Gasto del modelo · Tienda", "US$ 0,003 hoy", "Techo diario US$ 0,500", "parser 1 · caché 1 · modelo 1 (1 llamadas)"} {
		if !strings.Contains(d.Text, want) {
			t.Errorf("text lacks %q:\n%s", want, d.Text)
		}
	}
	rep := d.Report()
	if !rep.Census || rep.Subtitle == "" || len(rep.Facts) < 5 || rep.Level != "green" {
		t.Fatalf("report: %+v", rep)
	}
	if _, err := summary.Render(rep); err != nil {
		t.Fatalf("the census card must render the digest: %v", err)
	}
}
