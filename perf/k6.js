// Pickhour perf test — single script, PROFILE env selects the scenario:
//   smoke  2 VUs, short — verifies the script/endpoints before real runs
//   load   constant arrival rate (RPS env), checks SLO thresholds
//   stress ramping VUs until breakdown (no pass/fail threshold)
//   spike  idle -> burst -> idle, watches recovery
//   soak   low constant rate for DURATION (memory/conn leaks)
//
// Writes /perf/out/summary.json + /perf/out/report.html for artifacts.

import http from 'k6/http';
import { check, sleep } from 'k6';
import { Rate, Trend } from 'k6/metrics';
import { textSummary } from 'https://jslib.k6.io/k6-summary/0.0.2/index.js';

const BASE = __ENV.BASE_URL || 'http://localhost:18080';
const PROFILE = __ENV.PROFILE || 'smoke';
const DURATION = __ENV.DURATION || '1m';
const RPS = parseInt(__ENV.RPS || '30', 10);
const VUS = parseInt(__ENV.VUS || '20', 10);

const errors = new Rate('errors');
const spotsT = new Trend('spots_list_ms', true);
const recsT = new Trend('recommendations_ms', true);
const detailT = new Trend('spot_detail_ms', true);

// Amsterdam-area viewports; rotated per iteration for varied bbox shapes.
const BBOXES = [
  '4.70,52.30,4.85,52.42',
  '4.85,52.30,5.00,52.42',
  '4.70,52.36,4.85,52.50',
  '4.85,52.36,5.00,52.50',
  '4.78,52.33,4.95,52.47',
];
const CENTERS = [
  { lat: 52.37, lon: 4.90 },
  { lat: 52.35, lon: 4.85 },
  { lat: 52.42, lon: 4.95 },
];

export const options = {
  scenarios: scenarioFor(PROFILE),
  thresholds: thresholdsFor(PROFILE),
};

function scenarioFor(p) {
  switch (p) {
    case 'load':
      return {
        load: {
          executor: 'constant-arrival-rate',
          rate: RPS,
          timeUnit: '1s',
          duration: DURATION,
          preAllocatedVUs: Math.max(VUS, Math.ceil(RPS / 2)),
          maxVUs: VUS * 4,
        },
      };
    case 'stress':
      return {
        stress: {
          executor: 'ramping-vus',
          startVUs: 0,
          stages: [
            { duration: '1m', target: Math.ceil(VUS / 2) },
            { duration: DURATION, target: VUS },
            { duration: '1m', target: 0 },
          ],
        },
      };
    case 'spike':
      return {
        spike: {
          executor: 'ramping-vus',
          startVUs: 0,
          stages: [
            { duration: '30s', target: 2 },
            { duration: '10s', target: VUS * 5 }, // burst
            { duration: '1m', target: VUS * 5 },
            { duration: '10s', target: 2 },     // drop — watch recovery
            { duration: '1m', target: 2 },
          ],
        },
      };
    case 'soak':
      return {
        soak: {
          executor: 'constant-arrival-rate',
          rate: Math.max(5, Math.floor(RPS / 5)), // gentle sustained load
          timeUnit: '1s',
          duration: DURATION,
          preAllocatedVUs: 10,
          maxVUs: VUS,
        },
      };
    default: // smoke
      return {
        smoke: { executor: 'constant-vus', vus: 2, duration: DURATION },
      };
  }
}

function thresholdsFor(p) {
  if (p === 'stress' || p === 'spike' || p === 'soak') {
    // Exploratory profiles: report metrics, don't gate the run.
    return { errors: ['rate<0.20'] };
  }
  return {
    errors: ['rate<0.01'],
    http_req_failed: ['rate<0.01'],
    spots_list_ms: ['p(95)<1500'],
    recommendations_ms: ['p(95)<1500'],
    http_req_duration: ['p(95)<2000'],
  };
}

// Grab one real spot id so /v1/spots/:id and /forecast hit real rows.
export function setup() {
  const res = http.get(`${BASE}/v1/spots?bbox=4.70,52.30,4.90,52.45`);
  if (res.status !== 200) {
    throw new Error(`setup: /v1/spots -> ${res.status}: ${res.body.slice(0, 200)}`);
  }
  const fc = res.json();
  const id = fc.features && fc.features[0] && fc.features[0].properties
    ? fc.features[0].properties.id
    : null;
  return { spotId: id };
}

export default function (data) {
  const i = Math.floor(Math.random() * BBOXES.length);
  const roll = Math.random();

  if (roll < 0.5) {
    // Map browse — the hottest endpoint.
    const t = http.get(`${BASE}/v1/spots?bbox=${BBOXES[i]}&hours=12`, {
      tags: { name: 'spots_list' },
    });
    const ok = check(t, { 'spots 200': (r) => r.status === 200 });
    spotsT.add(t.timings.duration);
    errors.add(!ok);
  } else if (roll < 0.75) {
    const c = CENTERS[i % CENTERS.length];
    const t = http.get(
      `${BASE}/v1/recommendations?lat=${c.lat}&lon=${c.lon}&radius=5000`,
      { tags: { name: 'recommendations' } },
    );
    const ok = check(t, { 'recs 200': (r) => r.status === 200 });
    recsT.add(t.timings.duration);
    errors.add(!ok);
  } else if (data.spotId) {
    const which = Math.random() < 0.5 ? '' : '/forecast';
    const t = http.get(`${BASE}/v1/spots/${data.spotId}${which}`, {
      tags: { name: which ? 'spot_forecast' : 'spot_detail' },
    });
    const ok = check(t, { 'spot 200': (r) => r.status === 200 });
    detailT.add(t.timings.duration);
    errors.add(!ok);
  } else {
    sleep(0.1);
  }
}

export function handleSummary(data) {
  return {
    '/perf/out/summary.json': JSON.stringify(data, null, 2),
    '/perf/out/report.html': htmlReport(data),
    stdout: textSummary(data, { indent: ' ', enableColors: true }),
  };
}

// Minimal HTML report — enough to eyeball in CI artifacts without the
// full k6 cloud reporter.
function htmlReport(d) {
  const m = d.metrics || {};
  const row = (name) => {
    const x = m[name] && m[name].values;
    if (!x) return `<tr><td>${name}</td><td colspan="4">-</td></tr>`;
    const f = (v) => (v === undefined ? '-' : Math.round(v * 100) / 100);
    return `<tr><td>${name}</td><td>${f(x.avg)}</td><td>${f(x['p(95)'])}</td><td>${f(x.max)}</td><td>${f(x.rate)}</td></tr>`;
  };
  const names = ['http_reqs', 'http_req_duration', 'http_req_failed',
    'errors', 'spots_list_ms', 'recommendations_ms', 'spot_detail_ms'];
  return `<!doctype html><meta charset="utf-8"><title>k6 ${PROFILE}</title>
<h1>k6 ${PROFILE} — ${BASE}</h1>
<table border="1" cellpadding="6" cellspacing="0">
<tr><th>metric</th><th>avg</th><th>p95</th><th>max</th><th>rate</th></tr>
${names.map(row).join('\n')}
</table>`;
}
