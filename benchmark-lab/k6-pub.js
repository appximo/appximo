// k6-pub.js — S46 public comparative benchmark (Appximo vs NestJS).
//
// One script drives BOTH stacks so the request shape is provably identical:
//   GET /api/guides?filter[status]=pending&sort=created_at&order=desc&per_page=20
//   - Appximo: tenant from the Host subdomain, sort/order honored by the engine
//   - NestJS:    tenant from the verified JWT claim, ORDER BY created_at DESC is
//                hardcoded in the controller (sort/order params are ignored)
//   Both return the 20 newest 'pending' rows for tenant 10.
//
// Unlike the SLO gate script (sustained_2krps.js) there are NO abortOnFail
// thresholds: at saturation levels we WANT the full 30s latency distribution,
// not an aborted run. Saturation is judged after import (p95 > 100ms or
// error_rate > 1%).
//
// Env: RATE DURATION TARGET_URL TENANT_ID BENCH_TOKEN ENDPOINT

import http from 'k6/http';

const RATE     = parseInt(__ENV.RATE || '500', 10);
const DURATION = __ENV.DURATION || '30s';
const TARGET   = __ENV.TARGET_URL || 'http://localhost:8080';
const TENANT   = __ENV.TENANT_ID || '10';
const TOKEN    = __ENV.BENCH_TOKEN || '';
const ENDPOINT = __ENV.ENDPOINT ||
  '/api/guides?filter[status]=pending&sort=created_at&order=desc&per_page=20';
// NO_CACHE=1 sends Cache-Control: no-cache, which the Appximo response
// cache honors as a full bypass (pkg/cache/response_cache.go): every request
// reaches Postgres. Used for the cache-disabled headline variant (§4.4).
const NO_CACHE = __ENV.NO_CACHE === '1';

export const options = {
  // The load generator is a 1-vCPU box: every byte of response body k6 copies
  // into JS land costs CPU that inflates measured latency at high rates.
  // Bodies are discarded (the server still builds and sends the full payload;
  // http_req_duration still covers the complete response). Errors are counted
  // by the built-in http_req_failed metric (status >= 400), so no per-request
  // check() is needed.
  discardResponseBodies: true,
  scenarios: {
    pub: {
      executor:        'constant-arrival-rate',
      rate:            RATE,
      timeUnit:        '1s',
      duration:        DURATION,
      // At saturation latency can exceed 200ms; pre-allocate enough VUs that
      // arrival-rate scheduling never stalls waiting for VU init.
      preAllocatedVUs: Math.min(Math.max(50, Math.ceil(RATE / 4)), 800),
      maxVUs:          Math.min(RATE * 2, 3000),
    },
  },
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)', 'max'],
};

export function setup() {
  if (!TOKEN) throw new Error('BENCH_TOKEN is required');
}

export default function () {
  // status >= 400 is tracked by the built-in http_req_failed metric.
  const headers = {
    Authorization: `Bearer ${TOKEN}`,
    Host:          `${TENANT}.localhost`,
  };
  if (NO_CACHE) headers['Cache-Control'] = 'no-cache';
  http.get(`${TARGET}${ENDPOINT}`, { headers });
}

export function handleSummary(data) {
  const ms = (k) => {
    const v = data.metrics?.http_req_duration?.values?.[k];
    return v === undefined || isNaN(v) ? null : Math.round(v * 1000) / 1000;
  };
  const summary = {
    target_rate: RATE,
    duration:    DURATION,
    rps_actual:  Math.round(data.metrics?.http_reqs?.values?.rate ?? 0),
    p50_ms:      ms('med'),
    p95_ms:      ms('p(95)'),
    p99_ms:      ms('p(99)'),
    error_rate:  data.metrics?.http_req_failed?.values?.rate ?? null,
    dropped:     data.metrics?.dropped_iterations?.values?.count ?? 0,
    max_vus:     data.metrics?.vus_max?.values?.max ?? null,
  };
  return { stdout: '\n' + JSON.stringify(summary, null, 2) + '\n' };
}
