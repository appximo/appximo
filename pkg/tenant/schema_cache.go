package tenant

import (
	"sync"

	"github.com/miguelangel/appitools/pkg/schema"
)

// SchemaCache holds the compiled API schemas for active tenants in memory.
// Updated by the migration worker after a successful migration; invalidated
// by pg_notify events so the data plane reloads on the next request.
type SchemaCache struct {
	mu sync.RWMutex
	m  map[string]*schema.APISchema
}

func NewSchemaCache() *SchemaCache {
	return &SchemaCache{m: make(map[string]*schema.APISchema)}
}

func (c *SchemaCache) Set(tenantID string, s *schema.APISchema) {
	c.mu.Lock()
	c.m[tenantID] = s
	c.mu.Unlock()
}

func (c *SchemaCache) Get(tenantID string) (*schema.APISchema, bool) {
	c.mu.RLock()
	s, ok := c.m[tenantID]
	c.mu.RUnlock()
	return s, ok
}

// Invalidate removes a tenant's cached schema so the next request reloads it from DB.
func (c *SchemaCache) Invalidate(tenantID string) {
	c.mu.Lock()
	delete(c.m, tenantID)
	c.mu.Unlock()
}
