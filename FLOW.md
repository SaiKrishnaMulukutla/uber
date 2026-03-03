# Interview Preparation Guide — Ride-Hailing System

A complete reference for discussing this project in technical interviews.
Covers system design, detailed flows, tech stack decisions, every file, challenges, and progressive Q&A.

---

## Table of Contents

1. [Project Overview](#1-project-overview)
2. [Architecture](#2-architecture)
3. [System Flow — In Detail](#3-system-flow--in-detail)
4. [Tech Stack — Why Each Choice](#4-tech-stack--why-each-choice)
5. [File-by-File Breakdown](#5-file-by-file-breakdown)
6. [Database Schema](#6-database-schema)
7. [Challenges & Solutions](#7-challenges--solutions)
8. [Q&A — Basic Level](#8-qa--basic-level)
9. [Q&A — Moderate Level](#9-qa--moderate-level)
10. [Q&A — Advanced Level](#10-qa--advanced-level)

---

## 1. Project Overview

This is a **distributed ride-hailing backend** — similar in concept to Uber's core booking system — built entirely in Go. A rider requests a trip, the system finds the nearest available driver using geospatial search, assigns them automatically via an event-driven pipeline, tracks the trip through a well-defined state machine, and calculates a fare on completion.

The system is intentionally minimal but architecturally realistic: it uses the same core stack that production ride-hailing systems use — an event bus (Kafka) for decoupled matching, an in-memory geospatial index (Redis GEO) for real-time driver proximity, a relational database (PostgreSQL) for durable trip records, and WebSockets for live location streaming.

Everything runs as Docker containers orchestrated by Docker Compose. A single `make up` starts all 5 services (PostgreSQL, Redis, Kafka in KRaft mode, ride-service, NGINX).

---

## 2. Architecture

```mermaid
flowchart TD
    Client(["Client\nPostman / App / Browser"])

    subgraph gw ["API Gateway · :8000"]
        Nginx["nginx\nreverse proxy + WebSocket upgrade"]
    end

    subgraph svc ["Ride Service · :8080 (Go)"]
        Users["users\n/users/*"]
        Drivers["drivers\n/drivers/*"]
        Trips["trips\n/trips/*"]
        Matching["matching\nKafka consumer goroutine"]
        Tracking["tracking\n/ws/trips/:id"]
    end

    subgraph data ["Data Layer"]
        PG[("PostgreSQL\nusers · drivers · trips")]
        Redis[("Redis\nGEO locations · trip cache")]
        Kafka[["Kafka (KRaft)\nride.requested\ndriver.assigned\ntrip.completed"]]
    end

    Client -->|"HTTP / WS"| Nginx
    Nginx --> Users
    Nginx --> Drivers
    Nginx --> Trips
    Nginx -->|"WS upgrade"| Tracking

    Users   --> PG
    Drivers --> PG
    Drivers --> Redis
    Trips   --> PG
    Trips   --> Redis
    Trips   -->|"publish"| Kafka

    Kafka -->|"consume ride.requested"| Matching
    Matching --> Redis
    Matching -->|"publish driver.assigned"| Kafka
    Kafka -->|"consume driver.assigned"| Trips
    Trips -->|"publish trip.completed"| Kafka
```

### Layer Summary

| Layer | Components | Responsibility |
|---|---|---|
| **Gateway** | NGINX | Single entry point, reverse proxy, WebSocket upgrade, future TLS/rate-limiting |
| **Application** | Go ride-service | All business logic, HTTP handlers, background consumers |
| **Event Bus** | Kafka (KRaft mode) | Async decoupling of trip creation from matching; KRaft removes Zookeeper dependency |
| **Cache / GEO** | Redis | Sub-millisecond driver proximity lookup; trip caching |
| **Database** | PostgreSQL | Durable, ACID-compliant storage for all entities |

---

## 3. System Flow — In Detail

### A. Full Trip Lifecycle (End-to-End)

This is the most important flow to understand. Every arrow corresponds to actual code.

```
Client
  │
  │  POST /trips/request  {pickupLat, pickupLng, dropLat, dropLng}
  ▼
nginx (api-gateway/nginx.conf)
  │  proxy_pass http://ride_service
  ▼
trips/handler.go → Request()
  │  1. jwt.RequireAuth middleware — verifies Bearer token, extracts claims
  │  2. json.Decode request body
  │  3. validation.ValidateCoordinates(pickup) & ValidateCoordinates(drop)
  ▼
trips/service.go → Service.Request()
  │  4. uuid.New() — generate trip ID
  │  5. INSERT INTO trips (id, rider_id, pickup_lat, ..., status='REQUESTED')
  │  6. go func() { kafka.Publish("ride.requested", tripID, RideRequestedEvent) }
  │     └── goroutine so HTTP responds immediately (non-blocking)
  ▼
HTTP 201  {"trip_id": "...", "status": "REQUESTED"}

                    ┌── background ──────────────────────────────┐
                    │                                            │
                    ▼                                            │
          matching/matcher.go → Start()                         │
            │  Kafka consumer goroutine (group: "matching-group")│
            │  Consumes ride.requested event                     │
            │  redis.GetNearbyDrivers(pickup.lat, pickup.lng,    │
            │                        5.0 km, count=1)            │
            │  → GEOSEARCH driver:locations FROMLONLAT           │
            │            radius 5 km COUNT 1 ASC                 │
            │                                                    │
            │  if Redis error → return err (Kafka retries msg)  │
            │  if no drivers found → log, return nil (no retry) │
            │                                                    │
            │  assigned := drivers[0]  (nearest)                │
            │  kafka.Publish("driver.assigned", tripID,          │
            │                DriverAssignedEvent)                │
            │  redis.RemoveDriverLocation(assigned)              │
            │  → ZREM driver:locations driverID                  │
            │    (prevents double-assignment)                    │
            └────────────────────────────────────────────────────┘

                    ┌── background ──────────────────────────────┐
                    │                                            │
                    ▼                                            │
          trips/service.go → StartDriverAssignedConsumer()      │
            │  Kafka consumer goroutine (group: "trip-driver-assigned")
            │  Consumes driver.assigned event                   │
            │  UPDATE trips SET driver_id=$1, status='DRIVER_ASSIGNED'
            │  WHERE id=$2 AND status IN ('REQUESTED','MATCHING')│
            └────────────────────────────────────────────────────┘

  (5 seconds later — Kafka async)

Client
  │  GET /trips/:id
  ▼  → {"status": "DRIVER_ASSIGNED", "driver_id": "..."}

  │  PATCH /trips/:id/start
  ▼
trips/service.go → Start()
     UPDATE trips SET status='STARTED', started_at=NOW()
     WHERE id=$1 AND status='DRIVER_ASSIGNED'
     → 0 rows affected = error (state guard)

  │  PATCH /trips/:id/end  {distanceKm: 10.5}
  ▼
trips/service.go → End()
     km = distanceKm  (or haversineKm(pickup, drop) if not provided)
     fare = 50.0 + km * 12.0
     UPDATE trips SET status='COMPLETED', fare=$1, completed_at=NOW()
     WHERE id=$4 AND status='STARTED'          ← atomic SQL guard (no TOCTOU race)
     if RowsAffected() == 0 → 400 "trip not in STARTED state"
     go func() { kafka.Publish("trip.completed", tripID, TripCompletedEvent) }

  ▼
HTTP 200  {"status": "COMPLETED", "fare": 176, "completed_at": "..."}
```

---

### B. Authentication Flow

```
POST /users/register  {name, email, phone, password}
  │
  ▼
users/handler.go → Register()
  │  validation.ValidateName(name)     → len 2-200
  │  validation.ValidateEmail(email)   → regex + len ≤200
  │  validation.ValidatePhone(phone)   → E.164 regex
  │  validation.ValidatePassword(pass) → len 6-100
  ▼
users/service.go → Service.Register()
  │  SELECT EXISTS(SELECT 1 FROM users WHERE email=$1)  → 409 if exists
  │  SELECT EXISTS(SELECT 1 FROM users WHERE phone=$1)  → 409 if exists
  │  bcrypt.GenerateFromPassword(password, cost=10)
  │  INSERT INTO users (id, name, email, phone, password_hash, rating=5.0)
  │  jwt.Generate(userID, email, "rider")
  │   └── Claims{UserID, Email, Role="rider", exp=now+24h}
  │   └── HMAC-SHA256 signed with JWT_SECRET
  ▼
HTTP 201  {"token": "eyJ...", "user": {...}}

─────────────────────────────────────────────────────────────

Every protected request:
  │
  ▼
cmd/main.go: r.Use(jwt.OptionalAuth)   ← global middleware
  │  if Authorization: Bearer <token>
  │    jwt.Validate(token) → parse Claims
  │    r.WithContext(context.WithValue(ctx, "jwt_claims", claims))
  │  else → pass through (claims = nil in context)
  ▼
Route-level: r.Use(jwt.RequireAuth)    ← per-router middleware
  │  jwt.GetClaims(ctx) == nil → HTTP 401 {"error":"unauthorized"}
  │  else → next handler
  ▼
Handler: claims := jwt.GetClaims(r.Context())
         claims.UserID  →  authenticated user's ID
         claims.Role    →  "rider" or "driver"
```

---

### C. Driver Location & Geo-Matching Flow

```
PATCH /drivers/:id/location  {lat: 12.9716, lng: 77.5946}
  │
  ▼
drivers/handler.go → UpdateLocation()
  │  validation.ValidateCoordinates(lat, lng)
  │   lat ∈ [-90, 90], lng ∈ [-180, 180]
  ▼
drivers/service.go → UpdateLocation()
  │  redis.SetDriverLocation(driverID, lat, lng)
  │   → GEOADD driver:locations 77.5946 12.9716 "driverID"
  │      Redis stores as sorted set with geohash score
  ▼
HTTP 200  {"status": "location_updated"}

─────────────────────────────────────────────────────────────

When ride.requested event arrives at Matcher:
  │
  ▼
redis.GetNearbyDrivers(pickup.lat, pickup.lng, 5.0, 1)
  │  → GEOSEARCH driver:locations
  │       FROMLONLAT 77.5946 12.9716
  │       BYRADIUS 5 km
  │       ASC COUNT 1
  │  Returns: ["driverID_nearest"]   (sorted by distance, closest first)
  │
  ├── Redis error → return err (Kafka does NOT commit offset → retries)
  │
  ├── len(drivers) == 0 → log "no nearby drivers", return nil (offset committed)
  │
  └── drivers[0] = nearest driver
       kafka.Publish("driver.assigned", {TripID, DriverID: drivers[0]})
       redis.RemoveDriverLocation(drivers[0])
        → ZREM driver:locations "driverID"
           (atomic removal — driver won't be matched to another trip)
```

---

### D. WebSocket Live Tracking Flow

```
Client opens: ws://localhost:8000/ws/trips/:id
  │
  ▼
nginx: proxy_http_version 1.1
       proxy_set_header Upgrade $http_upgrade
       proxy_set_header Connection "upgrade"
       proxy_read_timeout 3600s   ← keeps long connections alive
  │
  ▼
tracking/ws.go → Hub.HandleWS()
  │  websocket.Upgrader.Upgrade(w, r, nil)
  │   CheckOrigin: always true (no CORS restriction for WS)
  │  safeC := &safeConn{Conn: conn}       ← wraps conn with its own sync.Mutex
  │  hub.mu.Lock()
  │  hub.conns[tripID] = append(hub.conns[tripID], safeC)
  │  hub.mu.Unlock()
  │
  │  for { conn.ReadMessage() }   ← blocks, keeps goroutine alive
  │  on error (disconnect) → break
  │
  │  removeConn(tripID, safeC)
  │   removes conn from slice
  │   if slice empty → delete(hub.conns, tripID)  ← memory cleanup
  │  conn.Close()

─────────────────────────────────────────────────────────────

When driver location broadcast happens:
  │
  ▼
Hub.BroadcastLocation(tripID, lat, lng)
  │  hub.mu.RLock()                      ← read lock (concurrent safe)
  │  conns := hub.conns[tripID]
  │  hub.mu.RUnlock()
  │
  │  msg := {trip_id, lat, lng, ts: time.Now().Unix()}
  │
  │  for each safeC in conns:
  │    safeC.mu.Lock()                   ← per-connection mutex (prevents concurrent writes)
  │    safeC.WriteJSON(msg)
  │    safeC.mu.Unlock()
  │     → {"trip_id":"...","lat":12.972,"lng":77.595,"ts":1772301481}
  ▼
All connected clients receive real-time location update
```

---

### E. Startup Sequence (cmd/main.go)

```
main()
  │
  1. jwt.Init(JWT_SECRET)          ← fails fast if secret missing
  2. db.Connect(DATABASE_URL)      ← retry 30x @ 2s = up to 60s wait
  3. database.RunMigrations(fs)    ← apply V1, V2, V3 SQL if not applied
  4. redis.NewClient(REDIS_ADDR)   ← retry 20x @ 2s
  5. kafka.NewClient(brokers)
  6. kafkaClient.EnsureTopics(...)  ← retry 20x @ 3s; creates 3 topics
  7. userSvc, driverSvc, tripSvc   ← wire services
  8. matching.NewMatcher.Start(ctx) ← goroutine: consume ride.requested
  9. tripSvc.StartDriverAssignedConsumer(ctx) ← goroutine: consume driver.assigned
  10. tracking.NewHub()             ← WebSocket hub
  11. chi.NewRouter() + middleware + routes
  12. http.Server.ListenAndServe()  ← serve on :8080
  13. wait for SIGINT/SIGTERM
  14. srv.Shutdown(10s timeout)     ← graceful: finish in-flight requests
  15. cancel()                      ← stop Kafka consumer goroutines
```

---

## 4. Tech Stack — Why Each Choice

### Go
- **Goroutines** make it trivial to run Kafka consumers as background tasks alongside the HTTP server in the same process
- **Compiled to a single binary** — the Docker runtime image is just alpine + the binary (~15 MB)
- **Static typing** catches bugs at compile time; no runtime type errors
- **Standard library** covers HTTP, crypto, JSON — minimal external dependencies
- Alternative considered: Node.js (slower, callback hell for concurrent consumers), Python (GIL limits true parallelism)

### PostgreSQL
- **ACID transactions** ensure the trip state machine is consistent — if two consumers try to assign the same driver, only one `UPDATE ... WHERE status IN ('REQUESTED','MATCHING')` wins
- **UUID primary keys** — globally unique, no auto-increment coordination needed across shards
- **Foreign keys** — `trips.rider_id REFERENCES users(id)` enforces referential integrity at the DB level
- **Indexes** on `email`, `phone`, `status`, `rider_id`, `driver_id` — queries never do full table scans
- Alternative: MySQL (less powerful JSONB/extensions), MongoDB (no joins, weaker consistency)

### Redis GEO
- **GEOADD / GEOSEARCH** are built-in Redis commands since v3.2 — no PostGIS extension needed
- **O(log N)** geospatial query against millions of drivers in under 1ms
- **Sorted set** backing — ZREM atomically removes a driver from the pool after assignment
- **In-memory** — driver location updates (every few seconds per driver) would overwhelm a relational DB
- Alternative: PostGIS (heavier, requires PostgreSQL extension), Elasticsearch geo_distance (overkill)

### Kafka (KRaft mode)
- **Decouples** trip creation from matching — the HTTP handler returns immediately; matching runs async
- **Durable event log** — if the matcher crashes, it restarts and reprocesses from the last committed offset
- **Consumer groups** — `matching-group` and `trip-driver-assigned` are independent; adding a `billing-group` requires zero code changes to existing consumers
- **Replay** — can reprocess all events from the beginning for analytics or debugging
- **3 partitions per topic** — allows 3 parallel consumers for horizontal scaling
- **KRaft mode** — Kafka manages its own metadata using the Raft consensus protocol; Zookeeper is not needed. Simpler setup, fewer moving parts, and the direction Kafka is heading in production.
- Alternative: RabbitMQ (message-queue semantics, not a log; no replay), Redis Pub/Sub (no persistence, no consumer groups)

### NGINX (API Gateway)
- **WebSocket upgrade** — sets `Upgrade` and `Connection: upgrade` headers that Go's `net/http` doesn't send by default
- **Single entry point** — all traffic on port 8000; Go service on 8080 is not exposed externally
- **`server_tokens off`** — hides nginx version from attackers
- **`proxy_read_timeout 3600s`** — keeps WebSocket connections alive for 1 hour
- Alternative: Traefik (heavier config), Kong (enterprise), AWS API Gateway (cloud-only)

### JWT (HS256)
- **Stateless** — no session table in the database; every service can validate tokens independently
- **Role in claims** — `"role": "rider"` or `"driver"` embedded; no extra DB lookup on each request
- **24-hour expiry** — balance between security and UX
- **HS256** — symmetric HMAC; simpler than RS256 (asymmetric); fine for a single service
- Risk: token revocation requires a denylist (not implemented — known limitation)

### bcrypt
- **Salted by design** — each hash includes a random salt; identical passwords produce different hashes
- **Adaptive cost** — `cost=10` takes ~100ms; can increase as hardware gets faster
- **Resistant to GPU attacks** — memory-hard algorithm
- Alternative: argon2id (more modern), scrypt (similar), MD5/SHA (never use for passwords)

### chi Router
- **100% net/http compatible** — handlers are standard `http.Handler`; no lock-in
- **Middleware chaining** — `r.Use(jwt.RequireAuth)` applies to a sub-router cleanly
- **URL parameters** via `chi.URLParam(r, "id")` — clean and simple
- Alternative: gin (heavier, own context type), echo (similar), gorilla/mux (older)

### pgx/v5
- **Native PostgreSQL protocol** — no `database/sql` overhead
- **`pgxpool`** — connection pool; handles max connections, idle timeout, health checks
- **Parameterized queries** built-in — prevents SQL injection
- Alternative: `database/sql` + `lib/pq` (slower, less features), GORM (ORM overhead, hides queries)

---

## 5. File-by-File Breakdown

### `ride-service/cmd/main.go`
**Purpose:** Application entry point. Wires every component together and starts the HTTP server.

**Key decisions:**
- Startup order is strict: JWT → DB → Redis → Kafka → services → consumers → router
- Uses `context.WithCancel` — the root context is passed to all Kafka consumers; calling `cancel()` on shutdown stops all goroutines cleanly
- `env(key, fallback)` helper — reads environment variables with defaults for local development
- Graceful shutdown: `signal.Notify(quit, SIGINT, SIGTERM)` + `srv.Shutdown(10s timeout)` — in-flight HTTP requests finish; consumers stop

**Interview point:** "Why not use `os.Exit` on shutdown?" — `defer` statements wouldn't run, connections wouldn't close cleanly.

---

### `ride-service/internal/users/handler.go`
**Purpose:** HTTP layer for user endpoints. Parses requests, validates input, calls service, writes JSON responses.

**Key decisions:**
- Validation happens in the handler (not the service) — handler's job is to validate HTTP input
- `writeJSON` helper avoids repeating `Content-Type` header and `json.Encode` everywhere
- `jwt.RequireAuth` applied at sub-router level — `/users/{id}` is protected; register/login are public

**What NOT to do:** Don't put DB queries in handlers — that's the service's job.

---

### `ride-service/internal/users/service.go`
**Purpose:** Business logic for user accounts. No HTTP knowledge here.

**Key decisions:**
- Email and phone uniqueness checked before INSERT with proper error propagation:
  ```go
  if err := s.db.QueryRow(ctx, "SELECT EXISTS(...)").Scan(&exists); err != nil {
      return nil, err  // DB error surfaced cleanly (not swallowed with _ =)
  }
  ```
  Previously used `_ =` which would silently ignore a DB error and let `exists` stay `false`, proceeding to a confusing INSERT failure.
- `bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)` — cost=10 by default
- Login uses a deliberately vague error: `"invalid credentials"` — never reveal whether email or password was wrong
- `jwt.Generate(id, email, "rider")` — role is always `"rider"` for users

---

### `ride-service/internal/users/model.go`
**Purpose:** Data structures for the users domain.

**Key decisions:**
- `PasswordHash string json:"-"` — the `-` tag ensures the hash is NEVER included in any JSON response
- `Rating float64 json:"rating"` — starts at 5.0, future feature to update
- Separate `RegisterRequest`, `LoginRequest`, `AuthResponse` structs — each endpoint has exactly the fields it needs

---

### `ride-service/internal/drivers/handler.go`
**Purpose:** HTTP layer for driver endpoints.

**Key decisions:**
- `r.Get("/nearby", h.GetNearby)` must be registered **before** `r.Get("/{id}", h.GetByID)` — chi routes in order; `/nearby` would otherwise match as an ID
- `strconv.ParseFloat` for query params `lat`, `lng`, `radius` — they come as strings from URL
- Explicit check for empty `lat`/`lng` before parsing — gives clearer error than a parse failure

---

### `ride-service/internal/drivers/service.go`
**Purpose:** Business logic for driver accounts and location management.

**Key decisions:**
- `UpdateLocation` calls `redis.SetDriverLocation` — driver location lives in Redis, not PostgreSQL
- `GetNearby` delegates to `redis.GetNearbyDrivers` — purely a Redis GEO query

---

### `ride-service/internal/trips/handler.go`
**Purpose:** HTTP layer for the trip lifecycle.

**Key decisions:**
- `r.Use(jwt.RequireAuth)` — ALL trip endpoints require auth; applied at router level, not per-handler
- `Request` handler extracts `claims.UserID` from context — the rider ID comes from the JWT, not the request body (prevents a rider from creating trips as another user)
- `End` handler: `json.NewDecoder(r.Body).Decode(&req)` without error check — body is optional for end-trip

---

### `ride-service/internal/trips/service.go`
**Purpose:** Core business logic — the trip state machine.

**Key decisions:**

*State machine enforcement — all atomic at DB level:*
```go
// AssignDriver — only works from REQUESTED or MATCHING
WHERE id=$3 AND status IN ($4,$5)   // $4=REQUESTED, $5=MATCHING
// RowsAffected() == 0 → 400 "trip not found or invalid state"

// Start — only works from DRIVER_ASSIGNED
WHERE id=$3 AND status=$4           // $4=DRIVER_ASSIGNED
// RowsAffected() == 0 → 400 "trip not found or not in DRIVER_ASSIGNED state"

// End — only works from STARTED (SQL guard, not Go code — prevents TOCTOU race)
WHERE id=$4 AND status=$5           // $5=STARTED
// RowsAffected() == 0 → 400 "trip not in STARTED state"
// Two concurrent End calls race at DB level — only one UPDATE matches; the other gets 0 rows
```

*Fare calculation:*
```go
fare = 50.0 + km * 12.0   // ₹50 base + ₹12/km
```

*Async Kafka publish:*
```go
go func() {
    // Uses context.Background() NOT the request context
    // Request context is cancelled when HTTP response is sent
    kafka.Publish(context.Background(), topic, id, event)
}()
```

*Haversine formula:* Calculates great-circle distance between two GPS coordinates. Used when `distanceKm` is not provided in end-trip request.

---

### `ride-service/internal/trips/model.go`
**Purpose:** Trip domain types and status constants.

**Key decisions:**
- `DriverID *string json:"driver_id,omitempty"` — pointer so it can be nil (unassigned); `omitempty` excludes it from JSON when nil
- `Fare *float64 json:"fare,omitempty"` — nil until trip is completed
- Status constants as package-level `const` — prevents magic strings scattered through code
- `TripRequest` uses camelCase JSON keys (`pickupLat` not `pickup_lat`) matching the API contract

---

### `ride-service/internal/matching/matcher.go`
**Purpose:** Kafka consumer that implements automatic driver matching.

**Key decisions:**
- Runs as a goroutine via `Start(ctx)` — never blocks the main goroutine
- Consumer group `"matching-group"` — if multiple instances run, Kafka partitions are distributed among them (horizontal scaling)
- `GetNearbyDrivers(..., count=1)` — only fetches the single nearest driver
- `RemoveDriverLocation` immediately after selection — prevents the same driver being assigned to two trips (at-most-once assignment within this service)
- **Redis error vs no drivers handled separately:**
  - `err != nil` → `return err` — Kafka does NOT commit the offset; the message is retried automatically on the next consumer poll. A Redis outage won't silently drop trips.
  - `len(drivers) == 0` → `return nil` — no drivers is an expected, non-error state; Kafka commits the offset. Trip stays in `REQUESTED` for manual assignment via `/assign`.

**Interview point:** "Why not return error for no drivers?" — returning an error would cause infinite retries on the same message. No available drivers is expected (normal off-peak behavior); a Redis crash is unexpected and should retry.

---

### `ride-service/internal/tracking/ws.go`
**Purpose:** WebSocket hub for real-time driver location streaming.

**Key decisions:**
- `safeConn` struct wraps `*websocket.Conn` with its own `sync.Mutex` — gorilla/websocket connections are **not safe for concurrent writes**. When multiple goroutines call `BroadcastLocation` simultaneously for the same trip, each `WriteJSON` must be serialized per connection.
- `Hub.conns map[string][]*safeConn` — one slice of safe connections per trip ID
- `Hub.mu sync.RWMutex` — protects the `conns` map itself; multiple readers (broadcasts) can hold `RLock` concurrently; adding/removing connections requires `Lock`
- `CheckOrigin: func(...) bool { return true }` — allows any origin (needed for browser clients during development)
- `removeConn` deletes the map key when empty — prevents unbounded map growth

**Interview point:** "Why two mutexes?" — the `Hub.mu` protects the map (which connections exist); each `safeConn.mu` protects the individual connection's write channel. They serve different purposes and are held for different durations.

---

### `ride-service/internal/events/events.go`
**Purpose:** Shared event payload structs used by both producers and consumers.

**Key decisions:**
- Single package prevents import cycles — both `trips` and `matching` import `events`, neither imports each other
- `LatLng` embedded struct — reused in both `RideRequestedEvent.Pickup` and `.Drop`
- All string timestamps (RFC3339 format) — JSON serializable without custom marshalling

---

### `ride-service/pkg/db/postgres.go`
**Purpose:** PostgreSQL connection pool and migration runner.

**Key decisions:**

*Connection retry:*
```go
for i := 0; i < 30; i++ {
    pool, err = pgxpool.New(ctx, dsn)
    if err == nil { if pool.Ping(ctx) == nil { return &DB{Pool: pool} } }
    time.Sleep(2 * time.Second)
}
```
PostgreSQL takes ~10s to start in Docker; 30 × 2s = 60s maximum wait.

*Migration runner:*
- Creates `schema_migrations` table on first run
- Reads `.sql` files from embedded filesystem (`//go:embed` in `migrations/embed.go`)
- Sorts files lexicographically — V1, V2, V3 run in order
- Idempotent — skips already-applied migrations (checked by filename in `schema_migrations`)
- No rollback support (simple forward-only migrations)

---

### `ride-service/pkg/kafka/client.go`
**Purpose:** Kafka producer and consumer wrapper.

**Key decisions:**

*EnsureTopics:*
- Creates topics with `NumPartitions: 3` — allows 3 parallel consumers per topic
- `ReplicationFactor: 1` — single broker in dev; increase for production
- Retry loop handles Kafka's ~15s startup time in Docker

*Publish:*
- Creates a new `kafka.Writer` per call — simpler than managing a shared writer with connection pooling
- `LeastBytes` balancer — sends to partition with fewest in-flight bytes
- `tripID` as message key — ensures all events for the same trip go to the same partition (ordering guarantee)

*Subscribe:*
- `GroupID` — consumer group offset management; restart resumes from last committed offset
- `MinBytes: 1` — fetch immediately when 1 byte is available (low latency)
- `MaxBytes: 10e6` — 10MB max fetch size

---

### `ride-service/pkg/redis/client.go`
**Purpose:** Redis operations for driver geolocation and trip caching.

**Key decisions:**

*GEO commands:*
```
GEOADD driver:locations <lng> <lat> <driverID>
  → stores as sorted set with geohash score

GEOSEARCH driver:locations FROMLONLAT <lng> <lat>
          BYRADIUS <km> km ASC COUNT <n>
  → returns driverIDs sorted by distance (nearest first)

ZREM driver:locations <driverID>
  → removes from sorted set; driver no longer matchable
```

*Trip cache:*
- `HSet` + `Expire(24h)` in a **pipeline** — two Redis commands in one round-trip
- Key pattern: `trip:<tripID>` — predictable, easy to debug with `redis-cli`

*Retry on connect:*
- 20 × 2s = 40s maximum wait for Redis startup

---

### `ride-service/pkg/jwt/jwt.go`
**Purpose:** JWT generation, validation, and HTTP middleware.

**Key decisions:**

*Two-middleware pattern:*
- `OptionalAuth` — global, extracts claims if token present, passes through if not; allows public endpoints to still read identity
- `RequireAuth` — per-router, rejects with 401 if no claims in context; applied only to protected route groups

*Context key:*
```go
type ctxKey string
const claimsCtxKey ctxKey = "jwt_claims"
```
Using a custom type (not `string`) as context key prevents collision with other packages that might use the same string key.

*Algorithm check:*
```go
if _, ok := t.Method.(*gojwt.SigningMethodHMAC); !ok { return nil, error }
```
Prevents the "algorithm confusion" attack where an attacker sends `"alg": "none"`.

---

### `ride-service/pkg/validation/validation.go`
**Purpose:** Centralised input validation functions used by all handlers.

**Key decisions:**
- Regex compiled once at package init (`var emailRegex = regexp.MustCompile(...)`) — not per-request
- `ValidateCoordinates` checks mathematical bounds: lat ∈ [-90, 90], lng ∈ [-180, 180]
- `ValidateName` trims spaces before length check — `"  a  "` shouldn't pass as a 2-char name
- All validators return `bool` — handlers decide what error message to send

---

### `ride-service/migrations/`

**`V1_create_users.sql`**
```sql
CREATE TABLE users (
    id            UUID PRIMARY KEY,          -- no auto-increment; UUID generated in Go
    email         VARCHAR(200) UNIQUE,        -- unique constraint at DB level (backup)
    phone         VARCHAR(50) UNIQUE,
    password_hash VARCHAR(255),               -- bcrypt output is always 60 chars
    rating        DOUBLE PRECISION DEFAULT 5.0
);
CREATE INDEX idx_users_email ON users(email); -- fast login lookup
CREATE INDEX idx_users_phone ON users(phone); -- fast duplicate check
```

**`V2_create_drivers.sql`**
```sql
CREATE TABLE drivers (
    status        VARCHAR(20) DEFAULT 'available',  -- available|busy|offline
    vehicle_type  VARCHAR(50) DEFAULT 'sedan',       -- optional on register
    license_plate VARCHAR(20)
);
CREATE INDEX idx_drivers_status ON drivers(status);  -- filter available drivers
```

**`V3_create_trips.sql`**
```sql
CREATE TABLE trips (
    rider_id  UUID NOT NULL REFERENCES users(id),    -- mandatory FK
    driver_id UUID REFERENCES drivers(id),            -- nullable (unassigned initially)
    fare      DECIMAL(12,2),                          -- null until completed
    status    VARCHAR(30) DEFAULT 'REQUESTED'
);
-- Indexes for the most common queries:
CREATE INDEX idx_trips_rider_id  ON trips(rider_id);  -- rider's trip history
CREATE INDEX idx_trips_driver_id ON trips(driver_id); -- driver's trip history
CREATE INDEX idx_trips_status    ON trips(status);    -- find active trips
```

**`embed.go`**
```go
//go:embed *.sql
var FS embed.FS
```
Embeds all SQL files into the Go binary at compile time — no external migration tool needed; migrations run automatically on startup.

---

### `api-gateway/nginx.conf`
**Purpose:** NGINX reverse proxy configuration.

**Key decisions:**
```nginx
upstream ride_service { server ride-service:8080; }
```
Docker service name `ride-service` resolves via Docker's internal DNS.

```nginx
proxy_http_version 1.1;
proxy_set_header Upgrade $http_upgrade;
proxy_set_header Connection "upgrade";
proxy_read_timeout 3600s;
proxy_send_timeout 3600s;
```
Required for WebSocket connections. Without `Upgrade` header, the WS handshake fails. 3600s keeps long-lived connections open.

```nginx
proxy_set_header Authorization $http_authorization;
```
Passes the JWT Bearer token through to the Go service (NGINX would strip custom headers by default).

---

### `api-gateway/Dockerfile`
```dockerfile
FROM nginx:alpine
COPY nginx.conf /etc/nginx/nginx.conf
EXPOSE 8000
```
Three lines. `nginx:alpine` is ~5 MB. No custom build step needed.

---

### `ride-service/Dockerfile`
**Two-stage build:**

*Stage 1 (build):*
```dockerfile
COPY go.mod go.sum ./
RUN go mod tidy          # download dependencies — cached if go.mod unchanged
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /ride-service ./cmd
```
Copying `go.mod`/`go.sum` first is a Docker layer cache optimization — dependency download is only re-run when `go.mod` changes, not on every source code change.

*Stage 2 (runtime):*
```dockerfile
FROM alpine:3.19
RUN apk add --no-cache ca-certificates tzdata wget
```
`wget` is needed for the Docker health check: `CMD wget -qO- http://localhost:8080/health`.
`ca-certificates` enables HTTPS calls. `tzdata` for correct time zones.

---

### `infra/docker-compose.yml`
**Purpose:** Orchestrates all 5 services in the correct dependency order: `postgres`, `redis`, `kafka`, `ride-service`, `api-gateway`.

**Key decisions:**
- `env_file: .env` — all secrets from git-ignored file, not hardcoded
- PostgreSQL health check:
  ```yaml
  healthcheck:
    test: ["CMD-SHELL", "pg_isready -U ${POSTGRES_USER}"]
  ```
  ride-service won't start until PostgreSQL passes health check.
- Port mapping: `5433:5432` (PostgreSQL), `6380:6379` (Redis) — avoids conflict with locally installed instances
- **Kafka in KRaft mode** — no Zookeeper service required. `KAFKA_PROCESS_ROLES: broker,controller` makes the single Kafka node both broker and controller. `CLUSTER_ID` must be a stable base64 UUID.
- Kafka `KAFKA_ADVERTISED_LISTENERS: PLAINTEXT://kafka:9092` — internal Docker network hostname; not `localhost`

**What was removed (and why):**
- **Zookeeper** — KRaft mode makes it redundant; eliminates one service and its coordination overhead
- **pgAdmin** — heavyweight GUI tool not needed in dev; `psql` or DBeaver serve the same purpose
- **Kafdrop** — Kafka topic browser; useful for debugging but not needed for normal development

---

### `infra/.env` (git-ignored)
Stores secrets locally. Never committed. Contains:
- `POSTGRES_PASSWORD` — 40-char hex (URL-safe, no special characters that break `DATABASE_URL`)
- `JWT_SECRET` — 64-char base64 (384-bit entropy)

Passwords are generated with `openssl rand -hex 40` / `openssl rand -base64 64` — cryptographically strong, not human-memorable strings.

### `infra/.env.example` (committed)
Template showing required variables with placeholder values. Safe to commit.

---

### `Makefile`
```makefile
make up      # docker-compose up -d --build
make down    # docker-compose down
make logs    # docker-compose logs -f ride-service
make clean   # docker-compose down -v --remove-orphans
```
Convenience wrappers so contributors don't need to remember Docker Compose flags.

---

### `test/test_all.sh`
98 automated tests using `curl` + `jq`. Covers every endpoint, every validation error case, and the full Kafka matching flow (with a 5s sleep for async processing).

### `test/demo.sh`
9-step end-to-end walkthrough with colored output. Auto-fallback to manual assignment if Kafka matching doesn't complete in 5s.

### `test/postman_collection.json`
Postman v2.1 collection with test scripts that auto-capture tokens and IDs between requests. Import into Postman for interactive testing.

---

## 6. Database Schema

```
users
  id            UUID PK
  name          VARCHAR(200)
  email         VARCHAR(200) UNIQUE
  phone         VARCHAR(50) UNIQUE
  password_hash VARCHAR(255)          ← bcrypt, never in JSON
  rating        DOUBLE PRECISION = 5.0
  created_at    TIMESTAMPTZ

drivers
  id            UUID PK
  name          VARCHAR(200)
  email         VARCHAR(200) UNIQUE
  phone         VARCHAR(50) UNIQUE
  password_hash VARCHAR(255)
  vehicle_type  VARCHAR(50) = 'sedan'
  license_plate VARCHAR(20)
  status        VARCHAR(20) = 'available'   ← available|busy|offline
  rating        DOUBLE PRECISION = 5.0
  created_at    TIMESTAMPTZ

trips
  id           UUID PK
  rider_id     UUID FK → users.id NOT NULL
  driver_id    UUID FK → drivers.id NULL     ← null until assigned
  pickup_lat   DOUBLE PRECISION
  pickup_lng   DOUBLE PRECISION
  drop_lat     DOUBLE PRECISION
  drop_lng     DOUBLE PRECISION
  fare         DECIMAL(12,2) NULL            ← null until completed
  status       VARCHAR(30) = 'REQUESTED'
  requested_at TIMESTAMPTZ
  started_at   TIMESTAMPTZ NULL
  completed_at TIMESTAMPTZ NULL
  created_at   TIMESTAMPTZ

schema_migrations
  version    VARCHAR(255) PK               ← filename e.g. "V1_create_users.sql"
  applied_at TIMESTAMPTZ
```

**Indexes:**
- `users`: email, phone
- `drivers`: email, status
- `trips`: rider_id, driver_id, status

---

## 7. Challenges & Solutions

| Challenge | Root Cause | Solution |
|---|---|---|
| Services crash on startup | PostgreSQL/Kafka/Redis take 10-20s to start in Docker | Retry loops: DB (30×2s), Redis (20×2s), Kafka (20×3s) |
| Driver double-assignment | Race between two `ride.requested` events finding the same driver | `ZREM driver:locations driverID` immediately after selection — atomic removal from GEO set |
| Trip End TOCTOU race | Two concurrent End requests both pass a Go-level status check, both write different fares | Moved status guard into SQL: `WHERE id=$4 AND status='STARTED'`; check `RowsAffected() == 0` — DB row lock makes it atomic |
| Trip state machine violations | Concurrent requests could move trip to wrong state | `UPDATE ... WHERE status IN (expected_states)` — 0 rows affected = 400 error |
| Kafka publish blocks HTTP | `kafka.Publish` is a network call that can take hundreds of ms | Publish in `go func()` with `context.Background()` — HTTP responds in <5ms |
| Redis error silently drops trips | Matcher returned `nil` for both "no drivers" and Redis errors | Split handling: `return err` for Redis failure (Kafka retries), `return nil` for empty drivers (expected) |
| DB exists-check error swallowed | `_ = s.db.QueryRow(...).Scan(&exists)` ignores Scan error; `exists` stays false | Propagate error: `if err := ...Scan(&exists); err != nil { return nil, err }` |
| Concurrent WebSocket writes | `gorilla/websocket` conns are not safe for concurrent writes; multiple broadcast goroutines could race | `safeConn` struct wraps conn + `sync.Mutex`; each write locks its own conn mutex |
| Secrets in version control | `.env` was accidentally un-ignored (`!infra/.env` in `.gitignore`) | Fixed `.gitignore`; removed file from git tracking; strong secrets via `openssl rand` |
| WebSocket memory leak | Disconnected clients left in `hub.conns` map | `removeConn` cleans slice; `delete(hub.conns, tripID)` when slice is empty |
| URL-encoding in DATABASE_URL | Password with `!` character broke `postgres://user:pass@host/db` parsing | Use pure hex passwords (no special chars) |
| Kafka consumer offset management | On restart, don't reprocess already-handled events | Consumer groups with committed offsets — Kafka tracks position per group |
| Zookeeper operational overhead | Extra service, extra failure point, complex coordination | Migrated Kafka to KRaft mode — Kafka manages its own metadata; Zookeeper removed entirely |

---

## 8. Q&A — Basic Level

**Q1: What is this project?**
A distributed ride-hailing backend similar to Uber's core booking system. A rider requests a ride, the system finds the nearest driver using GPS coordinates, assigns them automatically, and tracks the trip from pickup to completion with fare calculation.

---

**Q2: What is a REST API and how is it used here?**
REST (Representational State Transfer) is an architectural style for HTTP APIs using standard methods (GET, POST, PATCH) and URL paths to represent resources. Here: `POST /trips/request` creates a trip resource, `GET /trips/:id` reads it, `PATCH /trips/:id/start` updates its status.

---

**Q3: What is Docker and why use it?**
Docker packages an application and all its dependencies into a container — a portable, isolated environment. `docker-compose.yml` defines all 5 services (Go app, PostgreSQL, Redis, Kafka in KRaft mode, NGINX). `make up` starts everything reproducibly on any machine. Zookeeper, pgAdmin, and Kafdrop were removed to keep the setup lean — Kafka now manages its own metadata via KRaft.

---

**Q4: What is JWT and how does it work here?**
JWT (JSON Web Token) is a compact, self-contained token for authentication. Structure: `header.payload.signature`. The payload contains claims (`user_id`, `email`, `role`). Signed with `JWT_SECRET` using HMAC-SHA256. The server validates the signature on every request — no database lookup needed. Tokens expire after 24 hours.

---

**Q5: What endpoints does the API have?**

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | /health | — | Service health check |
| POST | /users/register | — | Create rider account |
| POST | /users/login | — | Authenticate rider |
| GET | /users/:id | Yes | Get rider profile |
| POST | /drivers/register | — | Create driver account |
| POST | /drivers/login | — | Authenticate driver |
| GET | /drivers/:id | Yes | Get driver profile |
| PATCH | /drivers/:id/location | Yes | Update GPS position |
| GET | /drivers/nearby | Yes | Find nearby drivers |
| POST | /trips/request | Yes | Request a ride |
| GET | /trips/:id | Yes | Get trip details |
| PATCH | /trips/:id/assign | Yes | Manually assign driver |
| PATCH | /trips/:id/start | Yes | Start the trip |
| PATCH | /trips/:id/end | Yes | Complete the trip |
| WS | /ws/trips/:id | — | Live location stream |

---

**Q6: What is PostgreSQL and why use it?**
PostgreSQL is a relational database with ACID guarantees. Used here for persistent storage of users, drivers, and trips. ACID means: Atomicity (all-or-nothing), Consistency (constraints always valid), Isolation (concurrent transactions don't interfere), Durability (committed data survives crashes).

---

**Q7: What is bcrypt and why use it for passwords?**
bcrypt is a password-hashing algorithm designed to be slow and computationally expensive. It automatically generates a random salt and includes it in the hash output. This means identical passwords produce different hashes (no rainbow table attacks). The `cost` parameter (10 here) controls how slow it is — slower = harder to brute-force.

---

**Q8: What happens when a user registers?**
1. Handler validates name, email, phone, password format
2. Service checks email uniqueness (SELECT EXISTS)
3. Service checks phone uniqueness
4. `bcrypt.GenerateFromPassword` hashes the password
5. `uuid.New()` generates a UUID
6. `INSERT INTO users` stores the record
7. `jwt.Generate` creates a signed token with role="rider"
8. Returns HTTP 201 with token and user object (no password hash)

---

**Q9: What is a database migration?**
A migration is a versioned SQL script that modifies the database schema. Here, V1/V2/V3 `.sql` files create the `users`, `drivers`, `trips` tables. They run automatically on startup via `RunMigrations()`. A `schema_migrations` table tracks which have been applied — each migration runs exactly once, even across restarts.

---

**Q10: Why Go instead of Python or Node.js?**
Go compiles to a single native binary — no runtime required in the Docker image. Goroutines are lightweight (~2KB each vs ~2MB OS threads), making it easy to run Kafka consumers concurrently with the HTTP server. Static typing catches bugs at compile time. The standard library covers most needs (HTTP, crypto, JSON) without heavy frameworks.

---

## 9. Q&A — Moderate Level

**Q11: How does the Kafka matching pipeline work step by step?**
1. `POST /trips/request` inserts trip to PostgreSQL with status `REQUESTED`
2. A goroutine publishes a `ride.requested` event to Kafka (async, doesn't block HTTP response)
3. `matching/matcher.go` runs a consumer goroutine subscribed to `ride.requested` with group `"matching-group"`
4. Matcher deserializes the event, calls `redis.GetNearbyDrivers(pickup, 5km, count=1)`
5. Redis `GEOSEARCH` returns the nearest driver ID within 5km
6. Matcher publishes `driver.assigned` event with `{TripID, DriverID}`
7. Matcher calls `redis.RemoveDriverLocation(driverID)` — removes driver from GEO pool atomically
8. `trips/service.go`'s `StartDriverAssignedConsumer` goroutine picks up `driver.assigned`
9. Consumer runs `UPDATE trips SET driver_id=..., status='DRIVER_ASSIGNED' WHERE status IN ('REQUESTED','MATCHING')`

---

**Q12: How does Redis GEO work?**
Redis GEO commands are built on top of sorted sets. Coordinates are encoded as a 52-bit geohash and stored as the sorted set score. This enables:
- `GEOADD` → stores `(driverID, geohash)` in the `driver:locations` sorted set
- `GEOSEARCH` → converts the search center+radius to geohash ranges, then does a range scan on the sorted set — O(log N + M) where M is results

This is far more efficient than a SQL `WHERE lat BETWEEN ... AND lng BETWEEN ...` query which requires a full table scan or complex spatial index.

---

**Q13: What is a Kafka consumer group?**
A consumer group is a set of consumers that collectively read from a topic. Each partition is assigned to exactly one consumer in the group. If one consumer crashes, Kafka reassigns its partitions to others. Here:
- `"matching-group"` reads `ride.requested`
- `"trip-driver-assigned"` reads `driver.assigned`

These are independent groups — both can read from the same topic without interfering. Adding a `"billing-group"` reading `trip.completed` requires zero changes to existing code.

---

**Q14: How does fare calculation work?**
```
fare = ₹50 (base) + ₹12 × distance_km
```
Distance is either:
- Provided explicitly in the end-trip request body (`distanceKm` field)
- Calculated using the **Haversine formula** from pickup/drop coordinates

Haversine gives the great-circle distance (shortest path on a sphere). For 10.5km: `50 + 12 × 10.5 = ₹176`.

---

**Q15: What is the trip state machine?**
```
REQUESTED  →  (Kafka auto-match)  →  DRIVER_ASSIGNED  →  STARTED  →  COMPLETED
    └────────── manual /assign ──────────┘                              ↘ CANCELLED (future)
```
All transitions are enforced atomically at the SQL level with conditional UPDATEs:
- `AssignDriver`: `WHERE id=... AND status IN ('REQUESTED', 'MATCHING')`
- `Start`: `WHERE id=... AND status='DRIVER_ASSIGNED'`
- `End`: `WHERE id=... AND status='STARTED'` ← SQL guard prevents TOCTOU race (previously a Go-level check that two concurrent requests could both pass)

If `RowsAffected() == 0`, the transition was invalid (wrong state) — returns 400.

---

**Q16: How do WebSockets work in this system?**
WebSocket is a full-duplex protocol over a single TCP connection. Here:
1. Client sends HTTP Upgrade request to `/ws/trips/:id`
2. NGINX forwards with `Upgrade: websocket` and `Connection: upgrade` headers
3. `gorilla/websocket` upgrades the connection
4. `Hub` stores the connection in `map[tripID][]*Conn`
5. When a driver's location changes, `Hub.BroadcastLocation(tripID, lat, lng)` pushes JSON to all subscribers
6. On disconnect, connection is cleaned up from the map

The 3600s NGINX timeout keeps connections alive for 1 hour without data.

---

**Q17: What is OptionalAuth vs RequireAuth?**

`OptionalAuth` (global middleware):
- Runs on every request
- If `Authorization: Bearer <token>` is present and valid → stores claims in context
- If no token → passes through (claims = nil)
- Allows public endpoints to still access identity if token is provided

`RequireAuth` (per-router middleware):
- Checks if claims exist in context
- If nil → returns HTTP 401 immediately
- Applied only to protected route groups

This two-layer design means public routes (register/login) work without tokens, while `/trips/*` always requires a valid token.

---

**Q18: How do database migrations run automatically?**
`migrations/embed.go` uses `//go:embed *.sql` to bake SQL files into the binary at compile time. On startup, `RunMigrations()`:
1. Creates `schema_migrations` table if it doesn't exist
2. Lists all `.sql` files from the embedded FS
3. Sorts them lexicographically (V1 < V2 < V3)
4. For each file: checks if its filename exists in `schema_migrations`
5. If not applied: executes the SQL, then inserts the filename into `schema_migrations`
6. Skips already-applied migrations

This is safe to run on every restart — idempotent by design.

---

**Q19: Why publish Kafka events in a goroutine?**
`kafka.Publish` is a network call that can take 50-500ms depending on load. If done synchronously in the HTTP handler, the client would wait for Kafka before getting their response. By publishing in `go func()`, the HTTP response (201 Created) returns in <5ms regardless of Kafka latency.

The goroutine uses `context.Background()` not the request context — because the request context is cancelled when the HTTP response is sent, which would cancel the Kafka write mid-flight.

---

**Q20: What happens if no nearby driver is found?**
The matcher logs `"no nearby drivers for trip X"` and returns `nil`. Returning `nil` (not an error) tells the Kafka consumer to commit the offset — the message won't be retried. The trip stays in `REQUESTED` status indefinitely. The fallback is the manual `PATCH /trips/:id/assign` endpoint which allows explicit driver assignment.

Future improvement: retry matching after a delay by re-publishing the event with a backoff.

---

**Q20b: What is Kafka KRaft mode and why switch to it?**
Traditionally, Kafka required Apache Zookeeper to manage cluster metadata (broker registration, topic configs, leader election). KRaft (Kafka Raft) is Kafka's built-in metadata management — it uses the Raft consensus protocol internally, replacing Zookeeper entirely.

**Why switch:**
- Fewer moving parts — one less service to run, configure, and monitor
- Simpler setup — no separate Zookeeper ensemble needed
- Better performance for large clusters (metadata operations are faster)
- The direction Kafka is heading — Zookeeper support is deprecated since Kafka 3.0

**In this project:** Kafka runs with `KAFKA_PROCESS_ROLES: broker,controller` — the same process acts as both data broker and cluster controller.

---

**Q21: How does graceful shutdown work?**
```go
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
<-quit  // blocks until signal received

shutCtx, _ := context.WithTimeout(context.Background(), 10*time.Second)
srv.Shutdown(shutCtx)  // stops accepting new requests; waits for in-flight to finish (max 10s)
cancel()               // cancels root context → stops all Kafka consumer goroutines
```
This ensures no requests are dropped mid-processing and no Kafka messages are lost mid-consumption.

---

**Q22: What is `pgxpool` and why use it?**
`pgxpool.Pool` is a connection pool for PostgreSQL. Instead of opening a new TCP connection for every query (expensive: ~100ms), the pool maintains a set of persistent connections and lends them to concurrent queries. Key settings: max connections, idle timeout, max lifetime. `pgx/v5` is used instead of `database/sql + lib/pq` because it's the native Go driver with better performance and PostgreSQL-specific features.

---

## 10. Q&A — Advanced Level

**Q23: How would you scale this system to 1 million rides per day?**

1 million rides/day ≈ 12 rides/second average, ~100 rides/second at peak.

**Horizontal scaling:**
- Run multiple `ride-service` instances behind NGINX (upstream with multiple servers)
- Kafka partitions (currently 3) distribute load across multiple matching consumers
- PostgreSQL read replicas for GET queries; primary for writes only
- Redis Cluster for horizontal GEO data partitioning

**Database:**
- Add connection pool limits (pgxpool `MaxConns`)
- Partition `trips` table by `created_at` (date partitioning)
- Archive completed trips to cold storage after 30 days

**Matching:**
- Extract `matching` into its own microservice with independent scaling
- Increase Kafka partitions to match desired parallelism

**Caching:**
- Cache `GET /trips/:id` responses in Redis with short TTL (1-5s)
- Cache `GET /users/:id` and `GET /drivers/:id` (rarely changes)

---

**Q24: What race conditions exist in this system?**

**1. Driver double-assignment (partially mitigated):**
- Two `ride.requested` events arrive simultaneously
- Both matchers find the same driver in Redis GEO
- Mitigation: `ZREM` is atomic in Redis, but there's a window between `GEOSEARCH` and `ZREM`
- Remaining gap: Redis Lua script for atomic `GEOSEARCH + ZREM` in one operation would close this fully

**2. Concurrent End requests — FIXED:**
- Previously: status check was in Go code (`if trip.Status != StatusStarted`), then a separate `UPDATE ... WHERE id=$4`. Two concurrent requests could both pass the Go check and both write different fares.
- Fix: status guard moved into SQL: `UPDATE ... WHERE id=$4 AND status='STARTED'`. DB row-level lock ensures only one UPDATE matches — the other gets `RowsAffected() == 0` and returns a clean 400.

**3. Concurrent trip state transitions (other endpoints):**
- Two requests call `PATCH /trips/:id/start` simultaneously
- Handled: conditional `UPDATE WHERE status='DRIVER_ASSIGNED'` — only one succeeds

**4. WebSocket concurrent writes — FIXED:**
- `gorilla/websocket` connections are not safe for concurrent writes
- Previously: multiple goroutines could call `WriteJSON` on the same connection simultaneously during a broadcast
- Fix: `safeConn` wraps each connection with its own `sync.Mutex`; every `WriteJSON` is locked per-connection

**5. Hub map concurrent access:**
- `BroadcastLocation` reads the `conns` map while `HandleWS` may be adding/removing connections
- Handled: `Hub.mu sync.RWMutex` — broadcasts hold `RLock`, add/remove hold `Lock`

---

**Q25: What are the CAP theorem tradeoffs in this system?**

CAP: Consistency, Availability, Partition Tolerance (can only guarantee 2 of 3).

**PostgreSQL (CP):** Chooses Consistency over Availability. During a network partition, PostgreSQL may refuse writes to avoid split-brain. Trip state machine correctness is preserved.

**Redis (AP):** Chooses Availability over Consistency. In a partition, Redis may serve stale driver locations but stays available. Acceptable for GEO data — slightly stale position is fine.

**Kafka (CP):** Messages are only considered "delivered" after replication to `replication.factor` brokers. Kafka sacrifices availability (producer blocks) to guarantee durability.

---

**Q26: What happens if the matching service crashes mid-assignment?**

1. Matcher reads `ride.requested` message from Kafka
2. Calls `redis.GetNearbyDrivers` → finds driver
3. **Crashes before `kafka.Publish("driver.assigned")`**

Result: Kafka consumer group offset was NOT committed (segmentio/kafka-go commits after `handler` returns without error). On restart, the matcher re-reads the same message and re-processes it — **at-least-once delivery**.

If `ZREM` was already called: driver was removed from pool but no `driver.assigned` published → trip stuck in `REQUESTED`. This is the current gap. Fix: use a Kafka transaction or two-phase approach (set driver status=busy in DB first, then publish).

---

**Q27: How would you add surge pricing?**

Add a multiplier based on supply/demand ratio:
```
surge_multiplier = max(1.0, nearby_riders / nearby_drivers)
fare = (50 + 12 × km) × surge_multiplier
```

Implementation:
1. At trip request time, count riders requesting in the same area (Redis counter with geohash as key)
2. Count available drivers in the area (Redis GEO count)
3. Compute ratio → multiplier → store in trip record
4. Apply multiplier in `End()` fare calculation

Requires a `surge_multiplier` column in `trips` table (new migration).

---

**Q28: How would you make driver matching smarter?**

Current: nearest driver by straight-line distance.

Improvements:
1. **ETA-based**: use road network distance/time (Google Maps API or OSRM) instead of straight-line
2. **Driver rating**: prefer higher-rated drivers when multiple are equidistant
3. **Vehicle type matching**: rider requests "SUV" → only match SUV drivers
4. **Driver acceptance rate**: deprioritize drivers who frequently decline
5. **Multi-factor scoring**: weighted score of distance + rating + acceptance rate
6. **Zone-based pre-positioning**: predict demand hotspots, encourage drivers to reposition

---

**Q29: How would you add rate limiting?**

Option 1: **NGINX rate limiting** (simplest, no code changes):
```nginx
limit_req_zone $binary_remote_addr zone=api:10m rate=10r/s;
limit_req zone=api burst=20 nodelay;
```

Option 2: **Redis token bucket in Go middleware**:
```go
func RateLimitMiddleware(redis *redis.Client) func(http.Handler) http.Handler {
    // INCR key, EXPIRE key 1s
    // if count > limit → 429 Too Many Requests
}
```

Option 3: **Kong/API Gateway** with rate limiting plugin.

Key considerations: rate limit by IP for unauthenticated endpoints, by user ID for authenticated ones.

---

**Q30: How would you replace this monolith with microservices?**

Current single service contains: users, drivers, trips, matching, tracking.

Split by bounded context:
```
user-service     → users table, auth
driver-service   → drivers table, location, availability
trip-service     → trips table, state machine
matching-service → Kafka consumers, GEO queries
tracking-service → WebSocket hub, location broadcast
notification-service → push notifications (new)
billing-service  → fare calculation, payment (from trip.completed events)
```

Each service gets its own:
- PostgreSQL schema or database
- Redis namespace
- Kafka consumer groups
- Docker container
- Independent deployment

Communication: synchronous (gRPC/HTTP) for queries; asynchronous (Kafka) for events.

**Challenge:** Distributed transactions — assigning a driver now spans `trip-service` and `driver-service`. Use the Saga pattern: sequence of local transactions with compensating actions on failure.

---

**Q31: What observability would you add in production?**

**Metrics (Prometheus + Grafana):**
- Request rate, error rate, latency per endpoint (RED metrics)
- Kafka consumer lag per group
- Redis GEO query latency
- PostgreSQL query duration
- Active WebSocket connections

**Tracing (OpenTelemetry + Jaeger):**
- Distributed trace from HTTP request through Kafka events
- Trace ID propagated in Kafka message headers
- Identify bottlenecks: where does a trip request spend the most time?

**Logging (structured JSON + ELK/Loki):**
- Current: `log.Printf` — replace with `slog` (Go 1.21 structured logging)
- Fields: `trip_id`, `driver_id`, `duration_ms`, `status`
- Centralize in Grafana Loki or Elasticsearch

**Alerting:**
- Kafka consumer lag > 1000 messages → matching is behind
- Error rate > 1% → page on-call
- P99 latency > 500ms → investigate

---

**Q32: What are the known limitations and what would you improve?**

| Limitation | Improvement |
|---|---|
| JWT tokens can't be revoked | Add Redis token denylist; check on each request |
| Driver GEO-assignment still has a small race | Use Redis Lua script for atomic `GEOSEARCH + ZREM` |
| No retry for unmatched trips | Re-publish `ride.requested` with exponential backoff |
| Driver location only updates on API call | Driver app sends GPS every 5s via WebSocket or HTTP |
| Single Kafka broker | 3-broker KRaft cluster for production availability |
| No payment processing | Integrate Stripe/Razorpay on `trip.completed` event |
| No driver acceptance flow | Driver gets a push notification; must accept/decline within 30s |
| Fare uses straight-line distance | Use road distance (OSRM) for accuracy |
| Fare stored as float64 | Use `DECIMAL` in DB and `decimal.Decimal` in Go to avoid floating-point precision errors |
| No ride cancellation | Add `CANCELLED` status with compensation logic |
| No tests in Go code | Add unit tests for service layer, integration tests for handlers |
| Passwords in DATABASE_URL env var | Use `pgx.ConnConfig` struct instead of DSN string |
