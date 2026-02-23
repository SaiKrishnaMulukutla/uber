# Ride-Hailing System

Minimal distributed ride-hailing backend written in **Go**, backed by PostgreSQL, Kafka, Redis, and an NGINX API Gateway.

## Architecture

```
┌──────────────┐      ┌──────────────┐
│  API Gateway │─────▶│ ride-service  │
│  (nginx:8000)│      │   (Go:8080)  │
└──────────────┘      └──────┬───────┘
                             │
              ┌──────────────┼──────────────┐
              ▼              ▼              ▼
         PostgreSQL       Kafka          Redis
         (users,        (events)       (driver
          drivers,                      locations,
          trips)                        trip cache)
```

## Project Structure

```
├── api-gateway/          # NGINX reverse proxy
│   ├── nginx.conf
│   └── Dockerfile
├── ride-service/          # Single Go backend
│   ├── cmd/main.go
│   ├── internal/
│   │   ├── users/         # User registration, login, profile
│   │   ├── drivers/       # Driver registration, login, location
│   │   ├── trips/         # Trip lifecycle (request → complete)
│   │   ├── matching/      # Kafka consumer: ride.requested → driver.assigned
│   │   ├── tracking/      # WebSocket: /ws/trips/:id
│   │   └── events/        # Shared event structs
│   ├── pkg/
│   │   ├── db/            # PostgreSQL pool + migration runner
│   │   ├── kafka/         # Producer / consumer wrapper
│   │   ├── redis/         # GEO location + caching
│   │   └── jwt/           # Token generation, validation, middleware
│   ├── migrations/        # SQL files (auto-applied on startup)
│   ├── go.mod
│   └── Dockerfile
├── infra/
│   └── docker-compose.yml
├── test_all.sh            # Automated 98-test suite
└── Makefile
```

## Quick Start

```bash
make up        # build & start everything
make logs      # tail ride-service logs
```

Wait ~30s for Kafka + Postgres to be ready, then the ride-service will connect and run migrations automatically.

## Services & Ports

| Service      | Host Port | URL                          |
|-------------|-----------|------------------------------|
| API Gateway | 8000      | http://localhost:8000        |
| ride-service| 8080      | http://localhost:8080        |
| PostgreSQL  | 5433      | `postgres://postgres:postgres@localhost:5433/ride_db` |
| pgAdmin     | 8081      | http://localhost:8081        |
| Redis       | 6380      | `localhost:6380`             |
| Kafka       | 9093      | `localhost:9093`             |
| Kafdrop     | 9001      | http://localhost:9001        |

## Kafka Topics

| Topic            | Producer           | Consumer         |
|-----------------|--------------------|------------------|
| ride.requested  | trips (on request) | matching         |
| driver.assigned | matching           | trips            |
| trip.completed  | trips (on end)     | (future billing) |

## Run All Tests (Automated)

```bash
bash test_all.sh
```

This runs **98 tests** covering every endpoint, edge case, and the full Kafka matching flow. Requires `curl` and `jq`.

---

## API Testing Guide

All requests go through the gateway at **http://localhost:8000**.

> **Tip:** Every response is JSON. Pipe any command through `| jq` for pretty output.

---

### 1. Health Check

The simplest test — no auth required.

```bash
curl -s http://localhost:8000/health | jq
```

**Expected response:**

```json
{
  "status": "ok",
  "service": "ride-service"
}
```

---

### 2. User (Rider) Registration

Create a rider account. Returns a JWT token and user details.

```bash
curl -s -X POST http://localhost:8000/users/register \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Sai Kumar",
    "email": "sai@test.com",
    "phone": "+919999999999",
    "password": "Pass123!"
  }' | jq
```

**Expected response (HTTP 201):**

```json
{
  "token": "eyJhbGciOi...",
  "user": {
    "id": "a1b2c3d4-...",
    "name": "Sai Kumar",
    "email": "sai@test.com",
    "phone": "+919999999999",
    "rating": 5
  }
}
```

**Save the token and user ID for later:**

```bash
RIDER_TOKEN="eyJhbGciOi..."
RIDER_ID="a1b2c3d4-..."
```

**Error cases to try:**

```bash
# Duplicate email → HTTP 409
curl -s -X POST http://localhost:8000/users/register \
  -H "Content-Type: application/json" \
  -d '{"name":"X","email":"sai@test.com","phone":"+910000000","password":"abc"}' | jq

# Invalid JSON → HTTP 400
curl -s -X POST http://localhost:8000/users/register \
  -H "Content-Type: application/json" \
  -d 'not json' | jq
```

---

### 3. User Login

Authenticate and get a fresh JWT.

```bash
curl -s -X POST http://localhost:8000/users/login \
  -H "Content-Type: application/json" \
  -d '{
    "email": "sai@test.com",
    "password": "Pass123!"
  }' | jq
```

**Expected response (HTTP 200):**

```json
{
  "token": "eyJhbGciOi...",
  "user": {
    "id": "a1b2c3d4-...",
    "name": "Sai Kumar",
    "email": "sai@test.com",
    "phone": "+919999999999",
    "rating": 5,
    "created_at": "2026-02-18T..."
  }
}
```

**Error cases to try:**

```bash
# Wrong password → HTTP 401
curl -s -X POST http://localhost:8000/users/login \
  -H "Content-Type: application/json" \
  -d '{"email":"sai@test.com","password":"WrongPass"}' | jq

# Non-existent email → HTTP 401
curl -s -X POST http://localhost:8000/users/login \
  -H "Content-Type: application/json" \
  -d '{"email":"nobody@test.com","password":"abc"}' | jq
```

---

### 4. Get User Profile

**Requires:** `Authorization: Bearer <token>`

```bash
curl -s http://localhost:8000/users/$RIDER_ID \
  -H "Authorization: Bearer $RIDER_TOKEN" | jq
```

**Expected response (HTTP 200):**

```json
{
  "id": "a1b2c3d4-...",
  "name": "Sai Kumar",
  "email": "sai@test.com",
  "phone": "+919999999999",
  "rating": 5,
  "created_at": "2026-02-18T..."
}
```

**Error cases to try:**

```bash
# No token → HTTP 401
curl -s http://localhost:8000/users/$RIDER_ID | jq

# Invalid token → HTTP 401
curl -s http://localhost:8000/users/$RIDER_ID \
  -H "Authorization: Bearer invalid.jwt.token" | jq

# Non-existent user → HTTP 404
curl -s http://localhost:8000/users/00000000-0000-0000-0000-000000000000 \
  -H "Authorization: Bearer $RIDER_TOKEN" | jq
```

---

### 5. Driver Registration

Create a driver account with vehicle info.

```bash
curl -s -X POST http://localhost:8000/drivers/register \
  -H "Content-Type: application/json" \
  -d '{
    "name": "Ravi Kumar",
    "email": "ravi@test.com",
    "phone": "+918888888888",
    "password": "Driver123!",
    "vehicle_type": "suv",
    "license_plate": "KA-01-AB-1234"
  }' | jq
```

**Expected response (HTTP 201):**

```json
{
  "token": "eyJhbGciOi...",
  "driver": {
    "id": "d5e6f7g8-...",
    "name": "Ravi Kumar",
    "email": "ravi@test.com",
    "phone": "+918888888888",
    "vehicle_type": "suv",
    "license_plate": "KA-01-AB-1234",
    "status": "available",
    "rating": 5
  }
}
```

**Save for later:**

```bash
DRIVER_TOKEN="eyJhbGciOi..."
DRIVER_ID="d5e6f7g8-..."
```

> **Note:** If `vehicle_type` is omitted, it defaults to `"sedan"`.

**Error cases to try:**

```bash
# Duplicate email → HTTP 409
curl -s -X POST http://localhost:8000/drivers/register \
  -H "Content-Type: application/json" \
  -d '{"name":"X","email":"ravi@test.com","phone":"+91000","password":"a","license_plate":"X"}' | jq
```

---

### 6. Driver Login

```bash
curl -s -X POST http://localhost:8000/drivers/login \
  -H "Content-Type: application/json" \
  -d '{
    "email": "ravi@test.com",
    "password": "Driver123!"
  }' | jq
```

**Expected response (HTTP 200):** Same structure as registration, with token + driver object.

---

### 7. Get Driver Profile

```bash
curl -s http://localhost:8000/drivers/$DRIVER_ID \
  -H "Authorization: Bearer $DRIVER_TOKEN" | jq
```

**Error cases:** Same as user profile (no token → 401, not found → 404).

---

### 8. Update Driver Location

Stores the driver's GPS position in Redis (GEO set). This is how the matching system finds nearby drivers.

```bash
curl -s -X PATCH http://localhost:8000/drivers/$DRIVER_ID/location \
  -H "Authorization: Bearer $DRIVER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "lat": 12.9716,
    "lng": 77.5946
  }' | jq
```

**Expected response (HTTP 200):**

```json
{
  "status": "location_updated"
}
```

---

### 9. Find Nearby Drivers

Query drivers within a radius (km) of a GPS point. Uses Redis GEO search.

```bash
curl -s "http://localhost:8000/drivers/nearby?lat=12.9716&lng=77.5946&radius=5" \
  -H "Authorization: Bearer $RIDER_TOKEN" | jq
```

**Expected response (HTTP 200):**

```json
{
  "drivers": [
    "d5e6f7g8-...",
    "another-driver-id-..."
  ]
}
```

> **Tip:** `radius` defaults to `5` km if omitted.

---

### 10. Request a Trip

**As a rider**, request a ride with pickup and drop coordinates.

```bash
curl -s -X POST http://localhost:8000/trips/request \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "pickupLat": 12.9716,
    "pickupLng": 77.5946,
    "dropLat": 12.9352,
    "dropLng": 77.6245
  }' | jq
```

**Expected response (HTTP 201):**

```json
{
  "trip_id": "t1r2i3p4-...",
  "status": "REQUESTED"
}
```

**Save for later:**

```bash
TRIP_ID="t1r2i3p4-..."
```

> **What happens behind the scenes:**
> 1. Trip is saved to PostgreSQL with status `REQUESTED`
> 2. A `ride.requested` event is published to Kafka
> 3. The matching consumer picks it up, finds the nearest driver in Redis, and publishes `driver.assigned`
> 4. The trip consumer updates the trip to `DRIVER_ASSIGNED`

---

### 11. Get Trip Details

```bash
curl -s http://localhost:8000/trips/$TRIP_ID \
  -H "Authorization: Bearer $RIDER_TOKEN" | jq
```

**Expected response (HTTP 200):**

```json
{
  "id": "t1r2i3p4-...",
  "rider_id": "a1b2c3d4-...",
  "driver_id": "d5e6f7g8-...",
  "pickup_lat": 12.9716,
  "pickup_lng": 77.5946,
  "drop_lat": 12.9352,
  "drop_lng": 77.6245,
  "status": "DRIVER_ASSIGNED",
  "requested_at": "2026-02-18T...",
  "created_at": "2026-02-18T..."
}
```

---

### 12. Assign Driver (Manual)

Manually assign a driver to a trip. Only works when trip is in `REQUESTED` or `MATCHING` status.

```bash
curl -s -X PATCH http://localhost:8000/trips/$TRIP_ID/assign \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"driverId\": \"$DRIVER_ID\"}" | jq
```

**Expected response (HTTP 200):**

```json
{
  "id": "t1r2i3p4-...",
  "status": "DRIVER_ASSIGNED",
  "driver_id": "d5e6f7g8-...",
  "..."
}
```

**Error cases to try:**

```bash
# Already assigned → HTTP 400
curl -s -X PATCH http://localhost:8000/trips/$TRIP_ID/assign \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -H "Content-Type: application/json" \
  -d "{\"driverId\": \"$DRIVER_ID\"}" | jq
# → {"error": "trip not found or invalid state for assignment"}
```

---

### 13. Start Trip

Moves the trip from `DRIVER_ASSIGNED` → `STARTED`. Sets `started_at`.

```bash
curl -s -X PATCH http://localhost:8000/trips/$TRIP_ID/start \
  -H "Authorization: Bearer $RIDER_TOKEN" | jq
```

**Expected response (HTTP 200):**

```json
{
  "id": "t1r2i3p4-...",
  "status": "STARTED",
  "started_at": "2026-02-18T...",
  "..."
}
```

**Error cases to try:**

```bash
# Not in DRIVER_ASSIGNED state → HTTP 400
curl -s -X PATCH http://localhost:8000/trips/$TRIP_ID/start \
  -H "Authorization: Bearer $RIDER_TOKEN" | jq
# → {"error": "trip not found or not in DRIVER_ASSIGNED state"}
```

---

### 14. End Trip

Moves the trip from `STARTED` → `COMPLETED`. Calculates fare and publishes `trip.completed` to Kafka.

**Option A — Auto-calculate fare via Haversine distance:**

```bash
curl -s -X PATCH http://localhost:8000/trips/$TRIP_ID/end \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}' | jq
```

**Option B — Provide explicit distance:**

```bash
curl -s -X PATCH http://localhost:8000/trips/$TRIP_ID/end \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"distanceKm": 25.5}' | jq
```

**Expected response (HTTP 200):**

```json
{
  "id": "t1r2i3p4-...",
  "status": "COMPLETED",
  "fare": 356,
  "completed_at": "2026-02-18T...",
  "..."
}
```

> **Fare formula:** `₹50 base + ₹12 × distance_km`
>
> | Distance | Fare |
> |----------|------|
> | 4.5 km   | ₹104 |
> | 10 km    | ₹170 |
> | 25.5 km  | ₹356 |

---

### 15. WebSocket — Real-time Trip Tracking

Connect to stream driver location updates for a trip.

**Using wscat (install with `npm install -g wscat`):**

```bash
wscat -c ws://localhost:8000/ws/trips/$TRIP_ID
```

**Using websocat:**

```bash
websocat ws://localhost:8000/ws/trips/$TRIP_ID
```

**Using browser JavaScript console:**

```javascript
const ws = new WebSocket("ws://localhost:8000/ws/trips/YOUR_TRIP_ID");
ws.onmessage = (e) => console.log(JSON.parse(e.data));
ws.onopen = () => console.log("Connected!");
ws.onclose = () => console.log("Disconnected");
```

**Messages received (when driver location is broadcast):**

```json
{
  "trip_id": "t1r2i3p4-...",
  "lat": 12.9720,
  "lng": 77.5950,
  "ts": 1771439400
}
```

---

### 16. Complete End-to-End Flow

Here's the full happy path you can copy-paste into your terminal:

```bash
# ─── Step 1: Register a rider ───
RIDER=$(curl -s -X POST http://localhost:8000/users/register \
  -H "Content-Type: application/json" \
  -d '{"name":"Test Rider","email":"rider@e2e.com","phone":"+911111111111","password":"Pass123!"}')
RIDER_TOKEN=$(echo $RIDER | jq -r '.token')
RIDER_ID=$(echo $RIDER | jq -r '.user.id')
echo "Rider ID: $RIDER_ID"

# ─── Step 2: Register a driver ───
DRIVER=$(curl -s -X POST http://localhost:8000/drivers/register \
  -H "Content-Type: application/json" \
  -d '{"name":"Test Driver","email":"driver@e2e.com","phone":"+912222222222","password":"Pass123!","vehicle_type":"sedan","license_plate":"KA-99-ZZ-0001"}')
DRIVER_TOKEN=$(echo $DRIVER | jq -r '.token')
DRIVER_ID=$(echo $DRIVER | jq -r '.driver.id')
echo "Driver ID: $DRIVER_ID"

# ─── Step 3: Set driver location (Bangalore) ───
curl -s -X PATCH http://localhost:8000/drivers/$DRIVER_ID/location \
  -H "Authorization: Bearer $DRIVER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"lat": 12.9716, "lng": 77.5946}' | jq

# ─── Step 4: Request a ride near the driver ───
TRIP=$(curl -s -X POST http://localhost:8000/trips/request \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"pickupLat":12.9716,"pickupLng":77.5946,"dropLat":12.9352,"dropLng":77.6245}')
TRIP_ID=$(echo $TRIP | jq -r '.trip_id')
echo "Trip ID: $TRIP_ID"

# ─── Step 5: Wait for Kafka auto-matching ───
echo "Waiting 5s for Kafka matching..."
sleep 5

# ─── Step 6: Verify driver was auto-assigned ───
curl -s http://localhost:8000/trips/$TRIP_ID \
  -H "Authorization: Bearer $RIDER_TOKEN" | jq '{status, driver_id}'

# ─── Step 7: Start the trip ───
curl -s -X PATCH http://localhost:8000/trips/$TRIP_ID/start \
  -H "Authorization: Bearer $RIDER_TOKEN" | jq '{status, started_at}'

# ─── Step 8: End the trip ───
curl -s -X PATCH http://localhost:8000/trips/$TRIP_ID/end \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}' | jq '{status, fare, completed_at}'
```

**Expected output flow:**

```
Rider ID: a1b2c3d4-...
Driver ID: d5e6f7g8-...
{ "status": "location_updated" }
Trip ID: t1r2i3p4-...
Waiting 5s for Kafka matching...
{ "status": "DRIVER_ASSIGNED", "driver_id": "d5e6f7g8-..." }
{ "status": "STARTED", "started_at": "2026-02-18T..." }
{ "status": "COMPLETED", "fare": 98.14, "completed_at": "2026-02-18T..." }
```

---

## Trip Status Lifecycle

```
REQUESTED → (Kafka matching) → DRIVER_ASSIGNED → STARTED → COMPLETED
     │                              │
     └── manual /assign ────────────┘             ↘ CANCELLED
```

| State             | How to reach it             |
|------------------|-----------------------------|
| `REQUESTED`      | `POST /trips/request`       |
| `DRIVER_ASSIGNED`| Auto (Kafka) or `PATCH /trips/:id/assign` |
| `STARTED`        | `PATCH /trips/:id/start`    |
| `COMPLETED`      | `PATCH /trips/:id/end`      |

## Fare Calculation

```
fare = ₹50 (base) + ₹12 × distance_km
```

If `distanceKm` is not provided in the end-trip request, the system computes distance using the **Haversine formula** from pickup to drop coordinates.

## JWT Authentication

- Tokens are valid for **24 hours**
- Include in all protected endpoints as: `Authorization: Bearer <token>`
- Two roles: `rider` (from user register/login) and `driver` (from driver register/login)
- Public endpoints (no token needed): `/health`, `/users/register`, `/users/login`, `/drivers/register`, `/drivers/login`
- All other endpoints require a valid JWT

## Teardown

```bash
make down      # stop containers
make clean     # stop + wipe volumes
```
# uber
