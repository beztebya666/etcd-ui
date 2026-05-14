package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	clientv3 "go.etcd.io/etcd/client/v3"

	"github.com/yourorg/etcd-ui/internal/etcdpool"
	"github.com/yourorg/etcd-ui/internal/httpx"
	"github.com/yourorg/etcd-ui/internal/k8sdecode"
	"github.com/yourorg/etcd-ui/internal/models"
)

// putK8sHandler closes the loop on the K8s decode pipeline: the SPA
// receives a pretty JSON representation of a kube-apiserver-stored
// object (via k8sdecode.Decode), the user edits it in the Browser
// editor, then this handler re-encodes the edits back into the wire
// format etcd expects (k8s\0 + runtime.Unknown for protobuf, or
// minified JSON for CRDs without a typed shim).
//
// Without this endpoint the structured viewer would be read-only and
// every edit would force the user back to `kubectl edit` — defeating
// the point of decoding the value in the first place.
//
// All writes go through the existing put-cas guard so concurrent edits
// from the SPA + a controller / kubectl don't silently clobber each
// other. baseRev=0 is allowed for new keys (CreateRevision==0 check).
func putK8sHandler(pool *etcdpool.Pool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if pool.APIVersion(id) == etcdpool.APIv2 {
			httpx.Err(w, 400, errors.New("put-k8s requires etcd v3"))
			return
		}
		cli, _, err := pool.Client(id)
		if err != nil {
			httpx.Err(w, 404, err)
			return
		}
		var req models.PutK8sRequest
		if err := httpx.Decode(r, &req); err != nil {
			httpx.Err(w, 400, err)
			return
		}
		if req.Key == "" || req.JSON == "" {
			httpx.Err(w, 400, errors.New("key and json required"))
			return
		}

		// Re-encode according to the format the value originally had.
		var raw []byte
		switch req.Format {
		case "k8s-proto":
			b, err := k8sdecode.EncodeFromJSON(req.APIVersion, req.Kind, req.JSON)
			if err != nil {
				httpx.Err(w, 400, fmt.Errorf("k8s-proto encode: %w", err))
				return
			}
			raw = b
		case "json", "":
			// "" means the SPA didn't specify — assume JSON pass-through.
			// Used for CRDs we don't have Go types for; the value was
			// stored as JSON to begin with.
			b, err := k8sdecode.EncodeJSON(req.JSON)
			if err != nil {
				httpx.Err(w, 400, fmt.Errorf("json encode: %w", err))
				return
			}
			raw = b
		default:
			httpx.Err(w, 400, fmt.Errorf("unknown format %q", req.Format))
			return
		}

		// Guarded write — same compare-and-set check the regular
		// put-cas handler uses. Keeps the K8s-edit flow safe under
		// concurrent kubectl writes.
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()

		var check clientv3.Cmp
		if req.BaseRev == 0 {
			check = clientv3.Compare(clientv3.CreateRevision(req.Key), "=", 0)
		} else {
			check = clientv3.Compare(clientv3.ModRevision(req.Key), "=", req.BaseRev)
		}
		resp, err := cli.Txn(ctx).If(check).Then(clientv3.OpPut(req.Key, string(raw))).Commit()
		if err != nil {
			httpx.Err(w, 502, err)
			return
		}
		if !resp.Succeeded {
			// Concurrent write — caller should re-fetch and re-edit.
			// Surface a clear 409 so the SPA can show a "somebody else
			// wrote to this key; reload?" banner.
			httpx.JSON(w, http.StatusConflict, models.PutK8sResponse{Status: "conflict"})
			return
		}
		httpx.JSON(w, 200, models.PutK8sResponse{
			Status:   "ok",
			Revision: resp.Header.Revision,
		})
	}
}
