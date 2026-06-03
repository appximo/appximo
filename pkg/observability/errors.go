package observability

import (
	"fmt"
	"hash/fnv"
	"sort"
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
			"examples":   examples[:min(3, len(examples))],
		}
	}
	return result
}
