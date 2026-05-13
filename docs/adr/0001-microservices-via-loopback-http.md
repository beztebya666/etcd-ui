# ADR-0001 — Five microservices over loopback HTTP, packaged as one image

- **Status**: Accepted
- **Date**: 2026-05-12
- **Tags**: architecture, packaging

## Context

etcd-ui has to do a handful of different things — proxy KV ops, watch
streams, run discovery probes, persist audit logs, manage RBAC, generate
snapshots. We wanted three things at once:

1. **Easy ops** — one Docker image, one open port, one Helm chart. Anything
   else is friction for the operator.
2. **Real separation of concerns** — discovery, KV, ops, audit and the
   gateway should not live in the same monolith. A bug in audit must not
   take down KV; a slow snapshot must not block range queries.
3. **A growth path** — if a fleet operator wants to scale audit or KV
   separately tomorrow, they should be able to without a refactor.

## Decision

Ship **five microservice binaries** + a tiny supervisor inside **one
Docker image**:

```
supervisor (pid 1)
├── gateway (:8080)      external + SPA + auth + audit middleware
├── cluster (:7001)      discovery, pool, summaries, member list
├── kv      (:7002)      range, put, delete, bulk, txn, watch SSE
├── ops     (:7003)      snapshot, restore, leases, compact, defrag, RBAC, metrics
└── audit   (:7004)      JSONL log + in-memory ring + SSE live tail
```

Internal RPC is plain HTTP + JSON over `127.0.0.1`. The only externally
exposed port is `:8080`.

Cluster registry is owned by `cluster`; `kv` and `ops` pull it on a 30 s
loop from `/internal/clusters` via the `internal/peer` package.

## Why not a monolith

- We get **fault isolation for free**: if `audit` crashes, the supervisor
  restarts it; KV stays up. With a monolith, a goroutine panic on disk
  full would take down everything.
- **Two-week migration path to k8s pods**: the only artifact that changes
  is the supervisor. The HTTP boundaries already exist.
- **Smaller blast radius for security review**: `gateway` is the only
  component that holds session secrets or speaks auth; `kv` and `ops`
  never see a session cookie.

## Why not gRPC

We considered it. The pros — typed contracts, streaming, less hot-path
`json.Marshal` — are real. The cons that pushed it to "future work":

- It would force a code-gen step in dev loop and CI, complicating the
  contributor onboarding (this project is meant to be hackable).
- Loopback JSON over a `http.Server` is fast enough: gateway routes
  resolve in <50 µs on commodity hardware, far below etcd's own latency.
- gRPC's streaming gives us SSE-equivalent semantics, but the browser
  consumes SSE natively; gRPC-Web needs a converter layer that would itself
  be a microservice.

If at some point we measure JSON-marshal cost dominating a hot path we
revisit. Internal API surface is small enough that swapping is a week of
work, not a rewrite.

## Why not bring k8s sidecars and ConfigMap-driven config

This UI is supposed to run on a laptop, in a single docker container, in
k8s, on bare metal, behind a Tailscale tunnel. Anything that assumes
"we're in k8s" pushes out the long tail of non-k8s users (Talos hosts,
Patroni on bare metal, embedded etcd in Vitess).

## Consequences

- Operators see one image, one port, one set of envs.
- Contributors can spin up the whole stack with `make dev` — five binaries
  under one Makefile target.
- The `peer.Syncer` is now a hard dependency between services. We accept
  the eventual-consistency tax: a freshly added cluster shows up in `kv`
  within ~30 s.
- If we ever hit per-pod scaling needs (say, audit gets very chatty), the
  supervisor pattern lets us "remove" a binary from the image and run it
  in its own pod with no code change.
