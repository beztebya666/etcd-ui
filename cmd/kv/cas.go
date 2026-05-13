package main

import (
	"context"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yourorg/etcd-ui/internal/etcdpool"
	"github.com/yourorg/etcd-ui/internal/httpx"
	"github.com/yourorg/etcd-ui/internal/models"
	"github.com/yourorg/etcd-ui/internal/threewaymerge"
	"github.com/yourorg/etcd-ui/internal/tracing"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// casHandler implements the compare-and-set + 3-way merge save flow. The
// sequence of conditions, in order:
//
//   1. baseRev == 0 (new key)                → straight put
//   2. baseRev matches current modRevision   → straight put, no contention
//   3. current modRevision moved forward     → fetch live value, run merge
//        a. merge.StatusNoChange             → ours == live; 200 with no-op
//        b. merge.StatusClean                → write merged; 200 status=merged
//        c. merge.StatusConflict             → 409 status=conflict; nothing written
//                                              unless AcceptConflicts=true
//
// We use a guarded etcd transaction (cli.Txn ... If ... ModRevision == baseRev)
// so multiple racers can't both think they're authoritative. On txn failure
// we re-fetch and re-attempt the merge, up to 3 times.
func casHandler(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req models.PutCASRequest
		if err := httpx.Decode(r, &req); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		// v2 doesn't have MVCC; fall back to plain set.
		if pool.APIVersion(id) == etcdpool.APIv2 {
			v2PutHandler(w, r, pool, id, models.PutRequest{Key: req.Key, Value: req.Value})
			return
		}
		cli, _, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}

		const maxAttempts = 3
		for attempt := 0; attempt < maxAttempts; attempt++ {
			ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
			done, err := tryCAS(ctx, w, cli, id, &req)
			cancel()
			if err != nil {
				httpx.Err(w, 502, err)
				return
			}
			if done {
				return
			}
		}
		// Three consecutive transactions failed — something is hammering
		// the key. Give up and let the client retry from scratch.
		httpx.Err(w, 503, errString("could not complete compare-and-set after 3 attempts"))
	}
}

// tryCAS runs one attempt. Returns (done, err):
//   done=true  → response already written, caller should stop
//   done=false → txn lost the race, caller should re-attempt
//   err != nil → hard failure, caller should write 502
func tryCAS(ctx context.Context, w http.ResponseWriter, cli *clientv3.Client, id string, req *models.PutCASRequest) (bool, error) {
	ctx, end := tracing.EtcdSpan(ctx, "put-cas", id)
	defer end()

	// Read current value + modRevision under one Get.
	cur, err := cli.Get(ctx, req.Key)
	if err != nil {
		return false, err
	}
	var liveValue string
	var liveRev int64
	if len(cur.Kvs) > 0 {
		liveValue = string(cur.Kvs[0].Value)
		liveRev = cur.Kvs[0].ModRevision
	}

	// Trivial paths: brand-new key, or no contention. Write straight.
	if req.BaseRev == 0 || liveRev == req.BaseRev {
		return cas(ctx, w, cli, req.Key, req.Value, req.BaseRev, "ok")
	}

	// Concurrent write happened. We don't know the base value the client
	// edited against — re-fetch at the historical revision.
	hist, herr := cli.Get(ctx, req.Key, clientv3.WithRev(req.BaseRev))
	var baseValue string
	if herr == nil && len(hist.Kvs) > 0 {
		baseValue = string(hist.Kvs[0].Value)
	}
	// If history was compacted (compaction returned ErrCompacted), fall
	// back to "treat live as base" — degraded but useful: the merge then
	// only happens if ours differs from theirs, which means the user did
	// type something on top of stale data.
	if herr != nil {
		baseValue = liveValue
	}

	result := threewaymerge.Merge(baseValue, req.Value, liveValue)
	switch result.Status {
	case threewaymerge.StatusNoChange:
		httpx.JSON(w, 200, models.PutCASResponse{
			Status:   "ok",
			Revision: liveRev,
		})
		return true, nil

	case threewaymerge.StatusClean:
		// Echo the merged value back so the SPA can refresh the editor
		// — otherwise the user keeps seeing their pre-merge draft and
		// thinks "their" edits silently disappeared, which is exactly
		// the kind of surprise CRDT is supposed to eliminate.
		return casEcho(ctx, w, cli, req.Key, result.Merged, liveRev, "merged", result.Merged)

	case threewaymerge.StatusConflict:
		if req.AcceptConflicts {
			// Caller asked to write the markers verbatim — do it under CAS.
			return casEcho(ctx, w, cli, req.Key, result.Merged, liveRev, "merged", result.Merged)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		httpx.JSON(w, http.StatusConflict, models.PutCASResponse{
			Status:    "conflict",
			Base:      baseValue,
			Theirs:    liveValue,
			Merged:    result.Merged,
			Conflicts: result.Conflicts,
		})
		return true, nil
	}
	return false, errString("unexpected merge status")
}

// cas writes value under the modRevision guard. Returns done=false if the
// guard failed, so the caller can re-attempt.
func cas(ctx context.Context, w http.ResponseWriter, cli *clientv3.Client, key, value string, expectRev int64, statusOnSuccess string) (bool, error) {
	return casEcho(ctx, w, cli, key, value, expectRev, statusOnSuccess, "")
}

// casEcho is cas() with an optional Merged value echoed back to the
// caller — set on the "merged" path so the SPA can refresh the editor
// to reflect what was actually written.
func casEcho(ctx context.Context, w http.ResponseWriter, cli *clientv3.Client, key, value string, expectRev int64, statusOnSuccess, mergedEcho string) (bool, error) {
	var (
		txn   clientv3.Txn
		check clientv3.Cmp
	)
	if expectRev == 0 {
		check = clientv3.Compare(clientv3.CreateRevision(key), "=", 0)
	} else {
		check = clientv3.Compare(clientv3.ModRevision(key), "=", expectRev)
	}
	txn = cli.Txn(ctx).If(check).Then(clientv3.OpPut(key, value))
	resp, err := txn.Commit()
	if err != nil {
		return false, err
	}
	if !resp.Succeeded {
		// Lost the race — let outer loop re-attempt.
		return false, nil
	}
	httpx.JSON(w, 200, models.PutCASResponse{
		Status:   statusOnSuccess,
		Revision: resp.Header.Revision,
		Merged:   mergedEcho,
	})
	return true, nil
}

