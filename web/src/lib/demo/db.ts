// In-browser DB for the demo build. All state lives in the visitor's own
// localStorage — every browser is a private sandbox; "Reset Demo" rebuilds it.
import type { ClusterSummary, Member, KV, Lease, LockEntry, RBACUser, RBACRole, AuditEvent } from "../api";
import { buildSeed } from "./seed";

export interface FedPeer {
  id: string; url: string; reachable: boolean; lastChecked: string; error?: string;
  health: "healthy" | "degraded" | "down" | "empty"; clustersTotal: number; clustersHealthy: number; clustersUnhealthy: number;
}
export interface AlertEntry { time: string; kind: "unhealthy" | "recovered" | "alarm" | "leader-flip"; cluster: string; detail?: string; }
export interface MetricNode { endpoint: string; used?: string; samples?: { name: string; labels?: string; value: number }[]; error?: string; }

export interface DemoDB {
  clusters: ClusterSummary[];
  members: Record<string, Member[]>; // clusterId -> members
  kvs: Record<string, KV[]>;         // clusterId -> all keys
  leases: Record<string, Lease[]>;
  locks: Record<string, LockEntry[]>;
  rbac: Record<string, { enabled: boolean; revision: number; users: RBACUser[]; roles: RBACRole[] }>;
  metrics: Record<string, MetricNode[]>;
  fedPeers: FedPeer[];
  alerts: AlertEntry[];
  audit: AuditEvent[];
  aclRules: { user: string; cluster: string; prefix?: string; access: "read" | "write" | "admin" }[];
  rev: number;
}

const KEY = "etcd-ui:demo:v1";
let db: DemoDB | null = null;

function load(): DemoDB {
  try { const raw = localStorage.getItem(KEY); if (raw) return JSON.parse(raw) as DemoDB; } catch { /* */ }
  const fresh = buildSeed();
  try { localStorage.setItem(KEY, JSON.stringify(fresh)); } catch { /* */ }
  return fresh;
}
export function getDB(): DemoDB { if (!db) db = load(); return db; }
export function saveDB() { try { localStorage.setItem(KEY, JSON.stringify(db)); } catch { /* */ } }
export function resetDemo() { db = buildSeed(); saveDB(); }

type Listener = (type: string, payload?: unknown) => void;
const listeners = new Set<Listener>();
export function onDemoEvent(l: Listener): () => void { listeners.add(l); return () => { listeners.delete(l); }; }
export function emitDemo(type: string, payload?: unknown) { for (const l of Array.from(listeners)) { try { l(type, payload); } catch { /* */ } } }

export const nowISO = () => new Date().toISOString();
export const minsAgo = (m: number) => new Date(Date.now() - m * 60_000).toISOString();
export const secsAgo = (s: number) => new Date(Date.now() - s * 1000).toISOString();
