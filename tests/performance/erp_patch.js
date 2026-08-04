// erp_patch.js — UPDATE-path bench for the ERP demo (tenant nimbus), used by the
// FIX-G5 gate. The state-machine transition guard (G5) runs on the UPDATE path, so
// this PATCHes a `solicitudes` row (whose `estado` field has NO state_machine) to
// measure the no-state-machine update path — it must stay no_change vs the baseline
// (the guard is a per-set-field nil check that appends NOTHING when no field carries
// a state machine). setup() creates one row; every iteration PATCHes it.
//
// Env: TARGET_URL, TENANT_ID (default nimbus), BENCH_TOKEN (REQUIRED — rrhh-admin),
//      RATE (default 50), DURATION (default 25s).
import http from 'k6/http';
import { check } from 'k6';

const RATE     = parseInt(__ENV.RATE || '50', 10);
const DURATION = __ENV.DURATION || '25s';
const TARGET   = __ENV.TARGET_URL || 'http://localhost:8080';
const TENANT   = __ENV.TENANT_ID || 'nimbus';
const TOKEN    = __ENV.BENCH_TOKEN || '';
const EMPLEADO_ID = '11111111-1111-1111-1111-111111111111';

export const options = {
  scenarios: {
    sustained_patch: {
      executor:        'constant-arrival-rate',
      rate:            RATE,
      timeUnit:        '1s',
      duration:        DURATION,
      preAllocatedVUs: Math.min(Math.max(Math.ceil(RATE / 5), 5), 200),
      maxVUs:          Math.min(RATE * 2, 500),
    },
  },
  thresholds: { http_req_failed: ['rate<0.01'] },
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)', 'max'],
};

// Create one solicitud and hand its id to every iteration.
export function setup() {
  if (!TOKEN) {
    throw new Error('BENCH_TOKEN required — mint with: appximo token --tenant nimbus --secret <JWT_SECRET> --role rrhh-admin');
  }
  const res = http.post(`${TARGET}/api/solicitudes`, JSON.stringify({
    empleado_id: EMPLEADO_ID, tipo: 'vacaciones',
    fecha_inicio: '2026-07-01T00:00:00Z', fecha_fin: '2026-07-05T00:00:00Z',
  }), { headers: { Authorization: `Bearer ${TOKEN}`, Host: `${TENANT}.localhost`, 'Content-Type': 'application/json' } });
  return { id: JSON.parse(res.body).id };
}

export default function (data) {
  // PATCH the same field to the same value — a real update round-trip (existence
  // check + ValidateWrite + CollectUpdate + RunUpdate, the path the G5 guard sits on).
  const res = http.patch(`${TARGET}/api/solicitudes/${data.id}`, JSON.stringify({ estado: 'aprobada' }), {
    headers: { Authorization: `Bearer ${TOKEN}`, Host: `${TENANT}.localhost`, 'Content-Type': 'application/json' },
  });
  check(res, { 'status is 200': (r) => r.status === 200 });
}
