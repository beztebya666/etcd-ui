package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	clientv3 "go.etcd.io/etcd/client/v3"
	"github.com/yourorg/etcd-ui/internal/etcdpool"
	"github.com/yourorg/etcd-ui/internal/httpx"
)

// Distributed-lock playground. Models locks as raw etcd primitives so
// behaviour matches what production code with `clientv3/concurrency` sees:
//
//   - A "lock" lives under a user-chosen prefix (e.g. `/locks/leader/`).
//   - Each holder writes a key `<prefix>/<leaseHex>` with a lease that
//     auto-expires after the requested TTL unless renewed.
//   - The smallest CreateRevision under the prefix is the active holder;
//     everything else is a queued waiter. This is exactly the ordering
//     `concurrency.Mutex` uses.
//   - Release = revoke the lease. Crash-safe: a holder whose process
//     vanishes loses the lock when the lease TTL elapses.

// LockEntry is one row in the holder/waiter list.
type LockEntry struct {
	Key            string `json:"key"`
	LeaseID        int64  `json:"leaseId"`
	CreateRevision int64  `json:"createRevision"`
	TTLSeconds     int64  `json:"ttlSeconds"`     // remaining
	GrantedTTL     int64  `json:"grantedTtl"`     // initially requested
	Holder         bool   `json:"holder"`
	Note           string `json:"note,omitempty"`
}

func locksListHandler(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if pool.APIVersion(id) == etcdpool.APIv2 {
			httpx.Err(w, 400, errors.New("locks playground requires etcd v3"))
			return
		}
		cli, _, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		prefix := r.URL.Query().Get("prefix")
		if prefix == "" {
			prefix = "/locks/"
		}
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		resp, err := cli.Get(ctx, prefix, clientv3.WithPrefix(), clientv3.WithSort(clientv3.SortByCreateRevision, clientv3.SortAscend))
		if err != nil {
			httpx.Err(w, 502, err)
			return
		}

		out := make([]LockEntry, 0, len(resp.Kvs))
		for i, kv := range resp.Kvs {
			le := LockEntry{
				Key:            string(kv.Key),
				LeaseID:        kv.Lease,
				CreateRevision: kv.CreateRevision,
				Holder:         i == 0, // smallest CreateRev wins
			}
			if kv.Lease != 0 {
				if info, err := cli.TimeToLive(ctx, clientv3.LeaseID(kv.Lease)); err == nil {
					le.TTLSeconds = info.TTL
					le.GrantedTTL = info.GrantedTTL
				}
			} else {
				le.Note = "no lease — eternal (likely stale)"
			}
			out = append(out, le)
		}
		// Show holder first, then waiters by creation order.
		sort.SliceStable(out, func(i, j int) bool {
			return out[i].CreateRevision < out[j].CreateRevision
		})
		httpx.JSON(w, 200, map[string]any{
			"prefix":  prefix,
			"entries": out,
			"holder":  firstHolder(out),
		})
	}
}

func firstHolder(es []LockEntry) *LockEntry {
	for i := range es {
		if es[i].Holder {
			return &es[i]
		}
	}
	return nil
}

func lockAcquireHandler(pool *etcdpool.Pool) http.HandlerFunc {
	type req struct {
		Prefix     string `json:"prefix"`
		TTLSeconds int64  `json:"ttlSeconds"`
		// HolderTag is an opaque label written into the lock value so
		// you can see WHO acquired it ("alice@laptop", "scheduler-pod-3").
		// Pure informational; etcd's ordering only cares about the key.
		HolderTag string `json:"holderTag"`
	}
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if pool.APIVersion(id) == etcdpool.APIv2 {
			httpx.Err(w, 400, errors.New("locks playground requires etcd v3"))
			return
		}
		cli, _, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		var body req
		if err := httpx.Decode(r, &body); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		if body.Prefix == "" {
			body.Prefix = "/locks/default/"
		}
		if !strings.HasSuffix(body.Prefix, "/") {
			body.Prefix += "/"
		}
		if body.TTLSeconds <= 0 || body.TTLSeconds > 3600 {
			body.TTLSeconds = 60
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()

		// 1. Mint a lease with the requested TTL.
		gr, err := cli.Grant(ctx, body.TTLSeconds)
		if err != nil {
			httpx.Err(w, 502, fmt.Errorf("grant lease: %w", err))
			return
		}

		// 2. Write our entry. The key is unique per leaseID so we can't
		//    collide with concurrent acquirers; ordering is by CreateRev.
		key := body.Prefix + strconv.FormatInt(int64(gr.ID), 16)
		val := body.HolderTag
		if val == "" {
			val = fmt.Sprintf("lease=%x", gr.ID)
		}
		if _, err := cli.Put(ctx, key, val, clientv3.WithLease(gr.ID)); err != nil {
			// Best-effort cleanup if the write fails.
			_, _ = cli.Revoke(context.Background(), gr.ID)
			httpx.Err(w, 502, fmt.Errorf("put: %w", err))
			return
		}

		// 3. Inspect ordering to tell the caller if they got the lock.
		resp, err := cli.Get(ctx, body.Prefix, clientv3.WithPrefix(), clientv3.WithSort(clientv3.SortByCreateRevision, clientv3.SortAscend))
		if err != nil {
			httpx.Err(w, 502, err)
			return
		}
		acquired := false
		var holder string
		var waiters int
		if len(resp.Kvs) > 0 {
			first := resp.Kvs[0]
			acquired = clientv3.LeaseID(first.Lease) == gr.ID
			holder = string(first.Value)
			waiters = len(resp.Kvs) - 1
		}

		httpx.JSON(w, 200, map[string]any{
			"leaseId":    int64(gr.ID),
			"key":        key,
			"acquired":   acquired,
			"holder":     holder,
			"waiters":    waiters,
			"ttlSeconds": body.TTLSeconds,
		})
	}
}

func lockReleaseHandler(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if pool.APIVersion(id) == etcdpool.APIv2 {
			httpx.Err(w, 400, errors.New("locks playground requires etcd v3"))
			return
		}
		cli, _, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		leaseID, _ := strconv.ParseInt(chi.URLParam(r, "leaseId"), 10, 64)
		if leaseID == 0 {
			httpx.Err(w, 400, errors.New("leaseId required"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		defer cancel()
		if _, err := cli.Revoke(ctx, clientv3.LeaseID(leaseID)); err != nil {
			httpx.Err(w, 502, err)
			return
		}
		w.WriteHeader(204)
	}
}
