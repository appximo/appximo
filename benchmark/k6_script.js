import http from 'k6/http';
import { sleep, check } from 'k6';

/**
 * Appitools benchmark — GET /api/guides
 *
 * Usage:
 *   k6 run benchmark/k6_script.js
 *
 * Override host:
 *   k6 run -e BASE_URL=http://acme.yourdomain.com benchmark/k6_script.js
 */

const BASE_URL = __ENV.BASE_URL || 'http://localhost:8080';
const TENANT_HOST = __ENV.TENANT_HOST || 'acme.localhost';

export const options = {
  stages: [
    { duration: '30s', target: 200 },  // ramp up to 200 VUs
    { duration: '60s', target: 200 },  // hold at 200 VUs
    { duration: '10s', target: 0 },    // ramp down
  ],
  thresholds: {
    http_req_duration: ['p(99)<200'],  // 99th percentile under 200ms
    http_req_failed: ['rate<0.01'],    // error rate under 1%
  },
};

const PARAMS = {
  headers: {
    Host: TENANT_HOST,
    'X-User-Role': 'super_admin',
  },
};

export default function () {
  const res = http.get(`${BASE_URL}/api/guides`, PARAMS);

  check(res, {
    'status is 200': (r) => r.status === 200,
    'response is JSON array': (r) => r.body.startsWith('['),
  });

  sleep(0.1);
}

export function handleSummary(data) {
  return {
    'benchmark/results.json': JSON.stringify(data, null, 2),
  };
}
