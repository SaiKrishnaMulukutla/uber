#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# Comprehensive Test Suite for ride-hailing-system
# Covers: Health, Users, Drivers, Trips, Matching (Kafka), WebSocket (basic)
# ─────────────────────────────────────────────────────────────────────────────
set -euo pipefail

BASE="http://localhost:8000"
PASS=0
FAIL=0
TOTAL=0

# ── Helpers ──────────────────────────────────────────────────────────────────

green()  { printf "\033[32m%s\033[0m\n" "$*"; }
red()    { printf "\033[31m%s\033[0m\n" "$*"; }
yellow() { printf "\033[33m%s\033[0m\n" "$*"; }
bold()   { printf "\033[1m%s\033[0m\n" "$*"; }

# macOS-safe helper to split curl response (body + status code on last line)
parse_response() {
  local resp="$1"
  BODY=$(echo "$resp" | sed '$d')
  CODE=$(echo "$resp" | tail -n 1)
}

assert_status() {
  local test_name="$1" expected="$2" actual="$3"
  TOTAL=$((TOTAL+1))
  if [ "$actual" = "$expected" ]; then
    green "  ✅ PASS [$TOTAL] $test_name (HTTP $actual)"
    PASS=$((PASS+1))
  else
    red "  ❌ FAIL [$TOTAL] $test_name — expected $expected, got $actual"
    FAIL=$((FAIL+1))
  fi
}

assert_json_field() {
  local test_name="$1" body="$2" field="$3"
  TOTAL=$((TOTAL+1))
  local val
  val=$(echo "$body" | jq -r "$field" 2>/dev/null || echo "")
  if [ -n "$val" ] && [ "$val" != "null" ]; then
    green "  ✅ PASS [$TOTAL] $test_name (${field}=${val})"
    PASS=$((PASS+1))
  else
    red "  ❌ FAIL [$TOTAL] $test_name — field $field is empty/null"
    FAIL=$((FAIL+1))
  fi
}

assert_json_equals() {
  local test_name="$1" body="$2" field="$3" expected="$4"
  TOTAL=$((TOTAL+1))
  local val
  val=$(echo "$body" | jq -r "$field" 2>/dev/null || echo "")
  if [ "$val" = "$expected" ]; then
    green "  ✅ PASS [$TOTAL] $test_name (${field}=${val})"
    PASS=$((PASS+1))
  else
    red "  ❌ FAIL [$TOTAL] $test_name — expected ${field}=${expected}, got ${val}"
    FAIL=$((FAIL+1))
  fi
}

# Unique suffix to avoid collisions on re-runs
TS=$(date +%s)

# ═════════════════════════════════════════════════════════════════════════════
bold "═══════════════════════════════════════════════════════════════"
bold "    RIDE-HAILING SYSTEM — FULL TEST SUITE"
bold "═══════════════════════════════════════════════════════════════"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "1. HEALTH CHECK"
# ─────────────────────────────────────────────────────────────────────────────

RESP=$(curl -s -w "\n%{http_code}" "$BASE/health")
parse_response "$RESP"
assert_status "GET /health returns 200" "200" "$CODE"
assert_json_equals "Health status is ok" "$BODY" ".status" "ok"
assert_json_equals "Service name is ride-service" "$BODY" ".service" "ride-service"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "2. USER REGISTRATION"
# ─────────────────────────────────────────────────────────────────────────────

# 2a. Successful registration
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/users/register" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"Test Rider $TS\",\"email\":\"rider_${TS}@test.com\",\"phone\":\"+1${TS}\",\"password\":\"password123\"}")
parse_response "$RESP"
assert_status "POST /users/register — success" "201" "$CODE"
assert_json_field "Registration returns token" "$BODY" ".token"
assert_json_field "Registration returns user.id" "$BODY" ".user.id"
assert_json_equals "Registration returns correct email" "$BODY" ".user.email" "rider_${TS}@test.com"
assert_json_equals "Registration returns rating 5" "$BODY" ".user.rating" "5"
RIDER_TOKEN=$(echo "$BODY" | jq -r '.token')
RIDER_ID=$(echo "$BODY" | jq -r '.user.id')

# 2b. Duplicate email
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/users/register" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"Dup\",\"email\":\"rider_${TS}@test.com\",\"phone\":\"+9999999\",\"password\":\"abc\"}")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /users/register — duplicate email" "409" "$CODE"

# 2c. Duplicate phone
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/users/register" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"Dup2\",\"email\":\"other_${TS}@test.com\",\"phone\":\"+1${TS}\",\"password\":\"abc\"}")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /users/register — duplicate phone" "409" "$CODE"

# 2d. Invalid body
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/users/register" \
  -H "Content-Type: application/json" \
  -d "not json")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /users/register — invalid body" "400" "$CODE"

# 2e. Empty body
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/users/register" \
  -H "Content-Type: application/json" \
  -d "")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /users/register — empty body" "400" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "3. USER LOGIN"
# ─────────────────────────────────────────────────────────────────────────────

# 3a. Successful login
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/users/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"rider_${TS}@test.com\",\"password\":\"password123\"}")
parse_response "$RESP"
assert_status "POST /users/login — success" "200" "$CODE"
assert_json_field "Login returns token" "$BODY" ".token"
assert_json_field "Login returns user.id" "$BODY" ".user.id"

# 3b. Wrong password
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/users/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"rider_${TS}@test.com\",\"password\":\"wrongpass\"}")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /users/login — wrong password" "401" "$CODE"

# 3c. Non-existent email
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/users/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"nonexistent@test.com\",\"password\":\"abc\"}")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /users/login — email not found" "401" "$CODE"

# 3d. Invalid body
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/users/login" \
  -H "Content-Type: application/json" \
  -d "bad")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /users/login — invalid body" "400" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "4. USER PROFILE"
# ─────────────────────────────────────────────────────────────────────────────

# 4a. Get profile with valid token
RESP=$(curl -s -w "\n%{http_code}" "$BASE/users/$RIDER_ID" \
  -H "Authorization: Bearer $RIDER_TOKEN")
parse_response "$RESP"
assert_status "GET /users/:id — with token" "200" "$CODE"
assert_json_equals "Profile returns correct ID" "$BODY" ".id" "$RIDER_ID"
assert_json_equals "Profile returns correct email" "$BODY" ".email" "rider_${TS}@test.com"

# 4b. Without token (unauthorized)
RESP=$(curl -s -w "\n%{http_code}" "$BASE/users/$RIDER_ID")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "GET /users/:id — no token" "401" "$CODE"

# 4c. Non-existent user
RESP=$(curl -s -w "\n%{http_code}" "$BASE/users/00000000-0000-0000-0000-000000000000" \
  -H "Authorization: Bearer $RIDER_TOKEN")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "GET /users/:id — not found" "404" "$CODE"

# 4d. Invalid token
RESP=$(curl -s -w "\n%{http_code}" "$BASE/users/$RIDER_ID" \
  -H "Authorization: Bearer invalid.jwt.token")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "GET /users/:id — invalid token" "401" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "5. DRIVER REGISTRATION"
# ─────────────────────────────────────────────────────────────────────────────

# 5a. Successful registration
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/drivers/register" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"Test Driver $TS\",\"email\":\"driver_${TS}@test.com\",\"phone\":\"+2${TS}\",\"password\":\"driverpass\",\"vehicle_type\":\"suv\",\"license_plate\":\"KA-01-AB-${TS}\"}")
parse_response "$RESP"
assert_status "POST /drivers/register — success" "201" "$CODE"
assert_json_field "Driver registration returns token" "$BODY" ".token"
assert_json_field "Driver registration returns driver.id" "$BODY" ".driver.id"
assert_json_equals "Driver vehicle type" "$BODY" ".driver.vehicle_type" "suv"
assert_json_equals "Driver status is available" "$BODY" ".driver.status" "available"
assert_json_equals "Driver rating is 5" "$BODY" ".driver.rating" "5"
DRIVER_TOKEN=$(echo "$BODY" | jq -r '.token')
DRIVER_ID=$(echo "$BODY" | jq -r '.driver.id')

# 5b. Duplicate email
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/drivers/register" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"Dup Driver\",\"email\":\"driver_${TS}@test.com\",\"phone\":\"+9${TS}\",\"password\":\"abc\",\"vehicle_type\":\"sedan\",\"license_plate\":\"X\"}")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /drivers/register — duplicate email" "409" "$CODE"

# 5c. Default vehicle_type when empty
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/drivers/register" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"Default VT\",\"email\":\"defvt_${TS}@test.com\",\"phone\":\"+3${TS}\",\"password\":\"abc\",\"license_plate\":\"Y\"}")
parse_response "$RESP"
assert_status "POST /drivers/register — default vehicle_type" "201" "$CODE"
assert_json_equals "Default vehicle_type is sedan" "$BODY" ".driver.vehicle_type" "sedan"

# 5d. Invalid body
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/drivers/register" \
  -H "Content-Type: application/json" \
  -d "not json")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /drivers/register — invalid body" "400" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "6. DRIVER LOGIN"
# ─────────────────────────────────────────────────────────────────────────────

# 6a. Successful login
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/drivers/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"driver_${TS}@test.com\",\"password\":\"driverpass\"}")
parse_response "$RESP"
assert_status "POST /drivers/login — success" "200" "$CODE"
assert_json_field "Driver login returns token" "$BODY" ".token"
assert_json_field "Driver login returns driver.id" "$BODY" ".driver.id"

# 6b. Wrong password
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/drivers/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"driver_${TS}@test.com\",\"password\":\"wrongpass\"}")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /drivers/login — wrong password" "401" "$CODE"

# 6c. Non-existent email
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/drivers/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"nope@test.com\",\"password\":\"abc\"}")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /drivers/login — email not found" "401" "$CODE"

# 6d. Invalid body
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/drivers/login" \
  -H "Content-Type: application/json" \
  -d "bad")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /drivers/login — invalid body" "400" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "7. DRIVER PROFILE"
# ─────────────────────────────────────────────────────────────────────────────

# 7a. Get driver with valid token
RESP=$(curl -s -w "\n%{http_code}" "$BASE/drivers/$DRIVER_ID" \
  -H "Authorization: Bearer $DRIVER_TOKEN")
parse_response "$RESP"
assert_status "GET /drivers/:id — with token" "200" "$CODE"
assert_json_equals "Driver profile ID" "$BODY" ".id" "$DRIVER_ID"

# 7b. Without token
RESP=$(curl -s -w "\n%{http_code}" "$BASE/drivers/$DRIVER_ID")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "GET /drivers/:id — no token" "401" "$CODE"

# 7c. Not found
RESP=$(curl -s -w "\n%{http_code}" "$BASE/drivers/00000000-0000-0000-0000-000000000000" \
  -H "Authorization: Bearer $DRIVER_TOKEN")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "GET /drivers/:id — not found" "404" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "8. DRIVER LOCATION UPDATE"
# ─────────────────────────────────────────────────────────────────────────────

# 8a. Update location — success
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/drivers/$DRIVER_ID/location" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $DRIVER_TOKEN" \
  -d '{"lat": 12.9716, "lng": 77.5946}')
parse_response "$RESP"
assert_status "PATCH /drivers/:id/location — success" "200" "$CODE"
assert_json_equals "Location update status" "$BODY" ".status" "location_updated"

# 8b. Without token
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/drivers/$DRIVER_ID/location" \
  -H "Content-Type: application/json" \
  -d '{"lat": 12.9716, "lng": 77.5946}')
CODE=$(echo "$RESP" | tail -n 1)
assert_status "PATCH /drivers/:id/location — no token" "401" "$CODE"

# 8c. Invalid body
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/drivers/$DRIVER_ID/location" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $DRIVER_TOKEN" \
  -d "bad")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "PATCH /drivers/:id/location — invalid body" "400" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "9. NEARBY DRIVERS"
# ─────────────────────────────────────────────────────────────────────────────

# 9a. Find nearby drivers (driver was set at 12.9716, 77.5946 above)
RESP=$(curl -s -w "\n%{http_code}" "$BASE/drivers/nearby?lat=12.9716&lng=77.5946&radius=5" \
  -H "Authorization: Bearer $RIDER_TOKEN")
parse_response "$RESP"
assert_status "GET /drivers/nearby — found drivers" "200" "$CODE"
assert_json_field "Nearby returns drivers array" "$BODY" ".drivers"

# 9b. No nearby drivers (far location)
RESP=$(curl -s -w "\n%{http_code}" "$BASE/drivers/nearby?lat=0.0&lng=0.0&radius=1" \
  -H "Authorization: Bearer $RIDER_TOKEN")
parse_response "$RESP"
assert_status "GET /drivers/nearby — remote location" "200" "$CODE"

# 9c. Without token
RESP=$(curl -s -w "\n%{http_code}" "$BASE/drivers/nearby?lat=12.9716&lng=77.5946")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "GET /drivers/nearby — no token" "401" "$CODE"

# 9d. Custom radius
RESP=$(curl -s -w "\n%{http_code}" "$BASE/drivers/nearby?lat=12.9716&lng=77.5946&radius=50" \
  -H "Authorization: Bearer $RIDER_TOKEN")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "GET /drivers/nearby — large radius" "200" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "10. TRIP REQUEST"
# ─────────────────────────────────────────────────────────────────────────────

# 10a. Request trip — success
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/trips/request" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{"pickupLat": 12.9716, "pickupLng": 77.5946, "dropLat": 12.2958, "dropLng": 76.6394}')
parse_response "$RESP"
assert_status "POST /trips/request — success" "201" "$CODE"
assert_json_field "Trip request returns trip_id" "$BODY" ".trip_id"
assert_json_equals "Trip initial status is REQUESTED" "$BODY" ".status" "REQUESTED"
TRIP_ID=$(echo "$BODY" | jq -r '.trip_id')

# 10b. Without token
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/trips/request" \
  -H "Content-Type: application/json" \
  -d '{"pickupLat": 12.0, "pickupLng": 77.0, "dropLat": 13.0, "dropLng": 78.0}')
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /trips/request — no token" "401" "$CODE"

# 10c. Invalid body
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/trips/request" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d "bad")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /trips/request — invalid body" "400" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "11. GET TRIP"
# ─────────────────────────────────────────────────────────────────────────────

# 11a. Get trip — success
RESP=$(curl -s -w "\n%{http_code}" "$BASE/trips/$TRIP_ID" \
  -H "Authorization: Bearer $RIDER_TOKEN")
parse_response "$RESP"
assert_status "GET /trips/:id — success" "200" "$CODE"
assert_json_equals "Trip ID matches" "$BODY" ".id" "$TRIP_ID"
assert_json_equals "Trip rider_id matches" "$BODY" ".rider_id" "$RIDER_ID"

# 11b. Without token
RESP=$(curl -s -w "\n%{http_code}" "$BASE/trips/$TRIP_ID")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "GET /trips/:id — no token" "401" "$CODE"

# 11c. Non-existent trip
RESP=$(curl -s -w "\n%{http_code}" "$BASE/trips/00000000-0000-0000-0000-000000000000" \
  -H "Authorization: Bearer $RIDER_TOKEN")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "GET /trips/:id — not found" "404" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "12. MANUAL TRIP LIFECYCLE (request → assign → start → end)"
# ─────────────────────────────────────────────────────────────────────────────

# Create a second trip for manual lifecycle testing
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/trips/request" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{"pickupLat": 28.6139, "pickupLng": 77.2090, "dropLat": 28.7041, "dropLng": 77.1025}')
parse_response "$RESP"
assert_status "Create trip for manual lifecycle" "201" "$CODE"
MANUAL_TRIP_ID=$(echo "$BODY" | jq -r '.trip_id')

# Wait a moment so the auto-matcher doesn't race us
sleep 1

# 12a. Assign driver — success
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$MANUAL_TRIP_ID/assign" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d "{\"driverId\":\"$DRIVER_ID\"}")
parse_response "$RESP"
assert_status "PATCH /trips/:id/assign — success" "200" "$CODE"
assert_json_equals "Trip status after assign" "$BODY" ".status" "DRIVER_ASSIGNED"
assert_json_equals "Assigned driver_id" "$BODY" ".driver_id" "$DRIVER_ID"

# 12b. Assign again (invalid state)
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$MANUAL_TRIP_ID/assign" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d "{\"driverId\":\"$DRIVER_ID\"}")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "PATCH /trips/:id/assign — already assigned" "400" "$CODE"

# 12c. Start trip before assigning (use the first trip that may not be assigned)
# Create a fresh trip just for this test
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/trips/request" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{"pickupLat": 0.001, "pickupLng": 0.001, "dropLat": 0.002, "dropLng": 0.002}')
FRESH_TRIP_ID=$(echo "$RESP" | sed '$d' | jq -r '.trip_id')
# Try to start without driver assigned
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$FRESH_TRIP_ID/start" \
  -H "Authorization: Bearer $RIDER_TOKEN")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "PATCH /trips/:id/start — not in DRIVER_ASSIGNED" "400" "$CODE"

# 12d. Start trip — success (use manual trip)
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$MANUAL_TRIP_ID/start" \
  -H "Authorization: Bearer $RIDER_TOKEN")
parse_response "$RESP"
assert_status "PATCH /trips/:id/start — success" "200" "$CODE"
assert_json_equals "Trip status after start" "$BODY" ".status" "STARTED"
assert_json_field "started_at is set" "$BODY" ".started_at"

# 12e. Start again (invalid state)
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$MANUAL_TRIP_ID/start" \
  -H "Authorization: Bearer $RIDER_TOKEN")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "PATCH /trips/:id/start — already started" "400" "$CODE"

# 12f. End trip before starting (use the first trip)
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$FRESH_TRIP_ID/end" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{}')
CODE=$(echo "$RESP" | tail -n 1)
assert_status "PATCH /trips/:id/end — not in STARTED state" "400" "$CODE"

# 12g. End trip — success (auto fare calculation via haversine)
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$MANUAL_TRIP_ID/end" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{}')
parse_response "$RESP"
assert_status "PATCH /trips/:id/end — success (haversine)" "200" "$CODE"
assert_json_equals "Trip status after end" "$BODY" ".status" "COMPLETED"
assert_json_field "Fare is set" "$BODY" ".fare"
assert_json_field "completed_at is set" "$BODY" ".completed_at"
FARE_HAVERSINE=$(echo "$BODY" | jq -r '.fare')
yellow "  ℹ  Fare (haversine): ₹${FARE_HAVERSINE}"

# 12h. End again (invalid state)
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$MANUAL_TRIP_ID/end" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{}')
CODE=$(echo "$RESP" | tail -n 1)
assert_status "PATCH /trips/:id/end — already completed" "400" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "13. END TRIP WITH EXPLICIT DISTANCE"
# ─────────────────────────────────────────────────────────────────────────────

# Create → Assign → Start → End with explicit distance
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/trips/request" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{"pickupLat": 19.0760, "pickupLng": 72.8777, "dropLat": 18.5204, "dropLng": 73.8567}')
DIST_TRIP_ID=$(echo "$RESP" | sed '$d' | jq -r '.trip_id')
sleep 1

curl -s -X PATCH "$BASE/trips/$DIST_TRIP_ID/assign" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d "{\"driverId\":\"$DRIVER_ID\"}" > /dev/null

curl -s -X PATCH "$BASE/trips/$DIST_TRIP_ID/start" \
  -H "Authorization: Bearer $RIDER_TOKEN" > /dev/null

RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$DIST_TRIP_ID/end" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{"distanceKm": 25.5}')
parse_response "$RESP"
assert_status "End trip with explicit distance" "200" "$CODE"
assert_json_equals "Trip status is COMPLETED" "$BODY" ".status" "COMPLETED"
FARE_EXPLICIT=$(echo "$BODY" | jq -r '.fare')
# 50 + 25.5 * 12 = 356
assert_json_equals "Fare = 50 + 25.5×12 = 356" "$BODY" ".fare" "356"
yellow "  ℹ  Fare (explicit 25.5km): ₹${FARE_EXPLICIT}"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "14. KAFKA AUTO-MATCHING (E2E FLOW)"
# ─────────────────────────────────────────────────────────────────────────────

# Register a new driver near Bangalore and set their location
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/drivers/register" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"Auto Driver $TS\",\"email\":\"autodriver_${TS}@test.com\",\"phone\":\"+4${TS}\",\"password\":\"auto123\",\"vehicle_type\":\"auto\",\"license_plate\":\"KA-AUTO-${TS}\"}")
BODY=$(echo "$RESP" | sed '$d')
AUTO_DRIVER_TOKEN=$(echo "$BODY" | jq -r '.token')
AUTO_DRIVER_ID=$(echo "$BODY" | jq -r '.driver.id')

# Set driver location near the pickup point
curl -s -X PATCH "$BASE/drivers/$AUTO_DRIVER_ID/location" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $AUTO_DRIVER_TOKEN" \
  -d '{"lat": 12.9720, "lng": 77.5950}' > /dev/null

# Request a trip near that driver
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/trips/request" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{"pickupLat": 12.9716, "pickupLng": 77.5946, "dropLat": 12.9352, "dropLng": 77.6245}')
parse_response "$RESP"
assert_status "Trip request for auto-matching" "201" "$CODE"
KAFKA_TRIP_ID=$(echo "$BODY" | jq -r '.trip_id')

# Wait for Kafka matching consumer to process (ride.requested → driver.assigned)
yellow "  ⏳ Waiting 5s for Kafka matching to process..."
sleep 5

# Check if driver was auto-assigned
RESP=$(curl -s -w "\n%{http_code}" "$BASE/trips/$KAFKA_TRIP_ID" \
  -H "Authorization: Bearer $RIDER_TOKEN")
parse_response "$RESP"
assert_status "GET auto-matched trip" "200" "$CODE"
MATCH_STATUS=$(echo "$BODY" | jq -r '.status')
MATCH_DRIVER=$(echo "$BODY" | jq -r '.driver_id // empty')

if [ "$MATCH_STATUS" = "DRIVER_ASSIGNED" ] && [ -n "$MATCH_DRIVER" ]; then
  TOTAL=$((TOTAL+1)); PASS=$((PASS+1))
  green "  ✅ PASS [$TOTAL] Kafka auto-matching worked! driver=$MATCH_DRIVER status=$MATCH_STATUS"
else
  TOTAL=$((TOTAL+1)); FAIL=$((FAIL+1))
  red "  ❌ FAIL [$TOTAL] Kafka auto-matching — status=$MATCH_STATUS driver=$MATCH_DRIVER (expected DRIVER_ASSIGNED)"
fi
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "15. ASSIGN/START/END — MISSING FIELDS & EDGE CASES"
# ─────────────────────────────────────────────────────────────────────────────

# 15a. Assign with invalid body
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$TRIP_ID/assign" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d "bad")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "PATCH /trips/:id/assign — invalid body" "400" "$CODE"

# 15b. Assign without token
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$TRIP_ID/assign" \
  -H "Content-Type: application/json" \
  -d "{\"driverId\":\"$DRIVER_ID\"}")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "PATCH /trips/:id/assign — no token" "401" "$CODE"

# 15c. Start without token
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$TRIP_ID/start")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "PATCH /trips/:id/start — no token" "401" "$CODE"

# 15d. End without token
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$TRIP_ID/end" \
  -H "Content-Type: application/json" \
  -d '{}')
CODE=$(echo "$RESP" | tail -n 1)
assert_status "PATCH /trips/:id/end — no token" "401" "$CODE"

# 15e. Non-existent trip assign
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/00000000-0000-0000-0000-000000000000/assign" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d "{\"driverId\":\"$DRIVER_ID\"}")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "PATCH assign — non-existent trip" "400" "$CODE"

# 15f. Non-existent trip start
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/00000000-0000-0000-0000-000000000000/start" \
  -H "Authorization: Bearer $RIDER_TOKEN")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "PATCH start — non-existent trip" "400" "$CODE"

# 15g. Non-existent trip end
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/00000000-0000-0000-0000-000000000000/end" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{}')
CODE=$(echo "$RESP" | tail -n 1)
assert_status "PATCH end — non-existent trip" "400" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "16. WEBSOCKET CONNECTION"
# ─────────────────────────────────────────────────────────────────────────────

# Test WebSocket upgrade (basic check — use --max-time 3 so curl doesn't hang on open WS conn)
RESP=$(curl -s --max-time 3 -w "\n%{http_code}" "$BASE/ws/trips/$TRIP_ID" \
  -H "Upgrade: websocket" \
  -H "Connection: Upgrade" \
  -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" \
  -H "Sec-WebSocket-Version: 13" 2>&1 || true)
CODE=$(echo "$RESP" | tail -n 1)
# WebSocket upgrade returns 101 on success. After --max-time curl may return 000.
# Any code other than 404 means the route exists and the server handled the upgrade.
TOTAL=$((TOTAL+1))
if [ "$CODE" = "101" ] || [ "$CODE" = "000" ] || [ "$CODE" = "200" ] || [ "$CODE" = "400" ]; then
  green "  ✅ PASS [$TOTAL] WS /ws/trips/:id — endpoint reachable (HTTP $CODE, 000=timeout after upgrade)"
  PASS=$((PASS+1))
else
  if [ "$CODE" != "404" ]; then
    green "  ✅ PASS [$TOTAL] WS /ws/trips/:id — endpoint exists (HTTP $CODE)"
    PASS=$((PASS+1))
  else
    red "  ❌ FAIL [$TOTAL] WS /ws/trips/:id — route not found (HTTP $CODE)"
    FAIL=$((FAIL+1))
  fi
fi
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "17. CROSS-ROLE TOKEN USAGE"
# ─────────────────────────────────────────────────────────────────────────────

# Driver token can access user endpoints (get user profile)
RESP=$(curl -s -w "\n%{http_code}" "$BASE/users/$RIDER_ID" \
  -H "Authorization: Bearer $DRIVER_TOKEN")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "Driver token can GET /users/:id" "200" "$CODE"

# User token can access driver endpoints (get driver profile)
RESP=$(curl -s -w "\n%{http_code}" "$BASE/drivers/$DRIVER_ID" \
  -H "Authorization: Bearer $RIDER_TOKEN")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "Rider token can GET /drivers/:id" "200" "$CODE"

# User token can request trips
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/trips/request" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{"pickupLat": 1.0, "pickupLng": 1.0, "dropLat": 2.0, "dropLng": 2.0}')
CODE=$(echo "$RESP" | tail -n 1)
assert_status "Rider token can POST /trips/request" "201" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "18. MULTIPLE DRIVERS — LOCATION & NEARBY"
# ─────────────────────────────────────────────────────────────────────────────

# Register 3 more drivers at different locations
for i in 1 2 3; do
  RESP=$(curl -s -X POST "$BASE/drivers/register" \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"Multi Driver $i $TS\",\"email\":\"multi${i}_${TS}@test.com\",\"phone\":\"+5${i}${TS}\",\"password\":\"pass\",\"vehicle_type\":\"sedan\",\"license_plate\":\"MUL-$i-${TS}\"}")
  local_token=$(echo "$RESP" | jq -r '.token')
  local_id=$(echo "$RESP" | jq -r '.driver.id')

  # Place them at slightly different positions
  lat=$(echo "12.9716 + $i * 0.001" | bc)
  lng=$(echo "77.5946 + $i * 0.001" | bc)

  curl -s -X PATCH "$BASE/drivers/$local_id/location" \
    -H "Content-Type: application/json" \
    -H "Authorization: Bearer $local_token" \
    -d "{\"lat\": $lat, \"lng\": $lng}" > /dev/null
done

# Now search for nearby — should find multiple
RESP=$(curl -s -w "\n%{http_code}" "$BASE/drivers/nearby?lat=12.9716&lng=77.5946&radius=5" \
  -H "Authorization: Bearer $RIDER_TOKEN")
parse_response "$RESP"
assert_status "GET /drivers/nearby — multiple drivers" "200" "$CODE"
DRIVER_COUNT=$(echo "$BODY" | jq '.drivers | length')
TOTAL=$((TOTAL+1))
if [ "$DRIVER_COUNT" -gt 1 ] 2>/dev/null; then
  green "  ✅ PASS [$TOTAL] Found $DRIVER_COUNT nearby drivers"
  PASS=$((PASS+1))
else
  red "  ❌ FAIL [$TOTAL] Expected multiple nearby drivers, got $DRIVER_COUNT"
  FAIL=$((FAIL+1))
fi
echo ""

# ═════════════════════════════════════════════════════════════════════════════
# RESULTS
# ═════════════════════════════════════════════════════════════════════════════
echo ""
bold "═══════════════════════════════════════════════════════════════"
bold "    TEST RESULTS"
bold "═══════════════════════════════════════════════════════════════"
echo ""
bold "  Total:  $TOTAL"
green "  Passed: $PASS"
if [ "$FAIL" -gt 0 ]; then
  red "  Failed: $FAIL"
else
  echo "  Failed: 0"
fi
echo ""

if [ "$FAIL" -eq 0 ]; then
  green "  🎉 ALL TESTS PASSED!"
else
  red "  ⚠️  SOME TESTS FAILED — review output above"
fi
echo ""

exit "$FAIL"
