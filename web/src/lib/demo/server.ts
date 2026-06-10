// In-browser "server" for the etcd-ui demo: maps fetch(path) → the demo DB.
import { getDB, saveDB, emitDemo, nowISO, type DemoDB } from "./db";
import type { KV } from "../api";

type Body = Record<string, unknown> | undefined;
interface Ctx { params: Record<string, string>; query: URLSearchParams; body: Body; }
const NO_CONTENT = Symbol("204");
const txt = (s: string) => ({ __text: s });

const idMatch = (p: string, path: string): Record<string, string> | null => {
  const pp = p.split("/"), ap = path.split("/");
  if (pp.length !== ap.length) return null;
  const params: Record<string, string> = {};
  for (let i = 0; i < pp.length; i++) {
    if (pp[i].startsWith(":")) params[pp[i].slice(1)] = decodeURIComponent(ap[i]);
    else if (pp[i] !== ap[i]) return null;
  }
  return params;
};

const db = () => getDB();
const cl = (id: string) => db().clusters.find((c) => c.id === id);
const kvsOf = (id: string): KV[] => db().kvs[id] || [];

type Handler = (c: Ctx) => unknown;
const R: [string, string, Handler][] = [];
const on = (m: string, p: string, h: Handler) => R.push([m, p, h]);

// --- auth / meta ---
on("GET", "/api/version", () => ({ app: "etcd-ui", version: "1.0.0", build: "demo", goEnv: "go1.25 linux/amd64" }));
on("GET", "/api/auth/me", () => ({ user: "demo", src: "demo", authDisabled: true }));
on("GET", "/api/auth/providers", () => ({ basic: true, oidc: true }));
on("POST", "/api/auth/login", () => ({ ok: true }));
on("POST", "/api/auth/logout", () => NO_CONTENT);
on("POST", "/api/auth/oidc/refresh", () => NO_CONTENT);

// --- clusters ---
on("GET", "/api/clusters", () => db().clusters);
on("GET", "/api/clusters/:id/summary", (c) => cl(c.params.id) ?? { __status: 404 });
on("GET", "/api/clusters/:id/members", (c) => db().members[c.params.id] || []);
on("POST", "/api/clusters", (c) => { const b = c.body as { id: string; name: string; endpoints: string[] }; const sum = { id: b.id, name: b.name, source: "static", endpoints: b.endpoints || [], healthy: true, memberCount: b.endpoints?.length || 1, dbSizeBytes: 1024 * 1024, dbSizeInUse: 800 * 1024, revision: 1, raftTerm: 1, lastChecked: nowISO(), apiVersion: "v3" as const, serverVersion: "3.5.13" }; db().clusters.push(sum); db().kvs[b.id] = []; db().members[b.id] = []; saveDB(); emitDemo("clusters"); return sum; });
on("DELETE", "/api/clusters/:id", (c) => { const d = db(); d.clusters = d.clusters.filter((x) => x.id !== c.params.id); saveDB(); emitDemo("clusters"); return NO_CONTENT; });
on("POST", "/api/clusters/:id/move-leader", (c) => { const ms = db().members[c.params.id] || []; const tgt = (c.body as { memberId?: string })?.memberId; ms.forEach((m) => (m.isLeader = String(m.id) === tgt)); const s = cl(c.params.id); if (s) s.raftTerm += 1; saveDB(); emitDemo("clusters"); return { transferredTo: Number(tgt) || 0 }; });
on("POST", "/api/clusters/:id/alarms/disarm", (c) => { const s = cl(c.params.id); if (s) s.alarms = []; saveDB(); return { ok: true }; });
on("POST", "/api/clusters/:id/compact", (c) => ({ compactedRevision: cl(c.params.id)?.revision || 0 }));
on("POST", "/api/clusters/:id/defrag", () => ({ "10.0.1.10:2379": "ok", "10.0.1.11:2379": "ok", "10.0.1.12:2379": "ok" }));

// --- KV ---
function rangeFilter(id: string, b: Body) {
  const o = (b || {}) as { prefix?: string; from?: string; end?: string; limit?: number; keysOnly?: boolean; keyRegex?: string; valueRegex?: string };
  let arr = kvsOf(id);
  if (o.prefix) arr = arr.filter((k) => k.key.startsWith(o.prefix!));
  if (o.from) arr = arr.filter((k) => k.key >= o.from!);
  if (o.end) arr = arr.filter((k) => k.key < o.end!);
  if (o.keyRegex) { try { const re = new RegExp(o.keyRegex); arr = arr.filter((k) => re.test(k.key)); } catch { /* */ } }
  if (o.valueRegex) { try { const re = new RegExp(o.valueRegex); arr = arr.filter((k) => re.test(k.value)); } catch { /* */ } }
  arr = arr.slice().sort((a, b2) => (a.key < b2.key ? -1 : 1));
  const count = arr.length;
  const limit = o.limit || 1000;
  let out = arr.slice(0, limit);
  if (o.keysOnly) out = out.map((k) => ({ ...k, value: "" }));
  return { kvs: out, more: count > limit, count };
}
on("POST", "/api/clusters/:id/range", (c) => rangeFilter(c.params.id, c.body));
on("POST", "/api/clusters/:id/range/counts", (c) => { const b = c.body as { prefix: string; depth?: number }; const depth = b.depth || 1; const base = (b.prefix || "/"); const buckets: Record<string, number> = {}; for (const k of kvsOf(c.params.id)) { if (!k.key.startsWith(base)) continue; const rest = k.key.slice(base.length).split("/").filter(Boolean).slice(0, depth).join("/"); const p = base + rest; buckets[p] = (buckets[p] || 0) + 1; } return { prefix: base, depth, total: kvsOf(c.params.id).filter((k) => k.key.startsWith(base)).length, buckets: Object.entries(buckets).map(([prefix, count]) => ({ prefix, count })) }; });
function putKV(id: string, key: string, value: string, lease?: number) {
  const d = db(); d.rev += 1; const arr = d.kvs[id] || (d.kvs[id] = []);
  const ex = arr.find((k) => k.key === key);
  if (ex) { ex.value = value; ex.modRevision = d.rev; ex.version += 1; if (lease) ex.lease = lease; }
  else arr.push({ key, value, createRevision: d.rev, modRevision: d.rev, version: 1, lease });
  const s = cl(id); if (s) { s.revision = d.rev; s.keyCount = arr.length; }
  saveDB(); emitDemo("watch", { type: "PUT", key, value, revision: d.rev });
  return d.rev;
}
on("POST", "/api/clusters/:id/put", (c) => { const b = c.body as { key: string; value: string; leaseId?: number }; return { revision: putKV(c.params.id, b.key, b.value, b.leaseId) }; });
on("POST", "/api/clusters/:id/put-cas", (c) => { const b = c.body as { key: string; value: string }; return { status: "ok", revision: putKV(c.params.id, b.key, b.value) }; });
on("POST", "/api/clusters/:id/put-k8s", (c) => { const b = c.body as { key: string; json: string }; return { status: "ok", revision: putKV(c.params.id, b.key, b.json) }; });
on("POST", "/api/clusters/:id/delete", (c) => { const b = c.body as { key: string; prefix?: boolean }; const d = db(); const before = (d.kvs[c.params.id] || []).length; d.kvs[c.params.id] = (d.kvs[c.params.id] || []).filter((k) => (b.prefix ? !k.key.startsWith(b.key) : k.key !== b.key)); d.rev += 1; const s = cl(c.params.id); if (s) { s.revision = d.rev; s.keyCount = d.kvs[c.params.id].length; } saveDB(); emitDemo("watch", { type: "DELETE", key: b.key, revision: d.rev }); return { revision: d.rev, deleted: before - d.kvs[c.params.id].length }; });
on("GET", "/api/clusters/:id/history", (c) => { const key = c.query.get("key") || ""; const cur = kvsOf(c.params.id).find((k) => k.key === key); const versions: KV[] = []; if (cur) for (let v = cur.version; v >= 1; v--) versions.push({ ...cur, value: v === cur.version ? cur.value : cur.value + "  (v" + v + ")", version: v, modRevision: cur.modRevision - (cur.version - v) * 7 }); return { key, currentRevision: cur?.modRevision || 0, versions }; });
on("GET", "/api/clusters/:id/diff", (c) => { const against = c.query.get("against") || ""; const left = kvsOf(c.params.id), right = kvsOf(against); const rk = new Map(right.map((k) => [k.key, k.value])); const diffs: { key: string; kind: string; left?: string; right?: string }[] = []; for (const k of left) { if (!rk.has(k.key)) diffs.push({ key: k.key, kind: "only-left", left: k.value }); else if (rk.get(k.key) !== k.value) diffs.push({ key: k.key, kind: "different", left: k.value, right: rk.get(k.key) }); } for (const k of right) if (!left.find((x) => x.key === k.key)) diffs.push({ key: k.key, kind: "only-right", right: k.value }); return { left: c.params.id, right: against, total: diffs.length, diffs }; });
on("POST", "/api/clusters/:id/bulk/put", (c) => { const b = c.body as { items: { key: string; value: string }[] }; (b.items || []).forEach((i) => putKV(c.params.id, i.key, i.value)); return { applied: (b.items || []).length, failed: 0, revision: db().rev }; });
on("POST", "/api/clusters/:id/bulk/delete", (c) => { const b = c.body as { keys?: string[]; prefixes?: string[] }; const d = db(); let del = 0; const arr = d.kvs[c.params.id] || []; d.kvs[c.params.id] = arr.filter((k) => { const hit = (b.keys || []).includes(k.key) || (b.prefixes || []).some((p) => k.key.startsWith(p)); if (hit) del++; return !hit; }); d.rev += 1; saveDB(); return { deleted: del, revision: d.rev }; });
on("POST", "/api/clusters/:id/txn", (c) => ({ succeeded: true, revision: (db().rev += 1) }));

// --- leases / locks ---
on("GET", "/api/clusters/:id/leases", (c) => db().leases[c.params.id] || []);
on("DELETE", "/api/clusters/:id/leases/:lease", (c) => { const d = db(); d.leases[c.params.id] = (d.leases[c.params.id] || []).filter((l) => String(l.id) !== c.params.lease); saveDB(); return NO_CONTENT; });
on("GET", "/api/clusters/:id/locks", (c) => { const entries = db().locks[c.params.id] || []; return { prefix: c.query.get("prefix") || "/locks/", holder: entries.find((e) => e.holder) || null, entries }; });
on("POST", "/api/clusters/:id/locks/acquire", (c) => { const b = c.body as { prefix: string; ttlSeconds: number; holderTag?: string }; const d = db(); const leaseId = 7587830000 + Math.floor(Math.random() * 9999); const entries = d.locks[c.params.id] || (d.locks[c.params.id] = []); const acquired = !entries.some((e) => e.holder); entries.push({ key: `${b.prefix}/${leaseId.toString(16)}`, leaseId, createRevision: (d.rev += 1), ttlSeconds: b.ttlSeconds, grantedTtl: b.ttlSeconds, holder: acquired, note: b.holderTag }); saveDB(); return { leaseId, key: `${b.prefix}/${leaseId.toString(16)}`, acquired, holder: b.holderTag || "demo", waiters: entries.filter((e) => !e.holder).length, ttlSeconds: b.ttlSeconds }; });
on("DELETE", "/api/clusters/:id/locks/:lease", (c) => { const d = db(); d.locks[c.params.id] = (d.locks[c.params.id] || []).filter((l) => String(l.leaseId) !== c.params.lease); const rem = d.locks[c.params.id]; if (rem[0]) rem[0].holder = true; saveDB(); return NO_CONTENT; });

// --- metrics / rbac / acl / federation / alerts / audit ---
on("GET", "/api/clusters/:id/metrics", (c) => ({ nodes: db().metrics[c.params.id] || [] }));
on("GET", "/api/clusters/:id/rbac/status", (c) => { const r = db().rbac[c.params.id]; return { enabled: r?.enabled || false, revision: r?.revision || 0 }; });
on("POST", "/api/clusters/:id/rbac/enable", (c) => { const r = db().rbac[c.params.id]; if (r) r.enabled = true; saveDB(); return { ok: true }; });
on("POST", "/api/clusters/:id/rbac/disable", (c) => { const r = db().rbac[c.params.id]; if (r) r.enabled = false; saveDB(); return { ok: true }; });
on("GET", "/api/clusters/:id/rbac/users", (c) => db().rbac[c.params.id]?.users || []);
on("POST", "/api/clusters/:id/rbac/users", (c) => { const b = c.body as { name: string }; const u = { name: b.name, roles: [] }; db().rbac[c.params.id]?.users.push(u); saveDB(); return u; });
on("DELETE", "/api/clusters/:id/rbac/users/:name", (c) => { const r = db().rbac[c.params.id]; if (r) r.users = r.users.filter((u) => u.name !== c.params.name); saveDB(); return NO_CONTENT; });
on("GET", "/api/clusters/:id/rbac/roles", (c) => db().rbac[c.params.id]?.roles || []);
on("GET", "/api/acl", () => ({ loaded: true, editable: true, rules: db().aclRules }));
on("PUT", "/api/acl", (c) => { db().aclRules = (c.body as never) || []; saveDB(); return { ok: true, rules: db().aclRules.length }; });
on("POST", "/api/acl/validate", (c) => ({ ok: true, rules: ((c.body as unknown as unknown[]) || []).length }));
on("GET", "/api/acl/history", () => [{ when: nowISO(), actor: "demo", id: "h1", rules: db().aclRules }]);
on("POST", "/api/acl/restore", () => ({ ok: true }));
on("GET", "/api/federation/peers", () => db().fedPeers);
on("GET", "/api/alerts/log", (c) => { let a = db().alerts; const k = c.query.get("kind"), cluster = c.query.get("cluster"); if (k) a = a.filter((x) => x.kind === k); if (cluster) a = a.filter((x) => x.cluster === cluster); return a; });
on("GET", "/api/audit/events", (c) => db().audit.slice(0, Number(c.query.get("limit")) || 100));
on("POST", "/api/clusters/:id/etcdctl", (c) => { const b = c.body as { args?: string[]; command?: string }; const args = (b.args || (b.command || "").split(/\s+/)).filter(Boolean); return { exitCode: 0, stdout: etcdctl(c.params.id, args), stderr: "", durationMs: 12 }; });
on("GET", "/api/clusters/:id/restore/recipe", (c) => ({ mode: c.query.get("mode") || "etcdctl", cluster: c.params.id, body: restoreRecipe(c.params.id, c.query.get("mode") || "etcdctl") }));

function etcdctl(id: string, args: string[]): string {
  const s = cl(id);
  if (args[0] === "member" && args[1] === "list") return (db().members[id] || []).map((m) => `${m.idStr}, started, ${m.name}, ${m.peerUrls[0]}, ${m.clientUrls[0]}, ${m.isLeader}`).join("\n");
  if (args[0] === "endpoint" && args[1] === "status") return (s?.endpoints || []).map((e, i) => `${e}, ${(db().members[id] || [])[i]?.idStr || "0x0"}, 3.5.13, ${Math.round((s?.dbSizeBytes || 0) / 1e6)} MB, ${(db().members[id] || [])[i]?.isLeader || false}, false, ${s?.raftTerm}, ${s?.revision}, ${s?.revision}, `).join("\n");
  if (args[0] === "endpoint" && args[1] === "health") return (s?.endpoints || []).map((e) => `${e} is healthy: successfully committed proposal: took = 2.1ms`).join("\n");
  if (args[0] === "get") { const k = kvsOf(id).find((x) => x.key === args[1]); return k ? `${k.key}\n${k.value}` : ""; }
  if (args[0] === "version" || args[0] === "--version") return "etcdctl version: 3.5.13\nAPI version: 3.5";
  return `Error: unknown command "${args.join(" ")}" — try: member list · endpoint status · endpoint health · get <key>`;
}
function restoreRecipe(id: string, mode: string): string {
  if (mode === "k8s") return `# Restore ${id} into a Kubernetes control plane\nkubectl -n kube-system scale --replicas=0 deploy/kube-apiserver\netcdutl snapshot restore snapshot.db --data-dir /var/lib/etcd-restore\n# swap the data dir on each control-plane node, then scale back up`;
  return `# Restore ${id} with etcdctl\nETCDCTL_API=3 etcdctl snapshot restore snapshot.db \\\n  --name etcd-cp-0 --initial-cluster etcd-cp-0=https://10.0.1.10:2380 \\\n  --initial-advertise-peer-urls https://10.0.1.10:2380 --data-dir /var/lib/etcd-new`;
}

export async function demoFetch(input: RequestInfo | URL, init?: RequestInit): Promise<Response> {
  const rawUrl = typeof input === "string" ? input : input instanceof URL ? input.toString() : input.url;
  const method = (init?.method || (typeof input !== "string" && !(input instanceof URL) ? (input as Request).method : "GET") || "GET").toUpperCase();
  const u = new URL(rawUrl, location.origin);
  if (!u.pathname.startsWith("/api/")) return realFetch(input, init);
  let body: Body;
  if (init?.body && typeof init.body === "string") { try { body = JSON.parse(init.body); } catch { /* */ } }
  await new Promise((r) => setTimeout(r, 50 + Math.random() * 90));
  for (const [m, pattern, handler] of R) {
    if (m !== method) continue;
    const params = idMatch(pattern, u.pathname);
    if (!params) continue;
    let out: unknown;
    try { out = handler({ params, query: u.searchParams, body }); } catch (e) { return json({ error: String(e) }, 500); }
    if (out === NO_CONTENT) return new Response(null, { status: 204 });
    if (out && typeof out === "object" && "__text" in out) return new Response((out as { __text: string }).__text, { headers: { "content-type": "text/plain" } });
    if (out && typeof out === "object" && "__status" in out) return json({ error: "not found" }, (out as { __status: number }).__status);
    return json(out, 200);
  }
  console.warn("[demo] unhandled", method, u.pathname);
  return json(method === "GET" ? [] : { ok: true }, 200);
}
const realFetch = window.fetch.bind(window);
function json(d: unknown, status: number) { return new Response(JSON.stringify(d), { status, headers: { "content-type": "application/json" } }); }
export type { DemoDB };
