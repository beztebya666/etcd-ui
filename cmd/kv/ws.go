package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/yourorg/etcd-ui/internal/httpx"
	"github.com/yourorg/etcd-ui/internal/models"

	clientv3 "go.etcd.io/etcd/client/v3"
)

// wsWatch is the WebSocket variant of the watch endpoint. Same payload as
// the SSE path so the SPA only needs one decoder. Sends a ping every 15s to
// keep proxies (HAProxy etc.) from idle-killing the upgrade.
func wsWatch(ws *httpx.WSConn, cli *clientv3.Client, r *http.Request) {
	defer ws.Close()

	key := r.URL.Query().Get("prefix")
	opts := []clientv3.OpOption{clientv3.WithPrefix(), clientv3.WithPrevKV()}
	if rev := r.URL.Query().Get("rev"); rev != "" {
		if n, _ := strconv.ParseInt(rev, 10, 64); n > 0 {
			opts = append(opts, clientv3.WithRev(n))
		}
	}

	wctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	ch := cli.Watch(wctx, key, opts...)

	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			if err := ws.Ping(); err != nil {
				return
			}
		case wr, ok := <-ch:
			if !ok {
				return
			}
			if err := wr.Err(); err != nil {
				_ = ws.SendText([]byte(`{"type":"error","error":` + jsonString(err.Error()) + `}`))
				return
			}
			for _, ev := range wr.Events {
				out := models.WatchEvent{
					Type:     ev.Type.String(),
					Key:      string(ev.Kv.Key),
					Value:    string(ev.Kv.Value),
					Revision: ev.Kv.ModRevision,
					Preview:  kvPreview(ev.Kv.Value),
				}
				if ev.PrevKv != nil {
					out.PrevValue = string(ev.PrevKv.Value)
				}
				b, _ := json.Marshal(out)
				if err := ws.SendText(b); err != nil {
					return
				}
			}
		}
	}
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
