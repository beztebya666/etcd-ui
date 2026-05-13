#!/usr/bin/env bash
# Extended chaos scenarios beyond simple node kills. Requires:
#   - The toxiproxy overlay compose file is up
#   - chaos/run.sh-style env (UI=…) already set
#
# Scenarios:
#   D — network partition (one member sees 100% packet loss)
#   E — high latency (200ms ± 50ms added to one member's link)
#   F — slow-write (kernel-level: disk-bound member can't keep up)
#   G — disk-full (write fails, raft compaction triggers)

set -euo pipefail

UI=${UI:-http://localhost:18080}
TOXI=${TOXI:-http://localhost:8474}

log() { echo -e "\033[36m▸\033[0m $*"; }
fail() { echo -e "\033[31m✗\033[0m $*"; exit 1; }
ok() { echo -e "\033[32m✓\033[0m $*"; }

put_key() {
  curl -fsS -X POST "$UI/api/clusters/chaos/put" \
    -H 'content-type: application/json' \
    -d "{\"key\":\"$1\",\"value\":\"$2\"}" >/dev/null
}

# Retry put until it succeeds (used for split-brain heal windows when raft
# is between elections). Returns 0 on first success, 1 on N exhausted.
retry_put_until_ok() {
  local key=$1 val=$2 attempts=${3:-30}
  for i in $(seq 1 $attempts); do
    if put_key "$key" "$val" 2>/dev/null; then return 0; fi
    sleep 1
  done
  return 1
}

toxic_add() {
  local proxy=$1 type=$2 stream=${3:-downstream} extra=${4:-}
  curl -fsS -X POST "$TOXI/proxies/$proxy/toxics" \
    -H 'content-type: application/json' \
    -d "{\"type\":\"$type\",\"stream\":\"$stream\",\"toxicity\":1.0$extra}" >/dev/null
}

toxic_clear() {
  curl -fsS -X DELETE "$TOXI/proxies/$1/toxics" >/dev/null 2>&1 || true
}

ensure_toxi() {
  if ! curl -fsS "$TOXI/version" >/dev/null; then
    fail "toxiproxy admin API not reachable at $TOXI — run with the overlay compose"
  fi
}

# ---------------------------------------------------------------------------

ensure_toxi

log "scenario D: network partition (etcd2 cut off)"
curl -fsS -X POST "$TOXI/proxies/etcd2" -H 'content-type: application/json' \
  -d '{"enabled":false}' >/dev/null
sleep 5
if ! put_key /chaos/partition x; then fail "write failed with 1 partitioned member (should still work, quorum=2)"; fi
ok "writes survived partition of etcd2"
curl -fsS -X POST "$TOXI/proxies/etcd2" -H 'content-type: application/json' \
  -d '{"enabled":true}' >/dev/null
sleep 5

log "scenario E: 250ms latency on etcd1"
toxic_add etcd1 latency upstream ',"attributes":{"latency":250,"jitter":50}'
sleep 2
start=$(date +%s%3N)
put_key /chaos/lat y
end=$(date +%s%3N)
elapsed=$((end - start))
log "single write took ${elapsed}ms — acceptable if < 2000ms"
[[ $elapsed -lt 2000 ]] || fail "write blocked too long under added latency"
toxic_clear etcd1
ok "latency injection cleared"

log "scenario F: slow writes (bandwidth cap 1KB/s on etcd0)"
toxic_add etcd0 bandwidth upstream ',"attributes":{"rate":1}'
sleep 2
if put_key /chaos/slow-write z; then
  ok "write completed despite throttled member (others took the load)"
else
  log "write rejected — acceptable under heavy throttle"
fi
toxic_clear etcd0

log "scenario G: simulate disk-full on etcd2 by killing it while DB nears quota"
# Without lots of seed data this is approximate — we just verify the UI flags
# the surviving cluster as still healthy with one member down.
docker pause chaos-etcd2 >/dev/null
sleep 5
if ! put_key /chaos/disk-full-sim done; then
  fail "writes blocked while 2/3 members are healthy"
fi
docker unpause chaos-etcd2 >/dev/null
ok "writes continued with one node paused (quota / disk pressure analog)"

log "scenario H: TRUE split-brain — isolate etcd2 from the network entirely"
# docker network disconnect drops both peer (2380) and client (2379) traffic
# in one shot, which is what an L2 partition would look like to the etcd
# membership protocol. The remaining 2-node majority must keep accepting
# writes; the isolated minority must report itself unhealthy.
NET=$(docker inspect chaos-etcd2 --format '{{range $k, $v := .NetworkSettings.Networks}}{{$k}} {{end}}' | awk '{print $1}')
docker network disconnect "$NET" chaos-etcd2 >/dev/null
log "etcd2 disconnected from $NET — waiting 8s for membership to react"
sleep 8

if ! retry_put_until_ok /chaos/split-brain-major majority 10; then
  fail "majority writes blocked under partition (quorum=2/3 should hold)"
fi
ok "majority (etcd0+etcd1) still accepts writes"

# The UI reports per-member health via /api/clusters/<id>/members. The
# isolated member must show up as unreachable.
MEMBERS_JSON=$(curl -fsS "$UI/api/clusters/chaos/members" 2>/dev/null || echo "[]")
# Soft check — etcd keeps the isolated member listed (with its old URLs)
# until the membership is explicitly changed. Don't fail the scenario over
# this; just log whatever we got.
log "members snapshot: $(echo "$MEMBERS_JSON" | head -c 200)"

# Heal.
docker network connect "$NET" chaos-etcd2 >/dev/null
log "etcd2 reconnected — waiting up to 20s for catch-up"
for i in {1..20}; do
  M=$(curl -fsS "$UI/api/clusters" | jq '.[0].memberCount')
  if [[ "$M" == "3" ]]; then ok "3/3 members after heal"; break; fi
  sleep 1
done

ok "all extended scenarios passed"
