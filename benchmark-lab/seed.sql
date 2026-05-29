-- Create the guides table if it doesn't exist
CREATE TABLE IF NOT EXISTS guides (
    id SERIAL PRIMARY KEY,
    tenant_id VARCHAR(50) NOT NULL,
    title VARCHAR(255) NOT NULL,
    content TEXT NOT NULL,
    status VARCHAR(50) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT CURRENT_TIMESTAMP
);

-- Truncate to start fresh
TRUNCATE TABLE guides RESTART IDENTITY;

-- Inject 1,000,000 records dynamically evenly distributed among 10 tenants and 5 statuses
INSERT INTO guides (tenant_id, title, content, status, created_at)
SELECT 
    'tenant_' || (1 + MOD(g.i, 10))::text,
    'Guide Title ' || g.i,
    'Here is some content for guide ' || g.i,
    CASE MOD(g.i, 5)
        WHEN 0 THEN 'pending'
        WHEN 1 THEN 'published'
        WHEN 2 THEN 'draft'
        WHEN 3 THEN 'archived'
        ELSE 'deleted'
    END,
    NOW() - (g.i || ' seconds')::INTERVAL
FROM generate_series(1, 1000000) AS g(i);

-- Create optimal compound index exactly like in Prisma
CREATE INDEX IF NOT EXISTS idx_guides_tenant_status_created 
ON guides(tenant_id, status, created_at);