# Testing Guide — Appitools

## Overview

The test suite is organized into four levels:

| Level | Location | Runner | When |
|---|---|---|---|
| **Unit** | `pkg/*/..._test.go` | `go test ./...` | Every PR |
| **Integration** | `internal/handlers/`, `pkg/controlplane/`, `pkg/migration/` | `go test ./...` | Every PR |
| **E2E** | `pkg/integration/` | `go test ./...` | Every PR (not `-short`) |
| **Benchmark** | `pkg/benchmark/` | `go test -bench=.` | Before releases |
| **Security** | `pkg/security/` | `go test ./...` | Every PR |

## Running the full suite

```bash
# Full suite (unit + integration + e2e + security)
go test ./... -timeout 180s -count=1

# Skip slow e2e and integration tests
go test ./... -short

# Single package
go test ./pkg/auth/... -v
```

## Requirements

- **Docker**: Required for all integration, e2e, and security tests that need Postgres/Redis. Tests use [testcontainers-go](https://testcontainers.com/guides/getting-started-with-testcontainers-for-go/) to spin up and tear down containers automatically.
- **Go 1.22+**: Required by the module.

## What each level covers

### Unit tests (`go test ./pkg/auth/... ./pkg/rbac/... ./pkg/schema/...`)

Fast tests with no external dependencies:
- `pkg/auth` — JWT generation, validation, middleware (8 tests, ~3ms)
- `pkg/rbac` — policy evaluation, RBAC middleware (7 tests)
- `pkg/schema` — schema loading, validation (11 tests)
- `pkg/tenant` — TenantMiddleware host parsing (5 tests)
- `pkg/extensions` — JS sandbox, webhook HMAC, retries (12 tests)

### Integration tests (testcontainers Postgres)

Test real SQL queries against a live Postgres 16 container:
- `pkg/db` — QueryTenant, ExecTenant, SET LOCAL isolation (3 tests)
- `pkg/controlplane` — RegisterTenant, duplicate detection, multi-resource (8 tests)
- `pkg/migration` — MigrationWorker with Redis + Postgres (3 tests)
- `internal/handlers` — Full CRUD, WHERE condition, field stripping (3 tests)

### E2E test (`pkg/integration/e2e_test.go`)

`TestE2E_FullLifecycle` — simulates a real customer's full lifecycle:

1. Register tenant with 5-resource logistics schema
2. Verify all 5 tables created in Postgres
3. Start full HTTP server (JWT + RBAC + hooks middleware)
4. Generate JWT tokens for 4 roles
5. CRUD cycle: POST → GET list → GET by ID → DELETE → 404
6. RBAC: operario isolation, public field restriction, 401 cases
7. JS hooks: reject without `code`, accept with `code`
8. Schema change via control plane API
9. Migration worker applies ALTER TABLE (max 10s wait)
10. New field `phone` appears in POST response

Run time: ~25–40s (dominated by container startup).

### Security tests (`pkg/security/isolation_test.go`)

Six attack scenarios:

| Test | Attack | Expected |
|---|---|---|
| `TestIsolation_SQLInjection_TenantID` | Malicious tenant IDs with `'`, `--`, `UNION` | Regex validation rejects, DB never touched |
| `TestIsolation_SQLInjection_FieldName` | SQL in field names | Schema validator rejects |
| `TestIsolation_CrossTenant` | Tenant B tries to read Tenant A's data | 0 rows — schema isolation via `SET LOCAL search_path` |
| `TestIsolation_JWTManipulation` | Modify JWT payload without re-signing | `ValidateToken` returns error |
| `TestIsolation_GojaEscapes` | `require('fs')`, infinite loop, `process.env` | Sandbox error or timeout |
| `TestIsolation_ConcurrentMigrations` | 10 goroutines run migration simultaneously | All succeed, table is consistent |

### Benchmarks (`pkg/benchmark/throughput_test.go`)

```bash
go test ./pkg/benchmark/... -bench=. -benchmem -benchtime=10s -count=3
```

Three benchmark functions measuring real throughput against Postgres in Docker:

| Benchmark | Scenario | Goroutines |
|---|---|---|
| `BenchmarkListHandler_NoContention` | GET /api/guides — 1000 rows | 50 |
| `BenchmarkCreateHandler_WithHook` | POST with active Goja JS hook | 20 |
| `BenchmarkMultiTenant_10Tenants` | GET distributed across 10 tenants | 10 |

Output includes `req/s`, `µs/p50`, `µs/p95`, `µs/p99`, and `heap_MB`.

## Advisory lock — garantía de exclusión mutua entre workers

El `MigrationWorker` usa `pg_try_advisory_lock` (session-level, no-bloqueante) para
garantizar que **dos workers nunca ejecuten la misma migración simultáneamente**.

### Cómo funciona

```
worker-1 recibe job(tenant_X)             worker-2 recibe job(tenant_X)
  → pg_try_advisory_lock(hash("tenant_X"))   → pg_try_advisory_lock(hash("tenant_X"))
  → true → procesa                           → false → re-encola con delay 2s
  → pg_advisory_unlock                       → (después) re-intenta → true → procesa
```

La clave del lock es `FNV-64a(pgSchema)`. Es determinística entre procesos y reinicios.
El lock se libera explícitamente antes de devolver la conexión al pool — nunca queda
atrapado en pool connections inactivas.

### Test: `TestMigrationWorker_TwoWorkersSameTenant`

Verifica las tres garantías:

1. **Exclusión mutua**: el lock impide que dos migrations corran en paralelo
2. **Sin pérdida de jobs**: 5 jobs encolados → 5 entradas `ok` en `migration_log`
3. **Estado consistente**: la tabla existe y acepta INSERTs al finalizar

```
migration worker [test-worker-2]: migration ok for tenant "locktenant"  ← job 1
migration worker [test-worker-2]: migration ok for tenant "locktenant"  ← job 2
migration worker [test-worker-2]: migration ok for tenant "locktenant"  ← job 3
...
```

Cuando un worker gana el lock, el otro re-encola sus jobs con `LockRetryDelay` (50ms
en tests, 2s en producción). Los jobs re-encolados conservan su `retry_count` — el
lock contention no cuenta como intento fallido.

### Por qué la concurrencia en `CREATE TABLE IF NOT EXISTS` es problemática

El test de seguridad `TestIsolation_ConcurrentMigrations` descubrió que PostgreSQL
puede fallar con `pg_type_typname_nsp_index` cuando múltiples conexiones ejecutan
`CREATE TABLE IF NOT EXISTS` simultáneamente para la misma tabla. El advisory lock
en el worker **previene este race completamente** en el flujo de producción, porque
solo un worker puede llamar a `ApplyTenantMigration` para un tenant dado a la vez.

---

## Adding new tests

### Unit test
```go
// pkg/yourpkg/yourpkg_test.go
package yourpkg_test

func TestYourFeature(t *testing.T) {
    // No containers — pure logic
}
```

### Integration test with Postgres
```go
func TestYourFeature_WithDB(t *testing.T) {
    pool, cleanup := startPostgres(t)  // testcontainers helper
    defer cleanup()
    // ...
}
```

### Benchmark
```go
func BenchmarkYourHandler(b *testing.B) {
    srv := setupServer(b)
    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            // make HTTP request
        }
    })
    reportPercentiles(b, latencies)
}
```

## Interpreting benchmark results

```
BenchmarkListHandler_NoContention-8   1234   8765432 ns/op   512 B/op   8 allocs/op
    req/s=1234  µs/p50=6234  µs/p95=12345  µs/p99=18234  heap_MB=45.2
```

- **req/s**: total operations / elapsed wall time across all goroutines
- **µs/p50**: median latency — the "typical" request
- **µs/p95**: 95th percentile — 5% of requests are slower than this
- **µs/p99**: 99th percentile — tail latency (important for SLA)
- **heap_MB**: current heap allocation (not peak)

> Numbers from `testcontainers` benchmarks include Docker networking overhead. 
> On a server with local Postgres, expect 3–5× higher throughput.
