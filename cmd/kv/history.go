package main

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yourorg/etcd-ui/internal/etcdpool"
	"github.com/yourorg/etcd-ui/internal/httpx"
	"github.com/yourorg/etcd-ui/internal/models"

	clientv3 "go.etcd.io/etcd/client/v3"
)

func historyHandler(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		key := r.URL.Query().Get("key")
		if key == "" {
			httpx.Err(w, 400, errString("key query param is required"))
			return
		}
		if pool.APIVersion(id) == etcdpool.APIv2 {
			v2NotSupported(w, "key history")
			return
		}
		cli, _, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		// Anchor: current revision.
		head, err := cli.Get(ctx, key)
		if err != nil {
			httpx.Err(w, 502, err)
			return
		}
		currentRev := head.Header.Revision

		// Walk backwards from current ModRevision via CreateRevision/ModRevision.
		// Strategy: for each version, Get at rev=modRev, capture, then jump to
		// modRev-1 and try again. Stop when we either fall before CreateRevision
		// or hit "required revision has been compacted".
		out := models.HistoryResponse{Key: key, CurrentRevision: currentRev}

		var seekRev int64 = currentRev
		max := 200 // soft cap
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, _ := strconv.Atoi(v); n > 0 && n <= 1000 {
				max = n
			}
		}

		for i := 0; i < max; i++ {
			opts := []clientv3.OpOption{}
			if seekRev > 0 {
				opts = append(opts, clientv3.WithRev(seekRev))
			}
			resp, err := cli.Get(ctx, key, opts...)
			if err != nil {
				out.TruncatedAt = err.Error()
				break
			}
			if len(resp.Kvs) == 0 {
				break
			}
			k := resp.Kvs[0]
			out.Versions = append(out.Versions, models.KV{
				Key:            string(k.Key),
				Value:          string(k.Value),
				CreateRevision: k.CreateRevision,
				ModRevision:    k.ModRevision,
				Version:        k.Version,
				Lease:          k.Lease,
				Preview:        kvPreview(k.Value),
			})
			if k.ModRevision <= 1 || k.ModRevision <= k.CreateRevision {
				break
			}
			seekRev = k.ModRevision - 1
		}

		httpx.JSON(w, 200, out)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
