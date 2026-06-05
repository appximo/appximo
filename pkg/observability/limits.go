package observability

// maxTrackedTenants bounds how many distinct tenant ids each per-tenant
// observability store will track (rings, error groups, latency histograms, the
// anomaly model, and Prometheus series).
//
// The tenant id is derived from the client-controlled Host subdomain, and these
// stores are written on the request path BEFORE authentication, so an attacker
// rotating the subdomain could otherwise grow them without bound — a memory /
// Prometheus-cardinality exhaustion DoS. Real deployments have far fewer active
// tenants than this ceiling; once it is reached, samples for previously unseen
// tenants are dropped (metrics aggregate them into a shared overflow series)
// rather than allocating new per-tenant state.
//
// It is a var (not a const) only so tests can shrink it; it is never mutated at
// runtime.
var maxTrackedTenants = 4096

// maxErrGroupsPerTenant bounds the number of distinct error groups retained for a
// single tenant. maxTrackedTenants caps how many TENANTS the error store tracks,
// but within one tenant the group map is keyed by an error fingerprint that
// includes the (often variable) error message — e.g. a malformed-JWT error embeds
// a byte offset. An attacker rotating malformed tokens at one subdomain could
// otherwise grow that tenant's group map without bound AND force a stack
// symbolization per new fingerprint (memory + CPU DoS). Past this cap, new error
// groups for that tenant are dropped. Var only so tests can shrink it.
var maxErrGroupsPerTenant = 512

// metricsOverflowTenant is the tenant_id label applied to requests once the
// metrics cardinality cap is reached, so attacker-rotated tenants collapse into
// one bounded series instead of minting one series each.
const metricsOverflowTenant = "__overflow__"
