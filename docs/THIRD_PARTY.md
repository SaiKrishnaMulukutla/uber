# Third-Party Services

Reference document for every external service used by RideGo. Covers connection method, env vars, free-tier limits, and how the shared package wraps each service.

---

## 1. Neon — PostgreSQL

**Why:** Serverless Postgres with a permanent free tier (no 90-day expiry like Render's free DB).

| Property | Value |
|---|---|
| Provider | [neon.tech](https://neon.tech) |
| Plan | Free (forever) |
| Version | Postgres 17 |
| Region | AWS Asia Pacific 1 (Singapore) |
| Storage | 0.5 GB |
| Compute | 100 CU-hours / month (scale-to-zero) |

**Env var:** `DATABASE_URL`
```
postgresql://neondb_owner:<pass>@<host>/neondb?sslmode=require
```

**Shared package:** `shared/pkg/db` (`postgres.go`)
- `pgxpool` with `MaxConns=25`, `MinConns=5`
- 30-attempt retry on startup (2s interval)
- `RunMigrations` reads embedded SQL files in lexicographic order, tracks applied versions in `schema_migrations` table
- `MustConnect` = Connect + Migrate + fatal on any error — used in every service `main()`

**Rules:**
- Always use the **direct connection** string (no `-pooler` suffix) — migrations require session state
- Each service owns its own schema — no cross-service DB queries

---

## 2. Upstash — Redis

**Why:** Serverless Redis with TLS, GEO ops, and persistent free tier. No infrastructure to manage.

| Property | Value |
|---|---|
| Provider | [upstash.com](https://upstash.com) |
| Plan | Free |
| Region | AWS ap-south-1 (Mumbai) |
| Commands | 500k / month |
| Storage | 256 MB |
| TLS | Enabled (`rediss://`) |

**Env var:** `REDIS_ADDR`
```
rediss://:<token>@<host>:6379
```

**Shared package:** `shared/pkg/redis` (`client.go`)

Auto-detects URL vs plain `host:port` from the prefix. Pool config: `PoolSize=3`, `MinIdleConns=1`, `MaxRetries=5`. 20-attempt retry on startup (2s interval).

| Key Pattern | TTL | Purpose |
|---|---|---|
| `driver:locations` | — | GEO set for nearby driver search |
| `driver:loc:{id}` | 24h | Last-known lat/lng (restore after trip) |
| `driver:lock:{id}` | 30s | SETNX distributed lock during matching |
| `driver:type:{id}` | — | Vehicle category cache (go/x/xl) |
| `offer:{tripID}` | caller-set | Pending driver offer |
| `offer:req:{tripID}` | caller-set | Original `ride.requested` event JSON |
| `surge:multiplier` | — | Surge pricing multiplier (default 1.0, max 5.0) |
| `trip:otp:{tripID}` | 2h | 4-digit ride confirmation OTP |

---

## 3. Aiven — Apache Kafka

**Why:** Fully managed Kafka with a free tier after Redpanda (previous provider) org got suspended.

| Property | Value |
|---|---|
| Provider | [aiven.io](https://aiven.io) |
| Plan | Free |
| Version | Apache Kafka 4.1 |
| Region | AWS ap-southeast-1 (Singapore) |
| Topics | Up to 5 (free tier) |
| Partitions | Up to 2 per topic (free tier) |
| Retention | 3 days |
| Auth | SASL SCRAM-SHA-256 + TLS |

**Env vars:**
| Key | Value |
|---|---|
| `KAFKA_BROKERS` | `ridego-kafka-ridego.k.aivencloud.com:25004` |
| `KAFKA_USERNAME` | `avnadmin` |
| `KAFKA_PASSWORD` | *(see memory)* |
| `KAFKA_CA_CERT` | Base64-encoded PEM of Aiven's project CA |

> `KAFKA_USERNAME` and `KAFKA_PASSWORD` are read via `os.Getenv` directly inside `NewClient` — **not** via the service config struct.

**Shared package:** `shared/pkg/kafka` (`client.go`)
- `buildTLSConfig()` — loads `KAFKA_CA_CERT` (base64 PEM) into `x509.CertPool`; Aiven uses a self-signed project CA not in the system trust store
- `SCRAM-SHA-256` mechanism applied to both `Dialer` (consumers) and `Transport` (producers)
- Writers cached in `sync.Map` — one writer per topic, reused across all publishes
- `EnsureTopics`: 20×3s retry; creates with `NumPartitions=1, ReplicationFactor=1` (matches free-tier limit)
- `Subscribe`: manual commit after handler success; per-message panic recovery
- `Publish`: 30s write timeout
- Local dev: if USERNAME/PASSWORD unset → `DefaultDialer` (plaintext, no auth)

**Topics (5):**
| Topic | Publisher | Consumers |
|---|---|---|
| `ride.requested` | trip-service | matching-service |
| `ride.offered` | matching-service | notification-service |
| `driver.assigned` | driver-service | trip-service, driver-service, notification-service |
| `trip.completed` | trip-service | payment-service, driver-service, notification-service |
| `ridego.events` | trip-service (`trip.cancelled`, `rating.submitted`), payment-service (`payment.completed`) | matching-service, driver-service, user-service, notification-service |

`ridego.events` uses `EventEnvelope{type string, payload json.RawMessage}`. Consumers switch on `type` and unmarshal `payload` into the concrete struct. Use `kafka.NewEnvelope(eventType, payload)` to publish.

---

## 4. Render — Hosting

**Why:** PaaS for deploying all 7 services with zero server management.

| Property | Value |
|---|---|
| Provider | [render.com](https://render.com) |
| Plan | Free (web services) |
| Region | Singapore |
| Auto-deploy | **Disabled** (manual trigger only) |

**Services:**
| Service | ID | Port |
|---|---|---|
| api-gateway (nginx) | `srv-d7ca95t8nd3s73fjji50` | 8000 |
| user-service | `srv-d7ca9fd8nd3s73fjjndg` | 8081 |
| driver-service | `srv-d7ca9f58nd3s73fjjnc0` | 8082 |
| trip-service | `srv-d7ca9f58nd3s73fjjncg` | 8083 |
| matching-service | `srv-d7ca95t8nd3s73fjji4g` | — |
| notification-service | `srv-d7ca9fd8nd3s73fjjnd0` | 8084 |
| payment-service | `srv-d7ca9fd8nd3s73fjjne0` | 8085 |

**Rules:**
- Never trigger `POST /deploys` without explicit user approval
- Env var changes via `PUT /v1/services/{id}/env-vars` — always GET first, merge, then PUT (never send partial payload — it replaces all vars)
- `render.yaml` `autoDeploy: false` — blueprint-level protection against git-push triggers

---

## 5. Brevo — Transactional Email

**Why:** Transactional email API. No SMTP port required — works on Render (which blocks port 465/587 outbound).

| Property | Value |
|---|---|
| Provider | [brevo.com](https://brevo.com) |
| Plan | Free (300 emails/day) |
| Auth | HTTP API key (`api-key` header) |
| Endpoint | `POST https://api.brevo.com/v3/smtp/email` |
| Sender | `sainavaneethvangara@gmail.com` |

**Env vars:**
| Key | Required by |
|---|---|
| `BREVO_API_KEY` | user-service, driver-service, notification-service |
| `EMAIL_USER` | Same (also used as SMTP sender identity) |
| `EMAIL_PASS` | Same (activates mailer code path; Gmail app password) |
| `EMAIL_HOST` | Same (defaults: `smtp.gmail.com`) |
| `EMAIL_PORT` | Same (defaults: `587`) |

**Shared package:** `shared/pkg/mailer`
- `NewBrevo(apiKey, senderEmail)` → HTTP API sender (primary on prod)
- `New(host, port, user, pass)` → SMTP sender (port 465 = direct TLS, else STARTTLS)
- `WithFallback(primary, secondary)` → tries primary, falls back to secondary on error
- `NewAsync(mailer, workers)` → buffered pool of 5 goroutines; `Send` returns immediately; drops and logs if buffer full

**Prod flow:** Brevo API → (fallback) Gmail SMTP *(Gmail SMTP will fail on Render — acceptable since Brevo API succeeds)*

**⚠️ Ban history:** Banned twice for using "Uber" in email content (flagged as phishing). All templates now use "RideGo" branding. Never reference Uber, Lyft, or any real ride-hailing brand in email copy.

---

## 6. Razorpay — Payments

**Why:** Indian payment gateway supporting UPI, cards, and webhooks.

| Property | Value |
|---|---|
| Provider | [razorpay.com](https://razorpay.com) |
| Mode | Test |
| Auth | Basic auth (Key ID + Key Secret) |
| Webhook | HMAC-SHA256 signature verification |

**Env vars:**
| Key | Service |
|---|---|
| `RAZORPAY_KEY_ID` | payment-service |
| `RAZORPAY_KEY_SECRET` | payment-service |
| `RAZORPAY_WEBHOOK_SECRET` | payment-service |
| `BASE_URL` | payment-service (Razorpay callback base) |
| `PAYMENT_PROVIDER` | payment-service (`razorpay` / `cash` / `simulate`) |

**Provider package:** `services/payment-service/internal/provider/razorpay`
- `CreateOrder` — amount in INR, converted to paise (×100) internally
- `VerifyPayment` — HMAC-SHA256 of `orderID|paymentID` against `keySecret`
- `ParseWebhook` — verifies `X-Razorpay-Signature` header, handles `payment.captured` only
- HTTP client timeout: 30s

**Test credentials:**
| Method | Value |
|---|---|
| Card | `4100 2800 0000 1007` (any expiry/CVV) |
| UPI | `success@razorpay` |
| Cash | Driver calls `POST /payments/{id}/confirm-cash` |

> `POST /payments/simulate-success` is dev/test only — never gate production features behind it.

---

## Requirements Checklist

| Requirement (CLAUDE.md) | Status |
|---|---|
| `shared/pkg/db` — pgx pool | ✅ Neon, direct connection, migrations auto-run |
| `shared/pkg/redis` — GEO, locks, OTP storage | ✅ Upstash, all key patterns implemented |
| `shared/pkg/kafka` — cached writers, SCRAM-SHA-256 TLS | ✅ Aiven, CA cert loaded, writers cached |
| `shared/pkg/mailer` — Brevo API + SMTP fallback | ✅ Brevo primary, Gmail SMTP fallback (local only) |
| `shared/pkg/otp` — 6-digit, Redis-backed, 5m TTL | ✅ (auth OTP — separate from 4-digit ride OTP) |
| `shared/pkg/jwt` — access 15m / refresh 7d / checkout 30m | ✅ |
| Payment: cash, UPI/card (Razorpay), simulate | ✅ All three providers wired |
| Razorpay webhook idempotent | ✅ |
| Internal secret (matching → trip) | ✅ `X-Internal-Secret` header, validated in trip-service |
| Auto-deploy disabled on Render | ✅ API + render.yaml both set |
| No Uber branding in emails | ✅ All templates rebranded to RideGo |
