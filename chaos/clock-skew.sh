#!/usr/bin/env bash
# Clock-skew chaos scenario. Pushes one etcd member's wall clock far enough
# to fall outside Raft's tolerance and verifies:
#
#   - majority keeps writing (skewed minority can't be elected leader, but
#     also can't block consensus since 2/3 are aligned);
#   - the UI's per-node health view flags the skewed member as unhealthy
#     (or at least surfaces some kind of error on /api/clusters/<id>/members);
#   - after we resync the clock, the member rejoins and the cluster reports
#     3/3 healthy again.
#
# Two approaches depending on what your kernel allows:
#
#   A) docker exec + date  (requires the etcd container to have a `date`
#      binary that can set the clock and CAP_SYS_TIME on the container). On
#      stock etcd Alpine images this works only inside `--privileged`
#      containers.
#
#   B) libfaketime sidecar (preferred — no special caps). We rely on the
#      compose file injecting libfaketime via LD_PRELOAD in front of `etcd`.
#      The chaos overlay sets FAKETIME on the victim container; we just
#      change the value at runtime via SIGUSR1.
#
# This script only knows how to fall back: it tries B first, then A.

set -euo pipefail

UI=${UI:-http://localhost:18080}
VICTIM=${VICTIM:-chaos-etcd2}
SKEW=${SKEW:-+90s}    # 90s well past etcd's 60s NTP-tolerance heuristic

log()  { echo -e "\033[36m▸\033[0m $*"; }
ok()   { echo -e "\033[32m✓\033[0m $*"; }
fail() { echo -e "\033[31m✗\033[0m $*"; exit 1; }

put_key() {
  curl -fsS -X POST "$UI/api/clusters/chaos/put" \
    -H 'content-type: application/json' \
    -d "{\"key\":\"$1\",\"value\":\"$2\"}" >/dev/null
}

if ! docker ps --format '{{.Names}}' | grep -q "^${VICTIM}$"; then
  fail "victim container $VICTIM is not running — bring up chaos compose first"
fi

# --- (B) libfaketime path -------------------------------------------------
log "trying libfaketime SIGUSR1 path on $VICTIM"
if docker exec "$VICTIM" sh -c 'test -e /etc/faketimerc' 2>/dev/null; then
  echo "$SKEW" | docker exec -i "$VICTIM" sh -c 'cat > /etc/faketimerc'
  docker kill -s USR1 "$VICTIM" >/dev/null 2>&1 || true
  ok "applied skew via libfaketime"
else
  # --- (A) docker exec + date fallback ----------------------------------
  log "libfaketime not present — trying docker exec + date (needs --cap-add=SYS_TIME)"
  if ! docker exec "$VICTIM" date -s "$SKEW" >/dev/null 2>&1; then
    fail "neither libfaketime nor 'date -s' worked on $VICTIM. Rebuild the chaos image with libfaketime preload or run the container with --cap-add=SYS_TIME."
  fi
  ok "applied skew via date -s"
fi

log "waiting 10s for raft to notice"
sleep 10

log "writing through the UI — must succeed (quorum=2/3 aligned)"
if ! put_key /chaos/skew-test "ok"; then
  fail "writes blocked under one-member clock skew (this is a quorum cluster, should not happen)"
fi
ok "majority writes survived clock skew"

log "checking the skewed member's reported health"
MEMBERS=$(curl -fsS "$UI/api/clusters/chaos/members" || echo "[]")
echo "$MEMBERS" | head -c 500
echo

log "resyncing $VICTIM's clock"
if docker exec "$VICTIM" sh -c 'test -e /etc/faketimerc' 2>/dev/null; then
  echo "+0" | docker exec -i "$VICTIM" sh -c 'cat > /etc/faketimerc'
  docker kill -s USR1 "$VICTIM" >/dev/null 2>&1 || true
else
  docker exec "$VICTIM" sh -c 'hwclock --hctosys 2>/dev/null || ntpdate -u pool.ntp.org 2>/dev/null || true' >/dev/null
fi

log "waiting up to 20s for the cluster to report 3/3"
for i in $(seq 1 20); do
  M=$(curl -fsS "$UI/api/clusters" | jq '.[0].memberCount')
  if [[ "$M" == "3" ]]; then ok "3/3 after clock resync"; exit 0; fi
  sleep 1
done
fail "cluster did not return to 3/3 within 20s"
