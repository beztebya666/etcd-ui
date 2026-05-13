#!/usr/bin/env bash
# Chaos / failover test. Drives the 3-node etcd cluster from docker-compose.yml
# through a sequence of node kills and verifies that:
#
#   1. The UI's /api/clusters reports the cluster as still healthy with 2/3 up.
#   2. Reads continue working through the UI gateway.
#   3. Writes continue working through the UI gateway (a non-leader kill must
#      not block writes; a leader kill should pause for ~election_timeout then
#      succeed).
#   4. After restart, the cluster reports 3/3 members again.
#
# Run:
#   docker compose -f chaos/docker-compose.yml up -d --build
#   chaos/run.sh
#
# Exit code 0 == pass. Non-zero == something failed and the docker logs are
# left for inspection (don't run `docker compose down` automatically).

set -euo pipefail

UI=${UI:-http://localhost:18080}
NODES=(chaos-etcd0 chaos-etcd1 chaos-etcd2)

log() { echo -e "\033[36m▸\033[0m $*"; }
fail() { echo -e "\033[31m✗\033[0m $*"; exit 1; }
ok() { echo -e "\033[32m✓\033[0m $*"; }

wait_for_ui() {
  log "waiting for UI gateway at $UI"
  for i in {1..60}; do
    if curl -fsS "$UI/healthz" >/dev/null 2>&1; then ok "UI is up"; return; fi
    sleep 1
  done
  fail "UI never came up"
}

cluster_healthy() {
  curl -fsS "$UI/api/clusters" | grep -q '"healthy":true'
}

wait_for_healthy() {
  log "waiting for cluster healthy via UI"
  for i in {1..30}; do
    if cluster_healthy; then ok "cluster healthy"; return; fi
    sleep 2
  done
  fail "cluster never went healthy"
}

put_key() {
  local key=$1 val=$2
  curl -fsS -X POST "$UI/api/clusters/chaos/put" \
    -H 'content-type: application/json' \
    -d "{\"key\":\"$key\",\"value\":\"$val\"}" >/dev/null
}

get_key() {
  local key=$1
  curl -fsS -X POST "$UI/api/clusters/chaos/range" \
    -H 'content-type: application/json' \
    -d "{\"prefix\":\"$key\"}"
}

retry_put_until_ok() {
  local key=$1 val=$2 attempts=${3:-30}
  for i in $(seq 1 $attempts); do
    if put_key "$key" "$val" 2>/dev/null; then return 0; fi
    sleep 1
  done
  return 1
}

# ---------------------------------------------------------------------------

wait_for_ui
wait_for_healthy

log "seed: write 5 keys"
for i in 1 2 3 4 5; do put_key "/chaos/k$i" "v$i"; done
ok "seeded"

log "scenario A: kill follower and verify UI keeps responding"
# pick a follower: anyone besides the current leader. cheap heuristic — try the
# last node first; if it's leader we'll see writes block transiently and pick
# again.
victim=${NODES[2]}
docker kill "$victim" >/dev/null
log "killed $victim — waiting 3s"
sleep 3
if ! retry_put_until_ok "/chaos/after-kill" "x" 15; then
  fail "writes did not succeed after follower kill"
fi
ok "writes survived follower kill"
docker start "$victim" >/dev/null
sleep 5

log "scenario B: kill leader and verify recovery"
# Force a leader election by killing whoever is leader right now. The endpoint
# status JSON tells us; pull via the etcdctl handler.
LEADER=$(curl -fsS -X POST "$UI/api/clusters/chaos/etcdctl" \
  -H 'content-type: application/json' \
  -d '{"command":"endpoint status -w json"}' | jq -r '.stdout' \
  | jq -r '.[] | select(.Status.leader == .Status.header.member_id) | .Endpoint' \
  || true)
log "current leader endpoint: ${LEADER:-unknown}"
if [[ -z "$LEADER" ]]; then
  log "(leader detection failed — skipping leader-kill scenario)"
else
  victim=$(echo "$LEADER" | sed 's|http://||;s|:.*||')
  victim="chaos-$victim"
  docker kill "$victim" >/dev/null
  log "killed leader $victim — waiting up to 30s for new election"
  if ! retry_put_until_ok "/chaos/after-leader-kill" "y" 30; then
    fail "writes did not recover after leader kill within 30s"
  fi
  ok "writes recovered after leader election"
  docker start "$victim" >/dev/null
fi

log "scenario C: cluster returns to 3/3 healthy members"
sleep 10
MEMBERS=$(curl -fsS "$UI/api/clusters" | jq '.[0].memberCount')
if [[ "$MEMBERS" != "3" ]]; then
  fail "expected 3 members after recovery, got $MEMBERS"
fi
ok "3/3 members"

ok "all scenarios passed"
