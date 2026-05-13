package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yourorg/etcd-ui/internal/etcdpool"
	"github.com/yourorg/etcd-ui/internal/httpx"
)

// moveLeaderHandler transfers raft leadership to a specified member.
//
// Why this lives outside the etcdctl shell:
//
//   The etcdctl exec endpoint (cmd/ops/etcdctl.go) covers move-leader too,
//   but it's gated behind ETCD_UI_ETCDCTL=on because shelling out to a
//   real CLI carries broader risk surface (file paths, subcommand allow-
//   list, argument injection). MoveLeader by itself is a single, narrow,
//   server-side gRPC call that fails fast and safely if the target isn't
//   healthy or hasn't caught up to the leader's log — etcd refuses the
//   transfer rather than producing split brain. Worth exposing without
//   the etcdctl gate so operators can recover from a degraded leader
//   without operator-level config changes.
//
// Body: {"memberId": <uint64>}
// 200 on success: {"transferredTo": <uint64>}
// 4xx on bad input, 502 on etcd refusal (e.g. target lagging).
func moveLeaderHandler(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		cli, c, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		var body struct {
			MemberID json.RawMessage `json:"memberId"`
		}
		if err := httpx.Decode(r, &body); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		raw := bytes.Trim(body.MemberID, `"`)
		target, perr := strconv.ParseUint(string(raw), 10, 64)
		if perr != nil || target == 0 {
			httpx.Err(w, 400, errString("invalid memberId"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		// MoveLeader is a "leader-only" RPC — it must be sent to the
		// current raft leader, not a follower. The cluster client's
		// gRPC balancer picks endpoints round-robin, so a 3-node cluster
		// has a 2/3 chance of hitting the wrong endpoint. We probe
		// Status on each endpoint to find which one currently reports
		// itself as leader, then issue MoveLeader against that specific
		// endpoint. Failure modes that still propagate verbatim:
		//   - target lagging: "etcdserver: not capable"
		//   - target in single-member quorum: "etcdserver: members not enough"
		//   - target is current leader: "etcdserver: bad leader transferee"
		var leaderEndpoint string
		for _, ep := range c.Endpoints {
			st, sErr := cli.Status(ctx, ep)
			if sErr != nil {
				continue
			}
			if st.Header.MemberId == st.Leader {
				leaderEndpoint = ep
				break
			}
		}
		if leaderEndpoint == "" {
			httpx.Err(w, 502, errString("no leader reachable among endpoints — cluster may be partitioned"))
			return
		}
		// Reconfigure the client to talk to ONLY the leader for this
		// call. SetEndpoints is the documented way to pin a clientv3
		// to a specific node temporarily.
		prevEndpoints := cli.Endpoints()
		cli.SetEndpoints(leaderEndpoint)
		defer cli.SetEndpoints(prevEndpoints...)

		if _, err := cli.MoveLeader(ctx, target); err != nil {
			httpx.Err(w, 502, err)
			return
		}
		httpx.JSON(w, 200, map[string]any{"transferredTo": target})
	}
}

