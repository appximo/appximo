package observability

import (
	"context"
	"testing"
	"time"
)

type recAlerter struct{ sent []Alert }

func (r *recAlerter) Send(_ context.Context, a Alert) error { r.sent = append(r.sent, a); return nil }

// The noise brake: a new group per minute alerts; a storm of new groups is one
// summary, not one alert per group.
func TestNewErrorNotifierBrake(t *testing.T) {
	rec := &recAlerter{}
	now := time.Unix(1_000_000, 0)
	n := NewNewErrorNotifier(rec, 3, 5*time.Minute)
	n.nowFn = func() time.Time { return now }
	for i := 0; i < 100; i++ {
		n.NewGroup(context.Background(), Alert{TenantID: "acme", Route: "/api/x", Message: "boom"})
	}
	if len(rec.sent) != 4 { // 3 individual + 1 storm summary
		t.Fatalf("storm of 100 new groups produced %d alerts, want 4", len(rec.sent))
	}
	if rec.sent[3].Kind != KindStorm || rec.sent[3].Count < 4 {
		t.Fatalf("4th alert must be the storm summary: %+v", rec.sent[3])
	}
	// The next minute opens the window again.
	now = now.Add(61 * time.Second)
	if !n.NewGroup(context.Background(), Alert{TenantID: "acme", Route: "/api/y", Message: "new"}) {
		t.Fatal("a new group in a fresh minute must alert individually")
	}
	// Another tenant is independent.
	if !n.NewGroup(context.Background(), Alert{TenantID: "beta", Route: "/api/z", Message: "m"}) {
		t.Fatal("tenants must not share the brake")
	}
}
