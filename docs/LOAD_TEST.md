# Load Test

Peak-throughput measurement of the RideGo services with [k6](https://k6.io/).

## How to run

```bash
cd infra && docker compose up -d --build        # local Postgres / Redis / Kafka, no cloud
# seed ~200 drivers into the Redis GEO index near the test center:
awk 'BEGIN{srand();for(i=1;i<=200;i++){printf "GEOADD driver:locations %.6f %.6f dddddddd-0000-0000-0000-%012d\n",77.5946+(rand()-0.5)*0.04,12.9716+(rand()-0.5)*0.04,i}}' \
  | docker exec -i redis1 redis-cli

SECRET=$(grep -E '^JWT_SECRET=' infra/.env | cut -d= -f2-)
docker run --rm --network infra_default -v "$PWD/load":/scripts:ro \
  -e JWT_SECRET="$SECRET" -e SCENARIO=nearby -e BASE_URL=http://driver-service:8082 -e PEAK_VUS=100 \
  grafana/k6 run /scripts/ridego.js
```

`load/ridego.js` mints HS256 tokens in-process with the service's own `JWT_SECRET`, so the
bcrypt login path is skipped. `SCENARIO` ∈ `{nearby, location, estimate, request}`; point
`BASE_URL` at a service (direct) or at `http://api-gateway:8000` (through NGINX).

## Environment

Single instance of each service, local Docker (Rancher Desktop VM: **8 vCPU / 8 GB**, on an
8-core Apple-Silicon Mac), local Postgres/Redis/Kafka containers. The whole stack **and** the k6
generator share that VM, so these are conservative single-box numbers. Load profile: ramp to
**100 VUs** over 15 s, hold 30 s, 5 s ramp-down. Tuning applied (all committed):

- Redis `PoolSize` 3 → 100, Postgres `MaxConns` 10 → 50.
- NGINX `worker_processes auto` + `worker_connections 4096`.
- NGINX **keepalive upstream pools** (reuse backend connections instead of one TCP connect
  per request) — without this the gateway threw `502`s and capped at ~1 k req/s under load.

## Results — direct to the service (Go capacity, NGINX bypassed)

| Endpoint | Work | Throughput | p50 | p95 | p99 | Errors |
|----------|------|-----------:|----:|----:|----:|-------:|
| `PATCH /drivers/{id}/location` | 2× Redis GEO write | **13,024 req/s** | 4.8 ms | 14.8 ms | 22.9 ms | 0 |
| `GET /drivers/nearby` | Redis `GEOSEARCH` | **12,574 req/s** | 5.0 ms | 15.6 ms | 25.4 ms | 0 |
| `POST /trips/estimate` | haversine compute + cached read | **9,098 req/s** | 5.3 ms | 25.2 ms | 58.9 ms | 0 |
| `POST /trips/request` | Postgres insert + Kafka publish | **3,527 req/s** | 15.2 ms | 30.7 ms | 71.8 ms | 0 |

## Results — through the NGINX gateway (tuned: auto workers + keepalive pools)

| Endpoint | Throughput | p95 | p99 | Errors |
|----------|-----------:|----:|----:|-------:|
| `GET /drivers/nearby` | 7,957 req/s | 25.3 ms | 46.6 ms | 0 |
| `PATCH /drivers/{id}/location` | 7,299 req/s | 27.1 ms | 46.1 ms | 0 |
| `POST /trips/estimate` | 6,780 req/s | 32.2 ms | 63.4 ms | 0 |
| `POST /trips/request` | 2,336 req/s | 54.0 ms | 548.9 ms | 0 |

The gateway sustains ~60–75 % of direct capacity at zero errors — the remainder is reverse-proxy
overhead in the same shared VM.

## Notes

- **`/trips/request`** is an enqueue-and-return path. At low concurrency each request waited the
  Kafka producer's ~1 s batch-linger; under real concurrency the batches fill by size and flush
  immediately, which is why throughput rose from ~90 req/s (early, low-core runs) to 3,500 req/s.
- **Rate limiting** is a deliberate per-IP control. At the original default (`api` zone = 30 r/s,
  `burst=20`), flooding `/trips/estimate` with 100 VUs from one IP had **99.6 % of requests
  rejected** (`503`) — per-IP throttling enforced at the edge. The committed config raises this
  limit for throughput testing; restore a production value (e.g. `rate=100r/s`) before deploying.
