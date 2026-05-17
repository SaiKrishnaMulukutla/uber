# Low-Level Design — RideGo

> Implementation-level detail: schemas, algorithms, state machines, API contracts, and shared package interfaces. For system-level design see [HLD.md](HLD.md).

---

## Table of Contents

1. [Database Schemas](#1-database-schemas)
2. [Redis Key Patterns](#2-redis-key-patterns)
3. [Kafka Events](#3-kafka-events)
4. [Matching Algorithm](#4-matching-algorithm)
5. [Fare Calculation](#5-fare-calculation)
6. [Trip State Machine](#6-trip-state-machine)
7. [Payment State Machine](#7-payment-state-machine)
8. [OTP Flows](#8-otp-flows)
9. [Service API Contracts](#9-service-api-contracts)
10. [Shared Package Interfaces](#10-shared-package-interfaces)

---

## 1. Database Schemas

Each service owns its own schema. No cross-service DB queries.

### 1.1 users (user-service)

```sql
CREATE TABLE users (
    id            UUID             PRIMARY KEY,
    name          VARCHAR(200)     NOT NULL,
    email         VARCHAR(200)     NOT NULL UNIQUE,
    phone         VARCHAR(50)      NOT NULL UNIQUE,
    password_hash VARCHAR(255)     NOT NULL,         -- bcrypt
    rating        DOUBLE PRECISION NOT NULL DEFAULT 5.0,
    rating_count  INTEGER          NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);
```

**Indexes:** implicit on `email` (UNIQUE), `phone` (UNIQUE)

**Rating update:** weighted incremental average — `rating = (rating * rating_count + new_score) / (rating_count + 1)`. Updated via `ridego.events {type:rating.submitted}` consumer.

---

### 1.2 drivers (driver-service)

```sql
CREATE TABLE drivers (
    id            UUID             PRIMARY KEY,
    name          VARCHAR(200)     NOT NULL,
    email         VARCHAR(200)     NOT NULL UNIQUE,
    phone         VARCHAR(50)      NOT NULL UNIQUE,
    password_hash VARCHAR(255)     NOT NULL,         -- bcrypt
    vehicle_type  VARCHAR(50)      NOT NULL DEFAULT 'sedan',  -- go | x | xl
    license_plate VARCHAR(20),
    status        VARCHAR(20)      NOT NULL DEFAULT 'available', -- available | busy | offline
    rating        DOUBLE PRECISION NOT NULL DEFAULT 5.0,
    rating_count  INTEGER          NOT NULL DEFAULT 0,
    created_at    TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_drivers_status ON drivers(status);
```

**Status transitions (DB level):**
- Registration → `available`
- `driver.assigned` consumer → `busy`
- `trip.completed` consumer → `available`
- `ridego.events {trip.cancelled}` consumer → `available` (if driver was assigned)
- Driver calls `PATCH /drivers/{id}/status` → `available` | `offline`

---

### 1.3 trips (trip-service)

```sql
CREATE TABLE trips (
    id               UUID             PRIMARY KEY,
    rider_id         UUID             NOT NULL,
    rider_email      TEXT             NOT NULL DEFAULT '',
    rider_phone      VARCHAR(20)      NOT NULL DEFAULT '',
    driver_id        UUID,                              -- NULL until DRIVER_ASSIGNED
    pickup_lat       DOUBLE PRECISION NOT NULL,
    pickup_lng       DOUBLE PRECISION NOT NULL,
    drop_lat         DOUBLE PRECISION NOT NULL,
    drop_lng         DOUBLE PRECISION NOT NULL,
    fare             DECIMAL(12,2),                    -- NULL until COMPLETED
    status           VARCHAR(30)      NOT NULL DEFAULT 'REQUESTED',
    vehicle_type     VARCHAR(10)      NOT NULL DEFAULT 'x',
    payment_method   VARCHAR(20)      NOT NULL DEFAULT 'cash',
    duration_seconds INTEGER,
    requested_at     TIMESTAMPTZ,
    started_at       TIMESTAMPTZ,
    completed_at     TIMESTAMPTZ,
    created_at       TIMESTAMPTZ      NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_trips_rider_created  ON trips(rider_id,  created_at DESC);
CREATE INDEX idx_trips_driver_created ON trips(driver_id, created_at DESC);
CREATE INDEX idx_trips_status         ON trips(status);
CREATE INDEX idx_trips_vehicle_type   ON trips(vehicle_type);
```

**Stuck trip detection:** poller runs every 30s, finds trips with `status = 'REQUESTED'` and `requested_at < NOW() - interval '10 minutes'`, cancels them, and publishes `ridego.events {trip.cancelled}`.

---

### 1.4 ratings (trip-service)

```sql
CREATE TABLE ratings (
    id         UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    trip_id    UUID        NOT NULL REFERENCES trips(id),
    rater_id   UUID        NOT NULL,
    rater_role VARCHAR(10) NOT NULL CHECK (rater_role IN ('rider', 'driver')),
    ratee_id   UUID        NOT NULL,
    ratee_role VARCHAR(10) NOT NULL CHECK (ratee_role IN ('rider', 'driver')),
    score      SMALLINT    NOT NULL CHECK (score >= 1 AND score <= 5),
    comment    TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE(trip_id, rater_id)                          -- one rating per rater per trip
);

CREATE INDEX idx_ratings_ratee_id ON ratings(ratee_id);
```

**Idempotency:** `CreateRating` uses `INSERT ... ON CONFLICT DO NOTHING` and returns a `created bool`. Kafka event is only published when `created = true`, preventing duplicate rating events on Kafka redelivery.

---

### 1.5 notifications (notification-service)

```sql
CREATE TABLE notifications (
    id              UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id         UUID         NOT NULL,
    type            VARCHAR(50)  NOT NULL,
    title           VARCHAR(255) NOT NULL,
    body            TEXT         NOT NULL,
    read            BOOLEAN      DEFAULT FALSE,
    idempotency_key VARCHAR(255),                      -- prevents duplicate notifications
    created_at      TIMESTAMPTZ  DEFAULT NOW()
);

CREATE INDEX idx_notifications_user_created ON notifications(user_id, created_at DESC);
CREATE UNIQUE INDEX idx_notifications_idempotency ON notifications(idempotency_key)
    WHERE idempotency_key IS NOT NULL;
```

**Notification types:** `ride_requested`, `ride_offered`, `driver_assigned`, `trip_completed`, `trip_cancelled`, `rating_received`, `payment_completed`

**Idempotency key format:** `{tripID}:{userID}:{type}` — prevents duplicate notifications if Kafka redelivers.

---

### 1.6 payments (payment-service)

```sql
CREATE TABLE payments (
    id                  UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
    trip_id             UUID          NOT NULL UNIQUE,   -- one payment per trip
    rider_id            UUID          NOT NULL,
    rider_email         VARCHAR(255)  NOT NULL DEFAULT '',
    rider_phone         VARCHAR(20)   NOT NULL DEFAULT '',
    driver_id           UUID          NOT NULL,
    amount              DECIMAL(12,2) NOT NULL,
    status              VARCHAR(30)   NOT NULL DEFAULT 'PENDING',
    payment_method      VARCHAR(30)   NOT NULL DEFAULT 'cash',
    provider            VARCHAR(20)   NOT NULL DEFAULT 'cash', -- cash | razorpay
    provider_order_id   VARCHAR(100),
    provider_payment_id VARCHAR(100),
    provider_signature  VARCHAR(512),
    failure_reason      TEXT,
    metadata            JSONB         NOT NULL DEFAULT '{}',
    attempts_count      INT           NOT NULL DEFAULT 0,
    created_at          TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
    completed_at        TIMESTAMPTZ,
    updated_at          TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_payments_rider_id          ON payments(rider_id);
CREATE INDEX idx_payments_driver_id         ON payments(driver_id);
CREATE INDEX idx_payments_provider_order_id ON payments(provider_order_id);
```

---

## 2. Redis Key Patterns

All services share a single Upstash Redis instance. Keys are namespaced by prefix.

| Key | Type | TTL | Owner | Purpose |
|---|---|---|---|---|
| `driver:locations` | GEO set | — | matching-service | Active driver positions for `GEORADIUS` search |
| `driver:loc:{driverID}` | string (`lat,lng`) | 24h | matching-service, trip-service | Last-known position backup; restored to GEO set after trip end/cancel |
| `driver:lock:{driverID}` | string | 65s (offerLockTTL) | matching-service | `SETNX` distributed lock; prevents double-matching same driver |
| `driver:type:{driverID}` | string | — | driver-service | Vehicle type cache (`go`/`x`/`xl`) for vehicle-category filtering in matching |
| `offer:{tripID}` | string (driverID) | 60s (offerTTL) | matching-service | Pending offer — checked by driver-service on response |
| `offer:req:{tripID}` | string (JSON) | 20s | matching-service | Original `ride.requested` event; used to re-queue if driver rejects/times out |
| `surge:multiplier` | string (float) | — | trip-service | Current surge multiplier (1.0–5.0); read on every fare calculation |
| `trip:otp:{tripID}` | string (4-digit) | 2h | trip-service | Ride confirmation OTP; deleted on successful start |
| OTP keys | managed by `shared/pkg/otp` | 5m | user-service, driver-service | 6-digit registration OTPs |

---

## 3. Kafka Events

### 3.1 Topic Overview

| Topic | Key | Publisher | Consumers |
|---|---|---|---|
| `ride.requested` | `tripID` | trip-service | matching-service |
| `ride.offered` | `tripID` | matching-service | notification-service |
| `driver.assigned` | `tripID` | driver-service | trip-service, driver-service, notification-service |
| `trip.completed` | `tripID` | trip-service | payment-service, driver-service, notification-service |
| `ridego.events` | `tripID` | trip-service, payment-service | matching-service, driver-service, user-service, notification-service |

### 3.2 Event Structs

```go
// ride.requested
type RideRequestedEvent struct {
    TripID        string   `json:"trip_id"`
    RiderID       string   `json:"rider_id"`
    Pickup        LatLng   `json:"pickup"`
    Drop          LatLng   `json:"drop"`
    RequestedAt   string   `json:"requested_at"`
    VehicleType   string   `json:"vehicle_type,omitempty"` // go | x | xl
    RetryCount    int      `json:"retry_count,omitempty"`
    SkipDriverIDs []string `json:"skip_driver_ids,omitempty"` // rejected/timed-out drivers
}

// ride.offered
type RideOfferedEvent struct {
    TripID         string `json:"trip_id"`
    DriverID       string `json:"driver_id"`
    RiderID        string `json:"rider_id"`
    Pickup         LatLng `json:"pickup"`
    Drop           LatLng `json:"drop"`
    VehicleType    string `json:"vehicle_type,omitempty"`
    OfferExpiresAt string `json:"offer_expires_at"` // RFC3339
}

// driver.assigned
type DriverAssignedEvent struct {
    TripID   string `json:"trip_id"`
    DriverID string `json:"driver_id"`
    RiderID  string `json:"rider_id,omitempty"`
}

// trip.completed
type TripCompletedEvent struct {
    TripID          string  `json:"trip_id"`
    DriverID        string  `json:"driver_id"`
    RiderID         string  `json:"rider_id"`
    RiderEmail      string  `json:"rider_email,omitempty"`
    RiderPhone      string  `json:"rider_phone,omitempty"`
    Fare            float64 `json:"fare"`
    PaymentMethod   string  `json:"payment_method,omitempty"`
    CompletedAt     string  `json:"completed_at"`
    DurationSeconds int64   `json:"duration_seconds"`
}

// ridego.events envelope
type EventEnvelope struct {
    Type    string          `json:"type"`    // trip.cancelled | rating.submitted | payment.completed
    Payload json.RawMessage `json:"payload"`
}

// ridego.events — type: trip.cancelled
type TripCancelledEvent struct {
    TripID      string `json:"trip_id"`
    DriverID    string `json:"driver_id,omitempty"`
    RiderID     string `json:"rider_id"`
    RiderEmail  string `json:"rider_email,omitempty"`
    Reason      string `json:"reason,omitempty"`
    CancelledAt string `json:"cancelled_at"`
}

// ridego.events — type: rating.submitted
type RatingSubmittedEvent struct {
    TripID    string `json:"trip_id"`
    RaterID   string `json:"rater_id"`
    RaterRole string `json:"rater_role"` // rider | driver
    RateeID   string `json:"ratee_id"`
    RateeRole string `json:"ratee_role"`
    Score     int    `json:"score"`
    Comment   string `json:"comment,omitempty"`
}

// ridego.events — type: payment.completed
type PaymentCompletedEvent struct {
    PaymentID   string  `json:"payment_id"`
    TripID      string  `json:"trip_id"`
    RiderID     string  `json:"rider_id"`
    RiderEmail  string  `json:"rider_email,omitempty"`
    DriverID    string  `json:"driver_id"`
    Amount      float64 `json:"amount"`
    CompletedAt string  `json:"completed_at"`
}
```

### 3.3 EventEnvelope Pattern

Publishers:
```go
env, err := kafka.NewEnvelope(kafka.EventTypeTripCancelled, ev)
kafkaClient.Publish(ctx, kafka.TopicRideGoEvents, tripID, env)
```

Consumers:
```go
var env kafka.EventEnvelope
json.Unmarshal(data, &env)
switch env.Type {
case kafka.EventTypeTripCancelled:
    var ev kafka.TripCancelledEvent
    json.Unmarshal(env.Payload, &ev)
    // handle...
case kafka.EventTypeRatingSubmitted:
    // ...
}
```

---

## 4. Matching Algorithm

**Input:** `ride.requested` event with pickup coordinates, vehicle type, retry count, skip list

**Steps:**

```
1. GEORADIUS driver:locations {pickup.lat} {pickup.lng} 5 km ASC COUNT 10
   → returns up to 10 driver IDs sorted nearest-first

2. Filter out:
   a. Drivers in ev.SkipDriverIDs (previously rejected or timed out)
   b. Drivers whose cached vehicle type (driver:type:{id}) doesn't match requested type

3. For each remaining driver (nearest first):
   a. SETNX driver:lock:{id} "1" EX 20
      → if returns 0 (already locked), try next driver
      → if returns 1 (locked), this is the chosen driver → break

4. If no driver locked:
   → return nil (no-op; trip stays REQUESTED; poller will cancel after 10 min)

5. Save driver location backup:
   GEOPOS driver:locations {driverID} → lat, lng
   SET driver:loc:{driverID} "{lat},{lng}" EX 86400

6. ZREM driver:locations {driverID}
   → removes driver from GEO pool (prevents double-matching while offer pending)

7. SET offer:{tripID} {driverID} EX 60
   SET offer:req:{tripID} {serialised event JSON} EX 65

8. Publish ride.offered → notification-service notifies driver

9. Goroutine: sleep 60s
   GET offer:{tripID}
   → if still == driverID (not consumed):
       DEL offer:{tripID}
       DEL driver:lock:{driverID}
       SET driver:loc:{driverID} back to GEO
       ev.SkipDriverIDs += driverID
       re-publish ride.requested (retry)
```

**Driver accept/reject (driver-service):**
- Accept: `GET offer:{tripID}` → validate driverID matches JWT → delete offer → publish `driver.assigned`
- Reject: `GET offer:{tripID}` → `GET offer:req:{tripID}` → restore driver to GEO → add to skip list → re-publish `ride.requested`

**Retry limit:** `RetryCount >= 5` → give up; trip-service poller cancels after 10 minutes regardless.

---

## 5. Fare Calculation

### Formula

```
fare = base_fare + per_km_rate × distance_km
```

Applied with surge:
```
estimated_fare = fare × surge_multiplier
```

Surge is applied to **estimates only** — the final fare on `PATCH /trips/{id}/end` does not re-apply surge (fare is computed from actual distance at time of trip end using base formula only).

### Vehicle Categories

| Category | Base (₹) | Per km (₹) | Vehicles |
|---|---|---|---|
| `go` | 30 | 8 | Hatchbacks, compacts |
| `x` (default) | 50 | 12 | Sedans |
| `xl` | 80 | 16 | SUVs, MUVs |

### Distance Validation

Driver submits optional `distance_km` on `PATCH /trips/{id}/end`. Accepted only if:
- `distance_km <= haversine_km × 1.5` (no more than 50% longer than straight-line)
- `distance_km <= 200 km`

If the claim fails validation, the haversine distance is used instead.

### Haversine Formula (implemented in trip-service)

```go
func haversineKm(lat1, lng1, lat2, lng2 float64) float64 {
    const R = 6371.0
    dLat := (lat2 - lat1) * math.Pi / 180
    dLng := (lng2 - lng1) * math.Pi / 180
    a := sin²(dLat/2) + cos(lat1°) × cos(lat2°) × sin²(dLng/2)
    return R × 2 × atan2(√a, √(1-a))
}
```

### Surge Pricing

- Stored as `surge:multiplier` key in Redis (float string)
- Read on every `POST /trips/estimate`
- Admin-only `PATCH /trips/surge { "multiplier": 1.5 }` — validates 1.0 ≤ value ≤ 5.0
- Surge is **not** stored on the trip; it only affects estimates shown before booking

### Duration Estimate

```
estimated_duration_min = (distance_km / 25.0) × 60
```

Assumes average city speed of 25 km/h.

---

## 6. Trip State Machine

```
                    ┌─────────────┐
                    │  REQUESTED  │◄────────────────────────────────┐
                    └──────┬──────┘                                 │
                           │                                        │
          driver.assigned  │                                        │
          (Kafka consumer) │          retry (re-publish             │
                           ▼          ride.requested)               │
                    ┌──────────────────┐                            │
                    │  DRIVER_ASSIGNED │                            │
                    └──────┬──────────┘                             │
                           │                                        │
         PATCH /start      │                                        │
         (OTP verified)    ▼                                        │
                    ┌─────────┐                                     │
                    │ STARTED │                                     │
                    └────┬────┘                                     │
                         │                                          │
         PATCH /end      │                                          │
                         ▼                                          │
                    ┌───────────┐                                   │
                    │ COMPLETED │                                   │
                    └───────────┘                                   │
                                                                    │
         CANCELLED ◄──── PATCH /cancel (REQUESTED or DRIVER_ASSIGNED only)
         CANCELLED ◄──── auto-cancel poller (REQUESTED > 10 min)
```

**Valid cancel actors:**
- Rider: can cancel in `REQUESTED` or `DRIVER_ASSIGNED`
- Driver: can cancel in `DRIVER_ASSIGNED` only

**Cancel side effects:**
- Driver's last-known location restored to Redis GEO set (`driver:loc:{id}` → `GEORADD driver:locations`)
- `ridego.events {type:trip.cancelled}` published
- matching-service consumer: clears `offer:{tripID}`, unlocks driver, restores GEO
- driver-service consumer: sets driver status → `available`
- notification-service consumer: sends in-app notification + email to rider

---

## 7. Payment State Machine

### Cash

```
PENDING ──► AWAITING_CASH_CONFIRM ──► COMPLETED
                                          └─► ridego.events {payment.completed}
                                          └─► WebSocket hub broadcast
```

Transition to `AWAITING_CASH_CONFIRM` happens automatically when the `trip.completed` Kafka event is consumed and `payment_method = cash`.

### UPI / Card (Razorpay)

```
PENDING ──► PROCESSING ──► COMPLETED
                │               └─► ridego.events {payment.completed}
                │               └─► WebSocket hub broadcast
                └──────────────► FAILED (webhook timeout, signature mismatch)
```

- `POST /payments/orders` moves status to `PROCESSING`
- Completion via `POST /payments/verify` (card) or Razorpay webhook (`payment.captured` / `qr_code.credited`)
- Webhook handler is **idempotent**: if status is already `COMPLETED`, returns 200 without re-publishing

### Razorpay Signature Verification (card)

```
HMAC-SHA256(provider_order_id + "|" + provider_payment_id, RAZORPAY_KEY_SECRET)
== razorpay_signature header
```

### Webhook Events Handled

| Event | Trigger |
|---|---|
| `payment.captured` | UPI VPA collect or card payment approved |
| `qr_code.credited` | UPI QR scan approved |

---

## 8. OTP Flows

### 8.1 Registration OTP (6-digit)

```
POST /users/register  { name, email, phone, password }
  └─► bcrypt hash password
  └─► store pendingRegistration as JSON → Redis SET pending_reg:{email} {data} EX 600
  └─► otp.Send(email) → random 6-digit code → Redis SET otp:{email} {code} EX 300
  └─► Brevo email: "Your RideGo OTP is 123456"
  └─► 202 { message: "OTP sent to {email}" }

POST /users/verify-register  { email, otp }
  └─► otp.VerifyOTP(email, otp)
  └─► Redis GETDEL pending_reg:{email}  (atomic get + delete)
  └─► create user record in Postgres
  └─► issue JWT access + refresh tokens
  └─► 201 { access_token, refresh_token, user }

POST /users/login  { email, password }
  └─► bcrypt.CompareHashAndPassword
  └─► issue JWT access + refresh tokens directly
  └─► 200 { access_token, refresh_token, user }

POST /users/forgot-password  { email }
  └─► verify email exists in DB
  └─► otp.Send(email) → Redis SET otp:{email} {code} EX 300
  └─► 202 { message: "OTP sent to {email}" }

POST /users/reset-password  { email, otp, new_password }
  └─► otp.VerifyOTP(email, otp)
  └─► bcrypt hash new_password
  └─► UPDATE users SET password_hash WHERE id
  └─► 200 { message: "password reset successfully" }
```

Registration TTL: **10 minutes** for pending_reg key, **5 minutes** for OTP key. Max OTP attempts: **5** (enforced by `shared/pkg/otp`). Same TTL and attempt limits apply to forgot-password OTP.

### 8.2 Ride OTP (4-digit)

```
driver.assigned Kafka event consumed by trip-service
  └─► crypto/rand → 4-digit code (0000–9999)
  └─► Redis SET trip:otp:{tripID} {code} EX 7200   (2 hour TTL)

GET /trips/{id} (called by rider, when status = DRIVER_ASSIGNED)
  └─► response includes ride_otp field (only populated for the rider)

PATCH /trips/{id}/start  { otp }  (called by driver)
  └─► Redis GET trip:otp:{tripID}
  └─► string compare → reject if mismatch
  └─► on match: DEL trip:otp:{tripID}, transition trip to STARTED
```

TTL: **2 hours**. No retry limit (wrong OTP simply returns 400). OTP is consumed on first successful start.

---

## 9. Service API Contracts

### user-service (:8081)

| Method | Path | Auth | Body | Response |
|---|---|---|---|---|
| POST | `/users/register` | — | `{name, email, phone, password}` | `202 {message: "OTP sent"}` |
| POST | `/users/verify-register` | — | `{email, otp}` | `201 {access_token, refresh_token, user}` |
| POST | `/users/login` | — | `{email, password}` | `200 {access_token, refresh_token, user}` |
| POST | `/users/forgot-password` | — | `{email}` | `202 {message: "OTP sent"}` |
| POST | `/users/reset-password` | — | `{email, otp, new_password}` | `200 {message: "password reset successfully"}` |
| POST | `/users/refresh` | — | `{refresh_token}` | `200 {access_token, refresh_token}` |
| GET | `/users/{id}` | Bearer (rider, IDOR) | — | `200 {user}` |

### driver-service (:8082)

| Method | Path | Auth | Body | Response |
|---|---|---|---|---|
| POST | `/drivers/register` | — | `{name, email, phone, password, vehicle_type, license_plate}` | `202 {message: "OTP sent"}` |
| POST | `/drivers/verify-register` | — | `{email, otp}` | `201 {access_token, refresh_token, driver}` |
| POST | `/drivers/login` | — | `{email, password}` | `200 {access_token, refresh_token, driver}` |
| POST | `/drivers/forgot-password` | — | `{email}` | `202 {message: "OTP sent"}` |
| POST | `/drivers/reset-password` | — | `{email, otp, new_password}` | `200 {message: "password reset successfully"}` |
| POST | `/drivers/refresh` | — | `{refresh_token}` | `200 {access_token, refresh_token}` |
| GET | `/drivers/{id}` | Bearer (driver, IDOR) | — | `200 {driver}` |
| PATCH | `/drivers/{id}/status` | Bearer (driver) | `{status}` | `200 {id, status}` |
| PATCH | `/drivers/{id}/location` | Bearer (driver) | `{lat, lng}` | `200 {status}` |
| GET | `/drivers/nearby` | Bearer | `?lat&lng&radius` | `200 {drivers:[]}` |
| POST | `/drivers/trips/{tripId}/respond` | Bearer (driver) | `{accept: bool}` | `200 {status}` |

### trip-service (:8083)

| Method | Path | Auth | Body | Response |
|---|---|---|---|---|
| POST | `/trips/estimate` | Bearer | `{pickup_lat, pickup_lng, drop_lat, drop_lng, vehicle_type?}` | `200 {estimated_fare, distance, duration_min, surge_multiplier, currency}` |
| POST | `/trips/request` | Bearer (rider) | `{pickup_lat, pickup_lng, drop_lat, drop_lng, payment_method?, vehicle_type?}` | `201 {trip_id, status}` |
| GET | `/trips/{id}` | Bearer | — | `200 {trip}` (includes `ride_otp` for rider when DRIVER_ASSIGNED) |
| PATCH | `/trips/{id}/assign` | X-Internal-Secret | `{driver_id}` | `200 {trip}` |
| PATCH | `/trips/{id}/start` | Bearer (driver) | `{otp}` | `200 {trip}` |
| PATCH | `/trips/{id}/end` | Bearer (driver) | `{distance_km?, duration_seconds?}` | `200 {trip}` |
| PATCH | `/trips/{id}/cancel` | Bearer | `{reason?}` | `200 {trip}` |
| POST | `/trips/{id}/rate` | Bearer | `{score, comment?}` | `201 {rating}` |
| POST | `/trips/{id}/location` | Bearer (driver) | `{lat, lng}` | `200 {status}` |
| GET | `/trips/history` | Bearer | `?limit&offset` | `200 {trips, total, limit, offset}` |
| GET | `/trips/surge` | Bearer | — | `200 {multiplier}` |
| PATCH | `/trips/surge` | Bearer | `{multiplier}` | `200 {multiplier}` |
| WS | `/ws/trips/{id}` | `?token=<jwt>` | — | JSON stream `{trip_id, lat, lng, ts}` |

### payment-service (:8085)

| Method | Path | Auth | Body | Response |
|---|---|---|---|---|
| GET | `/payments/{tripId}` | Bearer | — | `200 {payment}` |
| GET | `/payments/history` | Bearer | `?limit&offset` | `200 {payments, total}` |
| GET | `/payments/earnings` | Bearer (driver) | `?period=week\|month\|all` | `200 {period, total_earnings, trip_count, daily[]}` |
| POST | `/payments/orders` | Bearer (rider) | `{payment_id}` | `200 {provider_order_id, amount, currency, key_id, checkout_url}` |
| POST | `/payments/verify` | Checkout token | `{payment_id, provider_order_id, provider_payment_id, signature}` | `200 {payment}` |
| POST | `/payments/{id}/confirm-cash` | Bearer (driver) | — | `200 {payment}` |
| POST | `/payments/simulate-success` | Bearer | `{payment_id}` | `200 {payment}` |
| POST | `/payments/checkout/{id}/upi` | Checkout token | `{vpa}` | `200 {status}` |
| GET | `/payments/checkout/{id}` | — | — | `200 HTML` |
| POST | `/payments/webhook` | HMAC header | Razorpay body | `200` |
| WS | `/payments/ws/{id}` | — | — | `"completed"` message on success |

### notification-service (:8084)

| Method | Path | Auth | Body | Response |
|---|---|---|---|---|
| GET | `/notifications/` | Bearer | `?limit&offset` | `200 {notifications, total}` |
| PATCH | `/notifications/{id}/read` | Bearer | — | `200 {status}` |

---

## 10. Shared Package Interfaces

### `shared/pkg/db`

```go
func MustConnect(ctx context.Context, dsn string, migrations fs.FS) *pgxpool.Pool
// Connects with MaxConns=25, MinConns=5
// Runs embedded SQL migrations in lexicographic order
// Tracks applied migrations in schema_migrations table
// Retries 30× with 2s interval on startup; fatal on permanent failure
```

### `shared/pkg/redis`

```go
type Client struct { /* wraps go-redis */ }

func NewClient(addr string) (*Client, error)
// addr: plain host:port OR rediss://:token@host:port

// GEO
func (c *Client) SetDriverLocation(ctx, driverID, lat, lng) error   // GEOADD
func (c *Client) GetNearbyDrivers(ctx, lat, lng, radiusKm, count) ([]string, error)  // GEORADIUS
func (c *Client) RemoveDriverLocation(ctx, driverID) error           // ZREM
func (c *Client) GetDriverGeoPos(ctx, driverID) (lat, lng float64, error)  // GEOPOS

// Location backup
func (c *Client) SaveDriverLocation(ctx, driverID, lat, lng) error   // SET driver:loc:{id}
func (c *Client) GetDriverLocation(ctx, driverID) (lat, lng float64, error)

// Locks
func (c *Client) LockDriver(ctx, driverID, ttl) (bool, error)       // SET NX EX
func (c *Client) UnlockDriver(ctx, driverID) error                   // DEL

// Offers
func (c *Client) SetOffer(ctx, tripID, driverID, ttl) error
func (c *Client) GetOffer(ctx, tripID) (driverID string, error)
func (c *Client) DeleteOffer(ctx, tripID) error
func (c *Client) SetOfferEvent(ctx, tripID, data []byte, ttl) error

// Surge
func (c *Client) GetSurge(ctx) float64    // defaults to 1.0 if key missing
func (c *Client) SetSurge(ctx, float64) error

// Trip OTP
func (c *Client) SetTripOTP(ctx, tripID, otp) error   // EX 7200
func (c *Client) GetTripOTP(ctx, tripID) (string, error)
func (c *Client) DeleteTripOTP(ctx, tripID) error

// Driver type cache
func (c *Client) GetDriverType(ctx, driverID) string

// Ping
func (c *Client) Ping(ctx) error
func (c *Client) RDB() *redis.Client  // raw client (used by otp package)
```

### `shared/pkg/kafka`

```go
func NewClient(brokers []string) *Client
// Reads KAFKA_USERNAME + KAFKA_PASSWORD from env for SASL SCRAM-SHA-256
// Reads KAFKA_CA_CERT (base64 PEM) for Aiven self-signed CA
// Falls back to plaintext DefaultDialer if creds absent (local dev)

func (c *Client) EnsureTopics(ctx, topics ...string) error
// 20 attempts × 3s; creates with NumPartitions=1, ReplicationFactor=1

func (c *Client) WarmWriters(topics ...string)
// Pre-creates cached writers; avoids TLS handshake cost on first publish

func (c *Client) Publish(ctx, topic, key string, value any) error
// JSON-marshals value; 30s write timeout; writer cached per topic

func (c *Client) Subscribe(ctx, topic, groupID string, handler func([]byte) error)
// Manual commit after handler success
// Per-message panic recovery (panics commit + skip bad message)
// Retries indefinitely on handler error (Kafka redelivers)
```

### `shared/pkg/jwt`

```go
func Init(secret string) error           // call once at startup
func GenerateAccessToken(id, role string) (string, error)   // 15m TTL
func GenerateRefreshToken(id, role string) (string, error)  // 7d TTL
func GenerateCheckoutToken(paymentID string) (string, error) // 30m TTL
func OptionalAuth(next http.Handler) http.Handler  // sets claims if valid; continues either way
func RequireAuth(next http.Handler) http.Handler   // 401 if missing/invalid
func RequireRole(roles ...string) func(http.Handler) http.Handler  // 403 if wrong role
func GetClaims(ctx context.Context) *Claims        // nil if unauthenticated
```

### `shared/pkg/mailer`

```go
type Mailer interface {
    Send(to, subject, htmlBody string) error
}

func NewBrevo(apiKey, senderEmail string) Mailer      // HTTP POST to api.brevo.com
func New(host, port, user, pass string) Mailer         // SMTP (port 465 = TLS, else STARTTLS)
func WithFallback(primary, secondary Mailer) Mailer    // try primary; on error try secondary
func NewAsync(m Mailer, workers int) Mailer            // buffered worker pool; Send returns immediately
```

### `shared/pkg/otp`

```go
func New(rdb *redis.Client, mailer mailer.Mailer) *Client

func (c *Client) SendOTP(ctx, email string) error
// Generates 6-digit code; stores as otp:{email} with 5m TTL; sends email

func (c *Client) VerifyOTP(ctx, email, code string) error
// Checks code; increments attempt counter; rejects after 5 failures
// Deletes key on success
```

### `shared/pkg/validation`

```go
func ValidateEmail(email string) error
func ValidatePhone(phone string) error       // E.164 format
func ValidateName(name string) error         // 2–200 chars
func ValidatePassword(password string) error // min 6 chars
func ValidateCoordinates(lat, lng float64) error
func ValidateDriverStatus(status string) error  // available | busy | offline
func ValidateRatingScore(score int) error       // 1–5
```
