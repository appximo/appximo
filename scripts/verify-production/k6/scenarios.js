// scenarios.js — every load shape the production verification suite drives,
// in ONE k6 script so they cannot drift apart.
//
// Which shape runs is chosen by the SCENARIO env var. All of them hit the SAME
// public HTTPS URL through the SAME production chain (Caddy → engine → Postgres)
// with a real JWT, so what is measured is the PRODUCT, not the engine in
// isolation. The only exception is SCENARIO=read with TARGET_URL pointed at
// http://127.0.0.1:<engine port>, which is how the suite isolates the cost of
// the TLS + reverse-proxy layer (same script, same request, one hop removed).
//
// Env:
//   TARGET_URL   base url                      (required, e.g. https://api.example.com)
//   TOKEN        JWT for the tenant            (required)
//   SCENARIO     read|write|mix|heavy|aggregate|rest_include|graphql_nested
//                                              (default read)
//   RATE         requests/sec (open model)     (default 200)
//   DURATION     hold time                     (default 30s)
//   PER_PAGE     page size for list reads      (default 20)
//   HOST_HEADER  override the Host header      (default: derived from TARGET_URL)
//   CACHE_BUST   "1" to make every request URI unique  (default 0)
//   ORIGIN_IP    pin the target hostname to this IP     (bypass any CDN in front)
//
// About CACHE_BUST — the honesty switch. The engine ships a response cache
// (5 s TTL, keyed by tenant + request URI). Hammering ONE url therefore measures
// the cache, not Caddy→engine→Postgres. Both numbers are real and both are
// published by this suite:
//
//   CACHE_BUST=0  the production default. Models many clients loading the same
//                 dashboard/page — the cache legitimately absorbs them.
//   CACHE_BUST=1  every request carries a unique parameter, so every request
//                 reaches Postgres. Models all-distinct queries, and is the
//                 number to quote as the floor of what the stack sustains.
//
// Quoting only the cached number would be marketing; quoting only the bypassed
// one would understate the product people actually run. load.sh runs both.
//
// Open model (constant-arrival-rate) is deliberate: a closed model (fixed VUs)
// lets a slowing server throttle its own offered load, which hides saturation.
// The open model keeps the arrival rate fixed and lets latency and errors
// reveal the knee — the honest way to find capacity.

import http from 'k6/http';
import { check } from 'k6';
import { Trend } from 'k6/metrics';

const TARGET   = __ENV.TARGET_URL || 'http://localhost:8090';
const TOKEN    = __ENV.TOKEN || '';
const SCENARIO = __ENV.SCENARIO || 'read';
const RATE     = parseInt(__ENV.RATE || '200', 10);
const DURATION = __ENV.DURATION || '30s';
const PER_PAGE = parseInt(__ENV.PER_PAGE || '20', 10);

// The tenant is the Host subdomain. When we bypass Caddy and talk to the engine
// on 127.0.0.1 we must still send the public Host, or the engine resolves a
// different tenant (or none) — which would compare two different workloads.
const HOST_HEADER = __ENV.HOST_HEADER || (function () {
  const m = /^[a-z]+:\/\/([^/:]+)/.exec(TARGET);
  return m ? m[1] : '';
})();

// Per-scenario latency trends, so a mix run reports its read and write arms
// separately instead of blending two different distributions into one median.
const readLatency  = new Trend('scenario_read_ms', true);
const writeLatency = new Trend('scenario_write_ms', true);

// ORIGIN_IP pins DNS for the target hostname to a specific address. It exists
// because a domain is very often proxied by a CDN (Cloudflare, Fastly): the
// public name then resolves to the EDGE, and every latency number silently
// includes an extra internet hop plus the CDN's own TLS termination — measuring
// the CDN, not the product. Pinning the hostname to the origin IP measures the
// stack this suite is about (Caddy → engine → Postgres), still over real TLS
// with the correct SNI and the origin's own certificate.
//
// Both numbers are worth having, and run-all.sh reports both when it detects a
// CDN: the origin number is the PRODUCT's latency, the public number is what an
// end user experiences with that CDN in front.
const ORIGIN_IP = __ENV.ORIGIN_IP || '';
const TARGET_HOST = (function () {
  const m = /^[a-z]+:\/\/([^/:]+)/.exec(TARGET);
  return m ? m[1] : '';
})();

export const options = {
  hosts: (ORIGIN_IP && TARGET_HOST) ? { [TARGET_HOST]: ORIGIN_IP } : {},
  scenarios: {
    main: {
      executor:        'constant-arrival-rate',
      rate:            RATE,
      timeUnit:        '1s',
      duration:        DURATION,
      preAllocatedVUs: Math.max(10, Math.min(Math.ceil(RATE / 4), 400)),
      maxVUs:          Math.max(50, Math.min(RATE * 3, 1200)),
    },
  },
  // The loader is frequently a small box; not copying bodies into the JS runtime
  // is what lets 1 vCPU drive four-figure RPS. Bytes are still transferred over
  // the wire and counted in k6's `data_received`, so the REST-vs-GraphQL payload
  // comparison stays honest — the driver divides data_received by iterations.
  //
  // The cost of discarding: a body-level failure (notably GraphQL, which answers
  // HTTP 200 and puts errors in the body) is invisible here. That is why load.sh
  // runs a PRE-FLIGHT correctness request per scenario, body inspected, before
  // measuring — correctness is asserted once, throughput is measured cheaply.
  discardResponseBodies: true,
  // No thresholds with abortOnFail: this suite MEASURES the knee, it does not
  // gate on it. An aborting run would destroy the very data point we want.
  summaryTrendStats: ['count', 'min', 'med', 'p(90)', 'p(95)', 'p(99)', 'max', 'avg'],
  insecureSkipTLSVerify: false,
};

function headers(extra) {
  const h = {
    'Authorization': `Bearer ${TOKEN}`,
    'Content-Type': 'application/json',
  };
  if (HOST_HEADER) h['Host'] = HOST_HEADER;
  return Object.assign(h, extra || {});
}

export function setup() {
  if (!TOKEN) {
    throw new Error('TOKEN is required — mint one with: appximo token --secret "$JWT_SECRET" --tenant <id> --role admin');
  }
  return {};
}

// bust: appended to a read URL to defeat the response cache when CACHE_BUST=1.
// An unknown query parameter is ignored by the query builder (only `filter[…]`,
// `sort`, `order`, `per_page`, `after`, `include`, `count` are read), so this
// changes the CACHE KEY without changing the SQL — exactly what a cache-bypass
// control needs to do.
const CACHE_BUST = __ENV.CACHE_BUST === '1';
let seq = 0;
function bust(url) {
  if (!CACHE_BUST) return url;
  seq++;
  return `${url}&_cb=${__VU}_${seq}`;
}

// ── The request shapes ───────────────────────────────────────────────────────
// Each returns the k6 response so the caller can check + record it.

// READ_PATH parameterizes the read arm's endpoint (OPS-8): the default drives
// the verify-production bench schema (/api/orders), which only exists on a
// bare-engine bench install — a CONSUMER app points it at its own read surface
// (e.g. READ_PATH='/api/catalogo?per_page=20'). Must include its query `?` so
// the cache-bust suffix appends correctly.
const READ_PATH = __ENV.READ_PATH ||
  `/api/orders?filter[status][eq]=paid&sort=created_at&order=desc&per_page=${PER_PAGE}`;

function doRead() {
  // The single most common real request: a filtered, sorted, paginated list.
  const url = `${TARGET}${READ_PATH}`;
  return http.get(bust(url), { headers: headers(), tags: { arm: 'read' } });
}

function doWrite() {
  const body = JSON.stringify({
    status: 'pending',
    region: 'us-east',
    total: Math.round(Math.random() * 100000) / 100,
  });
  return http.post(`${TARGET}/api/orders`, body, { headers: headers(), tags: { arm: 'write' } });
}

function doHeavy() {
  // The "many records" question: a deep filtered page over the full table with a
  // secondary predicate, i.e. what a real dashboard query looks like once the
  // table is no longer small.
  const url = `${TARGET}/api/orders?filter[status][eq]=paid&filter[region][eq]=us-east`
    + `&sort=created_at&order=desc&per_page=${PER_PAGE}`;
  return http.get(bust(url), { headers: headers(), tags: { arm: 'heavy' } });
}

function doAggregate() {
  const url = `${TARGET}/api/orders/aggregate?count&sum=total&avg=total&group_by=status`;
  return http.get(bust(url), { headers: headers(), tags: { arm: 'aggregate' } });
}

function doRestInclude() {
  // REST's one-round-trip answer to "orders with their customer and their items".
  const url = `${TARGET}/api/orders?include=customer,items&per_page=${PER_PAGE}`;
  return http.get(bust(url), { headers: headers(), tags: { arm: 'rest_include' } });
}

function doGraphqlNested() {
  // The SAME logical query as doRestInclude, expressed as one GraphQL document.
  // Field-for-field equivalent so the comparison is apples to apples.
  const query = `{ orders(per_page: ${PER_PAGE}) { data { id status region total created_at `
    + `customer { id name email city tier } items { id product qty price } } } }`;
  return http.post(`${TARGET}/graphql`, JSON.stringify({ query }),
    { headers: headers(), tags: { arm: 'graphql_nested' } });
}

// REST's OTHER shape for the same data: the N+1 a client without ?include= is
// forced into — one list call, then one subroute call per row. This is the
// round-trip cost GraphQL is usually claimed to save, measured rather than
// assumed. Kept to a small page so one iteration is one user action.
function doRestNPlus1() {
  const listRes = http.get(`${TARGET}/api/orders?per_page=5`, { headers: headers(), tags: { arm: 'rest_n1_list' } });
  let calls = 1;
  // With discardResponseBodies we cannot read ids, so we issue the follow-up
  // round trips against the relation subroute of a known-good id supplied by the
  // driver. The POINT of this arm is the ROUND-TRIP COUNT, not the ids.
  const seed = __ENV.SAMPLE_ORDER_ID;
  if (seed) {
    for (let i = 0; i < 5; i++) {
      http.get(`${TARGET}/api/orders/${seed}/customer`, { headers: headers(), tags: { arm: 'rest_n1_sub' } });
      calls++;
    }
  }
  return listRes;
}

export default function () {
  let res;
  let arm = SCENARIO;

  switch (SCENARIO) {
    case 'write':
      res = doWrite(); writeLatency.add(res.timings.duration); break;
    case 'heavy':
      res = doHeavy(); readLatency.add(res.timings.duration); break;
    case 'aggregate':
      res = doAggregate(); readLatency.add(res.timings.duration); break;
    case 'rest_include':
      res = doRestInclude(); readLatency.add(res.timings.duration); break;
    case 'graphql_nested':
      res = doGraphqlNested(); readLatency.add(res.timings.duration); break;
    case 'rest_n1':
      res = doRestNPlus1(); readLatency.add(res.timings.duration); break;
    case 'mix': {
      // 80/20 read/write — the realistic shape of an app's traffic.
      if (Math.random() < 0.8) { res = doRead(); readLatency.add(res.timings.duration); arm = 'read'; }
      else                     { res = doWrite(); writeLatency.add(res.timings.duration); arm = 'write'; }
      break;
    }
    default:
      res = doRead(); readLatency.add(res.timings.duration); break;
  }

  // Transport-level success only (bodies are discarded — see the note on
  // discardResponseBodies; load.sh asserts body correctness pre-flight).
  check(res, { [`${arm} ok`]: (r) => r.status >= 200 && r.status < 300 });
}

// handleSummary writes ONE compact JSON per run — the thing load.sh consumes.
// k6's stdout summary is for humans; parsing it would be brittle. This is the
// contract between the k6 layer and the shell layer.
//
// Note `rps_achieved` next to `rate_requested`: with an open model, a saturated
// server makes k6 fall behind its own schedule (dropped iterations). Reporting
// only the requested rate would claim throughput the box never delivered, so the
// ladder's saturation check reads the ACHIEVED number.
export function handleSummary(data) {
  const m = data.metrics || {};
  const dur = m.http_req_duration || {};
  const v = dur.values || {};
  const reqs = (m.http_reqs && m.http_reqs.values) || {};
  const failed = (m.http_req_failed && m.http_req_failed.values) || {};
  const recv = (m.data_received && m.data_received.values) || {};
  const sent = (m.data_sent && m.data_sent.values) || {};
  const iters = (m.iterations && m.iterations.values) || {};
  const waiting = ((m.http_req_waiting || {}).values) || {};
  const tls = ((m.http_req_tls_handshaking || {}).values) || {};
  const dropped = (m.dropped_iterations && m.dropped_iterations.values) || {};
  // Per-arm trends: a `mix` run blends two very different distributions, and one
  // median over both hides which arm is actually slow. Reporting them separately
  // is what turns "the mix scenario is slow" into "the READS in the mix are slow".
  const rd = (m.scenario_read_ms && m.scenario_read_ms.values) || {};
  const wr = (m.scenario_write_ms && m.scenario_write_ms.values) || {};

  const count = reqs.count || 0;
  const out = {
    scenario: SCENARIO,
    target: TARGET,
    cache_bust: CACHE_BUST,
    origin_ip: ORIGIN_IP || null,
    per_page: PER_PAGE,
    rate_requested: RATE,
    duration: DURATION,
    requests: count,
    rps_achieved: reqs.rate ? Math.round(reqs.rate * 100) / 100 : null,
    dropped_iterations: dropped.count || 0,
    iterations: iters.count || 0,
    error_rate: failed.rate != null ? failed.rate : null,
    latency_ms: {
      min: v.min, p50: v.med, p90: v['p(90)'], p95: v['p(95)'],
      p99: v['p(99)'], max: v.max, avg: v.avg,
    },
    // Server-side time (waiting = TTFB minus connect/TLS): separating it from the
    // total is what lets the report say whether latency is the box or the network.
    waiting_ms: { p50: waiting.med, p95: waiting['p(95)'], avg: waiting.avg },
    tls_handshake_ms: { avg: tls.avg || 0 },
    bytes_received: recv.count || 0,
    bytes_sent: sent.count || 0,
    bytes_per_request: count ? Math.round((recv.count || 0) / count) : null,
    arms: {
      read:  rd.med !== undefined ? { n: rd.count || null, p50: rd.med, p95: rd['p(95)'] } : null,
      write: wr.med !== undefined ? { n: wr.count || null, p50: wr.med, p95: wr['p(95)'] } : null,
    },
  };
  const path = __ENV.SUMMARY_OUT || 'k6-summary.json';
  const res = {};
  res[path] = JSON.stringify(out, null, 2);
  res.stdout = `  ${SCENARIO} @${RATE}rps → ${count} reqs, p50 ${(v.med || 0).toFixed(2)}ms, `
    + `p95 ${(v['p(95)'] || 0).toFixed(2)}ms, err ${((failed.rate || 0) * 100).toFixed(2)}%\n`;
  return res;
}
