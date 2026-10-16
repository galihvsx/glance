#!/bin/bash
# visual.sh — build a glance worktree, boot it with seeded QA data, screenshot screens.
# Usage (from repo root): ./qa/visual.sh <worktree-path> <screen> [screen...]
# Screens: shell | board | detail | list | mywork | inbox | cycles
# Output: qa/shots/<screen>.png
#
# Credentials come from the environment; nothing real is baked in:
#   GLANCE_QA_DB_USER       Postgres role            (default: glance)
#   GLANCE_QA_DB_PASSWORD   Postgres role password   (default: glance, local dev only)
#   GLANCE_QA_DB_HOST       Postgres host            (default: 127.0.0.1)
#   GLANCE_QA_DB_PORT       Postgres port            (default: 5432)
set -euo pipefail

WT="${1:?usage: visual.sh <worktree-path> <screen>...}"; shift
SCREENS=("$@")
QA_DIR="$(cd "$(dirname "$0")" && pwd)"
mkdir -p "$QA_DIR/shots"
PORT=18080
BASE="http://127.0.0.1:$PORT"
EMAIL="qa@example.com"
# Unique scratch DB per run so parallel implementers never clobber each other.
DB="glance_qa_visual_$$"

export PATH=/home/hatch/workspace/.gotoolchain/go/bin:$PATH
export GOCACHE=/home/hatch/workspace/.gocache GOTMPDIR=/home/hatch/workspace/.gocache GOTOOLCHAIN=local

DB_USER="${GLANCE_QA_DB_USER:-glance}"
DB_HOST="${GLANCE_QA_DB_HOST:-127.0.0.1}"
DB_PORT="${GLANCE_QA_DB_PORT:-5432}"
export PGPASSWORD="${GLANCE_QA_DB_PASSWORD:-glance}" # local-dev default only; see qa/README.md

psql() { command psql -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" "$@"; }

echo "== installing web deps & building =="
(cd "$WT/web" && npm ci >/tmp/qa-web-build.log 2>&1 && npm run build >>/tmp/qa-web-build.log 2>&1)
echo "== building binary =="
(cd "$WT" && go build -o /tmp/glance-qa-bin ./cmd/glance)

echo "== resetting scratch db =="
psql -d postgres -c "DROP DATABASE IF EXISTS $DB" -c "CREATE DATABASE $DB" >/dev/null

echo "== booting server =="
DATABASE_URL="postgres://$DB_USER:$PGPASSWORD@$DB_HOST:$DB_PORT/$DB?sslmode=disable" PORT=$PORT \
  ALLOW_INSECURE_OTP_PEPPER=1 \
  /tmp/glance-qa-bin >/tmp/qa-server.log 2>&1 &
SRV=$!
trap "kill $SRV 2>/dev/null" EXIT
for i in $(seq 1 30); do curl -sf "$BASE/api/v1/openapi.json" >/dev/null 2>&1 && break; sleep 1; done

JAR=/tmp/qa-cookies.txt; rm -f $JAR
echo "== otp login =="
curl -sf -X POST "$BASE/api/v1/auth/otp/request" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\"}" >/dev/null
CODE=$(psql -d $DB -tA -c \
  "SELECT (payload->>'body') FROM outbox WHERE event='email.otp' ORDER BY id DESC LIMIT 1" | grep -oE '[0-9]{6}' | head -1)
[ -z "$CODE" ] && { echo "no OTP code found"; exit 1; }
curl -sf -c $JAR -X POST "$BASE/api/v1/auth/otp/verify" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"code\":\"$CODE\"}" >/dev/null
SESSION=$(grep glance_session $JAR | awk '{print $NF}')
[ -z "$SESSION" ] && { echo "login failed"; exit 1; }
echo "logged in"

api() { curl -sf -b $JAR -H 'Content-Type: application/json' "$@"; }

echo "== seeding =="
WS_SLUG=$(api -X POST "$BASE/api/v1/workspaces" -d '{"name":"Acme","slug":"acme"}' | python3 -c "import json,sys; print(json.load(sys.stdin)['slug'])")
api -X POST "$BASE/api/v1/workspaces/$WS_SLUG/projects" -d '{"name":"Example Workspace","identifier":"EXAMP"}' >/dev/null
STATES_JSON=$(api "$BASE/api/v1/workspaces/$WS_SLUG/projects/EXAMP/states")
declare -A SID
while IFS=$'\t' read -r name id; do SID[$name]=$id; done < <(echo "$STATES_JSON" | python3 -c "
import json,sys
d=json.load(sys.stdin)
items=d if isinstance(d,list) else d.get('states',d.get('data',[]))
for s in items: print(s['name']+'\t'+s['id'])")

mkissue() { # name priority state_name
  api -X POST "$BASE/api/v1/workspaces/$WS_SLUG/projects/EXAMP/issues" \
    -d "$(python3 -c "import json; print(json.dumps({'name':'''$1''','priority':$2,'state_id':'${SID[$3]}'}))")" >/dev/null
}
mkissue "Add full-text command menu search" 3 "Backlog"
mkissue "Document self-hosted upgrade strategy" 2 "Backlog"
mkissue "Support bulk label changes from list view" 1 "Backlog"
mkissue "Preserve board filters in shared links" 2 "Todo"
mkissue "Improve keyboard navigation in issue peek" 3 "In Progress"
mkissue "Add workspace-level webhook delivery log" 4 "In Progress"
mkissue "Reduce first-load bundle size" 3 "Done"
mkintake() { # name priority — opts into the intake inbox
  api -X POST "$BASE/api/v1/workspaces/$WS_SLUG/projects/EXAMP/issues" \
    -d "$(python3 -c "import json; print(json.dumps({'name':'''$1''','priority':$2,'intake':True}))")" >/dev/null
}
mkintake "Customer asks for SOC2 report" 2
mkintake "Idea: public roadmap page" 1
echo "seeded 7 issues"
echo "== seeding cycle =="
START=$(date -d "-4 days" +%F); END=$(date -d "+10 days" +%F)
CYCLE_ID=$(api -X POST "$BASE/api/v1/workspaces/$WS_SLUG/projects/EXAMP/cycles" -d "{\"name\":\"Sprint 1\",\"start_date\":\"$START\",\"end_date\":\"$END\"}" | python3 -c "import json,sys; print(json.load(sys.stdin)['id'])")
ISSUE_IDS=$(api "$BASE/api/v1/workspaces/$WS_SLUG/projects/EXAMP/issues?limit=100" | python3 -c "
import json,sys
d = json.load(sys.stdin)
items = d if isinstance(d, list) else d.get('results', d.get('issues', []))
print(','.join('\"%s\"' % i['id'] for i in items[:3]))")
api -X POST "$BASE/api/v1/workspaces/$WS_SLUG/projects/EXAMP/cycles/$CYCLE_ID/issues" -d "{\"issue_ids\":[$ISSUE_IDS]}" >/dev/null
echo "seeded cycle Sprint 1 ($START -> $END) with 3 issues"

echo "== screenshots =="
node "$QA_DIR/shot.mjs" "$BASE" "$SESSION" "$QA_DIR/shots" "${SCREENS[@]}"
echo "done -> $QA_DIR/shots"
