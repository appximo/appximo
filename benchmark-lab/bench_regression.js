import http from 'k6/http';
import { check } from 'k6';

const RATE = parseInt(__ENV.RATE || '500');
const TARGET = __ENV.TARGET_URL || 'http://PROD-VPS:8080';
const TENANT = __ENV.TENANT_ID || '10';
const TOKEN = __ENV.BENCH_TOKEN || '';
const STATUSES = ['pending', 'in_transit', 'delivered', 'returned'];

export const options = {
  scenarios: {
    stress: {
      executor: 'constant-arrival-rate',
      rate: RATE,
      timeUnit: '1s',
      duration: '30s',
      preAllocatedVUs: Math.min(Math.max(Math.ceil(RATE / 5), 50), 600),
      maxVUs: Math.min(RATE * 3, 2000),
    },
  },
  summaryTrendStats: ['avg', 'med', 'p(90)', 'p(95)', 'p(99)', 'p(99.9)', 'max'],
};

export default function () {
  const status = STATUSES[Math.floor(Math.random() * 4)];
  const res = http.get(
    `${TARGET}/api/guides?filter[status]=${status}&page=1&per_page=20`,
    { headers: { Authorization: `Bearer ${TOKEN}`, Host: `${TENANT}.localhost` } }
  );
  check(res, { 'status 200': (r) => r.status === 200 });
}

export function handleSummary(data) {
  const ms = (k) => {
    const v = data.metrics?.http_req_duration?.values?.[k];
    return v === undefined || isNaN(v) ? null : Math.round(v * 100) / 100;
  };
  const errRate = data.metrics?.http_req_failed?.values?.rate ?? null;
  return {
    stdout: JSON.stringify({
      rate: RATE,
      rps_actual: Math.round(data.metrics.http_reqs.values.rate),
      checks_ok: data.metrics?.checks?.values?.rate ?? null,
      p50: ms('med'),
      p90: ms('p(90)'),
      p95: ms('p(95)'),
      p99: ms('p(99)'),
      p999: ms('p(99.9)'),
      max: ms('max'),
      errors_pct: errRate !== null ? (errRate * 100).toFixed(3) + '%' : null,
      dropped: data.metrics.dropped_iterations?.values?.count ?? 0,
    }, null, 2) + '\n',
  };
}
