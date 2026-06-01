/**
 * Appitools benchmark — 3 escenarios secuenciales
 *
 * Uso:
 *   k6 run -e BENCH_TOKEN="$BENCH_TOKEN" benchmark/k6_script.js
 *
 * Variables de entorno:
 *   BASE_URL    (default: http://localhost:8080)
 *   TENANT_HOST (default: acme.localhost)
 *   BENCH_TOKEN (required — genera con: appitools token --role super_admin --tenant acme --secret "$JWT_SECRET")
 */
import http from 'k6/http';
import { check } from 'k6';
import { textSummary } from 'https://jslib.k6.io/k6-summary/0.0.2/index.js';

const BASE        = __ENV.BASE_URL    || 'http://localhost:8080';
const HOST        = __ENV.TENANT_HOST || 'acme.localhost';
const BENCH_TOKEN = __ENV.BENCH_TOKEN || '';

export const options = {
  scenarios: {
    // Escenario A: throughput máximo, sin sleep, 20 VUs
    max_throughput: {
      executor: 'constant-vus',
      vus: 20,
      duration: '30s',
      startTime: '0s',
      exec: 'listGuides',
    },
    // Escenario B: carga sostenida realista, ramp 0→50→0
    sustained_load: {
      executor: 'ramping-vus',
      stages: [
        { duration: '15s', target: 50 },
        { duration: '45s', target: 50 },
        { duration: '10s', target: 0 },
      ],
      startTime: '35s',
      exec: 'listGuides',
    },
    // Escenario C: mix lectura/escritura con hook JS activo
    read_write_mix: {
      executor: 'constant-vus',
      vus: 10,
      duration: '30s',
      startTime: '110s',
      exec: 'readWriteMix',
    },
  },
  thresholds: {
    'http_req_duration{scenario:max_throughput}': ['p(99)<500'],
    'http_req_duration{scenario:sustained_load}': ['p(99)<300'],
    'http_req_duration{scenario:read_write_mix}': ['p(99)<500'],
    http_req_failed: ['rate<0.01'],
  },
};

const AUTH = BENCH_TOKEN ? { 'Authorization': `Bearer ${BENCH_TOKEN}` } : {};

const HEADERS = {
  headers: { Host: HOST, ...AUTH },
};
const POST_HEADERS = {
  headers: { Host: HOST, 'Content-Type': 'application/json', ...AUTH },
};

export function listGuides() {
  const res = http.get(`${BASE}/api/guides`, HEADERS);
  check(res, { 'status 200': r => r.status === 200 });
}

let counter = 0;
export function readWriteMix() {
  if (++counter % 5 === 0) {
    // 20% writes — POST con hook JS before_create activo
    const res = http.post(
      `${BASE}/api/guides`,
      JSON.stringify({ code: `LIVE-${__VU}-${__ITER}`, origin: 'Alpha', destination: 'Beta' }),
      POST_HEADERS,
    );
    check(res, { 'create 201': r => r.status === 201 });
  } else {
    // 80% reads
    const res = http.get(`${BASE}/api/guides`, HEADERS);
    check(res, {
      'list 200': r => r.status === 200,
      'has data array': r => {
        try { return Array.isArray(JSON.parse(r.body).data); }
        catch { return false; }
      },
    });
  }
}

export function handleSummary(data) {
  return {
    'benchmark/results.json': JSON.stringify(data, null, 2),
    stdout: textSummary(data, { indent: ' ', enableColors: true }),
  };
}
