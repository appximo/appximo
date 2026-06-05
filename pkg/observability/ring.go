package observability

import (
	"sync"
)

// ringSize is the number of recent requests retained per tenant.
const ringSize = 256

// Sample is a fixed-size record of a single request. All fields are value types
// so a Sample never escapes to the heap and TenantRing.Record stays allocation-free.
//
//	Start   request start time, unix microseconds
//	DurUS   total request duration, microseconds
//	QueryUS time spent in DB queries, microseconds (0 when unmeasured)
//	Route   interned route-pattern id (resolve via Rings.RouteName)
//	Status  HTTP status code
//	TraceID first 8 bytes of the request trace id (16 hex chars decoded)
//	Spans   per-stage durations captured by the request's SpanTracker
//	NSpans  number of valid entries in Spans
//
// Spans/TraceID are value types too, so Record stays allocation-free.
type Sample struct {
	Start   int64
	DurUS   int32
	QueryUS int32
	Route   uint16
	Status  uint16
	TraceID [8]byte
	Spans   [maxSpans]Span
	NSpans  uint8
	// ErrMsg is the error message for an errored request ("" otherwise).
	// ErrorCapture carries the symbolized stack for a 500 (nil otherwise) — a
	// pointer, so non-error samples cost only 8 bytes and Record stays alloc-free.
	ErrMsg       string
	ErrorCapture *ErrorCapture
	// Client context (raw): browser/OS are parsed and country geo-resolved lazily
	// in the projection, so the hot path only stores two strings.
	IP        string
	UserAgent string
}

// TenantRing is a fixed circular buffer of the last ringSize samples for one tenant.
//
// A mutex — not lock-free atomics — guards the buffer on purpose: once head laps
// the array, two writers separated by ringSize records target the SAME physical
// slot, so an atomic head only serializes index allocation, not the 24-byte Sample
// copy. Those concurrent same-slot writes are a genuine data race (caught by -race).
// The lock keeps Record allocation-free (a single array assignment, no slices).
type TenantRing struct {
	mu   sync.Mutex
	buf  [ringSize]Sample
	head uint32 // total number of samples ever recorded
}

// Record stores s in the next slot, overwriting the oldest sample once the buffer
// is full. Allocation-free.
func (r *TenantRing) Record(s Sample) {
	r.mu.Lock()
	r.buf[r.head%ringSize] = s
	r.head++
	r.mu.Unlock()
}

// Snapshot returns a copy of the retained samples ordered most-recent first.
// Returns a non-nil empty slice (not nil) when nothing has been recorded, so it
// marshals to a JSON [] rather than null.
func (r *TenantRing) Snapshot() []Sample {
	r.mu.Lock()
	defer r.mu.Unlock()
	h := r.head
	n := h
	if n > ringSize {
		n = ringSize
	}
	out := make([]Sample, 0, n)
	// Walk backwards from the most recently written slot (h-1) to the oldest.
	for i := uint32(0); i < n; i++ {
		out = append(out, r.buf[(h-1-i)%ringSize])
	}
	return out
}

// Rings holds one TenantRing per tenant plus a route-pattern interner that maps
// variable-length route strings to compact uint16 ids stored in each Sample.
type Rings struct {
	mu sync.RWMutex
	m  map[string]*TenantRing

	routeMu  sync.RWMutex
	routeIDs map[string]uint16
	routes   []string // index = id; routes[0] reserved for "unknown"
}

// NewRings returns an empty registry ready for use.
func NewRings() *Rings {
	return &Rings{
		m:        make(map[string]*TenantRing),
		routeIDs: make(map[string]uint16),
		routes:   []string{""}, // id 0 == unknown/empty route
	}
}

// RouteID returns a stable uint16 id for route, interning it on first sight.
// Reads are lock-free-ish (RLock); only the first occurrence of a route takes
// the write lock. Ids saturate at the uint16 max to avoid overflow wraparound.
func (rs *Rings) RouteID(route string) uint16 {
	if route == "" {
		return 0
	}
	rs.routeMu.RLock()
	id, ok := rs.routeIDs[route]
	rs.routeMu.RUnlock()
	if ok {
		return id
	}
	rs.routeMu.Lock()
	defer rs.routeMu.Unlock()
	if id, ok := rs.routeIDs[route]; ok { // re-check after acquiring write lock
		return id
	}
	if len(rs.routes) >= 1<<16 {
		return 0 // interner full; degrade to unknown rather than overflow
	}
	id = uint16(len(rs.routes))
	rs.routes = append(rs.routes, route)
	rs.routeIDs[route] = id
	return id
}

// RouteName resolves an interned id back to its route string.
func (rs *Rings) RouteName(id uint16) string {
	rs.routeMu.RLock()
	defer rs.routeMu.RUnlock()
	if int(id) < len(rs.routes) {
		return rs.routes[id]
	}
	return ""
}

// Record appends a sample to the given tenant's ring, creating the ring on first use.
func (rs *Rings) Record(tenantID string, s Sample) {
	rs.mu.RLock()
	tr := rs.m[tenantID]
	rs.mu.RUnlock()
	if tr == nil {
		rs.mu.Lock()
		if tr = rs.m[tenantID]; tr == nil {
			if len(rs.m) >= maxTrackedTenants {
				// Cap reached: drop rather than allocate another [256]Sample ring
				// for a (possibly attacker-rotated) tenant. See limits.go.
				rs.mu.Unlock()
				return
			}
			tr = &TenantRing{}
			rs.m[tenantID] = tr
		}
		rs.mu.Unlock()
	}
	tr.Record(s)
}

// Count returns the number of distinct tenants that have recorded at least one request.
func (rs *Rings) Count() int {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	return len(rs.m)
}

// TenantIDs returns the IDs of every tenant that has recorded at least one request.
// Used by the SLO engine to sweep all active tenants each tick.
func (rs *Rings) TenantIDs() []string {
	rs.mu.RLock()
	defer rs.mu.RUnlock()
	ids := make([]string, 0, len(rs.m))
	for id := range rs.m {
		ids = append(ids, id)
	}
	return ids
}

// Snapshot returns the tenant's retained samples (most-recent first), or an empty
// slice when the tenant has no recorded requests.
func (rs *Rings) Snapshot(tenantID string) []Sample {
	rs.mu.RLock()
	tr := rs.m[tenantID]
	rs.mu.RUnlock()
	if tr == nil {
		return []Sample{}
	}
	return tr.Snapshot()
}

// RecentRequest is the JSON-friendly projection of a Sample with its route id
// resolved back to a human-readable pattern.
type RecentRequest struct {
	StartUS int64  `json:"start_us"`
	DurUS   int32  `json:"dur_us"`
	QueryUS int32  `json:"query_us"`
	Route   string `json:"route"`
	Status  uint16 `json:"status"`
}

// TraceView is the JSON projection of a Sample's trace: id, route, total, and the
// per-stage span breakdown. Served under /debug/tenant/{id} → "recent_traces".
type TraceView struct {
	TraceID string  `json:"trace_id"`
	TS      int64   `json:"ts"` // request start, unix microseconds
	Route   string  `json:"route"`
	TotalUS int32   `json:"total_us"`
	Status  uint16  `json:"status"`
	Spans   []Span  `json:"spans"`
	ErrMsg  string  `json:"error_msg,omitempty"` // set for error responses
	Stack   []Frame `json:"stack,omitempty"`     // set only for 500s
	// Request context for the error panel — populated for recent_traces from the
	// in-memory ErrorCapture (slow_traces persists only error_msg + stack).
	Method string `json:"method,omitempty"`
	UserID string `json:"user_id,omitempty"`
	Role   string `json:"role,omitempty"`
	// Client context — IP, parsed browser/OS, and geo country.
	IP        string `json:"ip,omitempty"`
	UserAgent string `json:"user_agent,omitempty"`
	Browser   string `json:"browser,omitempty"`
	OS        string `json:"os,omitempty"`
	Country   string `json:"country,omitempty"`
	// Reproducible-request context (persisted traces only): filtered headers +
	// full URL. Method (above) completes the curl reconstruction.
	Headers map[string]string `json:"headers,omitempty"`
	FullURL string            `json:"full_url,omitempty"`
}

// RecentTraces returns up to n of the tenant's most recent requests projected as
// TraceViews (trace id resolved to hex, route resolved to string, spans copied
// out of the fixed array).
func (rs *Rings) RecentTraces(tenantID string, n int) []TraceView {
	samples := rs.Snapshot(tenantID)
	if n > len(samples) {
		n = len(samples)
	}
	out := make([]TraceView, 0, n)
	for i := 0; i < n; i++ {
		s := samples[i]
		ns := int(s.NSpans)
		if ns > maxSpans {
			ns = maxSpans
		}
		spans := make([]Span, ns)
		copy(spans, s.Spans[:ns])
		tv := TraceView{
			TraceID:   traceIDString(s.TraceID),
			TS:        s.Start,
			Route:     rs.RouteName(s.Route),
			TotalUS:   s.DurUS,
			Status:    s.Status,
			Spans:     spans,
			ErrMsg:    s.ErrMsg,
			IP:        s.IP,
			UserAgent: s.UserAgent,
		}
		if s.ErrorCapture != nil {
			tv.Stack = s.ErrorCapture.Stack
			tv.Method = s.ErrorCapture.Method
			tv.UserID = s.ErrorCapture.UserID
			tv.Role = s.ErrorCapture.Role
			if tv.ErrMsg == "" {
				tv.ErrMsg = s.ErrorCapture.ErrMsg
			}
		}
		out = append(out, tv)
	}
	return out
}

// Recent returns the tenant's recent requests as RecentRequest values, ordered
// most-recent first, with route ids resolved to strings.
func (rs *Rings) Recent(tenantID string) []RecentRequest {
	samples := rs.Snapshot(tenantID)
	out := make([]RecentRequest, 0, len(samples))
	for _, s := range samples {
		out = append(out, RecentRequest{
			StartUS: s.Start,
			DurUS:   s.DurUS,
			QueryUS: s.QueryUS,
			Route:   rs.RouteName(s.Route),
			Status:  s.Status,
		})
	}
	return out
}
