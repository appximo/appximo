// erp_writes.js — WRITE-path bench for the ERP demo (tenant nimbus), used by the
// API-PRODUCTIVA-V1 CORS gate. POSTs valid `solicitudes` (no unique constraint, so
// rows simply accumulate on the disposable bench tenant — no cross-run collisions)
// with a rrhh-admin token (unrestricted create) and an Origin header, so the
// measurement includes the full CORS cost on the new binary. tipo=vacaciones keeps
// the before_create JS hook on its cheap path (no motivo requirement).
//
// solicitudes has events:["create"], so each write also emits to the outbox — a
// realistic ERP write. That cost is CONSTANT across baseline and new, so the
// pre/post delta still isolates CORS.
//
// Env: TARGET_URL, TENANT_ID (default nimbus), BENCH_TOKEN (REQUIRED — rrhh-admin),
//      RATE (default 30), DURATION (default 15s), ORIGIN.
import http from 'k6/http';
import { check } from 'k6';

const RATE     = parseInt(__ENV.RATE || '30', 10);
const DURATION = __ENV.DURATION || '15s';
const TARGET   = __ENV.TARGET_URL || 'http://localhost:8080';
const TENANT   = __ENV.TENANT_ID || 'nimbus';
const TOKEN    = __ENV.BENCH_TOKEN || '';
const ORIGIN   = __ENV.ORIGIN || 'https://app.example.com';

// A stable, well-formed UUID for empleado_id (no FK constraint in the engine).
const EMPLEADO_ID = '11111111-1111-1111-1111-111111111111';

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
  thresholds: { http_req_failed: ['rate<0.01'] },
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)', 'max'],
};

export function setup() {
  if (!TOKEN) {
    throw new Error('BENCH_TOKEN required — mint with: appitools token --tenant nimbus --secret <JWT_SECRET> --role rrhh-admin');
  }
}

export default function () {
  const body = JSON.stringify({
    empleado_id:  EMPLEADO_ID,
    tipo:         'vacaciones',
    fecha_inicio: '2026-07-01T00:00:00Z',
    fecha_fin:    '2026-07-05T00:00:00Z',
  });
  const res = http.post(`${TARGET}/api/solicitudes`, body, {
    headers: {
      Authorization:  `Bearer ${TOKEN}`,
      Host:           `${TENANT}.localhost`,
      Origin:         ORIGIN,
      'Content-Type': 'application/json',
    },
  });
  check(res, { 'status is 201': (r) => r.status === 201 });
}
