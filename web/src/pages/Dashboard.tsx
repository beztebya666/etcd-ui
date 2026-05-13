import { useEffect, useRef } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, bytesPretty, type ClusterSummary } from "../lib/api";
import {
  CheckCircle2,
  AlertTriangle,
  Crown,
  Database,
  Hash,
  Users,
  Zap,
} from "lucide-react";
import { motion } from "framer-motion";
import { useStore } from "../lib/store";
import { useNavigate } from "react-router-dom";
import { ProtocolBadge } from "../components/ProtocolBadge";

export function Dashboard() {
  const { data, isLoading } = useQuery({
    queryKey: ["clusters"],
    queryFn: api.clusters,
    refetchInterval: 3_000,
  });

  return (
    <div className="space-y-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight">Fleet overview</h1>
        <p className="muted mt-1">
          Every etcd cluster you connect to — Kubernetes control-plane, Patroni DCS, standalone — shown in one place,
          updated live.
        </p>
      </div>

      {isLoading && <SkeletonGrid />}

      {data && data.length === 0 && <EmptyState />}

      {data && data.length > 0 && (
        <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
          {data.map((c, i) => (
            <motion.div key={c.id} initial={{ opacity: 0, y: 8 }} animate={{ opacity: 1, y: 0 }}
                        transition={{ delay: i * 0.03 }}>
              <ClusterCard c={c} />
            </motion.div>
          ))}
        </div>
      )}
    </div>
  );
}

// rolling 60-point history of cluster revision deltas, in-memory only.
const sparkCache: Record<string, number[]> = {};

function ClusterCard({ c }: { c: ClusterSummary }) {
  const { setSelectedCluster } = useStore();
  const nav = useNavigate();
  const prevRev = useRef<number>(c.revision);

  useEffect(() => {
    const arr = (sparkCache[c.id] = sparkCache[c.id] ?? []);
    const delta = Math.max(0, c.revision - prevRev.current);
    prevRev.current = c.revision;
    arr.push(delta);
    if (arr.length > 60) arr.shift();
  }, [c.id, c.revision]);

  return (
    <button
      onClick={() => {
        setSelectedCluster(c.id);
        nav("/cluster");
      }}
      className="panel card-hover p-5 text-left w-full cursor-pointer"
    >
      <div className="flex items-start justify-between">
        <div>
          <div className="flex items-center gap-2">
            {c.healthy ? (
              <CheckCircle2 className="w-4 h-4 text-accent-500" />
            ) : (
              <AlertTriangle className="w-4 h-4 text-warn" />
            )}
            <div className="font-medium">{c.name}</div>
            <span className="pill">{c.source}</span>
            <ProtocolBadge api={c.apiVersion} serverVersion={c.serverVersion} />
          </div>
          <div className="text-xs muted mt-1 truncate max-w-[280px]">{c.endpoints.join(", ")}</div>
        </div>
        {!!c.alarms?.length && <span className="pill text-warn border-warn/40">{c.alarms.length} alarms</span>}
      </div>

      <div className="grid grid-cols-2 gap-3 mt-5">
        <Stat icon={Users} label="members" value={String(c.memberCount)} />
        <Stat icon={Crown} label="leader" value={c.leaderShort || c.leader || "—"} />
        <Stat icon={Database} label="db size" value={bytesPretty(c.dbSizeBytes)} />
        <Stat icon={Hash} label="keys" value={c.keyCount != null ? c.keyCount.toLocaleString() : c.revision.toLocaleString()} />
      </div>
      <div className="mt-4">
        <Sparkline points={sparkCache[c.id] ?? []} />
      </div>

      {c.error && (
        <div className="mt-4 text-xs text-danger border border-danger/30 rounded-lg px-3 py-2">
          <Zap className="inline w-3.5 h-3.5 mr-1" />
          {c.error}
        </div>
      )}
    </button>
  );
}

function Sparkline({ points }: { points: number[] }) {
  if (points.length < 2) {
    return <div className="h-6 muted text-[10px]">collecting…</div>;
  }
  const w = 200;
  const h = 24;
  const max = Math.max(1, ...points);
  const path = points
    .map((v, i) => {
      const x = (i / (points.length - 1)) * w;
      const y = h - (v / max) * h;
      return `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
  return (
    <div className="flex items-center gap-2">
      <div className="text-[10px] muted uppercase tracking-wider">∆ rev / 3s</div>
      <svg viewBox={`0 0 ${w} ${h}`} className="flex-1 h-6">
        <path d={path} fill="none" stroke="rgb(16,185,129)" strokeWidth={1.4} />
      </svg>
    </div>
  );
}

function Stat({
  icon: Icon,
  label,
  value,
}: {
  icon: React.ComponentType<{ className?: string }>;
  label: string;
  value: string;
}) {
  return (
    <div className="panel-2 rounded-lg px-3 py-2 border" style={{ borderColor: "rgb(var(--line))" }}>
      <div className="flex items-center gap-1.5 text-[11px] uppercase tracking-wider muted">
        <Icon className="w-3 h-3" />
        {label}
      </div>
      <div className="text-sm font-medium mt-0.5 truncate">{value}</div>
    </div>
  );
}

function SkeletonGrid() {
  return (
    <div className="grid grid-cols-1 md:grid-cols-2 xl:grid-cols-3 gap-4">
      {Array.from({ length: 6 }).map((_, i) => (
        <div key={i} className="panel p-5 animate-pulse h-[180px]" />
      ))}
    </div>
  );
}

function EmptyState() {
  return (
    <div className="panel p-10 text-center">
      <div className="text-lg font-medium">No clusters detected yet</div>
      <p className="muted mt-2 max-w-md mx-auto">
        Set <span className="kbd">ETCD_ENDPOINTS</span> when starting the container, or add a cluster from Settings.
        If you're in Kubernetes, etcd-ui will try to detect the control-plane automatically.
      </p>
    </div>
  );
}
