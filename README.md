# Uber — Ride-Hailing Backend

Distributed ride-hailing system built with Go microservices, event-driven architecture (Kafka), geospatial matching (Redis GEO), and PostgreSQL — orchestrated via Docker Compose with an NGINX API gateway.

---

## Architecture

```mermaid
flowchart TD
    Client(["Client"])

    subgraph Gateway["API Gateway · :8000"]
        Nginx["nginx · reverse proxy + WS upgrade"]
    end

    subgraph UserSvc["User Service · :8081"]
        U["/users"]
    end
    subgraph DriverSvc["Driver Service · :8082"]
        D["/drivers"]
    end
    subgraph TripSvc["Trip Service · :8083"]
        T["/trips"]
        WS["/ws/trips/:id"]
    end
    subgraph MatchSvc["Matching Service"]
        M["Kafka consumer · no HTTP"]
    end
    subgraph NotifSvc["Notification Service · :8084"]
        N["/notifications"]
    end
    subgraph PaySvc["Payment Service · :8085"]
        P["/payments"]
    end

    subgraph Data["Data Layer"]
        PG[("PostgreSQL · 5 DBs")]
        Redis[("Redis · GEO + OTP + locks")]
        Kafka[["Kafka KRaft · 6 topics"]]
    end

    Client -->|HTTP / WS| Nginx
    Nginx --> U & D & T & WS & N & P

    U & N & P --> PG
    D & T --> PG & Redis
    U & D --> Redis
    T & P -->|publish| Kafka
    Kafka -->|consume| M & D & U & N & P & T
    M --> Redis
    M -->|publish| Kafka
```

### Event Flow

```
POST /trips/request
    └─► ride.requested ──► matching-service (Redis GEO nearest driver)
                               └─► driver.assigned ──► trip-service, driver-service, notification-service

PATCH /trips/:id/end
    └─► trip.completed ──► payment-service, driver-service, notification-service

POST /trips/:id/rate
    └─► rating.submitted ──► driver-service, user-service, notification-service

payment completed
    └─► payment.completed ──► notification-service
```

---

## Services

| Service | Port | Database |
|---------|------|----------|
| api-gateway | 8000 | — |
| user-service | 8081 | users_db |
| driver-service | 8082 | drivers_db |
| trip-service | 8083 | trips_db |
| matching-service | — | — |
| notification-service | 8084 | notifications_db |
| payment-service | 8085 | payments_db |
| PostgreSQL | 5433 | — |
| Redis | 6380 | — |
| Kafka (KRaft) | 9093 | — |

## Kafka Topics

| Topic | Publisher | Consumers |
|-------|-----------|-----------|
| `ride.requested` | trip-service | matching-service |
| `driver.assigned` | matching-service | trip-service, driver-service, notification-service |
| `trip.completed` | trip-service | payment-service, driver-service, notification-service |
| `trip.cancelled` | trip-service | driver-service, notification-service |
| `rating.submitted` | trip-service | driver-service, user-service, notification-service |
| `payment.completed` | payment-service | notification-service |

---

## Quick Start

**Prerequisites:** Docker, Docker Compose, Go 1.21+

```bash
git clone https://github.com/SaiKrishnaMulukutla/uber && cd uber
cp infra/.env.example infra/.env
# edit infra/.env — set POSTGRES_PASSWORD, JWT_SECRET, and EMAIL_* for OTP delivery
make up
```

Services start in order. PostgreSQL and Kafka initialise first (~30s); application services connect and run migrations automatically.

```bash
make logs          # tail all service logs
make logs-trip     # tail a specific service
make down          # stop
make clean         # stop + wipe volumes
```

### Environment

| Variable | Required | Description |
|----------|----------|-------------|
| `POSTGRES_PASSWORD` | yes | PostgreSQL password |
| `JWT_SECRET` | yes | HS256 signing key (`openssl rand -base64 32`) |
| `EMAIL_HOST` / `EMAIL_PORT` / `EMAIL_USER` / `EMAIL_PASS` | yes | SMTP for OTP delivery |
| `PAYMENT_PROVIDER` | no | `cash` (default) or `razorpay` |
| `RAZORPAY_KEY_ID` / `RAZORPAY_KEY_SECRET` | if razorpay | Razorpay API credentials |
| `RAZORPAY_WEBHOOK_SECRET` | if razorpay | Webhook signing secret from Razorpay dashboard |
| `INTERNAL_SECRET` | no | Shared secret for service-to-service calls (`openssl rand -base64 32`) |
| `ALLOWED_ORIGINS` | no | Comma-separated WebSocket origin allowlist (empty = allow all, dev only) |

---

## API Reference

All requests route through **`http://localhost:8000`**. Authentication via `Authorization: Bearer <token>`.

### Users — `/users`

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/users/register` | — | Register rider, returns JWT |
| POST | `/users/login` | — | Step 1: validate password, send OTP |
| POST | `/users/verify-login` | — | Step 2: verify OTP, issue JWT |
| POST | `/users/refresh` | — | Rotate access token |
| GET | `/users/{id}` | Bearer (rider) | Get own profile |

### Drivers — `/drivers`

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/drivers/register` | — | Register driver, returns JWT |
| POST | `/drivers/login` | — | Step 1: validate password, send OTP |
| POST | `/drivers/verify-login` | — | Step 2: verify OTP, issue JWT |
| POST | `/drivers/refresh` | — | Rotate access token |
| GET | `/drivers/{id}` | Bearer (driver) | Get own profile |
| PATCH | `/drivers/{id}/location` | Bearer (driver) | Update GPS location |
| PATCH | `/drivers/{id}/status` | Bearer (driver) | Set online / offline |
| GET | `/drivers/nearby` | Bearer | Find nearby available drivers |

### Trips — `/trips`

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| POST | `/trips/estimate` | Bearer (rider) | Fare + duration estimate |
| POST | `/trips/request` | Bearer (rider) | Request a ride |
| GET | `/trips/history` | Bearer | Paginated trip history |
| GET | `/trips/{id}` | Bearer | Trip details (participants only) |
| PATCH | `/trips/{id}/assign` | X-Internal-Secret | Internal — matching-service only |
| PATCH | `/trips/{id}/start` | Bearer (driver) | Start trip |
| PATCH | `/trips/{id}/end` | Bearer (driver) | End trip, compute fare |
| PATCH | `/trips/{id}/cancel` | Bearer | Cancel trip |
| POST | `/trips/{id}/rate` | Bearer | Rate counterpart (1–5) |
| POST | `/trips/{id}/location` | Bearer (driver) | Push live location |
| WS | `/ws/trips/{id}?token=<jwt>` | JWT query param | Real-time driver location stream |

### Notifications — `/notifications`

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/notifications/` | Bearer | Paginated notification list |
| PATCH | `/notifications/{id}/read` | Bearer | Mark as read |

### Payments — `/payments`

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| GET | `/payments/history` | Bearer | Paginated payment history |
| GET | `/payments/{tripId}` | Bearer | Payment by trip (participants only) |
| POST | `/payments/orders` | Bearer (rider) | Create Razorpay order → returns `provider_order_id` + `key_id` |
| POST | `/payments/verify` | Bearer (rider) | Submit Razorpay signature → completes payment |
| POST | `/payments/webhook` | HMAC | Razorpay async backup confirmation |
| POST | `/payments/simulate-success` | Bearer | Complete payment without provider (dev/test) |

---

## End-to-End Flow

```bash
BASE=http://localhost:8000

# 1. Register rider and driver
RIDER=$(curl -s -X POST $BASE/users/register \
  -H "Content-Type: application/json" \
  -d '{"name":"Test Rider","email":"rider@test.com","phone":"+911111111111","password":"Pass123!"}')
RIDER_TOKEN=$(echo $RIDER | jq -r '.access_token')

DRIVER=$(curl -s -X POST $BASE/drivers/register \
  -H "Content-Type: application/json" \
  -d '{"name":"Test Driver","email":"driver@test.com","phone":"+912222222222","password":"Pass123!","vehicle_type":"sedan","license_plate":"KA-01-AB-1234"}')
DRIVER_TOKEN=$(echo $DRIVER | jq -r '.access_token')
DRIVER_ID=$(echo $DRIVER | jq -r '.driver.id')

# 2. Driver goes online and sets location
curl -s -X PATCH $BASE/drivers/$DRIVER_ID/status \
  -H "Authorization: Bearer $DRIVER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"status":"available"}' | jq .status

curl -s -X PATCH $BASE/drivers/$DRIVER_ID/location \
  -H "Authorization: Bearer $DRIVER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"lat":12.9716,"lng":77.5946}' | jq .

# 3. Rider requests a trip (matching happens via Kafka)
TRIP=$(curl -s -X POST $BASE/trips/request \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"pickupLat":12.9716,"pickupLng":77.5946,"dropLat":12.9352,"dropLng":77.6245}')
TRIP_ID=$(echo $TRIP | jq -r '.trip_id')

# 4. Wait for automatic matching
sleep 5
curl -s $BASE/trips/$TRIP_ID \
  -H "Authorization: Bearer $RIDER_TOKEN" | jq '{status,driver_id}'

# 5. Driver starts and ends the trip
curl -s -X PATCH $BASE/trips/$TRIP_ID/start \
  -H "Authorization: Bearer $DRIVER_TOKEN" | jq .status

curl -s -X PATCH $BASE/trips/$TRIP_ID/end \
  -H "Authorization: Bearer $DRIVER_TOKEN" \
  -H "Content-Type: application/json" -d '{}' | jq '{status,fare}'

# 6. Rate, check notifications and payment
curl -s -X POST $BASE/trips/$TRIP_ID/rate \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"score":5,"comment":"Smooth ride"}' | jq .

sleep 2
curl -s $BASE/notifications/ \
  -H "Authorization: Bearer $RIDER_TOKEN" | jq '[.notifications[].title]'

curl -s $BASE/payments/$TRIP_ID \
  -H "Authorization: Bearer $RIDER_TOKEN" | jq '{status,amount}'
```

---

## Payment Flow

### Cash (default)

```
PATCH /trips/{id}/end
  └─► trip.completed (Kafka)
        └─► payment-service: INSERT status=PENDING
              └─► immediately COMPLETED
                    └─► payment.completed (Kafka) ──► notification email
```

No frontend steps required. The payment completes automatically.

### Card (Razorpay)

```
[1]  PATCH /trips/{id}/end
       └─► trip.completed (Kafka) → payment created  status=PENDING

[2]  GET /payments/{tripId}
       ← { id: payment_id, status: "PENDING", amount }

[3]  POST /payments/orders  { payment_id }
       ← { provider_order_id, amount, currency: "INR", key_id }
       payment status → PROCESSING

[4]  Frontend — Razorpay Checkout
       new Razorpay({
         key:      key_id,
         order_id: provider_order_id,
         amount:   amount * 100,   // paise
         currency: "INR",
         handler(response) {
           // response.razorpay_payment_id
           // response.razorpay_order_id
           // response.razorpay_signature
           POST /payments/verify ← above fields + payment_id
         }
       }).open()

[5]  POST /payments/verify
       { payment_id, provider_order_id, provider_payment_id, signature }
       ← HMAC-SHA256(order_id|payment_id) verified against key_secret
       ← status=COMPLETED → payment.completed (Kafka) → notification email

[6]  Backup — Razorpay webhook (async, if browser closes before step 5)
       POST /payments/webhook  X-Razorpay-Signature: <hmac>
       ← HMAC-SHA256(body) verified against webhook_secret
       ← idempotent: no-op if already COMPLETED
```

### Simulate (dev/test, no Razorpay keys needed)

```bash
PAYMENT_ID=$(curl -s $BASE/payments/$TRIP_ID \
  -H "Authorization: Bearer $RIDER_TOKEN" | jq -r '.id')

curl -s -X POST $BASE/payments/simulate-success \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"payment_id\":\"$PAYMENT_ID\"}" | jq '{status,amount}'
```

### Enabling Razorpay

```bash
# infra/.env
PAYMENT_PROVIDER=razorpay
RAZORPAY_KEY_ID=rzp_test_...
RAZORPAY_KEY_SECRET=...
RAZORPAY_WEBHOOK_SECRET=...   # from Razorpay Dashboard → Webhooks
```

Configure the webhook URL in the [Razorpay Dashboard](https://dashboard.razorpay.com) → Webhooks:
- **URL:** `https://your-domain.com/payments/webhook`
- **Event:** `payment.captured`

For local testing use `ngrok http 8000` to get a public HTTPS URL.

---

## State Machines

### Trip

```
REQUESTED ──► DRIVER_ASSIGNED ──► STARTED ──► COMPLETED
    │               │                              └─ payment created, rating unlocked
    └── /cancel ──► CANCELLED
                    └─ driver restored to GEO pool
```

### Payment

```
PENDING ──► PROCESSING ──► COMPLETED
  │               │              └─ payment.completed published
  │               └─────────────► FAILED
  └─ cash / simulate ──────────► COMPLETED  (skips PROCESSING)
```

### Fare

```
₹50 base  +  ₹12 × distance_km  ×  surge_multiplier  (capped at 5×)
```

Driver-supplied distance is accepted only if within `1.5×` the straight-line (haversine) distance and under 200 km — otherwise the haversine value is used.

---

## Testing

```bash
# Unit tests
go test uber/shared/... uber/user-service/... uber/driver-service/... \
  uber/trip-service/... uber/notification-service/... uber/payment-service/...

# Build all
make build
```

---

<p align="center">Created with ❤️ by Mulukutla Sai Krishna</p>
