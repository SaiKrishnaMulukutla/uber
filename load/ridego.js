// RideGo load test. One scenario per run, selected via SCENARIO env:
//   nearby   -> GET  /drivers/nearby           (Redis GEOSEARCH, read)
//   location -> PATCH /drivers/{id}/location    (Redis GEOADD+SET, write)
//   estimate -> POST /trips/estimate            (haversine compute + cached read)
//   request  -> POST /trips/request             (Postgres insert + Kafka publish)
//
// Tokens are minted in-process (HS256) using the same JWT_SECRET the services use,
// so we skip the bcrypt login path entirely. Target is set by BASE_URL
// (a service port for the direct number, or the :8000 gateway).
import http from 'k6/http';
import { check } from 'k6';
import crypto from 'k6/crypto';
import encoding from 'k6/encoding';

const SECRET = __ENV.JWT_SECRET;
const BASE = __ENV.BASE_URL || 'http://localhost:8082';
const SCENARIO = __ENV.SCENARIO || 'nearby';
const PEAK_VUS = parseInt(__ENV.PEAK_VUS || '100', 10);

// Bangalore-ish center; drivers seeded within a few km of this.
const CENTER = { lat: 12.9716, lng: 77.5946 };
const DRIVER_COUNT = 200;
const DRIVER_IDS = Array.from(
  { length: DRIVER_COUNT },
  (_, i) => `dddddddd-0000-0000-0000-${String(i + 1).padStart(12, '0')}`,
);
const RIDER_ID = 'aaaaaaaa-0000-0000-0000-000000000001';

function b64url(input) {
  return encoding.b64encode(input, 'rawurl');
}

function mint(userId, role) {
  const header = b64url(JSON.stringify({ alg: 'HS256', typ: 'JWT' }));
  const now = Math.floor(Date.now() / 1000);
  const payload = b64url(
    JSON.stringify({
      user_id: userId,
      email: `${role}-${userId.slice(0, 8)}@load.local`,
      role: role,
      token_type: 'access',
      sub: userId,
      iat: now,
      exp: now + 3600,
    }),
  );
  const signingInput = `${header}.${payload}`;
  const sig = b64url(crypto.hmac('sha256', SECRET, signingInput, 'binary'));
  return `${signingInput}.${sig}`;
}

export function setup() {
  if (!SECRET) throw new Error('JWT_SECRET env is required');
  const driverTokens = DRIVER_IDS.map((id) => mint(id, 'driver'));
  return { driverTokens, riderToken: mint(RIDER_ID, 'rider') };
}

export const options = {
  scenarios: {
    load: {
      executor: 'ramping-vus',
      startVUs: 10,
      stages: [
        { duration: '15s', target: PEAK_VUS },
        { duration: '30s', target: PEAK_VUS },
        { duration: '5s', target: 0 },
      ],
    },
  },
  summaryTrendStats: ['avg', 'min', 'med', 'p(95)', 'p(99)', 'max'],
  thresholds: { http_req_failed: ['rate<0.01'] },
};

// jitter a coordinate by up to ~±0.02deg (~±2km)
function jitter(v) {
  return v + (Math.random() - 0.5) * 0.04;
}

export default function (data) {
  let res;
  if (SCENARIO === 'nearby') {
    const t = data.driverTokens[__VU % DRIVER_COUNT];
    res = http.get(
      `${BASE}/drivers/nearby?lat=${jitter(CENTER.lat)}&lng=${jitter(CENTER.lng)}&radius=5`,
      // constant name tag: the lat/lng vary per request, so without this k6 would
      // create a unique metric series per URL and exhaust its own memory.
      { headers: { Authorization: `Bearer ${t}` }, tags: { name: 'GET /drivers/nearby' } },
    );
    check(res, { 'nearby 200': (r) => r.status === 200 });
  } else if (SCENARIO === 'location') {
    const idx = __VU % DRIVER_COUNT;
    res = http.patch(
      `${BASE}/drivers/${DRIVER_IDS[idx]}/location`,
      JSON.stringify({ lat: jitter(CENTER.lat), lng: jitter(CENTER.lng) }),
      {
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${data.driverTokens[idx]}`,
        },
      },
    );
    check(res, { 'location 200': (r) => r.status === 200 });
  } else if (SCENARIO === 'estimate') {
    res = http.post(
      `${BASE}/trips/estimate`,
      JSON.stringify({
        pickup_lat: jitter(CENTER.lat),
        pickup_lng: jitter(CENTER.lng),
        drop_lat: jitter(CENTER.lat),
        drop_lng: jitter(CENTER.lng),
        vehicle_type: 'x',
      }),
      {
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${data.riderToken}`,
        },
      },
    );
    check(res, { 'estimate 200': (r) => r.status === 200 });
  } else if (SCENARIO === 'request') {
    res = http.post(
      `${BASE}/trips/request`,
      JSON.stringify({
        pickup_lat: jitter(CENTER.lat),
        pickup_lng: jitter(CENTER.lng),
        drop_lat: jitter(CENTER.lat),
        drop_lng: jitter(CENTER.lng),
        payment_method: 'cash',
        vehicle_type: 'x',
      }),
      {
        headers: {
          'Content-Type': 'application/json',
          Authorization: `Bearer ${data.riderToken}`,
        },
      },
    );
    check(res, { 'request 201': (r) => r.status === 201 });
  }
}
