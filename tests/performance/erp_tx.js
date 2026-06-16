// erp_tx.js — ATOMIC TRANSACTION bench (G4): POSTs /api/transaction with a 2-op
// batch (two `solicitudes` creates) to the ERP demo (tenant nimbus) as rrhh-admin.
// This measures the COST of an atomic multi-resource transaction — BEGIN/COMMIT +
// per-op RBAC/validation/emit on ONE Postgres transaction — versus a single-op
// create (erp_writes.js). It is REPORTED, not gated as no_change: a transaction of N
// ops is expected to cost more than one standalone write (the atomic boundary is the
// point). solicitudes has events:["create"], so each op also emits to the outbox in
// the same tx — a realistic atomic write.
//
// Env: TARGET_URL, TENANT_ID (default nimbus), BENCH_TOKEN (REQUIRED — rrhh-admin),
//      RATE (default 30), DURATION (default 20s).
import http from 'k6/http';
import { check } from 'k6';

const RATE     = parseInt(__ENV.RATE || '30', 10);
const DURATION = __ENV.DURATION || '20s';
const TARGET   = __ENV.TARGET_URL || 'http://localhost:8080';
const TENANT   = __ENV.TENANT_ID || 'nimbus';
const TOKEN    = __ENV.BENCH_TOKEN || '';

const EMPLEADO_ID = '11111111-1111-1111-1111-111111111111';

export const options = {
  scenarios: {
    sustained_tx: {
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
  const leg = {
    op: 'create', resource: 'solicitudes',
    data: { empleado_id: EMPLEADO_ID, tipo: 'vacaciones', fecha_inicio: '2026-07-01T00:00:00Z', fecha_fin: '2026-07-05T00:00:00Z' },
  };
  const body = JSON.stringify({ operations: [leg, leg] });
  const res = http.post(`${TARGET}/api/transaction`, body, {
    headers: {
      Authorization:  `Bearer ${TOKEN}`,
      Host:           `${TENANT}.localhost`,
      'Content-Type': 'application/json',
    },
  });
  check(res, { 'status is 200': (r) => r.status === 200 });
}
