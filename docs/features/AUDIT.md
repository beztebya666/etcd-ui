# Audit log

Every write performed through the etcd-ui gateway lands in the audit log. The
goal is post-hoc forensics: who deleted `/registry/services/default/foo` at
14:32?

## What's captured

For each mutation (POST/PUT/DELETE under `/api/clusters/*`):

| Field        | Source                                                 |
|--------------|--------------------------------------------------------|
| `time`       | server time at request receipt (UTC)                   |
| `actor`      | `X-Etcd-UI-User` header (Basic / OIDC / Session)       |
| `method`     | `POST` / `PUT` / `DELETE`                              |
| `cluster`    | parsed from path: `/api/clusters/{id}/…`               |
| `path`       | full request path                                      |
| `action`     | classified: `kv.put`, `kv.delete`, `rbac.*`, `txn`, …  |
| `key`        | the touched key (if applicable)                        |
| `status`     | HTTP response status                                   |
| `ip`         | `X-Forwarded-For` or RemoteAddr                        |
| `userAgent`  | UA header                                              |
| `note`       | `prev=<base64>` for delete events with captured value  |

## Storage

```text
in-memory ring buffer (5000 events, fast tail)
    +
append-only JSONL on disk (/app/data/audit.jsonl)
```

The ring serves the UI's quick `Tail` and `Since` reads; the JSONL is
canonical and survives restarts.

## Retention

Compaction runs **every hour**. It enforces two bounds:

```bash
ETCD_UI_AUDIT_MAX_BYTES=268435456   # 256 MiB
ETCD_UI_AUDIT_MAX_AGE=720h          # 30 days
```

When `audit.jsonl > maxBytes`, the file is rewritten from the in-memory ring
(newest events kept). When events in the ring are older than `maxAge`, they
are dropped from the ring and excluded from the next rewrite.

Manual trigger: `POST /api/audit/compact`. Returns `{bytesBefore, bytesAfter,
dropped}`.

## Live tail

```text
GET /api/audit/events/stream    # Server-Sent Events
```

The Audit UI subscribes on mount, merges the live stream with the initial
`Tail(500)` query, and de-dupes by event ID.

## Download

```text
GET /api/audit/events/download
```

Streams the full `audit.jsonl` as an attachment with a timestamped filename
(`audit-20260513T143200Z.jsonl`). Useful for offline grep, SIEM ingest, or
archival.

## One-click restore

Single-key deletes capture the previous value (capped at 4 KB) into the audit
note as `prev=<base64>`. The Audit UI renders a **Restore** button next to
those events — clicking it issues a `PUT` with the captured value.

For **bulk deletes**, the first 32 deleted keys' prev-values are fanned out as
separate audit events with the same `prev=…` shape, so per-key restore works.
Keys past the 32-entry cap are deleted but not audit-restorable (see
[TROUBLESHOOTING #4](../TROUBLESHOOTING.md)).

## What is NOT audited

- Reads (`GET /range`) — by design. Audit log is for mutations.
- Watch streams (read-only).
- Internal service-to-service calls (peer.Syncer, cluster discovery).
- Metrics scrape (`/metrics`).

## Disabling audit

There's no kill-switch on purpose. To "disable" you'd need to remove the audit
service from the supervisor config — at which point the gateway will log
warnings about dropped events but keep functioning.

## When "actor = anonymous" appears

If auth is disabled (no `AUTH_USERS`, no OIDC), every request reads as
`anonymous`. The Audit UI shows a yellow `auth disabled — actor = anonymous`
chip in this case.
