# Project Rules — Uber Ride-Hailing (Go Monorepo)

These rules are **non-negotiable** and must be followed in every feature, fix, or refactor.

## Architecture

- Go workspace (`go.work`, Go 1.21) — 7 modules: `shared` + 6 services.
- Services: `user-service` (:8081), `driver-service` (:8082), `trip-service` (:8083), `matching-service` (no HTTP), `notification-service` (:8084), `payment-service` (:8085).
- API gateway: nginx on `:8000` — all external traffic goes through it.
- **Never bypass nginx** by calling service ports directly from outside the stack.

## Shared Package (`shared/pkg/`)

- **Always use shared packages** — never reinvent what already exists:
  - `env` → config values
  - `jwt` → token generation/validation (access 15m / refresh 7d / checkout 30m)
  - `db` → pgx pool (do not create raw `database/sql` connections)
  - `redis` → GEO ops, distributed locks, OTP storage
  - `kafka` → cached writers, SCRAM-SHA-256 TLS
  - `mailer` → Brevo API + SMTP fallback
  - `otp` → 6-digit OTP, Redis-backed, 5m TTL
  - `validation` → email, phone, name, password, coordinates, driver status, rating score
- Before adding a new utility, **check `shared/pkg/`** for an existing equivalent.

## Service Boundaries

- Each service owns its own DB — **no cross-service DB queries**.
- Cross-service communication happens via **Kafka events only** (except matching-service → trip-service which uses `PATCH /trips/{id}/assign` with `X-Internal-Secret`).
- Kafka topic ownership (who publishes vs. consumes) is fixed — see architecture memory for the full table. Do not change publishers without updating all consumers.

## Kafka Events

- 7 topics: `ride.requested`, `ride.offered`, `driver.assigned`, `trip.completed`, `trip.cancelled`, `rating.submitted`, `payment.completed`.
- Event structs are defined in `shared/pkg/kafka/events.go` — **always use these**, never define ad-hoc structs.
- Consumers must be idempotent — Kafka can redeliver.

## Trip State Machine

- Valid states: `REQUESTED → DRIVER_ASSIGNED → STARTED → COMPLETED` (or `→ CANCELLED`).
- Cancel rules: rider can cancel in `REQUESTED` or `DRIVER_ASSIGNED`; driver can cancel in `DRIVER_ASSIGNED` only.
- Auto-cancel after 10 min for unmatched trips (poller runs every 30s) — do not remove this poller.

## Fare & Matching

- Fare formula: `₹50 base + ₹12 × distance_km × surge_multiplier` — stored in Redis key `surge:multiplier`.
- Driver-supplied distance is accepted only if `≤ 1.5×` haversine AND `< 200 km`; otherwise use haversine.
- Matching uses Redis `GEORADIUS`, 5 km radius, max 10 drivers, closest first, `SETNX` lock with 30s TTL.

## Payment

- Three methods: cash, UPI/card (Razorpay), simulate (dev only).
- `POST /payments/simulate-success` is for dev/test only — never gate production features behind it.
- Razorpay webhook (`POST /payments/webhook`) must remain idempotent.

## Code Quality

- **Production-ready code only** — think like a senior Go engineer.
- No `interface{}` / `any` without strong justification.
- Errors must be wrapped with context (`fmt.Errorf("...: %w", err)`).
- No magic numbers — use named constants.
- No business logic in HTTP handlers — keep handlers thin, logic in service layer.

## Docker / Local Dev

```bash
make up          # start all containers
make down        # stop containers
make clean       # stop + wipe volumes
make logs        # tail all logs
make logs-trip   # tail a specific service
make build       # go build uber/...
```

- Local Postgres: port `5433`, Redis: `6380`, Kafka: `9093`.
- Rate limits are enforced at nginx: auth endpoints 20 req/s, others 30 req/s — do not remove.

## Git

- **No `Co-Authored-By: Claude` or any Claude attribution** in commit messages — ever.
- Commit messages should focus on *why*, not *what*.

## Autonomy

- Proceed autonomously — don't repeatedly ask for confirmation.
- Only ask when requirements are genuinely ambiguous.
