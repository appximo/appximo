// sustained_2krps.js — S37 performance SLO gate (TESTING_PLAN.md, Escenario 5).
//
// Drives constant-arrival-rate traffic against the data plane with JWT + RBAC +
// multi-tenancy active, and enforces the project SLOs as k6 thresholds:
//
//   p95  < 15ms      (http_req_duration, abortOnFail)
//   errors < 1%      (http_req_failed,   abortOnFail)
//
// Any breach makes k6 exit 99 — the CI/`make test-perf` gate fails hard.
//
// Tunables (env):
//   TARGET_URL   data-plane base URL        (default http://localhost:8080)
//   TENANT_ID    tenant subdomain           (default acme)  -> Host: <id>.localhost
//   BENCH_TOKEN  HS256 JWT for that tenant  (REQUIRED; mint with `appximo token`)
//   RATE         requests/sec               (default 2000 — the SLO target)
//   DURATION     hold time                  (default 60s)
//   ENDPOINT     path + query               (default /api/guides?filter[status][eq]=pending&per_page=20)
//
// The 2000 RPS / p95<15ms target is calibrated to prod hardware (DO 2 vCPU). On a
// shared CI runner, lower RATE via env (the threshold still enforces the SLO at
// whatever rate is driven) — see tests/performance/README.md.

import http from 'k6/http';
import { check } from 'k6';

const RATE     = parseInt(__ENV.RATE || '2000', 10);
const DURATION = __ENV.DURATION || '60s';
const TARGET   = __ENV.TARGET_URL || 'http://localhost:8080';
const TENANT   = __ENV.TENANT_ID || 'acme';
const TOKEN    = __ENV.BENCH_TOKEN || '';
const ENDPOINT = __ENV.ENDPOINT || '/api/guides?filter[status][eq]=pending&per_page=20';

export const options = {
  scenarios: {
    sustained: {
      executor:        'constant-arrival-rate',
      rate:            RATE,
      timeUnit:        '1s',
      duration:        DURATION,
      preAllocatedVUs: Math.min(Math.ceil(RATE / 10), 500),
      maxVUs:          Math.min(RATE * 2, 2000),
    },
  },
  // abortOnFail: a sustained SLO breach aborts the run immediately and yields a
  // non-zero (99) exit code, so the gate fails fast instead of burning the full hold.
  thresholds: {
    http_req_duration: [{ threshold: 'p(95)<15', abortOnFail: true, delayAbortEval: '10s' }],
    http_req_failed:   [{ threshold: 'rate<0.01', abortOnFail: true, delayAbortEval: '10s' }],
  },
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)', 'max'],
};

export function setup() {
  if (!TOKEN) {
    throw new Error('BENCH_TOKEN is required — mint one with: appximo token --tenant <id> --secret <JWT_SECRET> --role super_admin');
  }
}

export default function () {
  const res = http.get(`${TARGET}${ENDPOINT}`, {
    headers: {
      Authorization: `Bearer ${TOKEN}`,
      Host:          `${TENANT}.localhost`,
    },
  });
  check(res, { 'status is 200': (r) => r.status === 200 });
}

export function handleSummary(data) {
  const ms = (key) => {
    const v = data.metrics?.http_req_duration?.values?.[key];
    return v === undefined || isNaN(v) ? null : Math.round(v * 100) / 100;
  };
  const errRate = data.metrics?.http_req_failed?.values?.rate ?? null;
  const summary = {
    target_rate: RATE,
    duration:    DURATION,
    rps_actual:  Math.round(data.metrics?.http_reqs?.values?.rate ?? 0),
    p50_ms:      ms('med'),
    p95_ms:      ms('p(95)'),
    p99_ms:      ms('p(99)'),
    error_rate:  errRate !== null ? +(errRate * 100).toFixed(3) + '%' : null,
    dropped:     data.metrics?.dropped_iterations?.values?.count ?? 0,
    slo:         { p95_lt_15ms: 'enforced', error_rate_lt_1pct: 'enforced' },
  };
  return {
    stdout: '\n' + JSON.stringify(summary, null, 2) + '\n',
    'tests/performance/last-run.json': JSON.stringify(summary, null, 2) + '\n',
  };
}
