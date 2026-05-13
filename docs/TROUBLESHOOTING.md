# Troubleshooting

A grep-friendly catalog of things that go wrong and how to fix them. Symptoms
on the left, fixes on the right. If your problem isn't here, open an issue and
we'll add it.

---

## 1. "Cluster shows as unhealthy"

### Symptom
Cluster card has a red dot or "context deadline exceeded".

### Likely cause + fix
| Cause | What to check |
|---|---|
| Wrong endpoint scheme | `ETCD_ENDPOINTS` must include scheme: `http://` or `https://`. A bare `etcd:2379` is rejected. |
| mTLS required, no certs mounted | Bind-mount `/etc/kubernetes/pki/etcd/` and set `ETCD_CA_FILE` / `ETCD_CERT_FILE` / `ETCD_KEY_FILE`. |
| Containerised etcd-ui can't reach host | Run with `--network host`, or expose etcd's client port to the etcd-ui pod's network namespace. |
| Cert SAN doesn't match endpoint | Set `ETCD_UI_TLS_INSECURE_SKIP_VERIFY=1` for **non-production**, or fix the cert. |
| Auth enabled, no creds | Set `ETCD_USERNAME` + `ETCD_PASSWORD` (or via Helm `auth.users[].password`). |

---

## 2. "Metrics page is empty"

### Symptom
`/metrics` page renders the headers and units but nothing else.

### Likely cause + fix
etcd serves `/metrics` on a **separate port** (default `2381`, plain HTTP for
kubeadm). We probe in this order:

1. `ETCD_UI_METRICS_URL_<cluster-id>` (override)
2. `http://<host>:2381/metrics`
3. `https://<host>:2381/metrics`
4. `https://<host>:2379/metrics`
5. `http://<host>:2379/metrics`

If all 5 fail, the page is empty. Fix:

- Confirm `--listen-metrics-urls` on each etcd member.
- Set the override env: `ETCD_UI_METRICS_URL_my-cluster=http://10.0.0.10:2381/metrics,http://10.0.0.11:2381/metrics`.
- For TLS-only metrics, mount certs the same way as the client port.

---

## 3. "Browser shows 5000 KEYS TRUNCATED"

This is intentional — we cap range fetches by default. Options:

- Click **load more** in the top-right pill (doubles the limit up to 2M).
- Use a narrower **prefix** (e.g. `/registry/pods/default/` instead of `/`).
- Filter by **valueRegex** to narrow rows after fetch.

---

## 4. "Bulk-delete didn't show in audit"

We capture prev-values for the first **32** deleted keys per bulk operation
(header-size budget). The rest are deleted but not audit-recoverable.

For audit-critical deletes prefer **single-key delete**, which always captures
the prev-value (up to 4 KB).

---

## 5. "Snapshot download is slow / times out"

Snapshots stream the entire MVCC state. On large clusters this can be hundreds
of MB. We set a **60-second timeout** by default — for clusters > ~5 GB:

- Schedule snapshots server-side instead (see Maintenance → snapshot schedule).
- Run on a member with low traffic: leader has the most up-to-date state but
  also the most contention.

---

## 6. "Tracing doesn't appear in Jaeger / Tempo"

We require an OTLP/HTTP collector. Check:

- `OTEL_EXPORTER_OTLP_ENDPOINT=http://otel-collector:4318` is set (note port).
- Sampler ratio: default `1.0` (sample all). Set `OTEL_TRACES_SAMPLER_ARG=0.1`
  to keep 10%.
- Spans now include `etcd.*` child spans inside handlers — if you see only
  `etcd-ui.gateway` server spans and nothing under them, the kv/ops services
  failed to init their own tracer. Check their logs for `tracing init failed`.

---

## 7. "Audit log grew to 4 GiB"

Compaction runs every hour, capped at:

- `ETCD_UI_AUDIT_MAX_BYTES` — default **256 MiB**
- `ETCD_UI_AUDIT_MAX_AGE` — default **30 days** (`720h`)

If your file is bigger, either the audit service is being restarted before
compaction runs (unlikely — first run is 30s after boot), or you've overridden
the env. Run a manual compaction:

```bash
curl -X POST http://localhost:8080/api/audit/compact
```

---

## 8. "Custom etcd cluster type not discovered"

The k8s discovery has 8 built-in presets. To add a custom one without
recompiling:

```bash
export ETCD_UI_K8S_SOURCES='[{
  "id":"my-app",
  "name":"my-app:cluster",
  "namespace":"prod",
  "labelSelector":"app=my-etcd",
  "containerPort":2379
}]'
```

Or use the file source: `CLUSTERS_FILE=/etc/etcd-ui/clusters.yaml`.

---

## 9. "etcdctl terminal is disabled"

`etcdctl` shell-out is **opt-in** for security. Enable with:

```bash
ETCD_UI_ETCDCTL=on
```

Only commands on the allowlist are executed (`get`, `put`, `del`, `member`,
`endpoint`, `alarm`, `lease`, `auth`, `user`, `role`, `snapshot`, `defrag`,
`compaction`, `move-leader`, `check`, `version`, `help`). Everything else is
rejected with HTTP 400.

---

## 10. "Light mode looks weird"

If you upgraded from an older build that hard-coded dark colours, clear
localStorage (`etcd-ui-state`) and reload. The theme system is driven by CSS
variables on `<html>`; old persisted state could keep `theme: "dark"` even
when you toggle.

---

## 11. "Port 8080 already in use"

Override `ETCD_UI_GATEWAY_ADDR`:

```bash
docker run -e ETCD_UI_GATEWAY_ADDR=:9090 -p 9090:9090 etcd-ui
```

Only the gateway port is exposed — the four internal services bind to
`127.0.0.1` and aren't reachable from outside the container.

---

## 12. "Race detector test fails on `go test -race`"

Open an issue with the `WARNING: DATA RACE` block from the output. Use
`make test-race` locally to reproduce.

---

## Reporting

If none of the above match, please open an issue with:

- `etcd-ui` version (`/healthz` → `version` field)
- etcd version (`etcdctl version` on a member)
- Deployment (k8s + helm, plain docker, kubeadm, Patroni…)
- Last 50 lines of `etcd-ui` logs (set `LOG_LEVEL=debug`)
- A screenshot if it's UI-related
