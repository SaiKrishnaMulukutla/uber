# ARCHITECTURE.md

> **IMPORTANT — READ THIS FIRST**
> This document is the primary authority for all code written in this repository.
> It is written for **AI (Claude Code) first, human developers second**.
> Every rule uses **MUST**, **MUST NOT**, **NEVER**, or **ALWAYS** — these are hard constraints, not preferences.
> These rules **override** any default behavior, prior patterns found in the codebase, or general conventions.
> Before making any code change, read the relevant section(s) of this document.

---

## Table of Contents

1. [Repository Layout](#1-repository-layout)
2. [Service Structure & Layers](#2-service-structure--layers)
3. [shared/ Package Rules](#3-shared-package-rules)
4. [Kafka Event Contracts](#4-kafka-event-contracts)
5. [Security & Auth Rules](#5-security--auth-rules)
6. [Error Handling](#6-error-handling)
7. [Config Pattern](#7-config-pattern)
8. [Infrastructure Rules](#8-infrastructure-rules)
9. [New Service Checklist](#9-new-service-checklist)

---

## 1. Repository Layout

This is the canonical structure. MUST NOT deviate.

```
uber/
├── go.work                    # workspace: shared + 7 service modules
├── go.work.sum
├── Dockerfile                 # multi-stage build for all services
├── Makefile                   # up / down / logs targets
├── ARCHITECTURE.md            # this file
│
├── shared/                    # module: uber/shared — infrastructure primitives ONLY
│   └── pkg/
│       ├── db/                # pgx pool + migration runner
│       ├── kafka/             # producer, consumer, topic constants, event structs
│       ├── redis/             # GEO set + backup key
│       ├── jwt/               # HS256 token signing + Chi middleware
│       ├── env/               # Get / GetInt with fallback
│       ├── validation/        # input validators (email, phone, password, etc.)
│       └── mailer/            # SMTP client + AsyncMailer worker-pool wrapper
│
├── services/
│   ├── api-gateway/           # nginx config only — no Go module
│   ├── user-service/          # :8081  users_db
│   ├── driver-service/        # :8082  drivers_db
│   ├── trip-service/          # :8083  trips_db
│   ├── matching-service/      # no HTTP  no DB  Kafka-only
│   ├── notification-service/  # :8084  notifications_db
│   ├── payment-service/       # :8085  payments_db
│   └── otp-service/           # :8086  Redis-backed (no Postgres)
│
└── infra/
    ├── docker-compose.yml
    └── init.sql               # creates all 5 databases
```

---

## 2. Service Structure & Layers

Every Go service MUST follow exactly this 4-layer structure. No exceptions.

```
services/{name}/
├── cmd/
│   └── main.go            # dependency wiring + server start ONLY
├── config/
│   └── config.go          # Config struct + Load()
├── internal/
│   ├── controllers/
│   │   ├── handler.go     # HTTP handlers
│   │   └── handler_test.go
│   ├── service/
│   │   └── service.go     # business logic
│   ├── repositories/
│   │   └── repository.go  # DB queries
│   └── model/
│       └── model.go       # domain structs + DTOs
├── migrations/
│   ├── V1_create_....sql
│   └── embed.go
├── go.mod
└── Dockerfile
```

### Layer Responsibilities

| Layer | Responsibility | MUST NOT |
|-------|---------------|----------|
| `controllers/` | Parse input → validate → call service → write response | Contain business logic or DB calls |
| `service/` | Business decisions, orchestration, Kafka publishing | Call DB directly, import concrete repo types |
| `repositories/` | SQL queries via pgx, return domain structs | Contain business logic or make HTTP calls |
| `model/` | Structs, constants, enums | Contain methods with logic |
| `cmd/main.go` | Wire all dependencies, start HTTP server, handle shutdown | Contain any business or query logic |

### Naming Rules

- Package names: exactly `controllers`, `service`, `repositories`, `model` — no aliases, no variations
- `handler.go` — HTTP handlers; `service.go` — business logic; `repository.go` — DB layer; `model.go` — structs
- NEVER name a file after the domain entity (e.g. `driver.go`) — always use the layer name

### Interface Rules

- `service/` MUST define an interface for itself (e.g. `DriverService interface`) — never expose the concrete struct outside the package
- `service/` MUST define an interface for every external dependency it calls:
  ```go
  // CORRECT — service owns the interface it needs
  type OTPClient interface {
      SendOTP(ctx context.Context, email string) error
      VerifyOTP(ctx context.Context, email, otp string) error
  }
  ```
- `controllers/` MUST depend on the service interface, never the concrete struct
- `repositories/` MUST define a repository interface — the service layer depends on the interface, not `*pgxpool.Pool`
- NEVER import a concrete type from another service's package

---

## 3. shared/ Package Rules

> **This is the most critical section. Violations here cause architectural decay.**

### The Rule

`shared/` MUST contain ONLY infrastructure primitives that satisfy ALL three conditions:

1. Usable by **3 or more services** without any modification
2. Contains **zero knowledge** of any service's domain (no `Driver`, `Trip`, `Payment` structs)
3. Is **infrastructure** (I/O, encoding, config, security) — not business logic

**If unsure: put it inside the service. NEVER add to shared/ speculatively.**

### Approved packages in shared/

| Package | What it provides | MUST NOT |
|---------|-----------------|----------|
| `pkg/db` | pgx pool + flyway-style migration runner | Service-specific schema knowledge |
| `pkg/kafka` | producer (cached writers), consumer, topic constants, event structs | Service-specific consumer logic |
| `pkg/redis` | GEO set, backup key, pool config | Service-specific key naming |
| `pkg/jwt` | HS256 sign/verify, Chi middleware, claims extraction | Service-specific role definitions beyond "rider"/"driver" |
| `pkg/env` | `Get(key, fallback)`, `GetInt(key, fallback)` | Nothing |
| `pkg/validation` | ValidateEmail, Phone, Name, Password, Coordinates, DriverStatus, RatingScore | Service-specific validation rules |
| `pkg/mailer` | `GmailMailer` (SMTP), `AsyncMailer` (worker-pool wrapper implementing `Mailer`) | Email templates specific to one service |

### What MUST NEVER be in shared/

- **Service-to-service HTTP clients** — e.g. an `otp_client` package. These belong in the calling service's `internal/` directory (e.g. `services/driver-service/internal/otpclient/`)
- **Business logic** of any kind
- **Domain structs** specific to one service (Driver, Trip, User, Payment, Notification)
- **Any package that encodes knowledge of another service's HTTP API contract**

### Practical test before adding to shared/

Ask: "Could I copy this package into a completely unrelated Go project and use it unchanged?" If no → it does not belong in shared/.

---

## 4. Kafka Event Contracts

### Where things live

- **Topic name constants** MUST be defined in `shared/pkg/kafka/client.go` alongside the `Client` type
- **Event structs** MUST be defined in `shared/pkg/kafka/events.go` — one struct per published event, JSON tags on all fields

### Current topics

5 topics total (Aiven free-tier limit). `ridego.events` multiplexes 3 low-traffic event types via `EventEnvelope{type, payload json.RawMessage}`. Consumers unmarshal `payload` only after checking `type`.

| Topic | Published by | Consumed by |
|-------|-------------|-------------|
| `ride.requested` | trip-service | matching-service |
| `ride.offered` | matching-service | notification-service |
| `driver.assigned` | driver-service (accept) — includes `rider_id` | trip-service (assign driver + generate ride OTP in Redis), driver-service (mark busy), notification-service (notify driver + rider) |
| `trip.completed` | trip-service | payment-service, driver-service, notification-service |
| `ridego.events` | trip-service (`trip.cancelled`, `rating.submitted`), payment-service (`payment.completed`) | matching-service, driver-service, user-service, notification-service |

### Rules

- MUST use **Kafka (async)** when the caller does not need an immediate result — e.g. sending emails, syncing status, updating ratings
- MUST use **HTTP (sync)** only when the caller must block for a result to continue its own flow — e.g. OTP verification where the user is actively waiting
- NEVER publish to a topic without a corresponding struct in `events.go`
- NEVER hardcode topic name strings inside a service — always import from `shared/pkg/kafka`
- Consumer handler functions MUST:
  - Return `nil` for **expected skips** (wrong role, rate-limited, intentionally ignored events)
  - Return `error` only for **retriable failures** (DB down, Redis unavailable, network error)
  - NEVER return an error for business-logic rejections — Kafka will redeliver indefinitely

### Adding a new topic

> ⚠️ Aiven free tier is capped at **5 topics**. Before creating a new topic, consider whether the event belongs on `ridego.events` (add a new `EventType*` constant and wrap with `NewEnvelope`).

If a dedicated topic is truly needed:
1. Add the constant to `shared/pkg/kafka/client.go`
2. Add the event struct to `shared/pkg/kafka/events.go`
3. Call `kafkaClient.EnsureTopics(ctx, kafka.TopicNewName)` in the publisher's `cmd/main.go`
4. Update the table above in this document

To add a new event type to `ridego.events` instead:
1. Add `EventTypeXxx = "xxx"` constant to `shared/pkg/kafka/events.go`
2. Add the payload struct to `shared/pkg/kafka/events.go`
3. Publish: `kafka.NewEnvelope(kafka.EventTypeXxx, payload)` → `Publish(ctx, kafka.TopicRideGoEvents, key, env)`
4. Consume: add a `case kafka.EventTypeXxx:` branch to the existing `ridego.events` subscriber

---

## 5. Security & Auth Rules

### JWT

- Every endpoint that accesses user-specific data MUST use `jwt.RequireAuth` middleware
- Every role-restricted endpoint MUST use `jwt.RequireRole(roles...)` chained after `RequireAuth`
- `jwt.OptionalAuth` is ONLY for the root Chi router — MUST NOT substitute for `RequireAuth` on any protected route
- JWT secret MUST come from `JWT_SECRET` env var — NEVER hardcoded in source
- Token TTLs are fixed: **access = 15 min**, **refresh = 7 days** — MUST NOT change without updating all services

### IDOR Protection

Every endpoint that operates on a user-owned resource by ID MUST verify ownership:

```go
// CORRECT
claims := jwt.GetClaims(r.Context())
if claims == nil || claims.UserID != chi.URLParam(r, "id") {
    writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
    return
}
```

NEVER rely on the ID in the URL alone to authorize access.

### Input Validation

- ALL user-supplied input MUST be validated in `controllers/` using `shared/pkg/validation` before calling the service
- Validation MUST NOT happen in `service/` or `repositories/`
- Use standard `json.Decoder` — MUST NOT use `DisallowUnknownFields` (breaks forward compatibility)

### NEVER

- Skip JWT validation on any endpoint that reads or writes user data
- Log passwords, tokens, OTPs, or any credential at any log level
- Return raw internal error messages, stack traces, or DB errors to HTTP clients — always return a generic user-facing message
- Trust user-supplied IDs for authorization without comparing against JWT claims

---

## 6. Error Handling

### Propagation Chain

```
repositories/ ──► raw pgx error  OR  sentinel (var ErrNotFound = errors.New(...))
                                              │
service/       ──► fmt.Errorf("service: op: %w", err)  OR  its own sentinels
                                              │
controllers/   ──► errors.Is(err, SentinelX) → HTTP status
                   NEVER exposes internal text to the client
```

### Sentinel Errors

- MUST be defined as package-level `var` in the layer that owns them
- MUST use `errors.Is()` for all comparisons — NEVER string matching on `err.Error()`

```go
// CORRECT — defined where the error originates
var ErrNotFound = errors.New("not found")
var ErrRateLimited = errors.New("rate limit exceeded")
```

### HTTP Status Mapping

| Condition | Status Code |
|-----------|-------------|
| Invalid input / validation failed | `400` |
| Missing / invalid / expired JWT | `401` |
| Valid JWT, wrong role, or IDOR mismatch | `403` |
| Resource not found | `404` |
| Conflict (duplicate entry, illegal state transition) | `409` |
| Rate limited | `429` |
| Internal / unexpected error | `500` |
| Async operation accepted (fire-and-forget) | `202` |

---

## 7. Config Pattern

Every service MUST follow this exact pattern — no variations.

```go
// config/config.go
package config

import (
    "strings"
    "uber/shared/pkg/env"
)

type Config struct {
    JWTSecret    string
    DatabaseURL  string
    RedisAddr    string        // omit if service doesn't use Redis
    KafkaBrokers []string      // omit if service doesn't use Kafka
    Port         string
}

func Load() Config {
    return Config{
        JWTSecret:    env.Get("JWT_SECRET", ""),
        DatabaseURL:  env.Get("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/{name}_db?sslmode=disable"),
        RedisAddr:    env.Get("REDIS_ADDR", "localhost:6379"),
        KafkaBrokers: strings.Split(env.Get("KAFKA_BROKERS", "localhost:9092"), ","),
        Port:         env.Get("PORT", "808X"),
    }
}
```

### Rules

- Config MUST live in `config/config.go` — nowhere else
- MUST use `shared/pkg/env.Get()` / `env.GetInt()` — NEVER call `os.Getenv()` directly anywhere in the codebase
- `Load()` MUST be called exactly once — in `cmd/main.go` at startup
- NEVER read env vars outside of `config/config.go`

---

## 8. Infrastructure Rules

### Dockerfile (multi-stage, root `Dockerfile`)

All services are built from the single root `Dockerfile`. Structure MUST be:

1. **`base` stage** — `golang:1.21-alpine`: copies all `go.work*`, `shared/`, `services/` and runs `go mod download` (produces a cached dep layer)
2. **`build-{name}` stage** — compiles: `CGO_ENABLED=0 GOOS=linux go build -o /out ./services/{name}/cmd`
3. **Runtime stage** — `alpine:3.19`: non-root `app` user, CA certificates, timezone data, copies binary only

```dockerfile
# CORRECT runtime stage pattern
FROM alpine:3.19 AS {name}
RUN adduser -D -g '' app && apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=build-{name} /out .
USER app
EXPOSE {PORT}
CMD ["./out"]
```

**NEVER** ship the Go toolchain or source code in the runtime image.
**NEVER** run any container as root.

### docker-compose.yml

- Every Go service MUST declare:
  ```yaml
  depends_on:
    postgres1:
      condition: service_healthy
  ```
- Every Go service MUST have a healthcheck:
  ```yaml
  healthcheck:
    test: ["CMD", "wget", "-qO-", "http://localhost:{PORT}/health"]
    interval: 10s
    timeout: 5s
    retries: 5
  ```
- The `/health` HTTP endpoint MUST ping DB (and Redis if used) — return `503` if any infra is unreachable
- Environment variable names in docker-compose MUST exactly match the keys used in `config/config.go`

### nginx (api-gateway)

- Every new service MUST have a `location /{name}/` proxy block in `nginx.conf`
- Auth endpoints (`/login`, `/register`, `/verify-login`, `/refresh`, `/send-otp`, `/verify-otp`) MUST use the `5r/s` rate-limit zone
- All other endpoints MUST use the `30r/s` zone
- Security headers MUST be present on all responses:
  ```nginx
  add_header X-Content-Type-Options "nosniff";
  add_header X-Frame-Options "DENY";
  add_header X-XSS-Protection "1; mode=block";
  add_header Referrer-Policy "strict-origin-when-cross-origin";
  ```

---

## 9. New Service Checklist

Follow these steps **in order** when adding a new microservice.

- [ ] 1. Create `services/{name}/go.mod` — `module uber/{name}`, `go 1.21`, add `replace uber/shared => ../../shared`
- [ ] 2. Add `./services/{name}` to the `use` block in `go.work`
- [ ] 3. Scaffold all directories: `cmd/`, `config/`, `internal/controllers/`, `internal/service/`, `internal/repositories/`, `internal/model/`, `migrations/`
- [ ] 4. Create `config/config.go` — `Config` struct + `Load()` using `env.Get()`
- [ ] 5. Create `migrations/V1_create_{table}.sql` and `migrations/embed.go` with `//go:embed *.sql`
- [ ] 6. Implement layers **in this order**: `model/model.go` → `repositories/repository.go` → `service/service.go` → `controllers/handler.go`
- [ ] 7. Create `cmd/main.go` — wire: config → DB → Kafka → Redis (if needed) → repo → service → handler → Chi router → HTTP server with graceful shutdown (10s timeout on SIGINT/SIGTERM)
- [ ] 8. Add `GET /health` endpoint — pings DB + Redis, returns `{"status":"ok"}` or 503
- [ ] 9. Add `build-{name}` and runtime stages to root `Dockerfile`
- [ ] 10. Add service block to `infra/docker-compose.yml` — build, env, ports, depends_on, healthcheck
- [ ] 11. Add `location /{name}/` to `services/api-gateway/nginx.conf` with correct rate-limit zone
- [ ] 12. Run `go work sync` from repo root
- [ ] 13. Run `go build uber/{name}/...` — must pass with zero errors
- [ ] 14. Update the Kafka topic table in Section 4 of this document if new topics are introduced
