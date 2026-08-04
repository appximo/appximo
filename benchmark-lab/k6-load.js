import http from 'k6/http';
import { check } from 'k6';

const RATE = parseInt(__ENV.RATE || '100');

export const options = {
  scenarios: {
    open_model: {
      executor:        'constant-arrival-rate',
      rate:            RATE,
      timeUnit:        '1s',
      duration:        '60s',
      preAllocatedVUs: Math.min(RATE, 20),
      maxVUs:          Math.min(RATE * 2, 50),
    },
  },
  thresholds: {
    http_req_duration: ['p(95)<500'],
    http_req_failed:   ['rate<0.01'],
  },
};

const TARGET  = __ENV.TARGET_URL  || 'http://localhost:8080';
const TENANT  = __ENV.TENANT_ID   || '10';
const TOKEN   = __ENV.BENCH_TOKEN || '';

const STATUSES = ['pending', 'in_transit', 'delivered', 'returned'];

export default function () {
  const status = STATUSES[Math.floor(Math.random() * 4)];
  const page   = Math.floor(Math.random() * 10) + 1;

  const res = http.get(
    // Sin operador explícito: Appximo defaultea a eq, NestJS recibe string directo
    `${TARGET}/api/guides?filter[status]=${status}&page=${page}&per_page=20`,
    {
      headers: {
        'Authorization': `Bearer ${TOKEN}`,
        'Host':          `${TENANT}.localhost`,   // Appximo: tenant desde subdomain
        'X-Tenant-ID':  `tenant_${TENANT}`,       // NestJS: tenant desde header
      },
      tags: { endpoint: 'list' },
    }
  );

  check(res, {
    'status 200': (r) => r.status === 200,
    'has data':   (r) => {
      try { return Array.isArray(JSON.parse(r.body).data); }
      catch { return false; }
    },
  });
}

export function handleSummary(data) {
  const ms = (key) => {
    const v = data.metrics.http_req_duration.values[key];
    return (v === undefined || isNaN(v)) ? null : Math.round(v);
  };
  return {
    stdout: JSON.stringify({
      rate:   RATE,
      rps:    Math.round(data.metrics.http_reqs.values.rate),
      p50:    ms('med'),      // k6 uses 'med' for median, not 'p(50)'
      p95:    ms('p(95)'),
      p99:    ms('p(99)'),
      errors: data.metrics.http_req_failed.values.rate,
    }, null, 2) + '\n',
  };
}
