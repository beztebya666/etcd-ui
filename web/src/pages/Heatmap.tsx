import { useEffect, useMemo, useRef, useState } from "react";
import { api, type WatchEvent } from "../lib/api";
import { useStore } from "../lib/store";
import { Activity, Play, Pause, Trash2, GripVertical } from "lucide-react";
import { cn } from "../lib/cn";

/**
 * Write-heatmap: aggregate live PUT/DELETE events by top-level prefix and
 * draw an intensity-colored bar per prefix updated in real time.
 */
export function HeatmapPage() {
  const cluster = useStore((s) => s.selectedCluster);
  const [paused, setPaused] = useState(false);
  const [prefix, setPrefix] = useState("/");
  const [depth, setDepth] = useState(2);
  const [counts, setCounts] = useState<Record<string, number>>({});
  const [keyColW, setKeyColW] = useState<number>(360);
  const dragRef = useRef<{ startX: number; startW: number } | null>(null);
  const closer = useRef<() => void>();

  useEffect(() => {
    if (!cluster || paused) return;
    closer.current?.();
    const close = api.watch(cluster, prefix === "/" ? "" : prefix, (e: WatchEvent) => {
      const parts = e.key.split("/").filter(Boolean);
      const bucket = "/" + parts.slice(0, depth).join("/");
      setCounts((prev) => ({ ...prev, [bucket]: (prev[bucket] ?? 0) + 1 }));
    });
    closer.current = close;
    return () => close();
  }, [cluster, prefix, paused, depth]);

  const ranked = useMemo(() => {
    return Object.entries(counts).sort((a, b) => b[1] - a[1]);
  }, [counts]);
  const max = ranked[0]?.[1] ?? 1;

  // Drag handler for the key-column resizer.
  useEffect(() => {
    const onMove = (e: MouseEvent) => {
      if (!dragRef.current) return;
      const dx = e.clientX - dragRef.current.startX;
      setKeyColW(Math.max(160, Math.min(960, dragRef.current.startW + dx)));
    };
    const onUp = () => { dragRef.current = null; document.body.style.cursor = ""; };
    window.addEventListener("mousemove", onMove);
    window.addEventListener("mouseup", onUp);
    return () => {
      window.removeEventListener("mousemove", onMove);
      window.removeEventListener("mouseup", onUp);
    };
  }, []);

  if (!cluster) return <div className="panel p-10 text-center muted">Pick a cluster first.</div>;

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight flex items-center gap-2">
          <Activity className="w-5 h-5 text-accent-500" /> Write heatmap
        </h1>
        <p className="muted mt-1">Where in the keyspace is your cluster actually being written? Aggregates live events per prefix bucket.</p>
      </div>

      <details className="panel p-3 text-xs muted">
        <summary className="cursor-pointer text-sm" style={{ color: "rgb(var(--fg))" }}>
          Why no <em>read</em> heatmap?
        </summary>
        <div className="mt-2 space-y-2 max-w-[920px]">
          <p>
            etcd doesn't broadcast reads. The Watch API only fires on PUT/DELETE — that's what
            powers the bars above. There's no equivalent "ReadStream" you can subscribe to.
          </p>
          <p>
            For per-key reads, the only options are:
          </p>
          <ul className="list-disc pl-5 space-y-0.5">
            <li>
              Turn on etcd's <span className="font-mono">--audit-policy-file</span> with a level
              that records Range requests. Rare in production K8s — audit cardinality explodes
              at K8s-write-rate, ~3000 events/s.
            </li>
            <li>
              Run a gRPC proxy in front of etcd that records every Range. Adds latency on every
              read for every controller, kubelet, and operator in the cluster. Not viable here.
            </li>
          </ul>
          <p>
            What we <strong>can</strong> show is <em>aggregate</em> read rate from etcd's metrics
            endpoint: <span className="font-mono">grpc_server_handled_total{`{grpc_method="Range"}`}</span>
            grows at one tick per Range RPC. Open the Metrics page and look at the
            proposals/CPU charts — Range rate isn't there yet, but the data is one query away
            if you want it (open an issue).
          </p>
        </div>
      </details>

      <div className="panel p-3 flex items-center gap-2 flex-wrap">
        <input
          value={prefix}
          onChange={(e) => setPrefix(e.target.value)}
          placeholder="prefix to watch"
          className="flex-1 min-w-[200px] h-9 px-3 rounded-lg text-sm font-mono"
          style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
        />
        <label className="flex items-center gap-2 text-sm muted">
          bucket depth
          <input
            type="number"
            min={1}
            max={6}
            value={depth}
            onChange={(e) => setDepth(Math.max(1, Math.min(6, Number(e.target.value) || 1)))}
            className="h-9 w-16 px-2 rounded-lg text-sm"
            style={{ background: "rgb(var(--panel-2))", border: "1px solid rgb(var(--line))" }}
          />
        </label>
        <button onClick={() => setPaused((p) => !p)} className="btn">
          {paused ? <Play className="w-4 h-4" /> : <Pause className="w-4 h-4" />}
          {paused ? "Resume" : "Pause"}
        </button>
        <button onClick={() => setCounts({})} className="btn">
          <Trash2 className="w-4 h-4" /> Reset
        </button>
      </div>

      <div className="panel p-4">
        {ranked.length === 0 ? (
          <div className="py-12 text-center text-sm">
            <Activity className={cn("w-8 h-8 mx-auto mb-2 muted", !paused && "animate-pulse")} />
            <div className="font-medium">{paused ? "Paused" : "Waiting for events"}</div>
            <div className="muted mt-1 max-w-md mx-auto">
              {paused
                ? "Click Resume to start sampling again."
                : "PUTs and DELETEs land here as they happen. If nothing arrives for a while, the cluster is genuinely idle under this prefix."}
            </div>
          </div>
        ) : (
          <ul className="space-y-1.5">
            {ranked.map(([bucket, n]) => {
              const w = Math.max(2, Math.round((n / max) * 100));
              return (
                <li key={bucket} className="flex items-center gap-3">
                  <span
                    className="font-mono text-xs truncate shrink-0"
                    style={{ width: keyColW }}
                    title={bucket}
                  >
                    {bucket}
                  </span>
                  <span
                    role="separator"
                    aria-orientation="vertical"
                    aria-label="Resize key column"
                    aria-valuemin={160}
                    aria-valuemax={960}
                    aria-valuenow={keyColW}
                    tabIndex={0}
                    onKeyDown={(e) => {
                      if (e.key === "ArrowLeft") {
                        e.preventDefault();
                        setKeyColW((w) => Math.max(160, w - 16));
                      } else if (e.key === "ArrowRight") {
                        e.preventDefault();
                        setKeyColW((w) => Math.min(960, w + 16));
                      }
                    }}
                    onMouseDown={(e) => {
                      dragRef.current = { startX: e.clientX, startW: keyColW };
                      document.body.style.cursor = "col-resize";
                    }}
                    className="shrink-0 muted hover:text-current cursor-col-resize px-0.5"
                    title="Drag, or focus + ← / → to resize"
                  >
                    <GripVertical className="w-3 h-3" />
                  </span>
                  <div
                    className="flex-1 h-3 rounded-md overflow-hidden"
                    style={{ background: "rgb(var(--panel-2))" }}
                  >
                    <div
                      className={cn("h-full transition-all", "bg-accent-500")}
                      style={{ width: `${w}%`, opacity: 0.4 + (n / max) * 0.6 }}
                    />
                  </div>
                  <span className="font-mono text-xs muted w-12 text-right shrink-0">{n}</span>
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </div>
  );
}
