// Tiny typed client over the gateway's REST API.

import { openStream } from "./stream";

export type ClusterSummary = {
  id: string;
  name: string;
  source: string;
  endpoints: string[];
  healthy: boolean;
  leader?: string;
  leaderShort?: string;
  leaderId?: number;
  memberCount: number;
  keyCount?: number;
  dbSizeBytes: number;
  dbSizeInUse: number;
  revision: number;
  raftTerm: number;
  lastChecked: string;
  alarms?: string[];
  error?: string;
  apiVersion?: "v2" | "v3";
  serverVersion?: string;
};

export type Member = {
  id: number;
  idStr: string;
  name: string;
  peerUrls: string[];
  clientUrls: string[];
  isLeader: boolean;
  isLearner: boolean;
};

export type KVPreview = {
  format: "k8s-proto" | "json" | string;
  apiVersion?: string;
  kind?: string;
  namespace?: string;
  name?: string;
  // Fully decoded object as pretty-printed JSON. Populated when the
  // server could resolve the Kind to a Go struct (every built-in K8s
  // resource — 500+ Kinds — or any JSON-encoded CRD). When empty, the
  // SPA falls back to the binary hex viewer.
  json?: string;
  decodeError?: string;
};

export type KV = {
  key: string;
  value: string;
  createRevision: number;
  modRevision: number;
  version: number;
  lease?: number;
  preview?: KVPreview;
};

export type WatchEvent = {
  type: "PUT" | "DELETE" | string;
  key: string;
  value?: string;
  prevValue?: string;
  revision: number;
  preview?: KVPreview;
};

export type LockEntry = {
  key: string;
  leaseId: number;
  createRevision: number;
  ttlSeconds: number;
  grantedTtl: number;
  holder: boolean;
  note?: string;
};

export type Lease = {
  id: number;
  ttl: number;
  grantedTtl: number;
  attachedKeys?: string[];
  attachedCount?: number;
  holderIdentity?: string;
  holderKind?: string;
  renewedAt?: string;
  acquiredAt?: string;
  leaseTransitions?: number;
};

export type RBACUser = { name: string; roles?: string[] };
export type RBACPermission = {
  type: "read" | "write" | "readwrite";
  key: string;
  rangeEnd?: string;
  prefix?: boolean;
};
export type RBACRole = { name: string; permissions?: RBACPermission[] };

export type AuditEvent = {
  id: number;
  time: string;
  actor: string;
  source: string;
  method: string;
  cluster?: string;
  path: string;
  action: string;
  key?: string;
  status: number;
  ip?: string;
  userAgent?: string;
  note?: string;
};

export type TxnCondition = {
  key: string;
  field: "value" | "createRevision" | "modRevision" | "version";
  op: "equal" | "not-equal" | "greater" | "less";
  target: string;
};
export type TxnOp =
  | { type: "put"; key: string; value: string }
  | { type: "delete"; key: string; prefix?: boolean }
  | { type: "get"; key: string; prefix?: boolean };
export type TxnResult = { succeeded: boolean; revision: number; error?: string };

// In-flight refresh promise — multiple parallel 401s share a single refresh
// round-trip rather than racing the IdP.
let refreshing: Promise<boolean> | null = null;

async function tryRefresh(): Promise<boolean> {
  if (!refreshing) {
    refreshing = (async () => {
      try {
        const r = await fetch("/api/auth/oidc/refresh", { method: "POST" });
        if (r.ok) return true;
      } catch {
        /* fall through to silent-auth */
      }
      // Refresh token gone (revoked / expired). Try the prompt=none iframe
      // dance — if the IdP still has an SSO session for this user we get
      // a fresh session cookie without any visible login UI.
      try {
        const { trySilentAuth } = await import("./silentAuth");
        return await trySilentAuth(
          window.location.pathname + window.location.search,
        );
      } catch {
        return false;
      }
    })().finally(() => {
      // Allow the next 401 batch to attempt a refresh again.
      setTimeout(() => (refreshing = null), 0);
    });
  }
  return refreshing;
}

async function req<T>(method: string, path: string, body?: unknown): Promise<T> {
  const doFetch = () =>
    fetch(path, {
      method,
      headers: body ? { "content-type": "application/json" } : undefined,
      body: body ? JSON.stringify(body) : undefined,
    });

  let res = await doFetch();

  // Auth endpoints don't refresh — they're either fine or genuinely 401.
  if (res.status === 401 && !path.startsWith("/api/auth/")) {
    const ok = await tryRefresh();
    if (ok) {
      res = await doFetch();
    }
  }

  if (res.status === 401 && !path.startsWith("/api/auth/")) {
    // Refresh failed or there was no session at all — hop to /login.
    if (!window.location.pathname.startsWith("/login")) {
      const to = window.location.pathname + window.location.search;
      window.location.assign("/login?returnTo=" + encodeURIComponent(to));
    }
    throw new Error("unauthenticated");
  }
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText);
    throw new Error(text || `${res.status} ${res.statusText}`);
  }
  if (res.status === 204) return undefined as T;
  const ct = res.headers.get("content-type") || "";
  if (ct.includes("application/json")) return res.json() as Promise<T>;
  return (await res.text()) as unknown as T;
}

export const api = {
  version: () =>
    req<{ app: string; version: string; build: string; goEnv?: string }>(
      "GET",
      "/api/version",
    ),
  me: () =>
    req<{ user: string; src?: string; exp?: number; authDisabled?: boolean }>("GET", "/api/auth/me"),
  authProviders: () =>
    req<{ basic: boolean; oidc: boolean }>("GET", "/api/auth/providers"),
  acl: () =>
    req<{
      loaded: boolean;
      editable: boolean;
      rules: {
        user: string;
        cluster: string;
        prefix?: string;
        access: "read" | "write" | "admin";
      }[];
    }>("GET", "/api/acl"),
  aclSave: (
    rules: {
      user: string;
      cluster: string;
      prefix?: string;
      access: "read" | "write" | "admin";
    }[],
  ) => req<{ ok: boolean; rules: number }>("PUT", "/api/acl", rules),
  aclValidate: (
    rules: {
      user: string;
      cluster: string;
      prefix?: string;
      access: "read" | "write" | "admin";
    }[],
  ) =>
    req<{ ok: boolean; error?: string; rules?: number }>(
      "POST",
      "/api/acl/validate",
      rules,
    ),
  aclHistory: () =>
    req<
      {
        when: string;
        actor: string;
        id: string;
        rules: {
          user: string;
          cluster: string;
          prefix?: string;
          access: "read" | "write" | "admin";
        }[];
      }[]
    >("GET", "/api/acl/history"),
  aclRestore: (id: string) =>
    req<{ ok: boolean }>("POST", "/api/acl/restore", { id }),
  login: (username: string, password: string) =>
    req<{ ok: boolean }>("POST", "/api/auth/login", { username, password }),
  logout: () => req<void>("POST", "/api/auth/logout"),
  oidcLoginURL: (returnTo: string) =>
    `/api/auth/oidc/login?returnTo=${encodeURIComponent(returnTo)}`,
  oidcRefresh: () => req<void>("POST", "/api/auth/oidc/refresh"),
  federationPeers: () =>
    req<
      {
        id: string;
        url: string;
        reachable: boolean;
        lastChecked: string;
        error?: string;
        clusters?: ClusterSummary[];
        health: "healthy" | "degraded" | "down" | "empty";
        clustersTotal: number;
        clustersHealthy: number;
        clustersUnhealthy: number;
      }[]
    >("GET", "/api/federation/peers"),

  clusters: () => req<ClusterSummary[]>("GET", "/api/clusters"),
  cluster: (id: string) => req<ClusterSummary>("GET", `/api/clusters/${encodeURIComponent(id)}/summary`),
  members: (id: string) => req<Member[]>("GET", `/api/clusters/${encodeURIComponent(id)}/members`),
  addCluster: (c: { id: string; name: string; endpoints: string[]; username?: string; password?: string }) =>
    req("POST", "/api/clusters", c),
  removeCluster: (id: string) => req<void>("DELETE", `/api/clusters/${encodeURIComponent(id)}`),

  // Compare-and-set put with 3-way merge fallback. Returns:
  //   { status: "ok" | "merged", revision }   when the server committed
  //   { status: "conflict", base, theirs, merged, conflicts }   on conflict
  // 409 responses are *expected* and carry the conflict payload — we
  // bypass req<> here so the thrown-error path doesn't eat the body.
  putCAS: async (
    id: string,
    body: { key: string; value: string; baseRev: number; acceptConflicts?: boolean },
  ): Promise<{
    status: "ok" | "merged" | "conflict";
    revision?: number;
    base?: string;
    theirs?: string;
    merged?: string;
    conflicts?: number;
  }> => {
    const res = await fetch(`/api/clusters/${encodeURIComponent(id)}/put-cas`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify(body),
    });
    if (res.status === 409) return res.json();
    if (!res.ok) {
      const text = await res.text().catch(() => res.statusText);
      throw new Error(text || `${res.status} ${res.statusText}`);
    }
    return res.json();
  },

  range: (
    id: string,
    body: {
      prefix?: string;
      from?: string;
      end?: string;
      limit?: number;
      keysOnly?: boolean;
      keyRegex?: string;
      valueRegex?: string;
    },
  ) =>
    req<{ kvs: KV[]; more: boolean; count: number }>("POST", `/api/clusters/${encodeURIComponent(id)}/range`, body),
  history: (id: string, key: string, limit = 100) =>
    req<{ key: string; currentRevision: number; versions: KV[]; truncatedAt?: string }>(
      "GET",
      `/api/clusters/${encodeURIComponent(id)}/history?key=${encodeURIComponent(key)}&limit=${limit}`,
    ),
  diff: (id: string, against: string, prefix = "") =>
    req<{
      left: string;
      right: string;
      total: number;
      diffs: { key: string; kind: "only-left" | "only-right" | "different"; left?: string; right?: string }[];
    }>(
      "GET",
      `/api/clusters/${encodeURIComponent(id)}/diff?against=${encodeURIComponent(against)}${prefix ? `&prefix=${encodeURIComponent(prefix)}` : ""}`,
    ),
  metrics: (id: string) =>
    req<{
      nodes: {
        endpoint: string;
        used?: string;
        error?: string;
        samples?: { name: string; labels?: string; value: number }[];
      }[];
    }>("GET", `/api/clusters/${encodeURIComponent(id)}/metrics`),
  restoreRecipe: (id: string, opts: { mode: "etcdctl" | "k8s"; path?: string; dataDir?: string; token?: string }) => {
    const qs = new URLSearchParams({ mode: opts.mode });
    if (opts.path) qs.set("path", opts.path);
    if (opts.dataDir) qs.set("dataDir", opts.dataDir);
    if (opts.token) qs.set("token", opts.token);
    return req<{ mode: string; cluster: string; body: string }>(
      "GET",
      `/api/clusters/${encodeURIComponent(id)}/restore/recipe?${qs.toString()}`,
    );
  },
  put: (id: string, body: { key: string; value: string; leaseId?: number }) =>
    req<{ revision: number }>("POST", `/api/clusters/${encodeURIComponent(id)}/put`, body),
  delete: (id: string, body: { key: string; prefix?: boolean }) =>
    req<{ revision: number; deleted: number }>("POST", `/api/clusters/${encodeURIComponent(id)}/delete`, body),

  leases: (id: string) => req<Lease[]>("GET", `/api/clusters/${encodeURIComponent(id)}/leases`),
  revokeLease: (id: string, leaseId: number) =>
    req<void>("DELETE", `/api/clusters/${encodeURIComponent(id)}/leases/${leaseId}`),

  compact: (id: string, rev?: number) =>
    req<{ compactedRevision: number }>("POST", `/api/clusters/${encodeURIComponent(id)}/compact${rev ? `?rev=${rev}` : ""}`),
  defrag: (id: string) => req<Record<string, string>>("POST", `/api/clusters/${encodeURIComponent(id)}/defrag`),

  // Historical alerts (leader flips, health flips, alarm onsets) the
  // cluster service has observed since first startup. Persisted under
  // $DATA_DIR/cluster-events.jsonl so the timeline survives SPA reloads
  // and pod restarts. ?cluster=&kind=&limit= optional filters.
  alertLog: (params: { cluster?: string; kind?: string; limit?: number } = {}) => {
    const q = new URLSearchParams();
    if (params.cluster) q.set("cluster", params.cluster);
    if (params.kind) q.set("kind", params.kind);
    if (params.limit) q.set("limit", String(params.limit));
    const qs = q.toString();
    return req<{
      time: string;
      kind: "unhealthy" | "recovered" | "alarm" | "leader-flip";
      cluster: string;
      detail?: string;
    }[]>("GET", `/api/alerts/log${qs ? "?" + qs : ""}`);
  },
  disarm: (id: string) => req("POST", `/api/clusters/${encodeURIComponent(id)}/alarms/disarm`),
  // Transfer raft leadership to the given member. etcd refuses the transfer
  // if the target isn't healthy or hasn't caught up, so a 502 here means
  // the cluster protected itself — the error message is etcd's verbatim.
  moveLeader: (id: string, memberId: string) =>
    req<{ transferredTo: number }>(
      "POST",
      `/api/clusters/${encodeURIComponent(id)}/move-leader`,
      { memberId },
    ),
  snapshotURL: (id: string) => `/api/clusters/${encodeURIComponent(id)}/snapshot`,
  exportURL: (id: string) => `/api/clusters/${encodeURIComponent(id)}/export`,

  // Distributed-lock playground. Locks live under a user-chosen prefix
  // and are held by whichever lease has the smallest CreateRevision under
  // that prefix (matches clientv3/concurrency.Mutex semantics).
  locksList: (id: string, prefix?: string): Promise<{
    prefix: string;
    holder: LockEntry | null;
    entries: LockEntry[];
  }> => req("GET", `/api/clusters/${encodeURIComponent(id)}/locks${prefix ? `?prefix=${encodeURIComponent(prefix)}` : ""}`),
  lockAcquire: (id: string, body: { prefix: string; ttlSeconds: number; holderTag?: string }) =>
    req<{ leaseId: number; key: string; acquired: boolean; holder: string; waiters: number; ttlSeconds: number }>(
      "POST",
      `/api/clusters/${encodeURIComponent(id)}/locks/acquire`,
      body,
    ),
  lockRelease: (id: string, leaseId: number) =>
    req<void>("DELETE", `/api/clusters/${encodeURIComponent(id)}/locks/${leaseId}`),

  // Suggest keys matching a prefix for autocomplete. KeysOnly so we
  // don't ship values across the wire just to show 5 suggestions.
  suggestKeys: async (id: string, prefix: string, limit = 50): Promise<string[]> => {
    if (!id) return [];
    const r = await api.range(id, {
      prefix,
      limit,
      keysOnly: true,
    });
    return (r.kvs ?? []).map((kv) => kv.key);
  },

  // Folder counts under a prefix at a given depth. Used by the SPA to
  // tag tree folders that have more keys than the current range loaded
  // (e.g. you set limit=10k on a 27k-key cluster — `pods` has 12k keys
  // total but the tree shows only the first 800 of them).
  rangeCounts: (
    id: string,
    body: { prefix: string; depth?: number },
  ): Promise<{
    prefix: string;
    depth: number;
    total: number;
    buckets: { prefix: string; count: number }[];
  }> => req(
    "POST",
    `/api/clusters/${encodeURIComponent(id)}/range/counts`,
    body,
  ),

  // etcdutl snapshot status — uploads a .db file, runs `etcdutl snapshot
  // status` against it offline, returns parsed hash/revision/size. Useful
  // to verify a backup file before halting the quorum for a restore.
  etcdutlSnapshotStatus: async (id: string, file: File): Promise<{
    ok: boolean;
    exitCode: number;
    sizeBytes: number;
    status?: { hash: number; revision: number; totalKey: number; totalSize: number } | null;
    stdout: string;
    stderr: string;
    durationMs: number;
  }> => {
    const res = await fetch(
      `/api/clusters/${encodeURIComponent(id)}/etcdutl/snapshot-status`,
      { method: "POST", body: file },
    );
    if (!res.ok) throw new Error(await res.text());
    return res.json();
  },

  // --- bulk / txn / restore ---
  bulkPut: (id: string, items: { key: string; value: string }[], transactional = true) =>
    req<{ applied: number; failed: number; revision?: number }>(
      "POST",
      `/api/clusters/${encodeURIComponent(id)}/bulk/put`,
      { items, transactional },
    ),
  bulkDelete: (id: string, body: { keys?: string[]; prefixes?: string[] }) =>
    req<{ deleted: number; revision: number }>(
      "POST",
      `/api/clusters/${encodeURIComponent(id)}/bulk/delete`,
      body,
    ),
  txn: (id: string, body: { conditions: TxnCondition[]; onSuccess: TxnOp[]; onFailure: TxnOp[] }) =>
    req<TxnResult>("POST", `/api/clusters/${encodeURIComponent(id)}/txn`, body),
  restore: async (
    id: string,
    file: File,
    opts?: { clearPrefix?: string; dryRun?: boolean },
  ) => {
    const fd = new FormData();
    fd.append("file", file);
    const qs = new URLSearchParams();
    if (opts?.clearPrefix) qs.set("clearPrefix", opts.clearPrefix);
    if (opts?.dryRun) qs.set("dryRun", "true");
    const url = `/api/clusters/${encodeURIComponent(id)}/restore${qs.toString() ? "?" + qs.toString() : ""}`;
    const res = await fetch(url, { method: "POST", body: fd });
    if (!res.ok) throw new Error(await res.text());
    return res.json();
  },

  // --- RBAC ---
  rbacStatus: (id: string) => req<{ enabled: boolean; revision: number }>(
    "GET", `/api/clusters/${encodeURIComponent(id)}/rbac/status`),
  rbacEnable: (id: string) => req("POST", `/api/clusters/${encodeURIComponent(id)}/rbac/enable`),
  rbacDisable: (id: string) => req("POST", `/api/clusters/${encodeURIComponent(id)}/rbac/disable`),
  rbacUsers: (id: string) => req<RBACUser[]>("GET", `/api/clusters/${encodeURIComponent(id)}/rbac/users`),
  rbacAddUser: (id: string, body: { name: string; password: string }) =>
    req<RBACUser>("POST", `/api/clusters/${encodeURIComponent(id)}/rbac/users`, body),
  rbacDeleteUser: (id: string, name: string) =>
    req<void>("DELETE", `/api/clusters/${encodeURIComponent(id)}/rbac/users/${encodeURIComponent(name)}`),
  rbacGrantRole: (id: string, user: string, role: string) =>
    req<void>("POST",
      `/api/clusters/${encodeURIComponent(id)}/rbac/users/${encodeURIComponent(user)}/roles`,
      { role }),
  rbacRevokeRole: (id: string, user: string, role: string) =>
    req<void>("DELETE",
      `/api/clusters/${encodeURIComponent(id)}/rbac/users/${encodeURIComponent(user)}/roles/${encodeURIComponent(role)}`),
  rbacRoles: (id: string) => req<RBACRole[]>("GET", `/api/clusters/${encodeURIComponent(id)}/rbac/roles`),
  rbacAddRole: (id: string, body: { name: string }) =>
    req<RBACRole>("POST", `/api/clusters/${encodeURIComponent(id)}/rbac/roles`, body),
  rbacDeleteRole: (id: string, name: string) =>
    req<void>("DELETE", `/api/clusters/${encodeURIComponent(id)}/rbac/roles/${encodeURIComponent(name)}`),
  rbacGrantPermission: (id: string, role: string, perm: RBACPermission) =>
    req<void>("POST",
      `/api/clusters/${encodeURIComponent(id)}/rbac/roles/${encodeURIComponent(role)}/permissions`,
      perm),

  // --- etcdctl ---
  etcdctl: (id: string, args: string[]) =>
    req<{
      stdout: string;
      stderr: string;
      exitCode: number;
      durationMs: number;
      command: string[];
    }>("POST", `/api/clusters/${encodeURIComponent(id)}/etcdctl`, { args }),

  // --- audit ---
  audit: (limit = 200) => req<AuditEvent[]>("GET", `/api/audit/events?limit=${limit}`),
  auditStream: (onEvent: (e: AuditEvent) => void) => {
    const s = openStream("/api/audit/events/stream", {
      onMessage: (data) => {
        try { onEvent(JSON.parse(data)); } catch { /* ignore */ }
      },
    });
    return () => s.close();
  },

  // Watch helper. Uses the unified WS-first transport with SSE fallback —
  // see lib/stream.ts. Caller treats it identically.
  watch: (id: string, prefix: string, onEvent: (e: WatchEvent) => void, onError?: (err: Error) => void) => {
    const url = `/api/clusters/${encodeURIComponent(id)}/watch?prefix=${encodeURIComponent(prefix)}`;
    const s = openStream(url, {
      onMessage: (data) => {
        try { onEvent(JSON.parse(data)); } catch { /* ignore */ }
      },
      onError,
    });
    return () => s.close();
  },
};

export function bytesPretty(n: number): string {
  if (n < 1024) return `${n} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(v < 10 ? 1 : 0)} ${units[i]}`;
}
