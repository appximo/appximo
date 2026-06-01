package observability

import (
	"fmt"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"
)

// ErrGroup aggregates repeated occurrences of the same logical error.
type ErrGroup struct {
	Count     atomic.Int64
	FirstSeen int64
	LastSeen  atomic.Int64
	Message   string
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

// ErrorStore deduplicates errors per tenant by fingerprinting type + message prefix.
type ErrorStore struct {
	mu     sync.RWMutex
	groups map[string]map[uint64]*ErrGroup
}

func NewErrorStore() *ErrorStore {
	return &ErrorStore{
		groups: make(map[string]map[uint64]*ErrGroup),
	}
}

// Record fingerprints err and increments its group counter for tenantID.
func (es *ErrorStore) Record(tenantID string, err error) {
	msg := err.Error()
	truncLen := min(100, len(msg))
	h := fnv.New64a()
	h.Write([]byte(fmt.Sprintf("%T:%s", err, msg[:truncLen])))
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
	g.add(msg)
}
