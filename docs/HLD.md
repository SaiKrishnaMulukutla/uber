# High-Level Design — RideGo

> This document describes the system from the outside in: what it does, why each piece exists, and how the components interact. No code details — see [LLD.md](LLD.md) for implementation specifics.

---

## 1. Problem Statement

Build a production-style ride-hailing backend that supports:
- Rider requests a trip from A to B
- Nearest available driver is matched automatically
- Driver accepts or rejects within a time window
- Trip progresses through defined states with OTP verification
- Fare is calculated and payment is processed post-trip
- All participants receive real-time notifications

---

## 2. System Goals

| Goal | Decision |
|---|---|
| Loose coupling between services | Kafka events — no direct service-to-service DB calls |
| Geographic driver matching | Redis GEO (`GEORADIUS`) — O(N+log M) spatial query |
| No ghost rides | 4-digit ride OTP: driver can only start trip after rider confirms verbally |
| Real-time trip tracking | WebSocket hub in trip-service; driver pushes location via HTTP |
| Multi-provider payments | Payment provider interface — pluggable: cash / Razorpay |
| Email without open SMTP port | Brevo HTTP API — works on Render (port 465/587 blocked) |
| Cost: zero | All infra on permanent free tiers (Neon, Upstash, Aiven, Render) |

---

## 3. Architecture Overview

```
                         ┌─────────────────────┐
                         │   Client (Browser /  │
                         │   Mobile App)        │
                         └──────────┬───────────┘
                                    │ HTTP / WebSocket
                         ┌──────────▼───────────┐
                         │    API Gateway        │
                         │  nginx :8000          │
                         │  Rate limiting        │
                         │  Reverse proxy        │
                         │  Security headers     │
                         └──┬───┬───┬───┬───┬───┘
                            │   │   │   │   │
              ┌─────────────┘   │   │   │   └──────────────┐
              │           ┌─────┘   └─────┐                │
              ▼           ▼               ▼                 ▼
      ┌───────────┐ ┌───────────┐ ┌───────────┐    ┌───────────┐
      │   User    │ │  Driver   │ │   Trip    │    │  Payment  │
      │  Service  │ │  Service  │ │  Service  │    │  Service  │
      │  :8081    │ │  :8082    │ │  :8083    │    │  :8085    │
      └─────┬─────┘ └─────┬─────┘ └─────┬─────┘    └─────┬─────┘
            │             │             │                  │
      ┌─────┘     ┌───────┘     ┌───────┘          ┌──────┘
      │           │             │                  │
      ▼           ▼             ▼                  ▼
┌─────────────────────────────────────────────────────────┐
│                    Aiven Kafka (5 topics)                 │
│  ride.requested · ride.offered · driver.assigned         │
│  trip.completed · ridego.events                          │
└──────────────────────────┬──────────────────────────────┘
                           │
        ┌──────────────────┼──────────────────┐
        ▼                  ▼                  ▼
┌──────────────┐  ┌──────────────┐  ┌──────────────┐
│   Matching   │  │Notification  │  │  All services │
│   Service    │  │  Service     │  │  (own DBs)    │
│  (no HTTP)   │  │  :8084       │  │  Neon Postgres│
└──────┬───────┘  └──────────────┘  └──────────────┘
       │
       ▼
┌──────────────┐
│  Upstash     │
│  Redis       │
│  GEO + locks │
└──────────────┘
```

---

## 4. Services

| Service | Port | Responsibility | DB |
|---|---|---|---|
| api-gateway | 8000 | nginx: reverse proxy, rate limiting, security headers, WebSocket upgrade | — |
| user-service | 8081 | Rider registration (OTP-verified), password login, forgot/reset password, profile, rating updates | `users` table |
| driver-service | 8082 | Driver registration (OTP-verified), password login, forgot/reset password, location updates, offer acceptance, rating updates | `drivers` table |
| trip-service | 8083 | Trip lifecycle (request → assign → start → end → cancel → rate), fare calculation, live location, surge pricing, WebSocket hub | `trips`, `ratings` tables |
| matching-service | — | Kafka consumer only — no HTTP. Receives `ride.requested`, finds nearest available driver via Redis GEO, publishes `ride.offered` | — |
| notification-service | 8084 | Kafka consumer — persists in-app notifications, sends HTML emails via Brevo | `notifications` table |
| payment-service | 8085 | Payment creation (post trip), Razorpay order/verify/webhook, cash confirm, WebSocket push on completion | `payments` table |

---

## 5. Data Stores

### Neon PostgreSQL
- One logical database, separate schemas per service (via separate connection strings pointed at same Neon project)
- 5 service databases: users, drivers, trips+ratings, notifications, payments
- Serverless, scale-to-zero — wakes on first query (~1s cold start)
- Migrations embedded in each service binary, run automatically at startup

### Upstash Redis
- Single Redis instance shared across services
- Namespaced by key prefix per concern (see LLD for full key table)
- Primary uses: driver GEO set, distributed locks, offer state, ride OTP, surge multiplier, registration OTP, password-reset OTP

### Aiven Kafka
- 5 topics, 1 partition each (free tier)
- SASL SCRAM-SHA-256 + TLS; self-signed CA loaded at startup
- `ridego.events` multiplexes 3 event types via `EventEnvelope{type, payload}`

---

## 6. External Services

| Service | Role | Free Tier |
|---|---|---|
| Neon | Postgres hosting | 0.5 GB, permanent |
| Upstash | Redis hosting | 500k commands/month |
| Aiven | Kafka hosting | 5 topics, 3-day retention |
| Render | App hosting | 7 web services, Singapore region |
| Brevo | Transactional email | 300 emails/day |
| Razorpay | Payment gateway | Test mode, no limits |

---

## 7. Ride Lifecycle — End-to-End Flow

```
Rider                   trip-service            Kafka             matching-service         Driver
  │                          │                    │                      │                   │
  │── POST /trips/request ──►│                    │                      │                   │
  │                          │── ride.requested ──►│                      │                   │
  │                          │                    │── ride.requested ────►│                   │
  │                          │                    │                      │─ GEORADIUS query   │
  │                          │                    │                      │   (5km, 10 drivers)│
  │                          │                    │                      │─ SETNX lock ──────►│
  │                          │                    │                      │─ store offer in    │
  │                          │                    │                      │   Redis (15s TTL)  │
  │                          │                    │── ride.offered ──────────────────────────►│
  │                          │                    │                      │                   │ (notif)
  │                          │                    │                      │                   │
  │                          │                    │               driver-service              │
  │                          │                    │                      │── POST /drivers/trips/{id}/respond
  │                          │                    │── driver.assigned ───────────────────────►│
  │                          │                    │                      │                   │
  │                          │◄─ driver.assigned ─│                      │                   │
  │                          │  (assign + OTP)    │                      │                   │
  │── GET /trips/{id} ──────►│                    │                      │                   │
  │◄─ { ride_otp: "4823" } ──│                    │                      │                   │
  │  (tells OTP verbally)    │                    │                      │                   │
  │                          │                    │                      │                   │
  │                          │◄─── PATCH /trips/{id}/start { otp } ─────────────────────────│
  │                          │  (validate OTP vs Redis, consume OTP)     │                   │
  │                          │                    │                      │                   │
  │                          │◄─── PATCH /trips/{id}/end ────────────────────────────────────│
  │                          │── trip.completed ──►│                      │                   │
  │                          │                    │── payment-service: create payment         │
  │                          │                    │── driver-service: mark available          │
  │                          │                    │── notification-service: email + in-app    │
  │                          │                    │                      │                   │
  │── POST /trips/{id}/rate ►│                    │                      │                   │
  │                          │── ridego.events ───►│ {type:rating.submitted}                 │
  │                          │                    │── driver-service: update rating           │
  │                          │                    │── notification-service: in-app            │
```

---

## 8. Payment Flow

```
trip.completed (Kafka)
    └─► payment-service inserts payment (status = PENDING)

── Cash ────────────────────────────────────────────────────────────────────────
status = PENDING → AWAITING_CASH_CONFIRM
Driver: POST /payments/{id}/confirm-cash
    └─► status = COMPLETED
          └─► ridego.events {payment.completed} → email + in-app
          └─► WebSocket hub broadcast → checkout page updates

── UPI / Card (Razorpay) ───────────────────────────────────────────────────────
POST /payments/orders → Razorpay order created, status = PROCESSING
    UPI: QR scan or VPA collect → Razorpay webhook → idempotent update
    Card: Razorpay Checkout modal → POST /payments/verify → HMAC validated
    Either path: status = COMPLETED
        └─► ridego.events {payment.completed} → email + in-app
        └─► WebSocket hub broadcast

── Dev shortcut ────────────────────────────────────────────────────────────────
POST /payments/simulate-success → COMPLETED immediately (no provider call)
```

---

## 9. Authentication & Security

### JWT
- HS256, single secret shared across all services via `JWT_SECRET` env var
- **Access token:** 15-minute TTL — sent on every authenticated request
- **Refresh token:** 7-day TTL — used only to obtain a new access token
- **Checkout token:** 30-minute TTL — scoped to a single payment, used for browser-based checkout flows

### Registration OTP
- Two-step flow: `POST /register` stores pending data in Redis (10-min TTL) and sends a 6-digit OTP; `POST /verify-register` confirms the OTP and creates the account
- JWT tokens issued only after OTP is verified — no account exists until then

### Login
- Password-only: `POST /login` validates email + password and issues JWT directly (no OTP on every login)

### Forgot Password (OTP)
- `POST /forgot-password {email}` — verifies email exists, sends 6-digit OTP via Brevo
- `POST /reset-password {email, otp, new_password}` — verifies OTP, bcrypts new password, updates DB
- OTP TTL: 5 minutes; max 5 attempts before lockout

### IDOR Protection
- Every endpoint that reads or modifies a user-owned resource verifies JWT claims match the resource owner
- No relying on URL path IDs alone

### nginx Rate Limiting
- Auth endpoints (`/login`, `/register`, `/verify-register`, `/forgot-password`, `/reset-password`, `/refresh`): 5 req/s per IP
- All other endpoints: 20 req/s per IP

### Service-to-Service Auth
- matching-service → trip-service (`PATCH /trips/{id}/assign`): `X-Internal-Secret` header checked against `INTERNAL_SECRET` env var
- No other direct service-to-service HTTP calls; all other cross-service communication via Kafka

### Transport Security
- All cloud connections use TLS: Neon (`sslmode=require`), Upstash (`rediss://`), Aiven (SASL+TLS with self-signed CA)
- CORS: `AllowedOrigins: ["*"]` (intentional for academic project — restrict in production)

---

## 10. Deployment Topology

```
                    Render (Singapore)
┌─────────────────────────────────────────────────────┐
│                                                     │
│  api-gateway      nginx    ← git push → auto-build  │
│  user-service     Go bin                            │
│  driver-service   Go bin                            │
│  trip-service     Go bin                            │
│  matching-service Go bin                            │
│  notification-service Go bin                        │
│  payment-service  Go bin                            │
│                                                     │
│  All services: free tier, spin-down after 15 min    │
│  Cold start: ~30s (nginx instant, Go ~5-10s)        │
└──────────┬───────────────────────────┬──────────────┘
           │                           │
  ┌────────▼────────┐       ┌──────────▼──────────┐
  │  Neon (SG)      │       │  Upstash (Mumbai)   │
  │  Postgres 17    │       │  Redis (TLS)         │
  │  scale-to-zero  │       │                     │
  └─────────────────┘       └─────────────────────┘
           │
  ┌────────▼────────┐
  │  Aiven (SG)     │
  │  Kafka 4.1      │
  │  5 topics       │
  └─────────────────┘
```

**Free-tier constraints:**
- Render services spin down after 15 minutes of inactivity; first request takes ~30s
- Neon compute pauses after 5 minutes; first DB query after pause takes ~1s (retried automatically)
- Aiven Kafka: 5 topics max, 1 partition per topic, 3-day retention
- Upstash Redis: 500k commands/month

---

## 11. Scalability Considerations

This system is designed for correctness on a single-instance free tier, not horizontal scale. Key limitations and their mitigations:

| Limitation | Current handling |
|---|---|
| Single Kafka partition per topic | Ordering guaranteed; consumer group has exactly 1 active consumer — acceptable at low traffic |
| Redis is single-threaded | All GEO + lock ops are atomic Redis commands; no race conditions |
| Render free tier spins down | Health check endpoint returns immediately (matching-service uses `atomic.Int32` ready flag) |
| No distributed tracing | Structured log lines with `trip_id`, `driver_id` correlation |
| No retry queue for failed Kafka handlers | Kafka redelivers on non-nil error return; panics are caught and committed (skip bad message) |

---

## 12. Technology Choices — Rationale

| Technology | Alternative considered | Why this |
|---|---|---|
| Go | Node.js, Python | Compiled, statically typed, excellent concurrency primitives, small binary size for Render |
| Kafka (Aiven) | RabbitMQ, Redis Streams | True pub/sub with consumer groups; multiple services consume same event independently |
| Redis GEO | PostGIS, Elasticsearch | Sub-millisecond spatial queries; SETNX for distributed locks in same store |
| Neon | Supabase, PlanetScale | Permanent free tier; standard Postgres 17; direct connection for migrations |
| Brevo | SendGrid, Mailgun | 300 emails/day free; HTTP API (no SMTP port needed on Render) |
| nginx gateway | Kong, Traefik, Go gateway | Zero overhead; battle-tested rate limiting and security headers |
| Razorpay | Stripe | Indian market: UPI + QR support; test mode with realistic flows |
