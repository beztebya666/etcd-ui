// Pristine demo dataset for etcd-ui — a realistic Kubernetes etcd cluster plus a
// Patroni DCS and a standalone node, with a rich KV tree, members, leases, locks,
// RBAC, metrics, federation, alerts and an audit trail.
import type { DemoDB } from "./db";
import type { KV } from "../api";

const iso = (ms: number) => new Date(ms).toISOString();
const ago = (ms: number) => iso(Date.now() - ms);
const M = 60_000, H = 3_600_000, D = 86_400_000;

let rev = 284500;
const kv = (key: string, value: string, opts: Partial<KV> = {}): KV => {
  rev += 1;
  return { key, value, createRevision: opts.createRevision ?? rev - 50, modRevision: opts.modRevision ?? rev, version: opts.version ?? 3, lease: opts.lease, preview: opts.preview };
};
const k8s = (key: string, kind: string, apiVersion: string, name: string, namespace: string, obj: object): KV =>
  kv(key, JSON.stringify(obj), { preview: { format: "k8s-proto", apiVersion, kind, namespace, name, json: JSON.stringify(obj, null, 2) } });

function kubeProdKVs(): KV[] {
  const out: KV[] = [];
  // namespaces
  for (const ns of ["default", "kube-system", "monitoring", "ingress-nginx"])
    out.push(k8s(`/registry/namespaces/${ns}`, "Namespace", "v1", ns, "", { apiVersion: "v1", kind: "Namespace", metadata: { name: ns }, status: { phase: "Active" } }));
  // pods
  const pods: [string, string, string][] = [
    ["default", "web-7d9f8c6b5-x2k9p", "Running"], ["default", "web-7d9f8c6b5-q4m1z", "Running"],
    ["default", "api-5c8b9d7f6-h3n2k", "Running"], ["kube-system", "coredns-787d4945fb-abcde", "Running"],
    ["kube-system", "kube-apiserver-cp-0", "Running"], ["kube-system", "etcd-cp-0", "Running"],
    ["monitoring", "prometheus-0", "Running"], ["monitoring", "grafana-66f8c-77ab", "Running"],
    ["ingress-nginx", "ingress-nginx-controller-x9z", "Running"], ["default", "batch-job-27839-pp4t", "Succeeded"],
  ];
  for (const [ns, name, phase] of pods)
    out.push(k8s(`/registry/pods/${ns}/${name}`, "Pod", "v1", name, ns, { apiVersion: "v1", kind: "Pod", metadata: { name, namespace: ns }, status: { phase, podIP: "10.244." + (out.length % 5) + "." + (10 + out.length) } }));
  // deployments / services / configmaps / secrets
  for (const [ns, name, rep] of [["default", "web", 3], ["default", "api", 2], ["monitoring", "grafana", 1]] as [string, string, number][])
    out.push(k8s(`/registry/deployments/${ns}/${name}`, "Deployment", "apps/v1", name, ns, { apiVersion: "apps/v1", kind: "Deployment", metadata: { name, namespace: ns }, spec: { replicas: rep }, status: { readyReplicas: rep, replicas: rep } }));
  out.push(k8s(`/registry/services/default/kubernetes`, "Service", "v1", "kubernetes", "default", { apiVersion: "v1", kind: "Service", metadata: { name: "kubernetes", namespace: "default" }, spec: { clusterIP: "10.96.0.1", ports: [{ port: 443 }] } }));
  out.push(k8s(`/registry/services/default/web`, "Service", "v1", "web", "default", { apiVersion: "v1", kind: "Service", metadata: { name: "web" }, spec: { type: "ClusterIP", clusterIP: "10.96.42.10", ports: [{ port: 80, targetPort: 8080 }] } }));
  out.push(k8s(`/registry/configmaps/kube-system/coredns`, "ConfigMap", "v1", "coredns", "kube-system", { apiVersion: "v1", kind: "ConfigMap", metadata: { name: "coredns" }, data: { Corefile: ".:53 {\n  kubernetes cluster.local\n  forward . /etc/resolv.conf\n}" } }));
  out.push(k8s(`/registry/secrets/default/db-credentials`, "Secret", "v1", "db-credentials", "default", { apiVersion: "v1", kind: "Secret", type: "Opaque", metadata: { name: "db-credentials" }, data: { username: "YXBw", password: "••••••••" } }));
  // k8s leader-election leases
  for (const name of ["kube-scheduler", "kube-controller-manager"])
    out.push(k8s(`/registry/leases/kube-system/${name}`, "Lease", "coordination.k8s.io/v1", name, "kube-system", { apiVersion: "coordination.k8s.io/v1", kind: "Lease", metadata: { name }, spec: { holderIdentity: "cp-0", leaseDurationSeconds: 15, renewTime: ago(2000) } }));
  // plain app config (non-k8s) under /myapp + /patroni
  out.push(kv("/myapp/config/database_url", "postgres://db.prod.svc:5432/app?sslmode=require"));
  out.push(kv("/myapp/config/log_level", "info"));
  out.push(kv("/myapp/feature_flags/new_dashboard", "true"));
  out.push(kv("/myapp/feature_flags/beta_search", "false"));
  out.push(kv("/myapp/locks/migrate", "held-by:web-7d9f8c6b5-x2k9p", { lease: 7587830124 }));
  out.push(kv("/service/patroni/leader", "patroni-0", { lease: 7587830777 }));
  out.push(kv("/service/patroni/members/patroni-0", JSON.stringify({ role: "leader", state: "running", conn_url: "postgres://10.0.3.1:5432/" })));
  out.push(kv("/service/patroni/members/patroni-1", JSON.stringify({ role: "replica", state: "running", conn_url: "postgres://10.0.3.2:5432/" })));
  return out;
}

export function buildSeed(): DemoDB {
  const kubeProd = kubeProdKVs();
  const stagingKVs = kubeProd.slice(0, 14);
  return {
    clusters: [
      { id: "kube-prod", name: "kube-prod (control-plane)", source: "kubeconfig", endpoints: ["https://10.0.1.10:2379", "https://10.0.1.11:2379", "https://10.0.1.12:2379"], healthy: true, leader: "etcd-cp-1", leaderShort: "cp-1", leaderId: 10501, memberCount: 3, keyCount: 1247, dbSizeBytes: 41 * 1024 * 1024, dbSizeInUse: 28 * 1024 * 1024, revision: rev, raftTerm: 14, lastChecked: ago(3000), apiVersion: "v3", serverVersion: "3.5.13" },
      { id: "kube-staging", name: "kube-staging", source: "kubeconfig", endpoints: ["https://10.1.1.10:2379"], healthy: true, leader: "etcd-stg-0", leaderShort: "stg-0", leaderId: 22001, memberCount: 1, keyCount: 318, dbSizeBytes: 9 * 1024 * 1024, dbSizeInUse: 7 * 1024 * 1024, revision: 90233, raftTerm: 6, lastChecked: ago(5000), apiVersion: "v3", serverVersion: "3.5.13" },
      { id: "patroni-dcs", name: "patroni-dcs (postgres HA)", source: "static", endpoints: ["https://10.0.3.10:2379", "https://10.0.3.11:2379", "https://10.0.3.12:2379"], healthy: true, leader: "dcs-1", leaderShort: "dcs-1", leaderId: 30102, memberCount: 3, keyCount: 64, dbSizeBytes: 3 * 1024 * 1024, dbSizeInUse: 2 * 1024 * 1024, revision: 18422, raftTerm: 9, lastChecked: ago(4000), apiVersion: "v3", serverVersion: "3.5.12" },
      { id: "standalone-dev", name: "standalone-dev", source: "static", endpoints: ["http://127.0.0.1:2379"], healthy: false, memberCount: 1, dbSizeBytes: 1024 * 1024, dbSizeInUse: 900 * 1024, revision: 412, raftTerm: 2, lastChecked: ago(60000), apiVersion: "v3", serverVersion: "3.5.9", alarms: ["NOSPACE"], error: "context deadline exceeded (endpoint unreachable)" },
    ],
    members: {
      "kube-prod": [
        { id: 10500, idStr: "0x290f", name: "etcd-cp-0", peerUrls: ["https://10.0.1.10:2380"], clientUrls: ["https://10.0.1.10:2379"], isLeader: false, isLearner: false },
        { id: 10501, idStr: "0x2905", name: "etcd-cp-1", peerUrls: ["https://10.0.1.11:2380"], clientUrls: ["https://10.0.1.11:2379"], isLeader: true, isLearner: false },
        { id: 10502, idStr: "0x2912", name: "etcd-cp-2", peerUrls: ["https://10.0.1.12:2380"], clientUrls: ["https://10.0.1.12:2379"], isLeader: false, isLearner: false },
      ],
      "kube-staging": [{ id: 22001, idStr: "0x55f1", name: "etcd-stg-0", peerUrls: ["https://10.1.1.10:2380"], clientUrls: ["https://10.1.1.10:2379"], isLeader: true, isLearner: false }],
      "patroni-dcs": [
        { id: 30101, idStr: "0x759d", name: "dcs-0", peerUrls: ["https://10.0.3.10:2380"], clientUrls: ["https://10.0.3.10:2379"], isLeader: false, isLearner: false },
        { id: 30102, idStr: "0x7596", name: "dcs-1", peerUrls: ["https://10.0.3.11:2380"], clientUrls: ["https://10.0.3.11:2379"], isLeader: true, isLearner: false },
        { id: 30103, idStr: "0x75a7", name: "dcs-2", peerUrls: ["https://10.0.3.12:2380"], clientUrls: ["https://10.0.3.12:2379"], isLeader: false, isLearner: true },
      ],
      "standalone-dev": [{ id: 41001, idStr: "0xa029", name: "default", peerUrls: ["http://127.0.0.1:2380"], clientUrls: ["http://127.0.0.1:2379"], isLeader: true, isLearner: false }],
    },
    kvs: { "kube-prod": kubeProd, "kube-staging": stagingKVs, "patroni-dcs": kubeProd.filter((k) => k.key.startsWith("/service/patroni")), "standalone-dev": [kv("/dev/test", "hello")] },
    leases: {
      "kube-prod": [
        { id: 7587830124, ttl: 11, grantedTtl: 15, attachedKeys: ["/myapp/locks/migrate"], attachedCount: 1, holderIdentity: "web-7d9f8c6b5-x2k9p", holderKind: "Pod", renewedAt: ago(2000), acquiredAt: ago(40 * M), leaseTransitions: 3 },
        { id: 7587830777, ttl: 24, grantedTtl: 30, attachedKeys: ["/service/patroni/leader"], attachedCount: 1, holderIdentity: "patroni-0", holderKind: "Lease", renewedAt: ago(1500), acquiredAt: ago(6 * H), leaseTransitions: 0 },
        { id: 7587831999, ttl: 7, grantedTtl: 15, attachedCount: 0, renewedAt: ago(3000), acquiredAt: ago(10 * M) },
      ],
      "kube-staging": [], "patroni-dcs": [{ id: 9001, ttl: 20, grantedTtl: 30, attachedKeys: ["/service/patroni/leader"], attachedCount: 1, holderIdentity: "dcs-1" }], "standalone-dev": [],
    },
    locks: {
      "kube-prod": [
        { key: "/locks/db-migration/694d8...a1", leaseId: 7587830124, createRevision: rev - 120, ttlSeconds: 11, grantedTtl: 15, holder: true, note: "db-migration" },
        { key: "/locks/db-migration/694d8...b7", leaseId: 7587831999, createRevision: rev - 80, ttlSeconds: 7, grantedTtl: 15, holder: false },
      ],
      "kube-staging": [], "patroni-dcs": [], "standalone-dev": [],
    },
    rbac: {
      "kube-prod": { enabled: true, revision: 14, users: [{ name: "root", roles: ["root"] }, { name: "apiserver", roles: ["rw-registry"] }, { name: "readonly", roles: ["ro-all"] }, { name: "patroni", roles: ["rw-patroni"] }], roles: [
        { name: "root", permissions: [{ type: "readwrite", key: " ", rangeEnd: " ", prefix: true }] },
        { name: "rw-registry", permissions: [{ type: "readwrite", key: "/registry/", prefix: true }] },
        { name: "ro-all", permissions: [{ type: "read", key: "", prefix: true }] },
        { name: "rw-patroni", permissions: [{ type: "readwrite", key: "/service/patroni/", prefix: true }] },
      ] },
      "kube-staging": { enabled: false, revision: 1, users: [], roles: [] },
      "patroni-dcs": { enabled: true, revision: 3, users: [{ name: "patroni", roles: ["rw-patroni"] }], roles: [{ name: "rw-patroni", permissions: [{ type: "readwrite", key: "/service/", prefix: true }] }] },
      "standalone-dev": { enabled: false, revision: 0, users: [], roles: [] },
    },
    metrics: {
      "kube-prod": ["https://10.0.1.10:2379", "https://10.0.1.11:2379", "https://10.0.1.12:2379"].map((ep, i) => ({ endpoint: ep, used: (26 + i) + " MiB", samples: [
        { name: "etcd_server_has_leader", value: 1 }, { name: "etcd_server_leader_changes_seen_total", value: 2 },
        { name: "etcd_mvcc_db_total_size_in_bytes", value: 41 * 1024 * 1024 }, { name: "etcd_network_peer_round_trip_time_seconds", labels: "p99", value: 0.0021 + i * 0.0003 },
        { name: "process_resident_memory_bytes", value: (210 + i * 8) * 1024 * 1024 }, { name: "etcd_disk_wal_fsync_duration_seconds", labels: "p99", value: 0.0034 },
      ] })),
      "kube-staging": [{ endpoint: "https://10.1.1.10:2379", used: "7 MiB", samples: [{ name: "etcd_server_has_leader", value: 1 }] }],
      "patroni-dcs": [], "standalone-dev": [],
    },
    fedPeers: [
      { id: "eu-west", url: "https://etcd-ui.eu-west.internal", reachable: true, lastChecked: ago(8000), health: "healthy", clustersTotal: 3, clustersHealthy: 3, clustersUnhealthy: 0 },
      { id: "us-east", url: "https://etcd-ui.us-east.internal", reachable: true, lastChecked: ago(9000), health: "degraded", clustersTotal: 2, clustersHealthy: 1, clustersUnhealthy: 1 },
    ],
    alerts: [
      { time: ago(60000), kind: "alarm", cluster: "standalone-dev", detail: "NOSPACE alarm raised (db > quota)" },
      { time: ago(20 * M), kind: "leader-flip", cluster: "kube-prod", detail: "leader moved cp-0 → cp-1 (raft term 14)" },
      { time: ago(2 * H), kind: "recovered", cluster: "kube-staging", detail: "cluster healthy again" },
      { time: ago(2 * H + 3 * M), kind: "unhealthy", cluster: "kube-staging", detail: "1/1 endpoints unreachable" },
    ],
    audit: Array.from({ length: 16 }, (_, i) => ({
      id: 1000 - i, time: ago(i * 11 * M + 4000), actor: ["root", "apiserver", "admin@corp", "readonly", "patroni"][i % 5], source: i % 3 ? "basic" : "oidc",
      method: ["GET", "POST", "PUT", "DELETE"][i % 4], cluster: ["kube-prod", "kube-prod", "patroni-dcs", "kube-staging"][i % 4],
      path: ["/api/clusters/kube-prod/range", "/api/clusters/kube-prod/put", "/api/clusters/kube-prod/txn", "/api/acl"][i % 4],
      action: ["range", "put", "txn", "acl-save", "delete", "lock-acquire"][i % 6], key: i % 2 ? "/registry/pods/default/web-7d9f8c6b5-x2k9p" : "/myapp/config/log_level",
      status: i % 7 === 5 ? 403 : 200, ip: "10.0.2." + (20 + (i % 8)), userAgent: "etcd-ui/1.0", note: i % 7 === 5 ? "permission denied" : undefined,
    })),
    aclRules: [
      { user: "admin@corp", cluster: "*", access: "admin" },
      { user: "oncall@corp", cluster: "kube-prod", prefix: "/registry/", access: "read" },
      { user: "dba@corp", cluster: "patroni-dcs", prefix: "/service/patroni/", access: "write" },
    ],
    rev,
  };
}
