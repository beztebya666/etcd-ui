package main

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/yourorg/etcd-ui/internal/etcdpool"
	"github.com/yourorg/etcd-ui/internal/httpx"
	"github.com/yourorg/etcd-ui/internal/models"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// restore accepts a JSON dump (the format produced by /api/clusters/{id}/export
// or by bulk-export from the UI) and applies every KV to the target cluster.
//
// The body is the JSON itself, OR a multipart form upload with field "file".
// Query params:
//
//	clearPrefix=/foo   delete everything under /foo before importing
//	dryRun=true        validate but do not apply
func restoreHandler(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		cli, _, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}

		var snap models.RestoreSnapshot
		ct := r.Header.Get("Content-Type")
		if len(ct) >= 19 && ct[:19] == "multipart/form-data" {
			if err := r.ParseMultipartForm(64 << 20); err != nil {
				httpx.Err(w, 400, err)
				return
			}
			f, _, err := r.FormFile("file")
			if err != nil {
				httpx.Err(w, 400, err)
				return
			}
			defer f.Close()
			if err := json.NewDecoder(f).Decode(&snap); err != nil {
				httpx.Err(w, 400, err)
				return
			}
		} else {
			if err := json.NewDecoder(r.Body).Decode(&snap); err != nil {
				httpx.Err(w, 400, err)
				return
			}
		}

		opts := models.RestoreOptions{
			ClearTargetPrefix: r.URL.Query().Get("clearPrefix"),
			DryRun:            r.URL.Query().Get("dryRun") == "true",
		}

		if opts.DryRun {
			httpx.JSON(w, 200, map[string]any{
				"dryRun":      true,
				"wouldApply":  len(snap.KVs),
				"wouldClear":  opts.ClearTargetPrefix,
				"fromCluster": snap.Cluster,
				"exportedAt":  snap.ExportedAt,
			})
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
		defer cancel()

		if opts.ClearTargetPrefix != "" {
			if _, err := cli.Delete(ctx, opts.ClearTargetPrefix, clientv3.WithPrefix()); err != nil {
				httpx.Err(w, 502, err)
				return
			}
		}

		const batch = 128
		applied := 0
		for i := 0; i < len(snap.KVs); i += batch {
			end := i + batch
			if end > len(snap.KVs) {
				end = len(snap.KVs)
			}
			ops := make([]clientv3.Op, 0, end-i)
			for _, kv := range snap.KVs[i:end] {
				ops = append(ops, clientv3.OpPut(kv.Key, kv.Value))
			}
			if _, err := cli.Txn(ctx).Then(ops...).Commit(); err != nil {
				httpx.JSON(w, 502, map[string]any{"applied": applied, "error": err.Error()})
				return
			}
			applied = end
		}

		httpx.JSON(w, 200, map[string]any{
			"applied":     applied,
			"fromCluster": snap.Cluster,
			"exportedAt":  snap.ExportedAt,
		})
	}
}
