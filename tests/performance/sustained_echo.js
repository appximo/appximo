// sustained_echo.js — OUTBOX-V1 write benchmark for POST /api/_echo.
//
// Drives constant-arrival-rate POST traffic against the echo endpoint, which
// calls outbox.Enqueue inside a transaction. Used to measure the cost of
// one Enqueue (one INSERT into public.outbox + tx overhead) relative to the
// CRUD write baseline (OUTBOX-BASELINE-WRITE).
//
// Tunables (env):
//   TARGET_URL   data-plane base URL        (default http://localhost:8080)
//   TENANT_ID    tenant subdomain           (default hntest)  -> Host: <id>.localhost
//   BENCH_TOKEN  HS256 JWT for that tenant  (REQUIRED)
//   RATE         requests/sec               (default 20)
//   DURATION     hold time                  (default 30s)

import http from 'k6/http';
import { check } from 'k6';

const RATE     = parseInt(__ENV.RATE || '20', 10);
const DURATION = __ENV.DURATION || '30s';
const TARGET   = __ENV.TARGET_URL || 'http://localhost:8080';
const TENANT   = __ENV.TENANT_ID || 'hntest';
const TOKEN    = __ENV.BENCH_TOKEN || '';

export const options = {
  scenarios: {
    sustained_echo: {
      executor:        'constant-arrival-rate',
      rate:            RATE,
      timeUnit:        '1s',
      duration:        DURATION,
      preAllocatedVUs: Math.min(Math.max(Math.ceil(RATE / 5), 5), 100),
      maxVUs:          Math.min(RATE * 2, 200),
    },
  },
  thresholds: {
    http_req_failed: ['rate<0.01'],
  },
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)', 'max'],
};

export function setup() {
  if (!TOKEN) {
    throw new Error('BENCH_TOKEN is required');
  }
}

export default function () {
  const payload = JSON.stringify({ msg: `echo-${__VU}-${__ITER}` });
  const res = http.post(`${TARGET}/api/_echo`, payload, {
    headers: {
      Authorization:  `Bearer ${TOKEN}`,
      Host:           `${TENANT}.localhost`,
      'Content-Type': 'application/json',
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
    scenario:    'sustained_echo (POST /api/_echo)',
    target_rate: RATE,
    duration:    DURATION,
    rps_actual:  Math.round(data.metrics?.http_reqs?.values?.rate ?? 0),
    p50_ms:      ms('med'),
    p95_ms:      ms('p(95)'),
    p99_ms:      ms('p(99)'),
    error_rate:  errRate !== null ? +(errRate * 100).toFixed(3) + '%' : null,
    dropped:     data.metrics?.dropped_iterations?.values?.count ?? 0,
  };
  return { stdout: '\n' + JSON.stringify(summary, null, 2) + '\n' };
}
