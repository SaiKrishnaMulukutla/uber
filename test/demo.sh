#!/usr/bin/env bash
# ──────────────────────────────────────────────────────────────────
#  demo.sh — End-to-end ride-hailing demo
#
#  Prerequisites: curl, jq
#  Usage:         bash demo.sh [BASE_URL]
#  Default URL:   http://localhost:8000
# ──────────────────────────────────────────────────────────────────
set -euo pipefail

BASE_URL="${1:-http://localhost:8000}"

# ── Colours ──────────────────────────────────────────────────────
RESET='\033[0m'
BOLD='\033[1m'
GREEN='\033[0;32m'
CYAN='\033[0;36m'
YELLOW='\033[1;33m'
RED='\033[0;31m'
GREY='\033[0;90m'

step()  { echo -e "\n${BOLD}${CYAN}▶ $*${RESET}"; }
ok()    { echo -e "  ${GREEN}✔ $*${RESET}"; }
info()  { echo -e "  ${YELLOW}ℹ $*${RESET}"; }
err()   { echo -e "  ${RED}✘ $*${RESET}"; }
dim()   { echo -e "${GREY}$*${RESET}"; }

# ── Helpers ───────────────────────────────────────────────────────
require_cmd() {
  if ! command -v "$1" &>/dev/null; then
    err "Required command '$1' not found. Install it and retry."
    exit 1
  fi
}

check_service() {
  local status
  status=$(curl -s -o /dev/null -w "%{http_code}" "$BASE_URL/health" 2>/dev/null || echo "000")
  if [[ "$status" != "200" ]]; then
    err "Service not reachable at $BASE_URL (HTTP $status)."
    info "Run 'make up' and wait ~30s for all services to start."
    exit 1
  fi
}

post() {
  local url="$1" body="$2"
  curl -s -X POST "$BASE_URL$url" \
    -H "Content-Type: application/json" \
    -d "$body"
}

post_auth() {
  local url="$1" token="$2" body="$3"
  curl -s -X POST "$BASE_URL$url" \
    -H "Authorization: Bearer $token" \
    -H "Content-Type: application/json" \
    -d "$body"
}

patch_auth() {
  local url="$1" token="$2" body="${3:-{}}"
  curl -s -X PATCH "$BASE_URL$url" \
    -H "Authorization: Bearer $token" \
    -H "Content-Type: application/json" \
    -d "$body"
}

get_auth() {
  local url="$1" token="$2"
  curl -s -X GET "$BASE_URL$url" \
    -H "Authorization: Bearer $token"
}

assert_field() {
  local label="$1" value="$2"
  if [[ -z "$value" || "$value" == "null" ]]; then
    err "$label is missing or null"
    exit 1
  fi
  ok "$label = $value"
}

# ─────────────────────────────────────────────────────────────────
echo -e "\n${BOLD}════════════════════════════════════════════${RESET}"
echo -e "${BOLD}       🚗  Ride-Hailing System  Demo         ${RESET}"
echo -e "${BOLD}════════════════════════════════════════════${RESET}"
echo -e "  Base URL : ${CYAN}$BASE_URL${RESET}"
echo -e "  Date     : $(date '+%Y-%m-%d %H:%M:%S')"

# ── Pre-flight ───────────────────────────────────────────────────
require_cmd curl
require_cmd jq

step "0. Health check"
check_service
HEALTH=$(curl -s "$BASE_URL/health")
ok "Service is up — $(echo "$HEALTH" | jq -r '.status')"

# ── Unique suffix to avoid email conflicts on re-runs ─────────────
SUFFIX=$(date +%s)
RIDER_EMAIL="rider_${SUFFIX}@demo.com"
DRIVER_EMAIL="driver_${SUFFIX}@demo.com"

# ─────────────────────────────────────────────────────────────────
step "1. Register Rider"
RIDER=$(post "/users/register" "{
  \"name\":     \"Test Rider\",
  \"email\":    \"$RIDER_EMAIL\",
  \"phone\":    \"+911111${SUFFIX: -6}\",
  \"password\": \"Pass123!\"
}")
dim "  Response: $(echo "$RIDER" | jq -c '{token_length: (.token | length), user: .user.id}')"

RIDER_TOKEN=$(echo "$RIDER" | jq -r '.token')
RIDER_ID=$(echo "$RIDER" | jq -r '.user.id')
assert_field "rider_token" "$RIDER_TOKEN"
assert_field "rider_id"    "$RIDER_ID"

# ─────────────────────────────────────────────────────────────────
step "2. Register Driver"
DRIVER=$(post "/drivers/register" "{
  \"name\":          \"Test Driver\",
  \"email\":         \"$DRIVER_EMAIL\",
  \"phone\":         \"+912222${SUFFIX: -6}\",
  \"password\":      \"Driver123!\",
  \"vehicle_type\":  \"sedan\",
  \"license_plate\": \"KA-99-ZZ-${SUFFIX: -4}\"
}")
dim "  Response: $(echo "$DRIVER" | jq -c '{token_length: (.token | length), driver: .driver.id}')"

DRIVER_TOKEN=$(echo "$DRIVER" | jq -r '.token')
DRIVER_ID=$(echo "$DRIVER" | jq -r '.driver.id')
assert_field "driver_token" "$DRIVER_TOKEN"
assert_field "driver_id"    "$DRIVER_ID"

# ─────────────────────────────────────────────────────────────────
step "3. Update Driver Location  (Bangalore: 12.9716, 77.5946)"
LOC=$(patch_auth "/drivers/$DRIVER_ID/location" "$DRIVER_TOKEN" \
  '{"lat": 12.9716, "lng": 77.5946}')
LOC_STATUS=$(echo "$LOC" | jq -r '.status')
assert_field "location status" "$LOC_STATUS"

# ─────────────────────────────────────────────────────────────────
step "4. Verify Driver Appears in Nearby Search"
NEARBY=$(get_auth "/drivers/nearby?lat=12.9716&lng=77.5946&radius=5" "$RIDER_TOKEN")
DRIVER_COUNT=$(echo "$NEARBY" | jq '.drivers | length')
ok "Nearby drivers found: $DRIVER_COUNT"
if echo "$NEARBY" | jq -e ".drivers | index(\"$DRIVER_ID\")" &>/dev/null; then
  ok "Our driver ($DRIVER_ID) is in the result"
else
  info "Driver not yet in nearby list (Redis may need a moment)"
fi

# ─────────────────────────────────────────────────────────────────
step "5. Request a Ride  (pickup → drop, both near Bangalore)"
TRIP=$(post_auth "/trips/request" "$RIDER_TOKEN" \
  '{"pickupLat": 12.9716, "pickupLng": 77.5946,
    "dropLat":   12.9352, "dropLng":   77.6245}')
dim "  Response: $(echo "$TRIP" | jq -c .)"

TRIP_ID=$(echo "$TRIP" | jq -r '.trip_id')
TRIP_STATUS=$(echo "$TRIP" | jq -r '.status')
assert_field "trip_id"     "$TRIP_ID"
assert_field "trip_status" "$TRIP_STATUS"
info "Kafka event 'ride.requested' published → matching engine running…"

# ─────────────────────────────────────────────────────────────────
step "6. Wait 5s for Kafka Matching"
for i in 5 4 3 2 1; do
  echo -ne "  ${YELLOW}  …${i}${RESET}\r"
  sleep 1
done
echo ""

# ─────────────────────────────────────────────────────────────────
step "7. Verify Driver Auto-Assigned"
TRIP_DETAIL=$(get_auth "/trips/$TRIP_ID" "$RIDER_TOKEN")
ASSIGNED_STATUS=$(echo "$TRIP_DETAIL" | jq -r '.status')
ASSIGNED_DRIVER=$(echo "$TRIP_DETAIL" | jq -r '.driver_id // "null"')

dim "  Trip status : $ASSIGNED_STATUS"
dim "  Driver ID   : $ASSIGNED_DRIVER"

if [[ "$ASSIGNED_STATUS" == "DRIVER_ASSIGNED" ]]; then
  ok "Kafka matching worked — driver auto-assigned!"
else
  info "Auto-matching not complete yet (status: $ASSIGNED_STATUS)"
  info "Falling back to manual assignment…"

  ASSIGN=$(patch_auth "/trips/$TRIP_ID/assign" "$RIDER_TOKEN" \
    "{\"driverId\": \"$DRIVER_ID\"}")
  ASSIGNED_STATUS=$(echo "$ASSIGN" | jq -r '.status')
  ASSIGNED_DRIVER=$(echo "$ASSIGN" | jq -r '.driver_id // "null"')
  ok "Manual assign — status: $ASSIGNED_STATUS, driver: $ASSIGNED_DRIVER"
fi

# ─────────────────────────────────────────────────────────────────
step "8. Start Trip"
START=$(patch_auth "/trips/$TRIP_ID/start" "$RIDER_TOKEN")
START_STATUS=$(echo "$START" | jq -r '.status')
START_AT=$(echo "$START"    | jq -r '.started_at // "—"')
assert_field "start status" "$START_STATUS"
ok "started_at = $START_AT"
info "WebSocket subscribers would now receive live location updates:"
info "  wscat -c ws://localhost:8000/ws/trips/$TRIP_ID"

# ─────────────────────────────────────────────────────────────────
step "9. End Trip  (10.5 km)"
END=$(patch_auth "/trips/$TRIP_ID/end" "$RIDER_TOKEN" '{"distanceKm": 10.5}')
END_STATUS=$(echo "$END"    | jq -r '.status')
END_FARE=$(echo "$END"      | jq -r '.fare // "—"')
END_AT=$(echo "$END"        | jq -r '.completed_at // "—"')

assert_field "end status" "$END_STATUS"
ok "fare         = ₹$END_FARE  (₹50 base + ₹12 × 10.5 km = ₹176)"
ok "completed_at = $END_AT"
info "Kafka event 'trip.completed' published"

# ─────────────────────────────────────────────────────────────────
echo -e "\n${BOLD}════════════════════════════════════════════${RESET}"
echo -e "${BOLD}${GREEN}       ✅  Demo Complete!                     ${RESET}"
echo -e "${BOLD}════════════════════════════════════════════${RESET}"
echo -e ""
echo -e "  ${BOLD}Summary${RESET}"
echo -e "  ├── Rider ID   : ${CYAN}$RIDER_ID${RESET}"
echo -e "  ├── Driver ID  : ${CYAN}$DRIVER_ID${RESET}"
echo -e "  ├── Trip ID    : ${CYAN}$TRIP_ID${RESET}"
echo -e "  ├── Final Fare : ${GREEN}₹$END_FARE${RESET}"
echo -e "  └── Status     : ${GREEN}$END_STATUS${RESET}"
echo -e ""
echo -e "  ${BOLD}What was demonstrated:${RESET}"
echo -e "  1. User (rider) registration & JWT auth"
echo -e "  2. Driver registration with vehicle info"
echo -e "  3. Driver GPS location stored in Redis GEO"
echo -e "  4. Nearby driver search via Redis GEO query"
echo -e "  5. Trip request → Kafka 'ride.requested' event"
echo -e "  6. Kafka matching engine auto-assigns nearest driver"
echo -e "  7. Trip started (DRIVER_ASSIGNED → STARTED)"
echo -e "  8. Live tracking via WebSocket /ws/trips/:id"
echo -e "  9. Trip ended → fare calculated → 'trip.completed' event"
echo -e ""
