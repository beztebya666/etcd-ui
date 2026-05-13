# Heatmap — where is your cluster being written?

Aggregates live `PUT` + `DELETE` events by **prefix bucket** and draws an
intensity-coloured bar per bucket. Updated in real time as events arrive over
the watch SSE stream.

Answers questions like:

- Which app dominates writes right now? (`/registry/pods/` vs `/registry/services/`)
- Is the controller-manager flapping a lease? (look for a hot `/registry/leases/`)
- After enabling X, did write volume actually go up under `/foo/`?

## How it works

```text
SSE: GET /api/clusters/:id/watch?prefix=/
       │
       ▼  every PUT/DELETE
counts[bucket(key, depth)] += 1
       │
       ▼ every animation frame
render bars sorted by count desc
```

The `bucket` function splits the key by `/`, takes the first `depth` segments,
and joins them back: `/registry/pods/default/foo` at depth=2 becomes
`/registry/pods`.

## Controls

| Control       | Effect                                                   |
|---------------|----------------------------------------------------------|
| `prefix`      | What range to watch. Empty = entire keyspace.            |
| `bucket depth`| How many path segments to group by (1–6, default 2).     |
| Pause/Resume  | Stop incrementing counters without losing the buffer.    |
| Reset         | Zero all counts and start fresh.                         |
| Drag handle   | Resize the key column (160–960px, ←/→ when focused).     |

## Read events?

**No.** The etcd watch protocol does not emit reads. Heatmap is a
*write*-heatmap — there's no way to observe `cli.Get` from outside etcd
without server-side instrumentation we don't have.

For read-pressure intuition use **Metrics** → `etcd_server_proposals_*` rate
and `etcd_disk_backend_commit_duration` quantiles.

## Caveats

- Counts reset on every page reload (in-memory only — no persistence).
- The watch stream can be **rate-limited** by an upstream proxy that doesn't
  understand SSE keep-alives. If counts stop updating but the cluster is
  active, check your reverse proxy's `proxy_buffering` / `X-Accel-Buffering`.
- Very deep keys (e.g. `/registry/leases/kube-system/node-leader-…`) at
  `depth=1` will collapse into a single `/registry` bar that dominates the
  chart. Bump depth to 3+ to see the real breakdown.

## Future work

- Per-bucket sparkline (last 60 seconds), not just absolute count.
- Click-through to filtered **Watch** page for that bucket.
- Snapshot the heatmap state to a `localStorage` ring so refreshes don't
  erase 30 minutes of observation.
