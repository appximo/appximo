#!/usr/bin/env bash
# Seed data for the HomeBoard demo instance — plain API calls, nothing special.
# Usage: BASE=http://127.0.0.1:8501 HOST=homeboard.localhost TOKEN=... bash seed.sh
set -euo pipefail
BASE="${BASE:-http://127.0.0.1:8501}"; HOST="${HOST:-homeboard.localhost}"
A() { curl -sf -X "$1" "$BASE$2" -H "Authorization: Bearer $TOKEN" -H "Host: $HOST" -H 'Content-Type: application/json' -d "$3"; }
id() { python3 -c "import json,sys; print(json.load(sys.stdin)['id'])"; }

AG1=$(A POST /api/agents '{"name":"Sofia Reyes","email":"sofia@homeboard.demo","phone":"+1 555 0101"}' | id)
AG2=$(A POST /api/agents '{"name":"Marcus Lee","email":"marcus@homeboard.demo","phone":"+1 555 0102"}' | id)
AG3=$(A POST /api/agents '{"name":"Priya Nair","email":"priya@homeboard.demo","phone":"+1 555 0103"}' | id)
C1=$(A POST /api/clients '{"name":"Dana Whitfield","email":"dana@example.com","phone":"+1 555 0201"}' | id)
C2=$(A POST /api/clients '{"name":"Tom Okafor","email":"tom@example.com","phone":"+1 555 0202"}' | id)
C3=$(A POST /api/clients '{"name":"Lucia Marino","email":"lucia@example.com","phone":"+1 555 0203"}' | id)

prop() { # title neigh beds baths sqft price agent → id (created draft, then published via the state machine)
  local pid
  pid=$(A POST /api/properties "{\"title\":\"$1\",\"neighborhood\":\"$2\",\"bedrooms\":$3,\"bathrooms\":$4,\"square_feet\":$5,\"price_cents\":$6,\"agent_id\":\"$7\",\"description\":\"$8\"}" | id)
  A PATCH "/api/properties/$pid" '{"status":"published"}' >/dev/null
  echo "$pid"
}
P1=$(prop "Sunny 3BR craftsman with garden" "Maplewood" 3 2 1650 78900000 "$AG1" "Restored 1920s craftsman, south-facing garden, walk to the farmers market.")
P2=$(prop "Loft over the old bakery" "Riverside" 1 1 890 45500000 "$AG2" "Exposed brick, 4m ceilings, freight elevator. Smells faintly of bread.")
P3=$(prop "Corner duplex, two balconies" "Elm Heights" 4 3 2100 112000000 "$AG1" "Corner light all day, separate in-law unit downstairs.")
P4=$(prop "Compact studio near campus" "University Park" 0 1 420 21900000 "$AG3" "Fifth floor, elevator building, laundry in basement.")
P5=$(prop "Mid-century ranch, big lot" "Cedar Flats" 3 2 1780 66400000 "$AG2" "Original terrazzo floors, carport, mature oak out back.")
P6=$(prop "Penthouse with skyline terrace" "Riverside" 2 2 1400 158000000 "$AG1" "Wraparound terrace, private elevator landing.")
P7=$(prop "Row house needing love" "Old Mill" 2 1 1200 33800000 "$AG3" "Solid bones, dated kitchen — priced for the renovation.")
P8=$(A POST /api/properties "{\"title\":\"Farmhouse 20 min out\",\"neighborhood\":\"Green Valley\",\"bedrooms\":5,\"bathrooms\":3,\"square_feet\":3200,\"price_cents\":97500000,\"agent_id\":\"$AG2\",\"description\":\"Barn included. Draft listing — photos pending.\"}" | id)

A POST /api/visits "{\"property_id\":\"$P1\",\"client_id\":\"$C1\",\"scheduled_at\":\"2026-08-20T15:00:00Z\",\"notes\":\"Second visit, bringing the inspector.\"}" >/dev/null
A POST /api/visits "{\"property_id\":\"$P2\",\"client_id\":\"$C2\",\"scheduled_at\":\"2026-08-19T10:30:00Z\",\"notes\":\"\"}" >/dev/null
A POST /api/visits "{\"property_id\":\"$P6\",\"client_id\":\"$C3\",\"scheduled_at\":\"2026-08-21T17:00:00Z\",\"notes\":\"Asked about pets policy.\"}" >/dev/null
O1=$(A POST /api/offers "{\"property_id\":\"$P1\",\"client_id\":\"$C1\",\"amount_cents\":76000000,\"notes\":\"Contingent on inspection.\"}" | id)
A POST /api/offers "{\"property_id\":\"$P5\",\"client_id\":\"$C2\",\"amount_cents\":63000000,\"notes\":\"\"}" >/dev/null
A PATCH "/api/offers/$O1" '{"status":"accepted"}' >/dev/null
A PATCH "/api/properties/$P1" '{"status":"reserved"}' >/dev/null
echo "seeded: 3 agents, 3 clients, 8 properties (7 published, 1 reserved via accepted offer), 3 visits, 2 offers"
