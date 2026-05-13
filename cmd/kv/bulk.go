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
	"github.com/yourorg/etcd-ui/internal/tracing"

	clientv3 "go.etcd.io/etcd/client/v3"
)

func bulkHandlers(pool *etcdpool.Pool) func(chi.Router) {
	return func(r chi.Router) {
		r.Post("/clusters/{id}/bulk/put", bulkPut(pool))
		r.Post("/clusters/{id}/bulk/delete", bulkDelete(pool))
		r.Post("/clusters/{id}/txn", runTxn(pool))
		r.Get("/clusters/{id}/export", export(pool))
	}
}

func bulkPut(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req models.BulkPutRequest
		if err := httpx.Decode(r, &req); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		if pool.APIVersion(id) == etcdpool.APIv2 {
			v2BulkPut(w, r, pool, id, req)
			return
		}
		cli, _, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		if req.Transactional {
			ops := make([]clientv3.Op, 0, len(req.Items))
			for _, it := range req.Items {
				if it.LeaseID != 0 {
					ops = append(ops, clientv3.OpPut(it.Key, it.Value, clientv3.WithLease(clientv3.LeaseID(it.LeaseID))))
				} else {
					ops = append(ops, clientv3.OpPut(it.Key, it.Value))
				}
			}
			resp, err := cli.Txn(ctx).Then(ops...).Commit()
			if err != nil {
				httpx.Err(w, 502, err)
				return
			}
			httpx.JSON(w, 200, models.BulkResult{Applied: len(req.Items), Revision: resp.Header.Revision})
			return
		}

		out := models.BulkResult{}
		for _, it := range req.Items {
			opts := []clientv3.OpOption{}
			if it.LeaseID != 0 {
				opts = append(opts, clientv3.WithLease(clientv3.LeaseID(it.LeaseID)))
			}
			resp, err := cli.Put(ctx, it.Key, it.Value, opts...)
			if err != nil {
				out.Failed++
				out.Error = err.Error()
				continue
			}
			out.Applied++
			out.Revision = resp.Header.Revision
		}
		httpx.JSON(w, 200, out)
	}
}

func bulkDelete(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req models.BulkDeleteRequest
		if err := httpx.Decode(r, &req); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		if pool.APIVersion(id) == etcdpool.APIv2 {
			v2BulkDelete(w, r, pool, id, req)
			return
		}
		cli, _, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()

		ops := make([]clientv3.Op, 0, len(req.Keys)+len(req.Prefixes))
		for _, k := range req.Keys {
			ops = append(ops, clientv3.OpDelete(k, clientv3.WithPrevKV()))
		}
		for _, p := range req.Prefixes {
			ops = append(ops, clientv3.OpDelete(p, clientv3.WithPrefix(), clientv3.WithPrevKV()))
		}
		ctx, endSpan := tracing.EtcdSpan(ctx, "bulk-delete", id)
		resp, err := cli.Txn(ctx).Then(ops...).Commit()
		endSpan(err)
		if err != nil {
			httpx.Err(w, 502, err)
			return
		}
		var deleted int64
		// Audit-undo: capture prev-values for the first N deleted keys. Header
		// budget is bounded so a 1M-key wipe doesn't try to round-trip 4 GiB
		// through gateway. Anything beyond the cap is silently dropped from
		// audit (the delete itself still went through).
		const maxBulkPrev = 32
		const maxValueBytes = 4096
		type prevKV struct {
			Key string `json:"k"`
			Val string `json:"v"` // base64
		}
		prevs := make([]prevKV, 0, maxBulkPrev)
		for _, r := range resp.Responses {
			dr := r.GetResponseDeleteRange()
			if dr == nil {
				continue
			}
			deleted += dr.Deleted
			for _, kv := range dr.PrevKvs {
				if len(prevs) >= maxBulkPrev {
					break
				}
				v := kv.Value
				if len(v) > maxValueBytes {
					v = v[:maxValueBytes]
				}
				prevs = append(prevs, prevKV{
					Key: string(kv.Key),
					Val: base64Std(string(v)),
				})
			}
		}
		if len(prevs) > 0 {
			if b, err := json.Marshal(prevs); err == nil {
				w.Header().Set("X-Etcd-UI-Bulk-Prev", base64Std(string(b)))
			}
		}
		httpx.JSON(w, 200, map[string]any{
			"deleted":  deleted,
			"revision": resp.Header.Revision,
		})
	}
}

func runTxn(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		var req models.TxnRequest
		if err := httpx.Decode(r, &req); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		if pool.APIVersion(id) == etcdpool.APIv2 {
			v2NotSupported(w, "transactions")
			return
		}
		cli, _, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}

		cmps, err := buildCompares(req.Conditions)
		if err != nil {
			httpx.Err(w, 400, err)
			return
		}
		success, err := buildOps(req.OnSuccess)
		if err != nil {
			httpx.Err(w, 400, err)
			return
		}
		failure, err := buildOps(req.OnFailure)
		if err != nil {
			httpx.Err(w, 400, err)
			return
		}

		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		txn := cli.Txn(ctx).If(cmps...).Then(success...).Else(failure...)
		resp, err := txn.Commit()
		if err != nil {
			httpx.Err(w, 502, err)
			return
		}
		httpx.JSON(w, 200, models.TxnResponse{
			Succeeded: resp.Succeeded,
			Revision:  resp.Header.Revision,
		})
	}
}

// export streams every KV the cluster has as a JSON file. Used for both UI
// downloads and as the canonical input format for /restore.
func export(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if pool.APIVersion(id) == etcdpool.APIv2 {
			// We need cluster.Name for the file name; cheap lookup via List.
			for _, c := range pool.List() {
				if c.ID == id {
					v2Export(w, r, pool, id, c.Name)
					return
				}
			}
			v2Export(w, r, pool, id, id)
			return
		}
		cli, c, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()

		resp, err := cli.Get(ctx, "\x00", clientv3.WithFromKey())
		if err != nil {
			httpx.Err(w, 502, err)
			return
		}
		out := models.RestoreSnapshot{
			Cluster:    c.Name,
			ExportedAt: time.Now().UTC().Format(time.RFC3339),
			Revision:   resp.Header.Revision,
			KVs:        make([]models.KV, 0, len(resp.Kvs)),
		}
		for _, k := range resp.Kvs {
			out.KVs = append(out.KVs, models.KV{
				Key:            string(k.Key),
				Value:          string(k.Value),
				CreateRevision: k.CreateRevision,
				ModRevision:    k.ModRevision,
				Version:        k.Version,
				Lease:          k.Lease,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Disposition",
			`attachment; filename="etcd-`+c.Name+`-`+time.Now().UTC().Format("20060102T150405Z")+`.json"`)
		httpx.JSON(w, 200, out)
	}
}
