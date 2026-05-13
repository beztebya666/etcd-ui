# ADR-0002 — Monaco editor lazy-loaded, not in the critical bundle

- **Status**: Accepted
- **Date**: 2026-05-12
- **Tags**: frontend, performance

## Context

The key editor needs syntax highlighting, JSON validation, and formatting
help — a `<textarea>` doesn't cut it for a power-user tool. Monaco is the
obvious choice (same engine VS Code uses) but it adds ~3 MB minified to
the bundle. Most pageviews in etcd-ui don't touch the editor at all
(Dashboard, Watch, Audit, Metrics, Diff, Heatmap).

## Decision

`@monaco-editor/react` is loaded **lazily** via a `React.lazy()` import
boundary inside `web/src/components/CodeEditor.tsx`. The Suspense
fallback is a `<Skeleton>` so the editor visually fades in.

The Monaco package itself is fetched from the CDN by default
(`@monaco-editor/loader`'s behaviour) — we don't ship Monaco assets in our
own bundle.

## Consequences

- First-paint of the SPA stays under 200 KB gzipped (React + Tailwind +
  framer-motion + Query + zustand + lucide).
- The editor takes ~200–400 ms to swap in the first time a user opens a
  key. We hide this behind the Suspense skeleton.
- Air-gapped deployments need to self-host Monaco. Document this in
  `CONTRIBUTING.md` when we ship the air-gap variant; for now,
  internet-egress is assumed.
