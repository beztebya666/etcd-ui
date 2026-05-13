# Distributed-lock playground

`/locks` lets you acquire, queue, and release named locks against any
registered etcd v3 cluster. The page exposes the exact primitive that
`clientv3/concurrency.Mutex` uses internally — lease-bound keys under a
prefix, ordered by CreateRevision — so what you see here matches how a
real production process would behave.

## Mental model

A "lock" is a **prefix**. Each holder writes a key:

```
<prefix>/<leaseID_hex>
```

bound to a lease with the requested TTL. The holder is whoever owns the
key with the **smallest CreateRevision** under that prefix. Everybody
else is a queued waiter.

Releasing = revoking the lease. The key disappears, the next-smallest
CreateRevision wins, the queue shifts forward.

Crash-safe: if the holder vanishes (browser closed, process killed, pod
evicted), the lease expires after its TTL and the key auto-deletes —
identical to what would happen with a real client.

## Workflow

1. **Pick a prefix**. Typically you'd use one path per logical lock —
   `/locks/leader-election/scheduler`, `/locks/migrate/v3-to-v4`, etc.
2. **Optionally tag the holder**. `holderTag` is written into the lock
   value. Pure informational; etcd's ordering only cares about the key.
   Useful for "who is holding it" — `alice@laptop`, `kube-scheduler-2`.
3. **Pick a TTL** (1..3600s, default 60). Acts as both lease lifetime
   and "automatic deadlock recovery" timer.
4. **Acquire**. The server mints a lease, writes the key, then inspects
   the prefix ordering and tells you whether you got the lock or got
   queued (with the current holder + position).
5. **Release** when done — or let the TTL expire.

## HTTP surface

| Verb   | Path                                          | Body                                                    |
| ------ | --------------------------------------------- | ------------------------------------------------------- |
| GET    | `/api/clusters/{id}/locks?prefix=/locks/x/`   | —                                                       |
| POST   | `/api/clusters/{id}/locks/acquire`            | `{prefix, ttlSeconds, holderTag?}`                      |
| DELETE | `/api/clusters/{id}/locks/{leaseId}`          | —                                                       |

`GET` returns `{prefix, holder, entries[]}`. Each entry carries
`{key, leaseId, createRevision, ttlSeconds, grantedTtl, holder}`. The
SPA polls every 1.5s so countdowns are visible.

## Real-code equivalent

```go
sess, _ := concurrency.NewSession(cli, concurrency.WithTTL(60))
mu := concurrency.NewMutex(sess, "/locks/leader-election/")
mu.Lock(ctx)         // blocks until you're the smallest CreateRev
defer mu.Unlock(ctx) // revokes the lease, next waiter wins
```

The playground talks to the same lease + key primitives — the only
difference is that the playground does **not** block on Lock. It writes
the entry, inspects the queue, and returns immediately. The SPA polls
to surface promotion.

## Limits

- v3 only. v2 clusters get a 400 — the v2 API lacks leases.
- TTL clamped to `[1, 3600]` seconds server-side.
- Holders can be force-released by anyone with admin on the cluster —
  this is a debugging UI, not a production access control point. Use
  per-cluster ACL (`__acl__`) to gate who can hit `/api/...locks/...`.
