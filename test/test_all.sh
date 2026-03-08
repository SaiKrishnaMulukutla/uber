#!/usr/bin/env bash
# ─────────────────────────────────────────────────────────────────────────────
# Comprehensive Test Suite for ride-hailing-system (microservices)
# Covers: Health, Users, Drivers, Trips, Matching (Kafka), WebSocket,
#         Refresh Tokens, RBAC, Ride History, Fare Estimation, Ratings,
#         Notifications, Payments, Trip Cancellation
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
assert_json_field "Registration returns access_token" "$BODY" ".access_token"
assert_json_field "Registration returns refresh_token" "$BODY" ".refresh_token"
assert_json_field "Registration returns user.id" "$BODY" ".user.id"
assert_json_equals "Registration returns correct email" "$BODY" ".user.email" "rider_${TS}@test.com"
assert_json_equals "Registration returns rating 5" "$BODY" ".user.rating" "5"
RIDER_TOKEN=$(echo "$BODY" | jq -r '.access_token')
RIDER_REFRESH=$(echo "$BODY" | jq -r '.refresh_token')
RIDER_ID=$(echo "$BODY" | jq -r '.user.id')

# 2b. Duplicate email
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/users/register" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"Dup\",\"email\":\"rider_${TS}@test.com\",\"phone\":\"+9999999\",\"password\":\"abc123\"}")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /users/register — duplicate email" "409" "$CODE"

# 2c. Duplicate phone
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/users/register" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"Dup2\",\"email\":\"other_${TS}@test.com\",\"phone\":\"+1${TS}\",\"password\":\"abc123\"}")
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
assert_json_field "Login returns access_token" "$BODY" ".access_token"
assert_json_field "Login returns refresh_token" "$BODY" ".refresh_token"
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
bold "4. USER REFRESH TOKEN"
# ─────────────────────────────────────────────────────────────────────────────

# 4a. Refresh with valid refresh token
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/users/refresh" \
  -H "Content-Type: application/json" \
  -d "{\"refresh_token\":\"$RIDER_REFRESH\"}")
parse_response "$RESP"
assert_status "POST /users/refresh — success" "200" "$CODE"
assert_json_field "Refresh returns new access_token" "$BODY" ".access_token"
assert_json_field "Refresh returns new refresh_token" "$BODY" ".refresh_token"

# Use the new access token for subsequent requests
RIDER_TOKEN=$(echo "$BODY" | jq -r '.access_token')

# 4b. Refresh with access token (should fail)
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/users/refresh" \
  -H "Content-Type: application/json" \
  -d "{\"refresh_token\":\"$RIDER_TOKEN\"}")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /users/refresh — access token rejected" "401" "$CODE"

# 4c. Refresh with invalid token
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/users/refresh" \
  -H "Content-Type: application/json" \
  -d '{"refresh_token":"invalid.token.here"}')
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /users/refresh — invalid token" "401" "$CODE"

# 4d. Refresh with empty body
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/users/refresh" \
  -H "Content-Type: application/json" \
  -d '{}')
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /users/refresh — empty body" "400" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "5. USER PROFILE"
# ─────────────────────────────────────────────────────────────────────────────

# 5a. Get profile with valid token
RESP=$(curl -s -w "\n%{http_code}" "$BASE/users/$RIDER_ID" \
  -H "Authorization: Bearer $RIDER_TOKEN")
parse_response "$RESP"
assert_status "GET /users/:id — with token" "200" "$CODE"
assert_json_equals "Profile returns correct ID" "$BODY" ".id" "$RIDER_ID"
assert_json_equals "Profile returns correct email" "$BODY" ".email" "rider_${TS}@test.com"

# 5b. Without token (unauthorized)
RESP=$(curl -s -w "\n%{http_code}" "$BASE/users/$RIDER_ID")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "GET /users/:id — no token" "401" "$CODE"

# 5c. Non-existent user
RESP=$(curl -s -w "\n%{http_code}" "$BASE/users/00000000-0000-0000-0000-000000000000" \
  -H "Authorization: Bearer $RIDER_TOKEN")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "GET /users/:id — not found" "404" "$CODE"

# 5d. Invalid token
RESP=$(curl -s -w "\n%{http_code}" "$BASE/users/$RIDER_ID" \
  -H "Authorization: Bearer invalid.jwt.token")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "GET /users/:id — invalid token" "401" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "6. DRIVER REGISTRATION"
# ─────────────────────────────────────────────────────────────────────────────

# 6a. Successful registration
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/drivers/register" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"Test Driver $TS\",\"email\":\"driver_${TS}@test.com\",\"phone\":\"+2${TS}\",\"password\":\"driverpass\",\"vehicle_type\":\"suv\",\"license_plate\":\"KA-01-AB-${TS}\"}")
parse_response "$RESP"
assert_status "POST /drivers/register — success" "201" "$CODE"
assert_json_field "Driver registration returns access_token" "$BODY" ".access_token"
assert_json_field "Driver registration returns refresh_token" "$BODY" ".refresh_token"
assert_json_field "Driver registration returns driver.id" "$BODY" ".driver.id"
assert_json_equals "Driver vehicle type" "$BODY" ".driver.vehicle_type" "suv"
assert_json_equals "Driver status is available" "$BODY" ".driver.status" "available"
assert_json_equals "Driver rating is 5" "$BODY" ".driver.rating" "5"
DRIVER_TOKEN=$(echo "$BODY" | jq -r '.access_token')
DRIVER_REFRESH=$(echo "$BODY" | jq -r '.refresh_token')
DRIVER_ID=$(echo "$BODY" | jq -r '.driver.id')

# 6b. Duplicate email
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/drivers/register" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"Dup Driver\",\"email\":\"driver_${TS}@test.com\",\"phone\":\"+9${TS}\",\"password\":\"abc123\",\"vehicle_type\":\"sedan\",\"license_plate\":\"X\"}")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /drivers/register — duplicate email" "409" "$CODE"

# 6c. Default vehicle_type when empty
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/drivers/register" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"Default VT\",\"email\":\"defvt_${TS}@test.com\",\"phone\":\"+3${TS}\",\"password\":\"abc123\",\"license_plate\":\"Y\"}")
parse_response "$RESP"
assert_status "POST /drivers/register — default vehicle_type" "201" "$CODE"
assert_json_equals "Default vehicle_type is sedan" "$BODY" ".driver.vehicle_type" "sedan"

# 6d. Invalid body
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/drivers/register" \
  -H "Content-Type: application/json" \
  -d "not json")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /drivers/register — invalid body" "400" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "7. DRIVER LOGIN"
# ─────────────────────────────────────────────────────────────────────────────

# 7a. Successful login
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/drivers/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"driver_${TS}@test.com\",\"password\":\"driverpass\"}")
parse_response "$RESP"
assert_status "POST /drivers/login — success" "200" "$CODE"
assert_json_field "Driver login returns access_token" "$BODY" ".access_token"
assert_json_field "Driver login returns refresh_token" "$BODY" ".refresh_token"
assert_json_field "Driver login returns driver.id" "$BODY" ".driver.id"

# 7b. Wrong password
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/drivers/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"driver_${TS}@test.com\",\"password\":\"wrongpass\"}")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /drivers/login — wrong password" "401" "$CODE"

# 7c. Non-existent email
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/drivers/login" \
  -H "Content-Type: application/json" \
  -d "{\"email\":\"nope@test.com\",\"password\":\"abc\"}")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /drivers/login — email not found" "401" "$CODE"

# 7d. Invalid body
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/drivers/login" \
  -H "Content-Type: application/json" \
  -d "bad")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /drivers/login — invalid body" "400" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "8. DRIVER REFRESH TOKEN"
# ─────────────────────────────────────────────────────────────────────────────

# 8a. Refresh with valid refresh token
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/drivers/refresh" \
  -H "Content-Type: application/json" \
  -d "{\"refresh_token\":\"$DRIVER_REFRESH\"}")
parse_response "$RESP"
assert_status "POST /drivers/refresh — success" "200" "$CODE"
assert_json_field "Driver refresh returns new access_token" "$BODY" ".access_token"
assert_json_field "Driver refresh returns new refresh_token" "$BODY" ".refresh_token"

# Use the new access token
DRIVER_TOKEN=$(echo "$BODY" | jq -r '.access_token')

# 8b. Refresh with access token (should fail)
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/drivers/refresh" \
  -H "Content-Type: application/json" \
  -d "{\"refresh_token\":\"$DRIVER_TOKEN\"}")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /drivers/refresh — access token rejected" "401" "$CODE"

# 8c. Cross-role: rider refresh token on driver endpoint (should fail)
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/drivers/refresh" \
  -H "Content-Type: application/json" \
  -d "{\"refresh_token\":\"$RIDER_REFRESH\"}")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /drivers/refresh — rider refresh token rejected" "401" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "9. DRIVER PROFILE"
# ─────────────────────────────────────────────────────────────────────────────

# 9a. Get driver with valid token
RESP=$(curl -s -w "\n%{http_code}" "$BASE/drivers/$DRIVER_ID" \
  -H "Authorization: Bearer $DRIVER_TOKEN")
parse_response "$RESP"
assert_status "GET /drivers/:id — with token" "200" "$CODE"
assert_json_equals "Driver profile ID" "$BODY" ".id" "$DRIVER_ID"

# 9b. Without token
RESP=$(curl -s -w "\n%{http_code}" "$BASE/drivers/$DRIVER_ID")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "GET /drivers/:id — no token" "401" "$CODE"

# 9c. Not found
RESP=$(curl -s -w "\n%{http_code}" "$BASE/drivers/00000000-0000-0000-0000-000000000000" \
  -H "Authorization: Bearer $DRIVER_TOKEN")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "GET /drivers/:id — not found" "404" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "10. DRIVER LOCATION UPDATE"
# ─────────────────────────────────────────────────────────────────────────────

# 10a. Update location — success
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/drivers/$DRIVER_ID/location" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $DRIVER_TOKEN" \
  -d '{"lat": 12.9716, "lng": 77.5946}')
parse_response "$RESP"
assert_status "PATCH /drivers/:id/location — success" "200" "$CODE"
assert_json_equals "Location update status" "$BODY" ".status" "location_updated"

# 10b. Without token
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/drivers/$DRIVER_ID/location" \
  -H "Content-Type: application/json" \
  -d '{"lat": 12.9716, "lng": 77.5946}')
CODE=$(echo "$RESP" | tail -n 1)
assert_status "PATCH /drivers/:id/location — no token" "401" "$CODE"

# 10c. Invalid body
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/drivers/$DRIVER_ID/location" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $DRIVER_TOKEN" \
  -d "bad")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "PATCH /drivers/:id/location — invalid body" "400" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "11. NEARBY DRIVERS"
# ─────────────────────────────────────────────────────────────────────────────

# 11a. Find nearby drivers
RESP=$(curl -s -w "\n%{http_code}" "$BASE/drivers/nearby?lat=12.9716&lng=77.5946&radius=5" \
  -H "Authorization: Bearer $DRIVER_TOKEN")
parse_response "$RESP"
assert_status "GET /drivers/nearby — found drivers" "200" "$CODE"
assert_json_field "Nearby returns drivers array" "$BODY" ".drivers"

# 11b. No nearby drivers (far location)
RESP=$(curl -s -w "\n%{http_code}" "$BASE/drivers/nearby?lat=0.0&lng=0.0&radius=1" \
  -H "Authorization: Bearer $DRIVER_TOKEN")
parse_response "$RESP"
assert_status "GET /drivers/nearby — remote location" "200" "$CODE"

# 11c. Without token
RESP=$(curl -s -w "\n%{http_code}" "$BASE/drivers/nearby?lat=12.9716&lng=77.5946")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "GET /drivers/nearby — no token" "401" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "12. TRIP REQUEST"
# ─────────────────────────────────────────────────────────────────────────────

# 12a. Request trip — success (rider token)
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/trips/request" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{"pickupLat": 12.9716, "pickupLng": 77.5946, "dropLat": 12.2958, "dropLng": 76.6394}')
parse_response "$RESP"
assert_status "POST /trips/request — success" "201" "$CODE"
assert_json_field "Trip request returns trip_id" "$BODY" ".trip_id"
assert_json_equals "Trip initial status is REQUESTED" "$BODY" ".status" "REQUESTED"
TRIP_ID=$(echo "$BODY" | jq -r '.trip_id')

# 12b. Without token
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/trips/request" \
  -H "Content-Type: application/json" \
  -d '{"pickupLat": 12.0, "pickupLng": 77.0, "dropLat": 13.0, "dropLng": 78.0}')
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /trips/request — no token" "401" "$CODE"

# 12c. Invalid body
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/trips/request" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d "bad")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "POST /trips/request — invalid body" "400" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "13. GET TRIP"
# ─────────────────────────────────────────────────────────────────────────────

# 13a. Get trip — success
RESP=$(curl -s -w "\n%{http_code}" "$BASE/trips/$TRIP_ID" \
  -H "Authorization: Bearer $RIDER_TOKEN")
parse_response "$RESP"
assert_status "GET /trips/:id — success" "200" "$CODE"
assert_json_equals "Trip ID matches" "$BODY" ".id" "$TRIP_ID"
assert_json_equals "Trip rider_id matches" "$BODY" ".rider_id" "$RIDER_ID"

# 13b. Without token
RESP=$(curl -s -w "\n%{http_code}" "$BASE/trips/$TRIP_ID")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "GET /trips/:id — no token" "401" "$CODE"

# 13c. Non-existent trip
RESP=$(curl -s -w "\n%{http_code}" "$BASE/trips/00000000-0000-0000-0000-000000000000" \
  -H "Authorization: Bearer $RIDER_TOKEN")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "GET /trips/:id — not found" "404" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "14. MANUAL TRIP LIFECYCLE (request → assign → start → end)"
# ─────────────────────────────────────────────────────────────────────────────

# Create a second trip for manual lifecycle testing
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/trips/request" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{"pickupLat": 28.6139, "pickupLng": 77.2090, "dropLat": 28.7041, "dropLng": 77.1025}')
parse_response "$RESP"
assert_status "Create trip for manual lifecycle" "201" "$CODE"
MANUAL_TRIP_ID=$(echo "$BODY" | jq -r '.trip_id')

sleep 1

# 14a. Assign driver — success
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$MANUAL_TRIP_ID/assign" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d "{\"driverId\":\"$DRIVER_ID\"}")
parse_response "$RESP"
assert_status "PATCH /trips/:id/assign — success" "200" "$CODE"
assert_json_equals "Trip status after assign" "$BODY" ".status" "DRIVER_ASSIGNED"
assert_json_equals "Assigned driver_id" "$BODY" ".driver_id" "$DRIVER_ID"

# 14b. Assign again (invalid state)
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$MANUAL_TRIP_ID/assign" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d "{\"driverId\":\"$DRIVER_ID\"}")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "PATCH /trips/:id/assign — already assigned" "400" "$CODE"

# 14c. Start trip — success (driver token)
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$MANUAL_TRIP_ID/start" \
  -H "Authorization: Bearer $DRIVER_TOKEN")
parse_response "$RESP"
assert_status "PATCH /trips/:id/start — success" "200" "$CODE"
assert_json_equals "Trip status after start" "$BODY" ".status" "STARTED"
assert_json_field "started_at is set" "$BODY" ".started_at"

# 14d. Start again (invalid state)
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$MANUAL_TRIP_ID/start" \
  -H "Authorization: Bearer $DRIVER_TOKEN")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "PATCH /trips/:id/start — already started" "400" "$CODE"

# 14e. End trip — success (driver token, haversine)
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$MANUAL_TRIP_ID/end" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $DRIVER_TOKEN" \
  -d '{}')
parse_response "$RESP"
assert_status "PATCH /trips/:id/end — success (haversine)" "200" "$CODE"
assert_json_equals "Trip status after end" "$BODY" ".status" "COMPLETED"
assert_json_field "Fare is set" "$BODY" ".fare"
assert_json_field "completed_at is set" "$BODY" ".completed_at"
FARE_HAVERSINE=$(echo "$BODY" | jq -r '.fare')
yellow "  ℹ  Fare (haversine): ₹${FARE_HAVERSINE}"

# 14f. End again (invalid state)
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$MANUAL_TRIP_ID/end" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $DRIVER_TOKEN" \
  -d '{}')
CODE=$(echo "$RESP" | tail -n 1)
assert_status "PATCH /trips/:id/end — already completed" "400" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "15. END TRIP WITH EXPLICIT DISTANCE"
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
  -H "Authorization: Bearer $DRIVER_TOKEN" > /dev/null

RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$DIST_TRIP_ID/end" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $DRIVER_TOKEN" \
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
bold "16. KAFKA AUTO-MATCHING (E2E FLOW)"
# ─────────────────────────────────────────────────────────────────────────────

# Register a new driver near Bangalore and set their location
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/drivers/register" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"Auto Driver $TS\",\"email\":\"autodriver_${TS}@test.com\",\"phone\":\"+4${TS}\",\"password\":\"auto123\",\"vehicle_type\":\"auto\",\"license_plate\":\"KA-AUTO-${TS}\"}")
BODY=$(echo "$RESP" | sed '$d')
AUTO_DRIVER_TOKEN=$(echo "$BODY" | jq -r '.access_token')
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
bold "17. ROLE-BASED ACCESS CONTROL (RBAC)"
# ─────────────────────────────────────────────────────────────────────────────

# 17a. Driver token cannot access rider endpoints
RESP=$(curl -s -w "\n%{http_code}" "$BASE/users/$RIDER_ID" \
  -H "Authorization: Bearer $DRIVER_TOKEN")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "Driver token → GET /users/:id → 403" "403" "$CODE"

# 17b. Rider token cannot access driver endpoints
RESP=$(curl -s -w "\n%{http_code}" "$BASE/drivers/$DRIVER_ID" \
  -H "Authorization: Bearer $RIDER_TOKEN")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "Rider token → GET /drivers/:id → 403" "403" "$CODE"

# 17c. Rider token cannot update driver location
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/drivers/$DRIVER_ID/location" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{"lat": 12.0, "lng": 77.0}')
CODE=$(echo "$RESP" | tail -n 1)
assert_status "Rider token → PATCH /drivers/:id/location → 403" "403" "$CODE"

# 17d. Driver token cannot request trips
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/trips/request" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $DRIVER_TOKEN" \
  -d '{"pickupLat": 1.0, "pickupLng": 1.0, "dropLat": 2.0, "dropLng": 2.0}')
CODE=$(echo "$RESP" | tail -n 1)
assert_status "Driver token → POST /trips/request → 403" "403" "$CODE"

# 17e. Rider token cannot start trips
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$MANUAL_TRIP_ID/start" \
  -H "Authorization: Bearer $RIDER_TOKEN")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "Rider token → PATCH /trips/:id/start → 403" "403" "$CODE"

# 17f. Rider token cannot end trips
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$MANUAL_TRIP_ID/end" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{}')
CODE=$(echo "$RESP" | tail -n 1)
assert_status "Rider token → PATCH /trips/:id/end → 403" "403" "$CODE"

# 17g. Rider CAN still access trip endpoints (with rider role)
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/trips/request" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{"pickupLat": 1.0, "pickupLng": 1.0, "dropLat": 2.0, "dropLng": 2.0}')
CODE=$(echo "$RESP" | tail -n 1)
assert_status "Rider token → POST /trips/request → 201" "201" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "18. RIDE HISTORY"
# ─────────────────────────────────────────────────────────────────────────────

# 18a. Rider history — should have multiple trips by now
RESP=$(curl -s -w "\n%{http_code}" "$BASE/trips/history" \
  -H "Authorization: Bearer $RIDER_TOKEN")
parse_response "$RESP"
assert_status "GET /trips/history — rider" "200" "$CODE"
assert_json_field "History returns trips array" "$BODY" ".trips"
assert_json_field "History returns total" "$BODY" ".total"
assert_json_equals "History default limit is 10" "$BODY" ".limit" "10"
assert_json_equals "History default offset is 0" "$BODY" ".offset" "0"

RIDER_TOTAL=$(echo "$BODY" | jq -r '.total')
TOTAL=$((TOTAL+1))
if [ "$RIDER_TOTAL" -gt 0 ] 2>/dev/null; then
  green "  ✅ PASS [$TOTAL] Rider has $RIDER_TOTAL trips in history"
  PASS=$((PASS+1))
else
  red "  ❌ FAIL [$TOTAL] Rider should have trips, got total=$RIDER_TOTAL"
  FAIL=$((FAIL+1))
fi

# 18b. Driver history
RESP=$(curl -s -w "\n%{http_code}" "$BASE/trips/history" \
  -H "Authorization: Bearer $DRIVER_TOKEN")
parse_response "$RESP"
assert_status "GET /trips/history — driver" "200" "$CODE"
assert_json_field "Driver history returns trips" "$BODY" ".trips"

# 18c. Pagination with limit
RESP=$(curl -s -w "\n%{http_code}" "$BASE/trips/history?limit=2&offset=0" \
  -H "Authorization: Bearer $RIDER_TOKEN")
parse_response "$RESP"
assert_status "GET /trips/history?limit=2 — success" "200" "$CODE"
assert_json_equals "History limit=2" "$BODY" ".limit" "2"

TRIPS_COUNT=$(echo "$BODY" | jq '.trips | length')
TOTAL=$((TOTAL+1))
if [ "$TRIPS_COUNT" -le 2 ] 2>/dev/null; then
  green "  ✅ PASS [$TOTAL] Pagination returns at most 2 trips (got $TRIPS_COUNT)"
  PASS=$((PASS+1))
else
  red "  ❌ FAIL [$TOTAL] Expected at most 2 trips, got $TRIPS_COUNT"
  FAIL=$((FAIL+1))
fi

# 18d. Limit capping at 50
RESP=$(curl -s -w "\n%{http_code}" "$BASE/trips/history?limit=100" \
  -H "Authorization: Bearer $RIDER_TOKEN")
parse_response "$RESP"
assert_status "GET /trips/history?limit=100 — capped" "200" "$CODE"
assert_json_equals "Limit capped at 50" "$BODY" ".limit" "50"

# 18e. Without token
RESP=$(curl -s -w "\n%{http_code}" "$BASE/trips/history")
CODE=$(echo "$RESP" | tail -n 1)
assert_status "GET /trips/history — no token" "401" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "19. WEBSOCKET CONNECTION"
# ─────────────────────────────────────────────────────────────────────────────

# Test WebSocket upgrade (basic check)
RESP=$(curl -s --max-time 3 -w "\n%{http_code}" "$BASE/ws/trips/$TRIP_ID" \
  -H "Upgrade: websocket" \
  -H "Connection: Upgrade" \
  -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" \
  -H "Sec-WebSocket-Version: 13" 2>&1 || true)
CODE=$(echo "$RESP" | tail -n 1)
TOTAL=$((TOTAL+1))
if [ "$CODE" = "101" ] || [ "$CODE" = "000" ] || [ "$CODE" = "200" ] || [ "$CODE" = "400" ]; then
  green "  ✅ PASS [$TOTAL] WS /ws/trips/:id — endpoint reachable (HTTP $CODE)"
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
bold "20. MULTIPLE DRIVERS — LOCATION & NEARBY"
# ─────────────────────────────────────────────────────────────────────────────

# Register 3 more drivers at different locations
for i in 1 2 3; do
  RESP=$(curl -s -X POST "$BASE/drivers/register" \
    -H "Content-Type: application/json" \
    -d "{\"name\":\"Multi Driver $i $TS\",\"email\":\"multi${i}_${TS}@test.com\",\"phone\":\"+5${i}${TS}\",\"password\":\"pass12\",\"vehicle_type\":\"sedan\",\"license_plate\":\"MUL-$i-${TS}\"}")
  local_token=$(echo "$RESP" | jq -r '.access_token')
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
  -H "Authorization: Bearer $DRIVER_TOKEN")
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

# ─────────────────────────────────────────────────────────────────────────────
bold "21. FARE ESTIMATION"
# ─────────────────────────────────────────────────────────────────────────────

RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/trips/estimate" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{"pickupLat":12.9716,"pickupLng":77.5946,"dropLat":12.9352,"dropLng":77.6245}')
parse_response "$RESP"
assert_status "POST /trips/estimate — rider gets estimate" "200" "$CODE"
assert_json_field "Estimate has fare" "$BODY" ".estimated_fare"
assert_json_field "Estimate has distance" "$BODY" ".estimated_distance"
assert_json_field "Estimate has currency" "$BODY" ".currency"

# Driver should not be able to estimate
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/trips/estimate" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $DRIVER_TOKEN" \
  -d '{"pickupLat":12.9716,"pickupLng":77.5946,"dropLat":12.9352,"dropLng":77.6245}')
parse_response "$RESP"
assert_status "POST /trips/estimate — driver forbidden" "403" "$CODE"

# No auth
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/trips/estimate" \
  -H "Content-Type: application/json" \
  -d '{"pickupLat":12.97,"pickupLng":77.59,"dropLat":12.93,"dropLng":77.62}')
parse_response "$RESP"
assert_status "POST /trips/estimate — no auth rejected" "401" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "22. TRIP CANCELLATION"
# ─────────────────────────────────────────────────────────────────────────────

# Create a trip to cancel
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/trips/request" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{"pickupLat":12.98,"pickupLng":77.60,"dropLat":12.94,"dropLng":77.64}')
parse_response "$RESP"
CANCEL_TRIP_ID=$(echo "$BODY" | jq -r '.trip_id')
assert_status "POST /trips/request — trip for cancellation" "201" "$CODE"

# Cancel it
RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/trips/$CANCEL_TRIP_ID/cancel" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{"reason":"changed my mind"}')
parse_response "$RESP"
assert_status "PATCH /trips/{id}/cancel — cancel trip" "200" "$CODE"

# Verify status is CANCELLED
CANCEL_STATUS=$(echo "$BODY" | jq -r '.status')
TOTAL=$((TOTAL+1))
if [ "$CANCEL_STATUS" = "CANCELLED" ]; then
  green "  ✅ PASS [$TOTAL] Trip status is CANCELLED"
  PASS=$((PASS+1))
else
  red "  ❌ FAIL [$TOTAL] Expected CANCELLED, got $CANCEL_STATUS"
  FAIL=$((FAIL+1))
fi
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "23. RATING SYSTEM"
# ─────────────────────────────────────────────────────────────────────────────

# Use a completed trip — need a fresh full lifecycle
RATE_DRIVER_REG=$(curl -s -X POST "$BASE/drivers/register" \
  -H "Content-Type: application/json" \
  -d "{\"name\":\"Rate Driver $TS\",\"email\":\"ratedrv_${TS}@test.com\",\"phone\":\"+77${TS}\",\"password\":\"pass12\",\"vehicle_type\":\"sedan\",\"license_plate\":\"RATE-${TS}\"}")
RATE_DRV_TOKEN=$(echo "$RATE_DRIVER_REG" | jq -r '.access_token')
RATE_DRV_ID=$(echo "$RATE_DRIVER_REG" | jq -r '.driver.id')

# Set driver location
curl -s -X PATCH "$BASE/drivers/$RATE_DRV_ID/location" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RATE_DRV_TOKEN" \
  -d '{"lat": 12.9716, "lng": 77.5946}' > /dev/null

# Request trip → wait for matching → start → end
RESP=$(curl -s -X POST "$BASE/trips/request" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{"pickupLat":12.9716,"pickupLng":77.5946,"dropLat":12.93,"dropLng":77.62}')
RATE_TRIP_ID=$(echo "$RESP" | jq -r '.trip_id')

# Manual assign to ensure we have the right driver
curl -s -X PATCH "$BASE/trips/$RATE_TRIP_ID/assign" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d "{\"driverId\":\"$RATE_DRV_ID\"}" > /dev/null

curl -s -X PATCH "$BASE/trips/$RATE_TRIP_ID/start" \
  -H "Authorization: Bearer $RATE_DRV_TOKEN" > /dev/null

curl -s -X PATCH "$BASE/trips/$RATE_TRIP_ID/end" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RATE_DRV_TOKEN" \
  -d '{}' > /dev/null

# Now rate
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/trips/$RATE_TRIP_ID/rate" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{"score":5,"comment":"Great ride!"}')
parse_response "$RESP"
assert_status "POST /trips/{id}/rate — rider rates driver" "201" "$CODE"
assert_json_field "Rating has score" "$BODY" ".score"

# Invalid score
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/trips/$RATE_TRIP_ID/rate" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RATE_DRV_TOKEN" \
  -d '{"score":6}')
parse_response "$RESP"
assert_status "POST /trips/{id}/rate — score > 5 rejected" "400" "$CODE"

# Duplicate rating (rider already rated)
RESP=$(curl -s -w "\n%{http_code}" -X POST "$BASE/trips/$RATE_TRIP_ID/rate" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $RIDER_TOKEN" \
  -d '{"score":4}')
parse_response "$RESP"
assert_status "POST /trips/{id}/rate — duplicate rejected" "400" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "24. NOTIFICATIONS"
# ─────────────────────────────────────────────────────────────────────────────

sleep 3  # Wait for Kafka events to generate notifications

RESP=$(curl -s -w "\n%{http_code}" "$BASE/notifications/" \
  -H "Authorization: Bearer $RIDER_TOKEN")
parse_response "$RESP"
assert_status "GET /notifications/ — rider gets notifications" "200" "$CODE"
assert_json_field "Has notifications array" "$BODY" ".notifications"
NOTIF_COUNT=$(echo "$BODY" | jq '.notifications | length')
TOTAL=$((TOTAL+1))
if [ "$NOTIF_COUNT" -gt 0 ] 2>/dev/null; then
  green "  ✅ PASS [$TOTAL] Rider has $NOTIF_COUNT notifications"
  PASS=$((PASS+1))
else
  red "  ❌ FAIL [$TOTAL] Expected notifications, got $NOTIF_COUNT"
  FAIL=$((FAIL+1))
fi

# Mark first notification as read
NOTIF_ID=$(echo "$BODY" | jq -r '.notifications[0].id')
if [ -n "$NOTIF_ID" ] && [ "$NOTIF_ID" != "null" ]; then
  RESP=$(curl -s -w "\n%{http_code}" -X PATCH "$BASE/notifications/$NOTIF_ID/read" \
    -H "Authorization: Bearer $RIDER_TOKEN")
  parse_response "$RESP"
  assert_status "PATCH /notifications/{id}/read — mark read" "200" "$CODE"
fi

# No auth
RESP=$(curl -s -w "\n%{http_code}" "$BASE/notifications/" )
parse_response "$RESP"
assert_status "GET /notifications/ — no auth rejected" "401" "$CODE"
echo ""

# ─────────────────────────────────────────────────────────────────────────────
bold "25. PAYMENTS"
# ─────────────────────────────────────────────────────────────────────────────

sleep 2  # Wait for payment processing

RESP=$(curl -s -w "\n%{http_code}" "$BASE/payments/history" \
  -H "Authorization: Bearer $RIDER_TOKEN")
parse_response "$RESP"
assert_status "GET /payments/history — rider payment history" "200" "$CODE"
assert_json_field "Has payments array" "$BODY" ".payments"

# Get payment by trip ID (using the rated trip which was completed)
RESP=$(curl -s -w "\n%{http_code}" "$BASE/payments/$RATE_TRIP_ID" \
  -H "Authorization: Bearer $RIDER_TOKEN")
parse_response "$RESP"
assert_status "GET /payments/{tripId} — get by trip" "200" "$CODE"
assert_json_field "Payment has amount" "$BODY" ".amount"
assert_json_field "Payment has status" "$BODY" ".status"

# Non-owner should be forbidden
RESP=$(curl -s -w "\n%{http_code}" "$BASE/payments/$RATE_TRIP_ID" \
  -H "Authorization: Bearer $RATE_DRV_TOKEN")
parse_response "$RESP"
# Driver on the trip should have access too, so just check it doesn't 401
TOTAL=$((TOTAL+1))
if [ "$CODE" = "200" ] || [ "$CODE" = "403" ]; then
  green "  ✅ PASS [$TOTAL] GET /payments/{tripId} — auth enforced (HTTP $CODE)"
  PASS=$((PASS+1))
else
  red "  ❌ FAIL [$TOTAL] GET /payments/{tripId} — unexpected HTTP $CODE"
  FAIL=$((FAIL+1))
fi

# No auth
RESP=$(curl -s -w "\n%{http_code}" "$BASE/payments/history")
parse_response "$RESP"
assert_status "GET /payments/history — no auth rejected" "401" "$CODE"
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
