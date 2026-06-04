package observability

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/miguelangel/appitools/pkg/tenant"
)

// Frame is a single symbolized stack frame.
type Frame struct {
	Function string `json:"function"`
	File     string `json:"file"`
	Line     int    `json:"line"`
}

// ErrGroup aggregates repeated occurrences of the same logical error.
type ErrGroup struct {
	Count     atomic.Int64
	FirstSeen int64
	LastSeen  atomic.Int64
	Message   string
	Stack     []Frame   // symbolized only on first occurrence
	stackOnce sync.Once // guarantees single symbolization per fingerprint
	mu        sync.Mutex
	examples  [10]string
	head      int
}

func (g *ErrGroup) add(msg string) {
	g.Count.Add(1)
	g.LastSeen.Store(time.Now().UnixMilli())
	g.mu.Lock()
	g.examples[g.head%10] = msg
	g.head++
	g.mu.Unlock()
}

// ErrorStore deduplicates errors per tenant by fingerprinting type + message prefix + call site.
type ErrorStore struct {
	mu     sync.RWMutex
	groups map[string]map[uint64]*ErrGroup
}

func NewErrorStore() *ErrorStore {
	return &ErrorStore{
		groups: make(map[string]map[uint64]*ErrGroup),
	}
}

// Record fingerprints err, captures a cheap PC snapshot on every call, and
// symbolizes the stack exactly once per unique fingerprint via stackOnce.
func (es *ErrorStore) Record(tenantID string, err error) {
	// 1. Capture raw PCs — cheap (~238ns), always.
	var pcs [32]uintptr
	n := runtime.Callers(2, pcs[:])
	captured := pcs[:n]

	// 2. Fingerprint: error type + message prefix + top-8 PCs for call-site precision.
	msg := err.Error()
	h := fnv.New64a()
	h.Write([]byte(fmt.Sprintf("%T", err)))
	truncLen := min(100, len(msg))
	h.Write([]byte(msg[:truncLen]))
	for _, pc := range captured[:min(8, len(captured))] {
		_ = binary.Write(h, binary.LittleEndian, pc)
	}
	fp := h.Sum64()

	es.mu.RLock()
	groups := es.groups[tenantID]
	es.mu.RUnlock()

	if groups == nil {
		es.mu.Lock()
		if es.groups[tenantID] == nil {
			es.groups[tenantID] = make(map[uint64]*ErrGroup)
		}
		groups = es.groups[tenantID]
		es.mu.Unlock()
	}

	es.mu.RLock()
	g := groups[fp]
	es.mu.RUnlock()

	if g == nil {
		es.mu.Lock()
		if groups[fp] == nil {
			groups[fp] = &ErrGroup{
				Message:   msg,
				FirstSeen: time.Now().UnixMilli(),
			}
		}
		g = groups[fp]
		es.mu.Unlock()
	}

	// 3. Symbolize only on first occurrence — expensive once, free thereafter.
	g.stackOnce.Do(func() {
		g.Stack = symbolize(captured)
	})

	g.add(msg)
}

// TopN returns the n most-frequent error groups for tenantID, sorted by count descending.
func (es *ErrorStore) TopN(tenantID string, n int) []map[string]any {
	es.mu.RLock()
	groups := es.groups[tenantID]
	es.mu.RUnlock()
	if groups == nil {
		return nil
	}

	type ranked struct {
		count int64
		g     *ErrGroup
	}
	all := make([]ranked, 0, len(groups))
	for _, g := range groups {
		all = append(all, ranked{g.Count.Load(), g})
	}
	sort.Slice(all, func(i, j int) bool {
		return all[i].count > all[j].count
	})
	if len(all) > n {
		all = all[:n]
	}

	result := make([]map[string]any, len(all))
	for i, r := range all {
		r.g.mu.Lock()
		examples := make([]string, 0, 10)
		for _, e := range r.g.examples {
			if e != "" {
				examples = append(examples, e)
			}
		}
		r.g.mu.Unlock()
		result[i] = map[string]any{
			"message":    r.g.Message,
			"count":      r.count,
			"first_seen": r.g.FirstSeen,
			"last_seen":  r.g.LastSeen.Load(),
			"stack":      r.g.Stack,
			"examples":   examples[:min(3, len(examples))],
		}
	}
	return result
}

// symbolize converts raw PCs to human-readable frames, filtering runtime
// internals and capping at 16 user frames.
func symbolize(pcs []uintptr) []Frame {
	cf := runtime.CallersFrames(pcs)
	var result []Frame
	for {
		f, more := cf.Next()
		if f.Function != "" &&
			!strings.Contains(f.Function, "runtime.") {
			result = append(result, Frame{
				Function: f.Function,
				File:     trimGoPath(f.File),
				Line:     f.Line,
			})
		}
		if !more || len(result) >= 16 {
			break
		}
	}
	return result
}

// trimGoPath strips the absolute build-time prefix, returning readable paths
// like pkg/auth/jwt.go or cmd/appitools/cmd_serve.go.
func trimGoPath(path string) string {
	for _, marker := range []string{"/pkg/", "/cmd/", "/internal/"} {
		if i := strings.Index(path, marker); i >= 0 {
			return path[i+1:]
		}
	}
	return path
}

// ── Per-request error capture for 500s, linked to trace_id ──────────────────

// ErrorCapture is a snapshot of a server error (HTTP 500) linked to the request's
// trace, so the stack trace can be shown next to the timeline. For client errors
// (4xx) the Stack is empty — those are not server bugs and never pay for
// runtime.Callers. UserID/Role/Route/Method are filled by the caller (the
// handler) because observability must not import auth (auth imports observability).
type ErrorCapture struct {
	TraceID   string  `json:"trace_id"`
	TenantID  string  `json:"tenant_id"`
	Route     string  `json:"route"`
	Method    string  `json:"method"`
	ErrMsg    string  `json:"error_msg"`
	Stack     []Frame `json:"stack"`
	UserID    string  `json:"user_id"`
	Role      string  `json:"role"`
	Timestamp int64   `json:"ts"`
}

// captureSymCache caches symbolized frames per fingerprint (message + call site).
// The Nth occurrence of the same 500 reuses the frames — no re-symbolization, no
// allocation.
var captureSymCache sync.Map // uint64 → []Frame

// CaptureError snapshots the current goroutine's stack for a server error (500)
// and links it to the request's trace_id and tenant (from ctx). It symbolizes the
// stack at most once per call site; repeat occurrences reuse the cached frames
// with ZERO allocations.
//
// The hit path captures only 3 PCs into a stack-local array used solely for the
// fingerprint (so nothing escapes to the heap); the expensive full-stack capture +
// symbolization lives in captureAndSymbolize, called only on a cache miss, so its
// unavoidable PC-slice escape never taxes the common (repeat) path.
//
// Returned BY VALUE so a caller that discards it allocates nothing. NEVER call on
// the happy path — even a hit pays for runtime.Callers.
func CaptureError(ctx context.Context, err error) ErrorCapture {
	msg := err.Error()

	// Cheap fingerprint capture: 3 PCs, read-only (no escape → stays on stack).
	var fppcs [3]uintptr
	nfp := runtime.Callers(2, fppcs[:]) // 0=Callers, 1=CaptureError → caller chain
	fp := captureFingerprint(msg, fppcs[:nfp])

	frames, ok := loadCaptureFrames(fp)
	if !ok {
		frames = storeCaptureFrames(fp, captureAndSymbolize())
	}

	return ErrorCapture{
		TraceID:   TraceIDFromCtx(ctx),
		TenantID:  tenantIDFromCtx(ctx),
		ErrMsg:    msg,
		Stack:     frames,
		Timestamp: time.Now().UnixMilli(),
	}
}

func loadCaptureFrames(fp uint64) ([]Frame, bool) {
	if v, ok := captureSymCache.Load(fp); ok {
		return v.([]Frame), true
	}
	return nil, false
}

// storeCaptureFrames caches frames for fp, returning the winner if another
// goroutine symbolized the same call site concurrently (idempotent).
func storeCaptureFrames(fp uint64, frames []Frame) []Frame {
	actual, _ := captureSymCache.LoadOrStore(fp, frames)
	return actual.([]Frame)
}

// captureAndSymbolize captures and symbolizes the current stack. Isolated in its
// own function so the [32]uintptr buffer (which escapes via CallersFrames) lives
// only here — on the miss path — and never forces an allocation on the hit path.
func captureAndSymbolize() []Frame {
	var pcs [32]uintptr
	// skip 0=Callers, 1=captureAndSymbolize, 2=CaptureError → frame[0] is the
	// handler that called CaptureError; infra frames are filtered out below.
	n := runtime.Callers(3, pcs[:])
	return symbolizeCapture(pcs[:n])
}

// captureFingerprint is an allocation-free FNV-64a over the message and the top-3
// PCs (the call site), so the same error from the same site dedups to one symbolize.
func captureFingerprint(msg string, pcs []uintptr) uint64 {
	const offset = uint64(14695981039346656037)
	const prime = uint64(1099511628211)
	h := offset
	for i := 0; i < len(msg); i++ {
		h ^= uint64(msg[i])
		h *= prime
	}
	top := pcs
	if len(top) > 3 {
		top = top[:3]
	}
	for _, pc := range top {
		v := uint64(pc)
		for j := 0; j < 8; j++ {
			h ^= (v >> (8 * j)) & 0xff
			h *= prime
		}
	}
	return h
}

// symbolizeCapture resolves PCs to frames, dropping runtime/net-http/chi and
// observability's own frames (noise when locating a bug) and capping at 10.
func symbolizeCapture(pcs []uintptr) []Frame {
	cf := runtime.CallersFrames(pcs)
	var result []Frame
	for {
		f, more := cf.Next()
		if f.Function != "" && !isInfraFrame(f.Function) {
			result = append(result, Frame{
				Function: f.Function,
				File:     trimGoPath(f.File),
				Line:     f.Line,
			})
		}
		if !more || len(result) >= 10 {
			break
		}
	}
	return result
}

// isInfraFrame reports whether fn is a runtime/net-http/chi/observability frame —
// stack noise that hides the application code that actually triggered the error.
func isInfraFrame(fn string) bool {
	switch {
	case strings.HasPrefix(fn, "runtime."),
		strings.Contains(fn, "net/http."),
		strings.Contains(fn, "go-chi/chi"),
		strings.Contains(fn, "/pkg/observability."):
		return true
	}
	return false
}

func tenantIDFromCtx(ctx context.Context) string {
	if tc := tenant.FromCtx(ctx); tc != nil {
		return tc.ID
	}
	return ""
}
