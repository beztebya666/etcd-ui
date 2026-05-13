# Contributing

Thanks for considering a contribution. This is a small project with strong
conventions — please read this once before submitting changes.

## Dev loop

```bash
make tidy            # go mod tidy
make dev             # backend + Vite dev server
make test            # go test ./... + vitest
make build-image     # docker build
```

Backend on `:8080`, Vite dev on `:5173` (proxies `/api` to the backend).

A 3-node etcd is one command away:

```bash
docker compose up etcd1 etcd2 etcd3
ETCD_ENDPOINTS=http://localhost:2379 make dev
```

## Layout

```
cmd/
  supervisor/   pid 1, supervises the others
  gateway/      :8080 — SPA + REST + audit middleware + auth
  cluster/      :7001 — discovery, pool, member health
  kv/           :7002 — range/put/delete/bulk/txn/watch (SSE)/history/diff
  ops/          :7003 — snapshot, restore, leases, defrag, compact, alarms, RBAC, metrics
  audit/        :7004 — JSONL audit log + ring + SSE tail
internal/
  audit/        ring buffer + persistence
  auth/         basic / OIDC / sessions
  config/       env loader + Cluster struct
  discovery/    env / file / DNS-SRV / Patroni / K8s sources
  etcdpool/     clientv3 pool with cert hot-reload
  httpx/        middleware (security headers, rate limit, metrics, readiness, readonly)
  metrics/      tiny Prometheus text-format parser
  models/       wire types shared across services
  peer/         loopback HTTP cluster registry sync
  persist/      on-disk JSON for UI-added clusters
web/
  src/
    components/ Shell, CodeEditor, VirtualTree, OnboardingTour, …
    pages/      Dashboard, Browser, WatchPage, Txn, RBAC, Audit, Diff, Metrics, …
    lib/        api, i18n, store, export, diff, cn
  e2e/          Playwright specs
deploy/
  helm/         packaged chart
  k8s.yaml      single-file manifest
docs/
  CONNECT.md    how to find etcd endpoints for any system
  adr/          architecture decision records
```

## Conventions

- **Microservices are real.** Each `cmd/` is its own binary; they talk over
  loopback HTTP + JSON. Don't import packages across services — share through
  `internal/`.
- **No new runtime dependencies without justification.** Backend stack is
  chi + zap + etcd + yaml + crypto. Frontend stack is React + React Query +
  zustand + Tailwind + framer-motion + lucide + Monaco (lazy) + react-virtual.
  If you need a new dep, mention it in the PR description.
- **One pattern per problem.** Modals → `confirm()`. Notifications → `toast`.
  Tables → `panel` + `divide-y`. Buttons → `.btn` / `.btn-primary`. Status
  badges → `.tag`.
- **Strings go through `t()`.** Add the English key + Russian translation in
  `web/src/lib/i18n.ts`. Missing keys gracefully fall back to English.
- **Server-side regex must be guarded.** When accepting user-supplied
  regular expressions, `regexp.Compile` and return 400 on failure.
- **Every mutation is audited.** If you add a new write endpoint, make sure
  the gateway routing classifies it (`classify` in `cmd/gateway/main.go`).

## Commit & PR style

- One logical change per PR. Splitting "ship the fix" from "refactor while
  you're in there" is encouraged.
- Conventional commits aren't enforced but headlines should start with a
  scope: `gateway:`, `helm:`, `kv:`, `web/browser:`, etc.
- Update `CHANGELOG.md` under `[Unreleased]` in the same PR.
- For UX changes, attach a before/after screenshot — the project cares about
  pixel-level polish.

## Testing

- Pure-Go logic ships with a `*_test.go` next to it.
- Anything that needs a real etcd lives behind `//go:build integration` and
  reads `ETCD_ENDPOINTS` from env. Don't make these the default path.
- Frontend smoke goes in `web/src/**/*.test.ts` (Vitest, jsdom). UI flows
  go in `web/e2e/*.spec.ts` (Playwright).

## A11y

- Modals: `role="dialog"` / `role="alertdialog"`, `aria-modal="true"`,
  bound `aria-labelledby`. Esc closes. Focus moves into the dialog.
- Live regions: any "after-fact" announcement uses `aria-live="polite"`.
- Keyboard: every interactive element reachable via Tab; `Enter`/`Space`
  activate; arrow keys move within lists/menus.

## Security

- No new dep gets added without a SBOM update path. The CI grype scan must
  stay green at severity `high`.
- Never log secrets. `config.Cluster.Password` is `json:"-"` for that reason —
  persistence has its own `storedCluster` shape that opts back in.
- HTTP responses to authenticated users include `X-Etcd-UI-User`. Don't echo
  it back to other users.

## Releases

Tagging `vX.Y.Z` triggers the CI release job: multi-arch image to
`ghcr.io/yourorg/etcd-ui`, SBOM, vuln scan. Cross-arch tarballs come from
`goreleaser` (`.goreleaser.yaml`).

## Code of conduct

Be excellent to each other. No discrimination, no harassment, no kicking
down. Disagreements are fine; rudeness isn't.
