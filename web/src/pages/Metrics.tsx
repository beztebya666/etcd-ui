import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api, bytesPretty } from "../lib/api";
import { useStore } from "../lib/store";
import {
  Activity,
  Database,
  Cpu,
  MemoryStick,
  Network,
  Crown,
  AlertTriangle,
  Gauge,
} from "lucide-react";
import { Skeleton, SkeletonCard } from "../components/Skeleton";
import { cn } from "../lib/cn";

type Sample = { name: string; labels?: string; value: number };
type NodeBlock = { endpoint: string; used?: string; error?: string; samples?: Sample[] };
type Point = { t: number; v: number };

function find(samples: Sample[] | undefined, name: string): number | null {
  if (!samples) return null;
  const s = samples.find((x) => x.name === name);
  return s ? s.value : null;
}

const TRACK_METRICS = [
  "etcd_server_proposals_committed_total",
  "etcd_server_proposals_applied_total",
  "etcd_server_leader_changes_seen_total",
  "etcd_mvcc_db_total_size_in_bytes",
  "etcd_mvcc_db_total_size_in_use_in_bytes",
  "process_resident_memory_bytes",
  "process_cpu_seconds_total",
];

type LeaderChange = { at: number; from: number; to: number };

export function MetricsPage() {
  const cluster = useStore((s) => s.selectedCluster);
  const [selected, setSelected] = useState<string>("__all__");
  // per-endpoint rolling history
  const [history, setHistory] = useState<Record<string, Record<string, Point[]>>>({});
  // per-endpoint observed leader changes (within this session)
  const [leaderLog, setLeaderLog] = useState<Record<string, LeaderChange[]>>({});
  const lastLeaderSeen = useRef<Record<string, number>>({});

  const q = useQuery({
    queryKey: ["metrics", cluster],
    enabled: !!cluster,
    queryFn: () => api.metrics(cluster!),
    refetchInterval: 2_000,
  });

  // record rolling history + leader-change events
  useEffect(() => {
    if (!q.data) return;
    const now = Date.now();
    setHistory((prev) => {
      const next = { ...prev };
      for (const node of q.data!.nodes) {
        if (!node.samples) continue;
        const cur = (next[node.endpoint] = { ...(next[node.endpoint] ?? {}) });
        for (const m of TRACK_METRICS) {
          const v = find(node.samples, m);
          if (v == null) continue;
          const arr = (cur[m] = (cur[m] ?? []).slice(-149));
          arr.push({ t: now, v });
        }
      }
      return next;
    });
    setLeaderLog((prev) => {
      const next = { ...prev };
      for (const node of q.data!.nodes) {
        const v = find(node.samples, "etcd_server_leader_changes_seen_total");
        if (v == null) continue;
        const last = lastLeaderSeen.current[node.endpoint];
        if (last != null && v > last) {
          const log = (next[node.endpoint] = [...(next[node.endpoint] ?? [])]);
          log.unshift({ at: now, from: last, to: v });
          if (log.length > 50) log.length = 50;
        }
        lastLeaderSeen.current[node.endpoint] = v;
      }
      return next;
    });
  }, [q.data]);

  if (!cluster) return <div className="panel p-10 text-center muted">Pick a cluster first.</div>;

  // First-load skeleton while we have no data yet.
  if (q.isLoading && !q.data) {
    return (
      <div className="space-y-5">
        <div>
          <Skeleton className="h-7 w-40" />
          <Skeleton className="h-4 w-72 mt-2" />
        </div>
        <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
          {Array.from({ length: 4 }).map((_, i) => (
            <SkeletonCard key={i} rows={2} />
          ))}
        </div>
        <SkeletonCard rows={6} />
      </div>
    );
  }

  const nodes = q.data?.nodes ?? [];
  const okNodes = nodes.filter((n) => n.samples && n.samples.length > 0);

  if (!q.isLoading && nodes.length === 0) {
    return (
      <div className="panel p-12 text-center">
        <Gauge className="w-10 h-10 mx-auto mb-3 muted" />
        <div className="font-medium">No metrics endpoint reachable</div>
        <p className="muted text-sm mt-2 max-w-md mx-auto">
          etcd serves <code className="kbd">/metrics</code> on a separate port
          — kubeadm uses <code className="kbd">2381</code> over plain HTTP.
          Set <code className="kbd">ETCD_UI_METRICS_URL_{cluster}</code> to override.
        </p>
      </div>
    );
  }
  if (!q.isLoading && nodes.length > 0 && okNodes.length === 0) {
    return (
      <div className="panel p-12 text-center">
        <AlertTriangle className="w-10 h-10 mx-auto mb-3 text-warn" />
        <div className="font-medium">Endpoint reachable but no samples returned</div>
        <p className="muted text-sm mt-2 max-w-md mx-auto">
          {nodes[0]?.error ?? "The /metrics body had no parseable Prometheus lines."}
        </p>
      </div>
    );
  }
  const isAll = selected === "__all__";
  // pick "active" sample set for top stats + charts
  const activeNode: NodeBlock | undefined = isAll
    ? okNodes.find((n) => (find(n.samples, "etcd_server_has_leader") ?? 0) > 0) ?? okNodes[0]
    : nodes.find((n) => n.endpoint === selected);

  const dbSize = find(activeNode?.samples, "etcd_mvcc_db_total_size_in_bytes") ?? 0;
  const dbUse = find(activeNode?.samples, "etcd_mvcc_db_total_size_in_use_in_bytes") ?? 0;
  const hasLeader = find(activeNode?.samples, "etcd_server_has_leader") ?? 0;
  const leaderChanges = find(activeNode?.samples, "etcd_server_leader_changes_seen_total") ?? 0;
  // RSS: sum across nodes in "All" view, single in per-node
  const memTotal = isAll
    ? okNodes.reduce((acc, n) => acc + (find(n.samples, "process_resident_memory_bytes") ?? 0), 0)
    : find(activeNode?.samples, "process_resident_memory_bytes") ?? 0;

  const histFor = (m: string): Point[] => {
    if (isAll) {
      // sum across all node series at each timestamp (best-effort, by index)
      const sums: Point[] = [];
      const lens = okNodes.map((n) => (history[n.endpoint]?.[m]?.length ?? 0));
      const minLen = Math.min(...(lens.length ? lens : [0]));
      for (let i = 0; i < minLen; i++) {
        let v = 0;
        let t = 0;
        for (const n of okNodes) {
          const p = history[n.endpoint][m][i];
          v += p.v;
          t = p.t;
        }
        sums.push({ t, v });
      }
      return sums;
    }
    return history[selected]?.[m] ?? [];
  };

  return (
    <div className="space-y-5">
      <div className="flex items-end justify-between flex-wrap gap-3">
        <div>
          <h1 className="text-2xl font-semibold tracking-tight flex items-center gap-2">
            <Activity className="w-5 h-5 text-accent-500" /> Metrics
          </h1>
          <p className="muted mt-1">
            {isAll ? (
              <>
                <strong>Cluster aggregate</strong> · {okNodes.length}/{nodes.length} nodes reachable ·
                refreshed every 2 s.
              </>
            ) : (
              <>
                Node <span className="font-mono">{selected}</span> ·{" "}
                <span className="kbd">{activeNode?.used ?? "—"}</span> · refreshed every 2 s.
              </>
            )}
          </p>
        </div>
        <span className={"tag " + (hasLeader ? "tag-success" : "tag-warn")}>
          {hasLeader ? "has leader" : "no leader"}
        </span>
      </div>

      {/* Node selector */}
      <div className="panel p-2 flex items-center gap-1 flex-wrap">
        <NodeTab active={isAll} onClick={() => setSelected("__all__")}>
          <Crown className="w-3.5 h-3.5" /> All ({okNodes.length})
        </NodeTab>
        {nodes.map((n) => (
          <NodeTab
            key={n.endpoint}
            active={!isAll && selected === n.endpoint}
            onClick={() => setSelected(n.endpoint)}
          >
            {n.error ? (
              <AlertTriangle className="w-3.5 h-3.5 text-warn" />
            ) : (
              <span className="w-1.5 h-1.5 rounded-full bg-accent-500" />
            )}
            <span className="font-mono text-xs">{n.endpoint}</span>
          </NodeTab>
        ))}
      </div>

      {activeNode?.error && (
        <div className="panel p-4 text-danger text-sm">
          <AlertTriangle className="inline w-4 h-4 mr-1" />
          {activeNode.error}
        </div>
      )}

      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <Stat icon={Database} label="db size" value={bytesPretty(dbSize)} />
        <Stat icon={Database} label="db in-use" value={bytesPretty(dbUse)} />
        <Stat
          icon={MemoryStick}
          label={isAll ? `rss · sum of ${okNodes.length}` : "rss"}
          value={bytesPretty(memTotal)}
        />
        <Stat icon={Network} label="leader changes (total)" value={String(leaderChanges)} />
      </div>

      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <Chart
          title="Proposals committed/s"
          subtitle="etcd_server_proposals_committed_total (derivative)"
          icon={Cpu}
          points={histFor("etcd_server_proposals_committed_total")}
          derivative
        />
        <Chart
          title="DB total size"
          subtitle="etcd_mvcc_db_total_size_in_bytes"
          icon={Database}
          points={histFor("etcd_mvcc_db_total_size_in_bytes")}
          format={bytesPretty}
        />
        <Chart
          title="CPU seconds/s"
          subtitle="process_cpu_seconds_total (derivative)"
          icon={Cpu}
          points={histFor("process_cpu_seconds_total")}
          derivative
        />
        <Chart
          title="Resident memory"
          subtitle="process_resident_memory_bytes"
          icon={MemoryStick}
          points={histFor("process_resident_memory_bytes")}
          format={bytesPretty}
        />
      </div>

      {/* Persistent + session leader-change log */}
      <PersistentLeaderLog cluster={cluster ?? ""} sessionLog={leaderLog} sessionNodes={okNodes} />
    </div>
  );
}

// PersistentLeaderLog merges three sources:
//   1. /api/alerts/log — events the cluster service has *persisted to disk*
//      since first startup ($DATA_DIR/cluster-events.jsonl). Gives a real
//      timeline across SPA reloads and pod restarts.
//   2. The in-memory leaderLog this Metrics page built from polling — the
//      same source we had before, useful for sub-second precision the
//      moment a change happens.
//   3. The counter total from the metrics endpoint, so we can say
//      "5 changes total, here are the N we have timestamps for".
//
// What we deliberately can NOT show is *why* each change happened. etcd
// does not export the cause of an election in any metric or event. The
// usual suspects (disk fsync stalls, network partition, manual move-leader,
// shutdown of the previous leader) all leave footprints in different
// places — disk metrics, kubelet/network logs, the etcd member's own
// stdout. We surface a hint pointing at where to look.
function PersistentLeaderLog({
  cluster,
  sessionLog,
  sessionNodes,
}: {
  cluster: string;
  sessionLog: Record<string, { at: number; from: number; to: number }[]>;
  sessionNodes: { endpoint: string }[];
}) {
  const histQ = useQuery({
    queryKey: ["alert-log", cluster, "leader-flip"],
    enabled: !!cluster,
    queryFn: () => api.alertLog({ cluster, kind: "leader-flip", limit: 100 }),
    refetchInterval: 15_000,
  });

  // Total leader changes from the prometheus counter (newest value across
  // healthy nodes). Lets us say "5 reported by counter, 3 with timestamps".
  const totalCount = useMemo(() => {
    let max = 0;
    for (const n of sessionNodes) {
      const v = sessionLog[n.endpoint]?.[0]?.to ?? 0;
      if (v > max) max = v;
    }
    return max;
  }, [sessionNodes, sessionLog]);

  const merged = useMemo(() => {
    const rows: { at: number; from?: string; to?: string; src: "disk" | "session" }[] = [];
    for (const e of histQ.data ?? []) {
      const parts = (e.detail ?? "").match(/leader\s+([0-9a-f]+)\s*→\s*([0-9a-f]+)/i);
      rows.push({
        at: Date.parse(e.time),
        from: parts?.[1],
        to: parts?.[2],
        src: "disk",
      });
    }
    // Session log: only add entries not already covered by disk (rough
    // dedupe by 5s window + matching to/from).
    for (const n of sessionNodes) {
      for (const ev of sessionLog[n.endpoint] ?? []) {
        const dup = rows.some(
          (r) => Math.abs(r.at - ev.at) < 5000 && r.to === String(ev.to),
        );
        if (!dup) rows.push({ at: ev.at, from: String(ev.from), to: String(ev.to), src: "session" });
      }
    }
    rows.sort((a, b) => b.at - a.at);
    return rows;
  }, [histQ.data, sessionLog, sessionNodes]);

  return (
    <div className="panel p-5">
      <div className="text-sm font-medium mb-1 flex items-center gap-2">
        <Crown className="w-4 h-4 text-accent-500" /> Leader-change log
      </div>
      <p className="muted text-xs mb-3 max-w-[920px]">
        Recorded leader transitions. The counter total ({totalCount}) is the
        ground truth from etcd; what's listed below is the subset for which
        etcd-ui has timestamps. Older transitions (before this etcd-ui pod
        ever ran) can only be recovered from etcd's own logs.
      </p>
      {merged.length === 0 ? (
        <div className="muted text-sm">
          No leader changes recorded yet. {totalCount > 0 && (
            <>The counter reports <span className="font-mono">{totalCount}</span> cumulative
            transitions that happened before etcd-ui started observing.</>
          )}
        </div>
      ) : (
        <ul className="divide-y" style={{ borderColor: "rgb(var(--line))" }}>
          {merged.slice(0, 50).map((r, i) => (
            <li key={i} className="py-2 grid grid-cols-[180px_1fr_auto] gap-3 text-sm items-center">
              <span className="font-mono text-xs muted">
                {new Date(r.at).toLocaleString()}
              </span>
              <span className="font-mono text-xs truncate">
                {r.from && r.to ? (
                  <>
                    member <span className="text-danger">{r.from}</span>
                    <span className="muted"> → </span>
                    <span className="text-accent-500">{r.to}</span>
                  </>
                ) : (
                  <span className="muted">(counter increment, ids unknown)</span>
                )}
              </span>
              <span className={cn(
                "pill !text-[10px]",
                r.src === "disk" ? "border-accent-500/40 text-accent-500" : "border-warn/40 text-warn",
              )}>
                {r.src}
              </span>
            </li>
          ))}
        </ul>
      )}
      <details className="mt-4 text-xs muted">
        <summary className="cursor-pointer">why we can't tell the <em>cause</em></summary>
        <div className="mt-2 space-y-1 pl-4">
          <p>
            etcd's metrics + the gRPC API expose <em>that</em> a change happened (counter
            increment) but not <em>why</em>. To diagnose the cause, look at — in this order:
          </p>
          <ul className="list-disc pl-4">
            <li>
              <span className="font-mono">journalctl -u etcd</span> or pod logs on the
              member that lost leadership — usually says
              <span className="font-mono"> "lost the TCP streaming connection"</span> or
              <span className="font-mono"> "took too long to commit"</span>.
            </li>
            <li>
              <span className="font-mono">etcd_disk_wal_fsync_duration_seconds</span>
              and <span className="font-mono">etcd_disk_backend_commit_duration_seconds</span>
              — spikes &gt; 500ms force re-election.
            </li>
            <li>
              <span className="font-mono">etcd_network_peer_round_trip_time_seconds</span>
              — peer RTT bumps just before the flip = network partition.
            </li>
            <li>
              <span className="font-mono">etcd_server_proposals_failed_total</span> jumping
              indicates the old leader couldn't replicate to a quorum.
            </li>
            <li>
              Manual: someone ran <span className="font-mono">etcdctl move-leader &lt;id&gt;</span>
              — check the Audit page for an entry with <span className="font-mono">action=etcdctl</span>.
            </li>
          </ul>
        </div>
      </details>
    </div>
  );
}

function NodeTab({
  active,
  onClick,
  children,
}: {
  active: boolean;
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      onClick={onClick}
      className={
        "inline-flex items-center gap-1.5 px-2.5 h-8 rounded-md text-xs transition " +
        (active ? "soft-active text-current" : "soft-hover muted")
      }
    >
      {children}
    </button>
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
    <div className="panel p-4">
      <div className="flex items-center gap-1.5 text-[11px] uppercase tracking-wider muted">
        <Icon className="w-3 h-3" />
        {label}
      </div>
      <div className="text-lg font-medium mt-1 truncate">{value}</div>
    </div>
  );
}

function Chart({
  title,
  subtitle,
  icon: Icon,
  points,
  derivative,
  format,
}: {
  title: string;
  subtitle: string;
  icon: React.ComponentType<{ className?: string }>;
  points: Point[];
  derivative?: boolean;
  format?: (n: number) => string;
}) {
  const series = useMemo(() => (derivative ? toRate(points) : points), [points, derivative]);
  const w = 600;
  const h = 140;
  const pad = 8;
  const values = series.map((p) => p.v);
  const min = Math.min(0, ...values);
  const max = Math.max(1, ...values);
  const xs = (i: number) => pad + (i / Math.max(series.length - 1, 1)) * (w - pad * 2);
  const ys = (v: number) => h - pad - ((v - min) / (max - min || 1)) * (h - pad * 2);
  const path = series.map((p, i) => `${i === 0 ? "M" : "L"}${xs(i).toFixed(1)},${ys(p.v).toFixed(1)}`).join(" ");
  const area = series.length
    ? path + ` L ${xs(series.length - 1).toFixed(1)},${h - pad} L ${pad},${h - pad} Z`
    : "";
  const last = series[series.length - 1]?.v ?? 0;

  // Hover state: nearest point + relative cursor x. Tracked at SVG
  // viewport coordinates (0..w) for crispness — we convert client x
  // via getBoundingClientRect(). Grafana-style vertical guide + dot.
  const svgRef = useRef<SVGSVGElement>(null);
  const [hoverIdx, setHoverIdx] = useState<number | null>(null);

  return (
    <div className="panel p-4">
      <div className="flex items-start gap-2">
        <Icon className="w-4 h-4 text-accent-500 mt-0.5" />
        <div className="min-w-0">
          <div className="text-sm font-medium">{title}</div>
          <div className="text-xs muted">{subtitle}</div>
        </div>
        <div className="ml-auto font-mono text-sm">
          {hoverIdx != null && series[hoverIdx]
            ? format ? format(series[hoverIdx].v) : series[hoverIdx].v.toFixed(2)
            : format ? format(last) : last.toFixed(2)}
        </div>
      </div>
      <svg
        ref={svgRef}
        viewBox={`0 0 ${w} ${h}`}
        preserveAspectRatio="none"
        className="w-full mt-2 block"
        onMouseMove={(e) => {
          if (!svgRef.current || series.length < 2) return;
          const rect = svgRef.current.getBoundingClientRect();
          const x = ((e.clientX - rect.left) / rect.width) * w;
          // Find the index whose xs() is closest to x. Linear scan —
          // these series stay under ~120 samples so binary search isn't
          // worth the complexity.
          let best = 0;
          let bestD = Infinity;
          for (let i = 0; i < series.length; i++) {
            const d = Math.abs(xs(i) - x);
            if (d < bestD) {
              bestD = d;
              best = i;
            }
          }
          setHoverIdx(best);
        }}
        onMouseLeave={() => setHoverIdx(null)}
      >
        <defs>
          <linearGradient id="gradAccent" x1="0" x2="0" y1="0" y2="1">
            <stop offset="0%" stopColor="rgb(16,185,129)" stopOpacity="0.35" />
            <stop offset="100%" stopColor="rgb(16,185,129)" stopOpacity="0" />
          </linearGradient>
        </defs>
        {series.length > 1 ? (
          <>
            <path d={area} fill="url(#gradAccent)" />
            <path d={path} fill="none" stroke="rgb(16,185,129)" strokeWidth={1.5} />
            {hoverIdx != null && series[hoverIdx] && (
              <>
                {/* vertical guide */}
                <line
                  x1={xs(hoverIdx)}
                  x2={xs(hoverIdx)}
                  y1={pad}
                  y2={h - pad}
                  stroke="rgb(255,255,255)"
                  strokeOpacity={0.25}
                  strokeDasharray="3 3"
                  strokeWidth={1}
                />
                {/* point marker */}
                <circle
                  cx={xs(hoverIdx)}
                  cy={ys(series[hoverIdx].v)}
                  r={3.5}
                  fill="rgb(16,185,129)"
                  stroke="rgb(var(--panel))"
                  strokeWidth={2}
                />
              </>
            )}
          </>
        ) : (
          <text x={w / 2} y={h / 2} textAnchor="middle" className="fill-current opacity-40" fontSize={11}>
            collecting data…
          </text>
        )}
      </svg>
      {/* Tooltip caption: timestamp + value, shown below the chart so
          it never occludes the data itself. */}
      <div className="text-[10px] muted font-mono mt-1 h-4">
        {hoverIdx != null && series[hoverIdx] ? (
          <>
            {new Date(series[hoverIdx].t).toLocaleTimeString()}
            {" · "}
            <span style={{ color: "rgb(var(--fg))" }}>
              {format ? format(series[hoverIdx].v) : series[hoverIdx].v.toFixed(4)}
            </span>
          </>
        ) : (
          <span className="opacity-60">hover to inspect samples</span>
        )}
      </div>
    </div>
  );
}

function toRate(points: Point[]): Point[] {
  if (points.length < 2) return [];
  const out: Point[] = [];
  for (let i = 1; i < points.length; i++) {
    const dt = (points[i].t - points[i - 1].t) / 1000;
    const dv = points[i].v - points[i - 1].v;
    out.push({ t: points[i].t, v: dt > 0 ? Math.max(0, dv / dt) : 0 });
  }
  return out;
}
