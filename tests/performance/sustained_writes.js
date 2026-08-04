// sustained_writes.js — S44 write-path benchmark (no SLO gate).
//
// Drives constant-arrival-rate POST traffic against the data plane with JWT +
// RBAC + multi-tenancy + hooks active, to measure the WRITE path latency
// distribution (used by the bench protocol pre/post comparisons). Writes have
// no defined SLO yet, so the thresholds here are informative only — nothing
// aborts the run.
//
// Tunables (env):
//   TARGET_URL   data-plane base URL        (default http://localhost:8080)
//   TENANT_ID    tenant subdomain           (default acme)  -> Host: <id>.localhost
//   BENCH_TOKEN  HS256 JWT for that tenant  (REQUIRED; mint with `appximo token`)
//   RATE         requests/sec               (default 20 — real INSERTs, keep low on small hosts)
//   DURATION     hold time                  (default 15s)
//
// Each iteration POSTs a VALID guides payload with varying data (__VU/__ITER +
// a per-run seed keep `code` unique across runs — it has a UNIQUE constraint).
// Rows are left to accumulate (bench tenant on a disposable env; no DELETEs).
// NOTE: status deliberately NEVER "pending" — the read benchmark
// (sustained_2krps.js) filters status eq pending, so write-bench rows must stay
// out of that index range to avoid contaminating read-path comparisons.

import http from 'k6/http';
import { check } from 'k6';

const RATE     = parseInt(__ENV.RATE || '20', 10);
const DURATION = __ENV.DURATION || '15s';
const TARGET   = __ENV.TARGET_URL || 'http://localhost:8080';
const TENANT   = __ENV.TENANT_ID || 'acme';
const TOKEN    = __ENV.BENCH_TOKEN || '';

const STATUSES = ['in_transit', 'delivered', 'returned']; // never 'pending' (see header)

export const options = {
  scenarios: {
    sustained_writes: {
      executor:        'constant-arrival-rate',
      rate:            RATE,
      timeUnit:        '1s',
      duration:        DURATION,
      preAllocatedVUs: Math.min(Math.max(Math.ceil(RATE / 5), 5), 100),
      maxVUs:          Math.min(RATE * 2, 500),
    },
  },
  // Informative only — no abortOnFail: a breach is reported in the summary but
  // never gates or aborts (writes have no SLO defined yet).
  thresholds: {
    http_req_failed: ['rate<0.01'],
  },
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)', 'max'],
};

export function setup() {
  if (!TOKEN) {
    throw new Error('BENCH_TOKEN is required — mint one with: appximo token --tenant <id> --secret <JWT_SECRET> --role super_admin');
  }
  // Per-run seed: makes `code` (UNIQUE column) collision-free across protocol
  // runs, where __VU/__ITER pairs repeat.
  return { seed: Date.now() };
}

// RESOURCE selects which schema this bench writes against: 'guides' (the
// logistics fixture, default — S44 baseline) or 'tasks' (the quickstart
// todo-api). Same script either way so pre/post comparisons stay symmetric.
const RESOURCE = __ENV.RESOURCE || 'items';   // canonical fixture; 'guides' is RETIRED

function buildPayload(data) {
  if (RESOURCE === 'items') {
    // examples/blank/schema.json — the CANONICAL bench fixture (one string
    // field), the same one the read protocol uses. Without this branch a write
    // bench against the canonical tenant posted the `guides` payload, every
    // request 422'd on unknown fields, and k6 aborted with zero datapoints —
    // which reads exactly like a failed run rather than a misconfigured one.
    return { name: `bench ${data.seed}-${__VU}-${__ITER}` };
  }
  if (RESOURCE === 'tasks') {
    return {
      title:  `bench ${data.seed}-${__VU}-${__ITER}`,
      status: __ITER % 2 === 0 ? 'open' : 'done',
    };
  }
  return {
    code:           `S44-${data.seed}-${__VU}-${__ITER}`,
    status:         STATUSES[__ITER % STATUSES.length],
    origin:         `Bodega ${__ITER % 50}`,
    destination:    `Ciudad ${(__VU + __ITER) % 30}`,
    weight_kg:      0.5 + (__ITER % 200) * 0.25,
    declared_value: 10000 + ((__ITER * 37 + __VU) % 5000) * 100,
  };
}

export default function (data) {
  const payload = buildPayload(data);
  const res = http.post(`${TARGET}/api/${RESOURCE}`, JSON.stringify(payload), {
    headers: {
      Authorization:  `Bearer ${TOKEN}`,
      Host:           `${TENANT}.localhost`,
      'Content-Type': 'application/json',
    },
  });
  check(res, { 'status is 201': (r) => r.status === 201 });
}

export function handleSummary(data) {
  const ms = (key) => {
    const v = data.metrics?.http_req_duration?.values?.[key];
    return v === undefined || isNaN(v) ? null : Math.round(v * 100) / 100;
  };
  const errRate = data.metrics?.http_req_failed?.values?.rate ?? null;
  const summary = {
    scenario:    `sustained_writes (POST /api/${RESOURCE})`,
    target_rate: RATE,
    duration:    DURATION,
    rps_actual:  Math.round(data.metrics?.http_reqs?.values?.rate ?? 0),
    p50_ms:      ms('med'),
    p95_ms:      ms('p(95)'),
    p99_ms:      ms('p(99)'),
    error_rate:  errRate !== null ? +(errRate * 100).toFixed(3) + '%' : null,
    dropped:     data.metrics?.dropped_iterations?.values?.count ?? 0,
    slo:         'none (informative thresholds only)',
  };
  return {
    stdout: '\n' + JSON.stringify(summary, null, 2) + '\n',
    'tests/performance/last-write-run.json': JSON.stringify(summary, null, 2) + '\n',
  };
}
