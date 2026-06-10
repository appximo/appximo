// Package events implements the in-process pub/sub hub behind the per-resource
// SSE subscription endpoints (GET /api/{resource}/events, S45).
//
// ADR-015: single-node by design. The engine runs as one process today, so the
// hub is an in-memory map with no external broker; when HA/multi-node ships, a
// broker (or Postgres LISTEN/NOTIFY) replaces the fan-out — the Publish call
// sites stay the same. Delivery is at-most-once with no replay/Last-Event-ID:
// a subscriber that connects after an event, or that is closed as a slow
// consumer, misses it (documented limitation; replay is future work).
package events

import (
	"errors"
	"sync"
)

// subscriberBuffer is each subscriber's channel capacity. A subscriber that
// falls more than subscriberBuffer events behind is closed (slow-client
// policy): closing is simpler and more honest than silently dropping events —
// the client reconnects and resyncs instead of trusting a gapped stream.
const subscriberBuffer = 64

// DefaultMaxPerTenant bounds concurrent subscribers per tenant when no explicit
// limit is configured (APPITOOLS_MAX_SSE_PER_TENANT).
const DefaultMaxPerTenant = 1000

// ErrTenantLimit is returned by Subscribe when the tenant has reached its
// concurrent-subscriber cap. The HTTP layer maps it to 429.
var ErrTenantLimit = errors.New("tenant SSE subscriber limit reached")

// Event is one resource change, broadcast to every subscriber of
// (tenant, resource). Record is the post-write row (RETURNING *) for
// create/update, and nil for delete (the row is gone; only the id is known —
// the DELETE path does not pay for a RETURNING just in case someone listens).
type Event struct {
	Type     string // "create" | "update" | "delete"
	Resource string
	ID       string
	Record   map[string]any // shared across subscribers — receivers must not mutate
}

// Subscriber is one open SSE connection's mailbox. The HTTP handler owns the
// receive side: it selects on C (events), Slow (closed by the hub when this
// subscriber's buffer overflows), and the request context.
type Subscriber struct {
	// C delivers events. Never closed (avoids send-on-closed races); the
	// handler exits via Slow or its request context instead.
	C chan Event
	// Slow is closed (once) when the subscriber's buffer overflows. The handler
	// must send a final error event and terminate the connection.
	Slow chan struct{}

	// AllowedFields is the RBAC field allowlist captured at subscribe time
	// (empty = all fields). The SSE writer applies the same FilterFields the
	// GET handlers use, so a role never sees a field in an event that it
	// cannot see in a GET.
	AllowedFields []string
	// CondField/CondValue carry the row-level RBAC condition captured at
	// subscribe time ("" = none). Only eq is supported — the only operator the
	// policy engine emits today; events for non-matching rows are not
	// delivered (same row scoping as the list endpoint).
	CondField string
	CondValue any

	tenant   string
	resource string
	slowOnce sync.Once
}

// Hub is the process-wide registry of subscribers, keyed tenant → resource →
// subscriber set. A nil *Hub is valid and inert: Publish/Count no-op and
// Subscribe fails, so tests and callers that don't wire SSE pass nil.
type Hub struct {
	mu           sync.RWMutex
	subs         map[string]map[string]map[*Subscriber]struct{}
	perTenant    map[string]int
	maxPerTenant int
}

// NewHub builds a Hub. maxPerTenant <= 0 falls back to DefaultMaxPerTenant.
func NewHub(maxPerTenant int) *Hub {
	if maxPerTenant <= 0 {
		maxPerTenant = DefaultMaxPerTenant
	}
	return &Hub{
		subs:         make(map[string]map[string]map[*Subscriber]struct{}),
		perTenant:    make(map[string]int),
		maxPerTenant: maxPerTenant,
	}
}

// Subscribe registers a new subscriber for (tenant, resource) and returns it.
// Returns ErrTenantLimit when the tenant is at its concurrent cap.
func (h *Hub) Subscribe(tenant, resource string, allowedFields []string, condField string, condValue any) (*Subscriber, error) {
	if h == nil {
		return nil, errors.New("events hub not configured")
	}
	sub := &Subscriber{
		C:             make(chan Event, subscriberBuffer),
		Slow:          make(chan struct{}),
		AllowedFields: allowedFields,
		CondField:     condField,
		CondValue:     condValue,
		tenant:        tenant,
		resource:      resource,
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.perTenant[tenant] >= h.maxPerTenant {
		return nil, ErrTenantLimit
	}
	byRes, ok := h.subs[tenant]
	if !ok {
		byRes = make(map[string]map[*Subscriber]struct{})
		h.subs[tenant] = byRes
	}
	set, ok := byRes[resource]
	if !ok {
		set = make(map[*Subscriber]struct{})
		byRes[resource] = set
	}
	set[sub] = struct{}{}
	h.perTenant[tenant]++
	return sub, nil
}

// Unsubscribe removes sub from the hub. Idempotent; safe on a sub that was
// never registered. Every connection MUST call this on exit (defer) — it is
// what prevents subscriber/goroutine leaks.
func (h *Hub) Unsubscribe(sub *Subscriber) {
	if h == nil || sub == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	byRes, ok := h.subs[sub.tenant]
	if !ok {
		return
	}
	set, ok := byRes[sub.resource]
	if !ok {
		return
	}
	if _, present := set[sub]; !present {
		return
	}
	delete(set, sub)
	if len(set) == 0 {
		delete(byRes, sub.resource)
	}
	if len(byRes) == 0 {
		delete(h.subs, sub.tenant)
	}
	if h.perTenant[sub.tenant]--; h.perTenant[sub.tenant] <= 0 {
		delete(h.perTenant, sub.tenant)
	}
}

// Publish broadcasts ev to every subscriber of (tenant, ev.Resource). It NEVER
// blocks the caller (the request path): the per-subscriber send is a
// non-blocking select, and a subscriber whose buffer is full is marked slow
// (its Slow channel is closed once) so its connection terminates. With zero
// subscribers the cost is one RLock + two map lookups.
func (h *Hub) Publish(tenant string, ev Event) {
	if h == nil {
		return
	}
	h.mu.RLock()
	set := h.subs[tenant][ev.Resource]
	if len(set) == 0 {
		h.mu.RUnlock()
		return
	}
	// Copy the receivers out so the send loop runs without the lock (a slow
	// subscriber's Slow-close must not stall Subscribe/Unsubscribe).
	receivers := make([]*Subscriber, 0, len(set))
	for s := range set {
		receivers = append(receivers, s)
	}
	h.mu.RUnlock()

	for _, s := range receivers {
		// Row-level RBAC scoping: a subscriber with an eq condition only
		// receives events whose record matches. Delete events carry no record,
		// so a row-scoped subscriber cannot be given them safely → skip
		// (fail closed; documented with the at-most-once limitation).
		if s.CondField != "" {
			if ev.Record == nil || !looseEq(ev.Record[s.CondField], s.CondValue) {
				continue
			}
		}
		select {
		case s.C <- ev:
		default:
			s.slowOnce.Do(func() { close(s.Slow) })
		}
	}
}

// Count returns the number of live subscribers for a tenant (all resources).
func (h *Hub) Count(tenant string) int {
	if h == nil {
		return 0
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.perTenant[tenant]
}

// looseEq compares a record value against an RBAC condition value using string
// normalization: row values come from Postgres via RowsToMaps (uuids already
// hyphenated strings) while condition values come from JWT claims (strings).
func looseEq(a, b any) bool {
	if a == nil || b == nil {
		return false
	}
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok && bok {
		return as == bs
	}
	return a == b
}
