# Changelog

All notable changes to **etcd-ui** are documented here. The project follows
[Semantic Versioning](https://semver.org/) and the format of [Keep a
Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
- **K8s decode + edit** — `kv` links `k8s.io/api` (519 Kinds across 19 groups). Pods, Services, Deployments, ConfigMaps, etc. round-trip from kube-apiserver protobuf through their generated Go types into pretty JSON. `POST /clusters/{id}/put-k8s` accepts the edited JSON, validates it against the typed schema with `DisallowUnknownFields`, re-marshals to protobuf, wraps in `runtime.Unknown`, writes under CAS guard. See `docs/features/K8S_DECODE.md`. ([cmd/kv/put_k8s.go], [internal/k8sdecode/encode.go])
- **CRDT-lite for KV edits** — `POST /clusters/{id}/put-cas` snapshots `modRevision` at edit-open; if a concurrent write advanced the revision, the server runs a line-level diff3 weave merge (`internal/threewaymerge`). Clean merges go through; conflicts surface in a 3-pane resolver UI. See `docs/features/CRDT.md`.
- **Federation hub** — `ETCD_UI_PEERS` aggregates remote etcd-ui instances into one view; per-peer health + cluster roster; OIDC `client_credentials` for the peer-to-peer hop; `system:peer:<id>` / `peer:<id>/<user>` ACL principals with dedicated `/federation` page.
- **Distributed-lock playground** — `/locks` page mints lease-bound keys under a chosen prefix matching `clientv3/concurrency.Mutex` semantics. See `docs/features/LOCKS.md`.
- **Move-leader from UI** — `POST /clusters/{id}/move-leader` on the Cluster page with confirm modal. Auto-routes to the current leader endpoint (probes `Status` per endpoint, picks the one reporting itself as leader, `SetEndpoints` for the call). Member IDs travel as JSON strings (`idStr`) so JS `Number` precision loss doesn't target the wrong member.
- **Tree truncation hints** — `POST /clusters/{id}/range/counts` returns per-folder key counts at a configurable depth; the SPA marks `▸ pods 8.2k/12.3k` on folders whose loaded subset is smaller than the cluster's actual.
- **Live leader-change timeline** — alerts service persists every observed leader flip to `$DATA_DIR/cluster-events.jsonl`; `/api/alerts/log` returns the history across SPA reloads and pod restarts. Surfaced on the Metrics page with `from → to` member IDs and a "why we can't tell the cause" explainer pointing at the right etcd metrics.
- **etcdutl bundled** — `etcdctl` + offline `etcdutl` both ship in the image. `POST /clusters/{id}/etcdutl/snapshot-status` accepts an uploaded `.db` and returns hash/revision/totalKey/totalSize from real `etcdutl snapshot status`. UI in Maintenance → Validate snapshot.
- **K8s lease enrichment** — `/leases` endpoint Get's each lease's first attached key, decodes it as a `coordination.k8s.io/v1.Lease`, and surfaces holderIdentity + role classification (`kubelet · worker-3`, `kube-scheduler`, namespaced controllers).
- **Protocol badge** — auto-detected `gRPC v3` / `HTTP v2` chip on Dashboard + Cluster pages, with the cluster's reported server version next to it.
- **Resizable left pane** in Browser with rAF-throttled drag handle; collapsible sidebar (full ↔ 56px icons-only) persisted in localStorage.
- **Production-grade autocomplete** ([web/src/components/Autocomplete.tsx]) on prefix/watch inputs: async provider, top-N+Load more, Tab to insert, ↑↓ navigation, hit highlighting.
- **Grafana-style chart hover** — vertical guide + dot + timestamp/value caption on Metrics charts.
- **Clipboard fallback** — `navigator.clipboard.writeText` only works in secure contexts (HTTPS / localhost). Helper falls back to `execCommand('copy')` on HTTP origins so copy buttons stop being silently broken on `http://10.x.x.x:8080`.
- **Prism syntax highlighting** — replaced homebrew regex tokeniser with `prismjs` + a custom dark theme (keys cyan, strings amber, numbers violet, bool/null rose). +6 KB gzipped.
- HTTP Basic + OIDC Bearer auth, signed-cookie sessions, `/api/auth/login | logout | me`.
- Security headers (CSP, X-Frame-Options, Referrer-Policy, Permissions-Policy, optional HSTS).
- Per-cluster read-only enforcement (`ETCD_UI_READONLY_CLUSTERS`, per-cluster `readOnly` in YAML).
- In-memory rate limiter on the gateway (`ETCD_UI_RATE_RPS` / `_BURST`).
- TLS listener on the gateway (`ETCD_UI_TLS_CERT`/`_KEY`).
- pprof endpoints behind auth (`ETCD_UI_PPROF=on`).
- `/metrics` Prometheus exposition for the gateway itself.
- `/readyz` distinct from `/healthz` — only true when every downstream service is ready.
- Cert file hot-reload — etcd-client rotation no longer requires restart.
- Scheduled snapshots (`ETCD_UI_SNAPSHOT_SCHEDULE=cluster:period:retain,…`) with retention.
- Snapshot listing & download endpoints (`/clusters/{id}/snapshots[/{name}]`).
- Audit "undo" — kv delete handler captures previous value and forwards it in the audit event.
- Monaco editor (lazy-loaded) replaces the textarea in the key browser.
- Key-tree virtualization via `@tanstack/react-virtual`.
- Version-diff inside the HistoryDrawer (LCS-based line diff).
- Heatmap page (write activity aggregated by prefix bucket).
- Sparklines on dashboard cluster cards.
- Drag-reorderable pinned clusters (store action wired; UI handle uses native HTML5 DnD).
- Saved searches (per cluster) + shareable URL state (prefix/regex synced to query string).
- A11y: `role`s and `aria-modal` on dialogs, `aria-live` on toaster, skip-to-main link.
- Full RU translation of advanced screens (RBAC, Txn, Diff, Metrics, Heatmap, Audit).
- Vitest coverage for store + diff in addition to existing i18n/export.
- Playwright smoke e2e and CI job.
- Backend integration test gated by `-tags=integration` against `ETCD_ENDPOINTS`.
- Multi-arch container builds (linux/amd64 + linux/arm64), build SHA injected via `-ldflags`.
- SBOM (syft, SPDX-JSON) + grype scan in CI.
- goreleaser config for cross-arch static binaries.
- Helm: `PodDisruptionBudget`, `NetworkPolicy`, `priorityClassName`, `topologySpreadConstraints`,
  startup/readiness/liveness probes split, `/readyz` wired.
- `CONTRIBUTING.md`, `CHANGELOG.md`, ADR-0001 in `docs/adr/`.

### Changed
- Gateway audit emitter is now a 500 ms / 64-event batcher (was per-request).
- ClusterPicker uses a status dot instead of icon swap; outside-click + Esc dismiss.
- Sidebar Tips column is grid-aligned so all `<kbd>` are the same width.
- Dashboard cards show a 60-point Δrev sparkline.

### Fixed
- Trailing-slash routing in chi-mounted upstreams (`middleware.StripSlashes`).
- kv/ops services pre-dial env clusters at startup to avoid first-request 404 race.

## [0.1.0]

- Initial public skeleton: supervisor + 5 microservices, auto-discovery (env / file / DNS-SRV
  / Patroni / Kubernetes with 8 label presets), multi-cluster KV / watch / bulk / txn / export,
  RBAC, audit log, command palette, themes, Helm chart, onboarding tour.
