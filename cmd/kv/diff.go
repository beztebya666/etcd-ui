package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yourorg/etcd-ui/internal/etcdpool"
	"github.com/yourorg/etcd-ui/internal/httpx"
	"github.com/yourorg/etcd-ui/internal/models"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// diffHandler returns the set difference between two clusters under a given
// prefix. Three kinds of entries are reported:
//
//	only-left   — present in left, absent in right
//	only-right  — present in right, absent in left
//	different   — present in both but with different values
//
// Equal keys are omitted from the response to keep payloads small.
func diffHandler(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		left := chi.URLParam(r, "id")
		right := r.URL.Query().Get("against")
		prefix := r.URL.Query().Get("prefix")
		if right == "" {
			httpx.Err(w, 400, errString("against query param is required"))
			return
		}
		if pool.APIVersion(left) == etcdpool.APIv2 || pool.APIVersion(right) == etcdpool.APIv2 {
			v2NotSupported(w, "cluster diff")
			return
		}

		leftCli, _, err := pool.Client(left)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		rightCli, _, err := pool.Client(right)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		leftKVs, err := rangeAll(ctx, leftCli, prefix)
		if err != nil {
			httpx.Err(w, 502, err)
			return
		}
		rightKVs, err := rangeAll(ctx, rightCli, prefix)
		if err != nil {
			httpx.Err(w, 502, err)
			return
		}

		seen := map[string]string{}
		for k, v := range leftKVs {
			if rv, ok := rightKVs[k]; ok {
				if rv != v {
					seen[k] = "different"
				}
			} else {
				seen[k] = "only-left"
			}
		}
		for k := range rightKVs {
			if _, ok := leftKVs[k]; !ok {
				seen[k] = "only-right"
			}
		}

		out := models.DiffResponse{Left: left, Right: right}
		for k, kind := range seen {
			d := models.DiffEntry{Key: k, Kind: kind}
			if v, ok := leftKVs[k]; ok {
				d.Left = v
			}
			if v, ok := rightKVs[k]; ok {
				d.Right = v
			}
			out.Diffs = append(out.Diffs, d)
		}
		out.Total = len(out.Diffs)
		httpx.JSON(w, 200, out)
	}
}

func rangeAll(ctx context.Context, cli *clientv3.Client, prefix string) (map[string]string, error) {
	key := prefix
	opts := []clientv3.OpOption{}
	if prefix == "" {
		key = "\x00"
		opts = append(opts, clientv3.WithFromKey())
	} else if !strings.HasSuffix(prefix, "*") {
		opts = append(opts, clientv3.WithPrefix())
	}
	resp, err := cli.Get(ctx, key, opts...)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(resp.Kvs))
	for _, kv := range resp.Kvs {
		out[string(kv.Key)] = string(kv.Value)
	}
	return out, nil
}
