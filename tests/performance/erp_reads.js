// erp_reads.js — READ-path bench for the ERP demo (tenant nimbus), used by the
// API-PRODUCTIVA-V1 CORS gate. Identical request shape to sustained_2krps.js but
// (a) targets an ERP resource and (b) sends an Origin header so the measurement
// includes the FULL CORS middleware cost (origin match + Allow-Origin/Vary writes)
// on the new binary — the baseline binary (no CORS) just ignores the header, so the
// pre/post delta isolates exactly what CORS adds to the read hot path.
//
// Env: TARGET_URL, TENANT_ID (default nimbus), BENCH_TOKEN (REQUIRED — rrhh-admin),
//      RATE (default 50), DURATION (default 30s), ENDPOINT, ORIGIN.
import http from 'k6/http';
import { check } from 'k6';

const RATE     = parseInt(__ENV.RATE || '50', 10);
const DURATION = __ENV.DURATION || '30s';
const TARGET   = __ENV.TARGET_URL || 'http://localhost:8080';
const TENANT   = __ENV.TENANT_ID || 'nimbus';
const TOKEN    = __ENV.BENCH_TOKEN || '';
const ENDPOINT = __ENV.ENDPOINT || '/api/empleados?per_page=20';
const ORIGIN   = __ENV.ORIGIN || 'https://app.example.com';

export const options = {
  scenarios: {
    sustained: {
      executor:        'constant-arrival-rate',
      rate:            RATE,
      timeUnit:        '1s',
      duration:        DURATION,
      preAllocatedVUs: Math.min(Math.ceil(RATE / 5), 200),
      maxVUs:          Math.min(RATE * 2, 500),
    },
  },
  // Informative only — the bench protocol consumes the latency distribution; a
  // breach must not abort the run on a shared/steal-prone host.
  thresholds: { http_req_failed: ['rate<0.01'] },
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)', 'max'],
};

export function setup() {
  if (!TOKEN) {
    throw new Error('BENCH_TOKEN required — mint with: appitools token --tenant nimbus --secret <JWT_SECRET> --role rrhh-admin');
  }
}

export default function () {
  const res = http.get(`${TARGET}${ENDPOINT}`, {
    headers: {
      Authorization: `Bearer ${TOKEN}`,
      Host:          `${TENANT}.localhost`,
      Origin:        ORIGIN,
    },
  });
  check(res, { 'status is 200': (r) => r.status === 200 });
}
