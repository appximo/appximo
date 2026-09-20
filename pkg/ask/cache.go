package ask

import (
	"sync"
	"time"
)

// PlanCache remembers the PLAN a question translated to — never its data.
// The same question, the same tenant, the same role → the same plan, with no
// model call; the number is recomputed against the database every time. A
// cached plan stays valid across days because a period is a TOKEN the engine
// resolves at execution ("today" is tomorrow's today) and the only time
// literal, "now", is resolved the same way; a plan with a `match` is cached
// BEFORE the name resolves, so a new row with that name is found tomorrow.
// The key is normalized (case, accents, punctuation, spaces), bounded in size
// and age, and scoped by tenant+role (the caller builds the scope).
type PlanCache struct {
	mu      sync.Mutex
	entries map[string]*cacheEntry
	max     int
	ttl     time.Duration
	hits    int
	misses  int
	now     func() time.Time
}

type cacheEntry struct {
	plan   Plan
	source string
	at     time.Time
	used   time.Time
}

// NewPlanCache bounds the cache to max entries and ttl per entry.
func NewPlanCache(max int, ttl time.Duration) *PlanCache {
	if max <= 0 {
		max = 2000
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &PlanCache{entries: map[string]*cacheEntry{}, max: max, ttl: ttl, now: time.Now}
}

// Key builds the scoped, normalized key.
func Key(scope, question string) string { return scope + "\x00" + normalize(question) }

// Get returns the cached plan for key, if fresh.
func (c *PlanCache) Get(key string) (Plan, string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.entries[key]
	now := c.now()
	ttl := c.ttl
	if e != nil && (e.plan.Kind == "unclear" || e.plan.Kind == "write") {
		ttl = UnclearTTL
	}
	if e == nil || now.Sub(e.at) > ttl {
		if e != nil {
			delete(c.entries, key)
		}
		c.misses++
		return Plan{}, "", false
	}
	e.used = now
	c.hits++
	return e.plan, e.source, true
}

// UnclearTTL bounds how long a "no entendí" (and the model's write refusal)
// is remembered (VOZ-TRAZABILIDAD-S1): long enough that a loop re-asking the
// same nonsense does not re-bill every second, short enough that a phrase
// the model could answer is not denied all day because one call went wrong
// (temperature 0 is deterministic in intent, not a guarantee of identical
// output from the API). Re-examined in VOZ-AHORRO-S2 and kept: the case that
// argued for shortening it — a synonym declared AFTER the refusal — is now
// handled by the vocabulary fingerprint in the key (the old entry is simply
// never found), and the sentences that were never a question are discarded
// by the parser before the cache; what remains under this TTL is a genuine
// model verdict, and an hour is the right memory for one.
const UnclearTTL = time.Hour

// Cacheable reports whether a plan is worth remembering: an executable read
// (24 h); a write PLAN — create/update — likewise (VOZ-AHORRO-S2 Part B: the
// plan is the only thing that costs; its names, row, time tokens and
// required fields are re-prepared before every confirmation, so a cached
// «anotá pagar la luz» yields a fresh pending each time, never a result);
// `unclear` and the model's `write` refusal for UnclearTTL — re-asking
// within the hour only re-bills. The cache key carries the vocabulary's
// fingerprint, so a schema change (a synonym declared, a resource added)
// invalidates every plan translated against the old vocabulary.
func Cacheable(p Plan) bool {
	switch p.Kind {
	case "count", "list", "sum", "avg", "min", "max", "unclear", "create", "update", "write":
		return true
	}
	return false
}

// Put stores a plan; when full, the least recently used entry goes.
func (c *PlanCache) Put(key string, p Plan, source string) {
	if !Cacheable(p) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.entries) >= c.max {
		var oldestK string
		var oldest time.Time
		for k, e := range c.entries {
			if oldestK == "" || e.used.Before(oldest) {
				oldestK, oldest = k, e.used
			}
		}
		delete(c.entries, oldestK)
	}
	now := c.now()
	c.entries[key] = &cacheEntry{plan: p, source: source, at: now, used: now}
}

// Stats returns hits, misses and the current size.
func (c *PlanCache) Stats() (hits, misses, size int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.hits, c.misses, len(c.entries)
}
