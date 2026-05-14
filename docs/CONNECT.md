# Connecting etcd-ui to anything that uses etcd

etcd is "just" a key-value store, so dozens of systems embed or sit on top of it. This document walks through **how to find your etcd endpoints, ports and credentials** for each one — then you paste them into the Add-cluster wizard (or use auto-discovery).

Skip to your system:

- [Universal: what etcd-ui actually needs](#universal-what-etcd-ui-actually-needs)
- [Kubernetes — stacked etcd (kubeadm / kops / kubespray)](#kubernetes--stacked-etcd-kubeadm--kops--kubespray)
- [Kubernetes — external etcd](#kubernetes--external-etcd)
- [Kubernetes — managed (EKS / GKE / AKS / DOKS)](#kubernetes--managed-eks--gke--aks--doks)
- [Patroni (PostgreSQL HA)](#patroni-postgresql-ha)
- [Vitess (TopoServer)](#vitess-toposerver)
- [HashiCorp Vault (etcd backend)](#hashicorp-vault-etcd-backend)
- [Apache APISIX](#apache-apisix)
- [Cilium (KV store)](#cilium-kv-store)
- [KubeEdge](#kubeedge)
- [Karmada](#karmada)
- [Talos Linux](#talos-linux)
- [OpenStack (tooz / DLM)](#openstack-tooz--dlm)
- [M3DB](#m3db)
- [CoreDNS (etcd plugin)](#coredns-etcd-plugin)
- [SkyDNS / Calico / others](#skydns--calico--others)
- [Generic discovery patterns](#generic-discovery-patterns)
- [Auto-discovery setup reference](#auto-discovery-setup-reference)

---

## Universal: what etcd-ui actually needs

For **any** cluster, etcd-ui needs:

| Field            | Required | Example                                            |
| ---------------- | -------- | -------------------------------------------------- |
| **Endpoints**    | yes      | `http://10.0.0.1:2379, http://10.0.0.2:2379`       |
| **Scheme**       | yes      | `http` or `https` (mTLS)                           |
| **CA cert**      | mTLS     | `/certs/ca.crt`                                    |
| **Client cert**  | mTLS     | `/certs/client.crt`                                |
| **Client key**   | mTLS     | `/certs/client.key`                                |
| **Username/pwd** | basic-auth | `admin / s3cret`                                 |

etcd-ui auto-detects whether the cluster speaks **gRPC (etcd v3)** or **HTTP/REST (etcd v2)** by probing `/version`; the active protocol shows up as a `gRPC v3` or `HTTP v2` badge on Dashboard and Cluster pages. The badge also surfaces the reported server version (e.g. `3.5.15`). v2-only clusters get a degraded experience — bulk-import, txn, watch fragments, snapshot, lock playground and 3-way merge all require v3.

**For Kubernetes etcd clusters** specifically, the `kv` service links `k8s.io/api` (519 Kinds across 19 groups: core/apps/batch/networking/rbac/storage/policy/coordination/discovery/autoscaling/admissionregistration/apiserverinternal/authentication/authorization/certificates/events/flowcontrol/node/scheduling, every versioned subgroup). Pod, Service, Deployment, ConfigMap, Secret, Job, CronJob, Lease, Endpoints, NetworkPolicy and friends round-trip from kube-apiserver's protobuf storage through their generated Go structs into clean structured JSON. **Edits round-trip the other way** via `POST /api/clusters/{id}/put-k8s`: the SPA's `Edit` toggle in the decoded view sends the modified JSON, server validates it against the typed schema (`DisallowUnknownFields` — typos in field names are caught here, not on etcd write), re-marshals to protobuf, wraps in `runtime.Unknown`, writes under a CAS guard. See [`docs/features/K8S_DECODE.md`](features/K8S_DECODE.md) for the full pipeline.

CRDs stored as JSON (no compiled-in Go type) are pretty-printed and edit-able verbatim. CRDs stored as proto via a typed shim we don't link (rare — Prometheus Operator / Istio / Argo CD with their own gen'd protos) fall back to metadata-only preview with a `Raw protobuf` toggle in the viewer; to edit them in-place, switch to the raw view, patch the bytes, save through the standard `/put` endpoint, or use `kubectl edit` until we add the relevant CRD modules to `go.mod`.

If your cluster's K8s version is newer than the `k8s.io/api` module pinned in [`go.mod`](../go.mod), an edit that adds a field new to that version will fail validation. To fix: bump `k8s.io/api` to match your control-plane minor and rebuild the image.

Three ways to give them to etcd-ui:

1. **Env at container start** (one cluster):
   ```bash
   docker run ... -e ETCD_ENDPOINTS=http://e1:2379,http://e2:2379 etcd-ui
   ```
2. **YAML config file** (many clusters, hot-reloaded):
   ```bash
   docker run ... -e CLUSTERS_FILE=/etc/etcd-ui/clusters.yaml \
     -v /etc/etcd-ui:/etc/etcd-ui:ro etcd-ui
   ```
   See [`clusters.yaml` schema](#auto-discovery-setup-reference).
3. **UI wizard** — Settings → Add cluster… → pick a preset → paste endpoints.

---

## Kubernetes — stacked etcd (kubeadm / kops / kubespray)

The control-plane etcd lives in `kube-system`, runs as a static pod, listens on **port 2379 with mTLS only**.

**Find endpoints** (run on any control-plane node):

```bash
# pod IPs
kubectl -n kube-system get pods -l component=etcd \
  -o jsonpath='{.items[*].status.podIP}'

# or look at the etcd container args (--advertise-client-urls)
kubectl -n kube-system get pod etcd-<node> -o yaml | grep advertise-client
```

**Find certs** (on the control-plane node):

```bash
ls /etc/kubernetes/pki/etcd/
# ca.crt
# server.crt, server.key
# peer.crt, peer.key
# healthcheck-client.crt, healthcheck-client.key  ← use these
```

**Easiest setup**: deploy etcd-ui inside the same cluster and let auto-discovery do the work:

```bash
helm install etcd-ui ./deploy/helm/etcd-ui \
  -n kube-system \
  --set discovery.kubernetes=true \
  --set certs.secretName=etcd-client \
  --set certs.mountPath=/certs
```

Where `etcd-client` is a Secret containing `ca.crt / client.crt / client.key`. The Helm chart sets the right env vars (`ETCD_CA_FILE` etc.).

**Sanity check from a master node**:

```bash
ETCDCTL_API=3 etcdctl --endpoints=https://127.0.0.1:2379 \
  --cacert=/etc/kubernetes/pki/etcd/ca.crt \
  --cert=/etc/kubernetes/pki/etcd/healthcheck-client.crt \
  --key=/etc/kubernetes/pki/etcd/healthcheck-client.key \
  endpoint status
```

---

## Kubernetes — external etcd

Your cluster was bootstrapped with `--external-etcd-servers=...`. Find them in `/etc/kubernetes/manifests/kube-apiserver.yaml`:

```bash
grep -E 'etcd-servers|etcd-cafile|etcd-certfile|etcd-keyfile' \
  /etc/kubernetes/manifests/kube-apiserver.yaml
```

Use these endpoints + cert paths directly.

---

## Kubernetes — managed (EKS / GKE / AKS / DOKS)

**You cannot reach etcd.** The cloud provider firewalls it. There is no API exposed for it. etcd-ui won't help you here — use the cloud console.

The only exception: you brought-your-own etcd as the cluster's datastore (rare). In that case treat it like "external etcd" above.

---

## Patroni (PostgreSQL HA)

Patroni uses a DCS — the most common is **etcd**. Two ways to discover:

**A. Patroni REST API (auto-discovery in etcd-ui):**

```bash
docker run ... -e PATRONI_URLS=http://pg-1:8008,http://pg-2:8008 etcd-ui
```

etcd-ui calls `GET /cluster` on the Patroni REST endpoint, learns the scope and member hosts, and assumes etcd lives at `:2379` on each host. Override the port via `PATRONI_ETCD_PORT=...`.

**B. Read `/etc/patroni.yml` manually:**

```yaml
etcd:
  hosts: pg-1:2379, pg-2:2379, pg-3:2379
```

Paste those into the UI.

---

## Vitess (TopoServer)

Vitess uses etcd as the topology server. Find endpoints from any `vtctld` or `vtgate` pod:

```bash
kubectl get pod -l app=vtctld -o yaml | grep -E 'topo_global_server_address'
# --topo_global_server_address=etcd-global-1:2379,etcd-global-2:2379

# Or if you used the planetscale operator:
kubectl get pods -l planetscale.com/component=etcd -A
```

In Kubernetes, etcd-ui's preset `planetscale.com/component=etcd` will find these automatically.

---

## HashiCorp Vault (etcd backend)

**Only when** Vault is configured with `storage "etcd"` (legacy — most clusters now use Raft).

Check `vault.hcl`:

```hcl
storage "etcd" {
  address  = "https://etcd-1.vault:2379,https://etcd-2.vault:2379"
  path     = "vault/"
  ha_enabled = "true"
}
```

Use that `address` value. If Vault's etcd has TLS, the same certs Vault uses (`tls_cert_file`/`tls_key_file`/`tls_ca_file` from the storage block) work for etcd-ui.

---

## Apache APISIX

APISIX persists config in etcd. Find endpoints in `/usr/local/apisix/conf/config.yaml`:

```yaml
deployment:
  role: traditional
  role_traditional:
    config_provider: etcd
  etcd:
    host:
      - "http://etcd-headless:2379"
    prefix: "/apisix"
    timeout: 30
```

Or in Kubernetes, etcd is usually deployed alongside under the Helm release name:

```bash
kubectl -n apisix get svc -l app.kubernetes.io/name=etcd
```

The auto-discovery preset `app.kubernetes.io/part-of=apisix` will catch operator-style installs.

---

## Cilium (KV store)

Cilium supports etcd as its KV store, especially with the `cilium-etcd-operator`. Find it:

```bash
# Operator-deployed etcd
kubectl -n kube-system get pods -l io.cilium/app=etcd-operator

# Endpoint info
kubectl -n kube-system get cm cilium-config -o jsonpath='{.data.kvstore-opt}'
# typically: '{"etcd.config": "/var/lib/etcd-config/etcd.config"}'

# Then check the referenced Secret/ConfigMap for the actual endpoints.
```

---

## KubeEdge

KubeEdge's CloudCore can use a dedicated etcd:

```bash
kubectl get pods -A -l k8s-app=kubeedge-etcd
# or check the cloudcore deployment env:
kubectl -n kubeedge get deploy cloudcore -o yaml | grep -A2 etcd
```

---

## Karmada

Karmada runs its own etcd in `karmada-system`:

```bash
kubectl -n karmada-system get pods -l app=etcd
kubectl -n karmada-system get pod etcd-0 -o yaml | grep advertise-client
```

Certs come from the `karmada-cert` Secret.

---

## Talos Linux

Talos runs etcd for the K8s control plane. You don't typically expose etcd's client port outside the node, so the easiest path is:

1. Run etcd-ui **inside** the Talos cluster as a Pod; auto-discovery handles it.
2. Or use the Talos CLI to interact (read-only):
   ```bash
   talosctl -n <node> etcd members
   talosctl -n <node> etcd status
   talosctl -n <node> etcd snapshot /tmp/etcd.db
   ```
3. To reach etcd directly: `talosctl -n <node> service etcd config` shows the listen URLs but they're bound to the host network; you'd need a `talosctl forward` plus the Talos PKI.

---

## OpenStack (tooz / DLM)

If OpenStack is configured with the etcd driver for `tooz` (the cross-service coordination layer), endpoints are in each service's config under `[coordination]`:

```ini
# /etc/cinder/cinder.conf
[coordination]
backend_url = etcd3+https://etcd-1.openstack:2379,etcd-2.openstack:2379
```

---

## M3DB

M3DB stores cluster placement and KV state in etcd:

```yaml
# m3coordinator config
clusters:
  - etcdClusters:
      - zone: embedded
        endpoints:
          - http://etcd-0.m3db:2379
          - http://etcd-1.m3db:2379
```

---

## CoreDNS (etcd plugin)

CoreDNS's `etcd` plugin reads zone data from etcd. Endpoints are in the Corefile:

```
example.com {
    etcd example.com {
        endpoint http://etcd-1:2379 http://etcd-2:2379
        path /skydns
    }
}
```

---

## SkyDNS / Calico / others

- **SkyDNS** — endpoints in `/etc/skydns/skydns.conf` (`machines`).
- **Calico (etcdv3 datastore)** — `calicoctl get datastore` or env `ETCD_ENDPOINTS` of the `calico-node` DaemonSet.
- **Rook** — uses Kubernetes API, not etcd directly.
- **Trident (NetApp)** — uses K8s CRDs.

When in doubt: `kubectl get pods -A | grep etcd` is your friend.

---

## Generic discovery patterns

If you don't see your system above, one of these will work:

### A. DNS SRV records

Many production clusters publish `_etcd-client._tcp.example.com` SRV records:

```bash
dig +short SRV _etcd-client._tcp.example.com
```

Tell etcd-ui to resolve them:

```bash
docker run ... \
  -e ETCD_UI_DNS_SRV="prod=Production=_etcd-client._tcp.prod.example.com,https" \
  etcd-ui
```

Format: `id=display-name=record,scheme`. Multiple entries separated by `;`.

### B. Kubernetes pod label selector

Tell etcd-ui to scan for arbitrary labels:

```bash
docker run ... \
  -e ETCD_UI_K8S_SOURCES='[
    {"id":"my-etcd","name":"my-etcd","namespace":"prod","labelSelector":"app=my-etcd","port":2379,"scheme":"https"}
  ]' \
  etcd-ui
```

The wizard ships with presets for control-plane, Vitess, Cilium, KubeEdge, Karmada, APISIX, M3DB and generic `app=etcd`. Add your own via the env above.

### C. YAML config file

For multi-cluster setups, a single file is easiest:

```yaml
# /etc/etcd-ui/clusters.yaml
clusters:
  - id: vitess-prod
    name: Vitess (prod)
    endpoints: ["http://vt-1:2379","http://vt-2:2379"]

  - id: patroni-pg-eu
    name: Patroni EU
    endpoints: ["http://pg-eu-1:2379","http://pg-eu-2:2379"]

  - id: control-plane
    name: Kubernetes
    endpoints: ["https://10.0.0.10:2379"]
    caFile:   /certs/k8s/ca.crt
    certFile: /certs/k8s/client.crt
    keyFile:  /certs/k8s/client.key
```

```bash
docker run ... -v /etc/etcd-ui:/etc/etcd-ui:ro -e CLUSTERS_FILE=/etc/etcd-ui/clusters.yaml etcd-ui
```

The file is re-read every 30 s — edits propagate without a restart.

### D. Manual via UI

Settings → Add cluster… → pick "Custom (manual)" → paste endpoints.

---

## Auto-discovery setup reference

### Discovery
| Env var                       | What it does                                                              |
| ----------------------------- | ------------------------------------------------------------------------- |
| `ETCD_ENDPOINTS`              | One static cluster (also: `ETCD_USERNAME`, `ETCD_PASSWORD`, `ETCD_CA_FILE`, `ETCD_CERT_FILE`, `ETCD_KEY_FILE`, `ETCD_UI_CLUSTER_NAME`, `ETCD_READONLY`) |
| `CLUSTERS_FILE`               | YAML with many clusters, hot-reloaded every 30 s                          |
| `ETCD_UI_DNS_SRV`             | One or more SRV records (`id=name=_etcd-client._tcp.x,scheme;…`)          |
| `PATRONI_URLS`                | Comma-separated Patroni REST URLs                                         |
| `ETCD_UI_K8S_DISCOVERY`       | `auto` (default) / `off`                                                  |
| `ETCD_UI_K8S_SOURCES`         | JSON array of pod-selector specs (see above)                              |
| `ETCD_UI_K8S_ENDPOINTS`       | Explicit endpoints — short-circuits k8s discovery                         |

### Access control & auth

| Env var                              | What it does                                                                                                                                          |
| ------------------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| `AUTH_USERS`                         | Basic-auth users: `alice:plain:secret,bob:bcrypt:$2a$10$…`                                                                                            |
| `AUTH_SESSION_SECRET`                | HMAC secret for signed session cookies (keep stable across pods)                                                                                      |
| `ETCD_UI_SESSION_TTL_SECONDS`        | Idle session lifetime (default 3600)                                                                                                                  |
| `ETCD_UI_OIDC_ISSUER`                | OIDC issuer base URL (Keycloak / Okta / Dex / Auth0). Enables Bearer-token verification.                                                              |
| `ETCD_UI_OIDC_AUDIENCE`              | Expected `aud` claim                                                                                                                                  |
| `ETCD_UI_OIDC_USERNAME_CLAIM`        | Claim to read as username (default `email` → `sub`)                                                                                                   |
| `ETCD_UI_OIDC_CLIENT_ID/_SECRET`     | Enables the full PKCE login flow (in addition to Bearer verification)                                                                                 |
| `ETCD_UI_OIDC_REDIRECT_URL`          | Where the IdP bounces back (e.g. `https://etcd-ui.example.com/api/auth/oidc/callback`)                                                                |
| `ETCD_UI_OIDC_SCOPES`                | Default includes `offline_access` so the IdP returns refresh tokens. Override only if your IdP rejects the scope.                                      |
| `ETCD_UI_READONLY_CLUSTERS`          | Comma-separated cluster IDs that kv/ops refuse to mutate (cluster-wide read-only)                                                                     |
| `ETCD_UI_ACL` / `ETCD_UI_ACL_FILE`   | Per-cluster-per-user RBAC (JSON inline or file). File is hot-reloaded — inotify on Linux, 5s mtime polling elsewhere. See [`docs/PERMISSIONS.md`](PERMISSIONS.md). |
| `ETCD_UI_ACL_BOOTSTRAP_ADMIN`        | Idempotently grant this user `__acl__` admin on startup (lets them edit the ACL through the Permissions page) when no such rule exists yet.           |
| `ETCD_UI_PEER_CLIENT_IDS`            | When this etcd-ui acts as a *peer* in federation: incoming OIDC tokens with matching `azp`/`client_id` are tagged as `system:peer:<id>` for ACL.       |

### Operations

| Env var                            | What it does                                                                                                                                          |
| ---------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| `ETCD_UI_SNAPSHOT_SCHEDULE`        | `<cluster>:<period>:<retain>[,…]`. Period: `hourly\|daily\|weekly\|<Go-duration>`.                                                                    |
| `ETCD_UI_SNAPSHOT_S3_*`            | Offload each snapshot to S3-compatible storage (AWS / MinIO / GCS / R2 / Wasabi). See [`docs/features/SNAPSHOTS.md`](features/SNAPSHOTS.md).          |
| `ETCD_UI_ETCDCTL`                  | `on` to enable the in-browser etcdctl terminal (off by default; allow-listed subcommands)                                                             |
| `ETCD_UI_AUDIT_MAX_BYTES`          | Audit JSONL rotation cap (default 256 MiB)                                                                                                            |
| `ETCD_UI_AUDIT_MAX_AGE`            | Drop events older than this from the ring + on-disk file (default 720h / 30 d)                                                                        |
| `ETCD_UI_ALERT_WEBHOOKS`           | Comma-separated Slack/Discord/Teams URLs for health-flip notifications                                                                                |
| `ETCD_UI_ALERT_THROTTLE`           | Per-(cluster,kind,recipient) throttle window (default `5m`)                                                                                           |
| `ETCD_UI_PEERS`                    | Comma-separated other etcd-ui URLs for federation (hub mode)                                                                                          |
| `ETCD_UI_PEER_TOKEN`               | Static bearer for peer→peer hop (prefer the OIDC service-account below)                                                                               |
| `ETCD_UI_PEER_OIDC=on`             | Mint client_credentials tokens against `ETCD_UI_OIDC_ISSUER` and use them between peers                                                               |
| `ETCD_UI_PEER_OIDC_CLIENT_ID/SECRET`| Credentials for the SA token mint. Pair the `_CLIENT_ID` with the peer's `ETCD_UI_PEER_CLIENT_IDS` to enable `system:peer:<id>` principal resolution. |
| `ETCD_UI_DATA_DIR`                 | Persistent state dir (default `/app/data`); audit log, UI clusters, snapshots                                                                         |
| **Etcd cert rotation**             | Cert mtimes are watched; on change the affected client is re-dialed automatically. No restart needed.                                                 |

### Observability

| Env var                                       | What it does                                                                                                  |
| --------------------------------------------- | ------------------------------------------------------------------------------------------------------------- |
| `OTEL_EXPORTER_OTLP_ENDPOINT`                 | OTLP/HTTP collector for **traces**; W3C TraceContext propagates across every internal hop                     |
| `OTEL_EXPORTER_OTLP_METRICS_ENDPOINT`         | Optional separate collector for **metrics**; periodic exporter every 30 s                                     |
| `OTEL_TRACES_SAMPLER_ARG`                     | Sampling ratio: `0.1` / `0.5` / `1.0`                                                                         |
| `OTEL_METRICS_INTERVAL`                       | Override the metrics export cadence (default `30s`)                                                           |

### v2 fallback

When `/version` reports an etcd 2.x server (or 3.x with `--enable-v2`), the
pool transparently dials over the legacy v2 HTTP API. Surface parity:

| Surface              | v2 status                                                                                              |
| -------------------- | ------------------------------------------------------------------------------------------------------ |
| Range / Put / Delete | ✓ Tree flattened to a flat KV list                                                                     |
| Bulk put / delete    | ✓ Serial Set/Delete loop (no txn) with prev-value capture for audit-undo (first 32 keys × 4 KB)        |
| Watch                | ✓ `Watcher.Next()` → same WS / SSE wire shape as v3                                                    |
| Export               | ✓ Recursive `Get` under `/`                                                                            |
| Summary              | ✓ Reachability + member count; raft term / db size / alarms not exposed by v2                          |
| History              | ✗ `501` — v2 has no MVCC                                                                               |
| Txn                  | ✗ `501` — v2 has no transactions                                                                       |
| Diff                 | ✗ `501` if either side is v2                                                                           |

All sources can coexist. UI-added clusters (Settings → Add cluster) are
persisted to `$ETCD_UI_DATA_DIR/clusters.json` and survive restart.

If a cluster shows up red on the Dashboard with an error: hover the card, the tooltip carries the underlying etcd error (TLS handshake failure, auth refused, dial timeout, …). Most issues are a wrong scheme (`http` vs `https`) or missing certs. For everything else see [`docs/TROUBLESHOOTING.md`](TROUBLESHOOTING.md).
