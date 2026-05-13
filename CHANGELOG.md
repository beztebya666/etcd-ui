# Changelog

All notable changes to **etcd-ui** are documented here. The project follows
[Semantic Versioning](https://semver.org/) and the format of [Keep a
Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added
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
