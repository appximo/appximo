package observability

import (
	"encoding/binary"
	"fmt"
	"hash/fnv"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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
