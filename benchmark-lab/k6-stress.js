import http from 'k6/http';
import { check } from 'k6';

const RATE = parseInt(__ENV.RATE || '600');

export const options = {
  scenarios: {
    stress: {
      executor:        'constant-arrival-rate',
      rate:            RATE,
      timeUnit:        '1s',
      duration:        '30s',
      preAllocatedVUs: Math.min(Math.ceil(RATE / 10), 200),
      maxVUs:          Math.min(RATE * 2, 1000),
    },
  },
  thresholds: {
    'http_req_duration': ['p(95)<2000', 'p(99)<5000'],
    'http_req_failed':   ['rate<0.05'],
  },
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)', 'max'],
};

const TARGET = __ENV.TARGET_URL || 'http://localhost:8080';
const TENANT = __ENV.TENANT_ID  || 'acme';
const TOKEN  = __ENV.BENCH_TOKEN || '';

const STATUSES = ['pending', 'in_transit', 'delivered', 'returned'];

export default function () {
  const status = STATUSES[Math.floor(Math.random() * 4)];
  const res = http.get(
    `${TARGET}/api/guides?filter[status]=${status}&page=1&per_page=20`,
    {
      headers: {
        'Authorization': `Bearer ${TOKEN}`,
        'Host':          `${TENANT}.localhost`,
      },
    }
  );
  check(res, { 'status 200': (r) => r.status === 200 });
}

export function handleSummary(data) {
  const ms = (key) => {
    const v = data.metrics?.http_req_duration?.values?.[key];
    return (v === undefined || isNaN(v)) ? null : Math.round(v);
  };
  const errRate = data.metrics?.http_req_failed?.values?.rate ?? null;
  return {
    stdout: JSON.stringify({
      rate:       RATE,
      rps_actual: Math.round(data.metrics.http_reqs.values.rate),
      p50:        ms('med'),
      p95:        ms('p(95)'),
      p99:        ms('p(99)'),
      errors_pct: errRate !== null ? (errRate * 100).toFixed(2) + '%' : null,
      dropped:    data.metrics.dropped_iterations?.values?.count ?? 0,
    }, null, 2) + '\n',
  };
}
