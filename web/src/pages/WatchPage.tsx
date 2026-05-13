import { useEffect, useMemo, useRef, useState } from "react";
import { api, type WatchEvent } from "../lib/api";
import { useStore } from "../lib/store";
import { Pause, Play, Trash2, Radio } from "lucide-react";
import { cn } from "../lib/cn";
import { looksBinary } from "../components/CodeEditor";
import { Autocomplete } from "../components/Autocomplete";

type OpFilter = "all" | "put" | "delete";

export function WatchPage() {
  const cluster = useStore((s) => s.selectedCluster);
  const [prefix, setPrefix] = useState("/");
  const [paused, setPaused] = useState(false);
  const [events, setEvents] = useState<WatchEvent[]>([]);
  const [expanded, setExpanded] = useState<number | null>(null);
  const [opFilter, setOpFilter] = useState<OpFilter>("all");
  const closer = useRef<() => void>();

  useEffect(() => {
    if (!cluster || paused) return;
    closer.current?.();
    const close = api.watch(
      cluster,
      prefix === "/" ? "" : prefix,
      (e) => setEvents((prev) => [e, ...prev].slice(0, 500)),
    );
    closer.current = close;
    return () => close();
  }, [cluster, prefix, paused]);

  const filtered = useMemo(() => {
    if (opFilter === "all") return events;
    return events.filter((e) =>
      opFilter === "put" ? e.type === "PUT" : e.type === "DELETE",
    );
  }, [events, opFilter]);

  const counts = useMemo(() => {
    let put = 0, del = 0;
    for (const e of events) {
      if (e.type === "PUT") put++;
      else if (e.type === "DELETE") del++;
    }
    return { put, del };
  }, [events]);

  if (!cluster) {
    return <div className="panel p-10 text-center muted">Pick a cluster first.</div>;
  }

  return (
    <div className="space-y-4">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight flex items-center gap-2">
          <Radio className="w-5 h-5 text-accent-500 animate-pulseDot" />
          Live watch
        </h1>
        <p className="muted mt-1">Stream every PUT and DELETE happening under a prefix in real time.</p>
      </div>

      <div className="panel p-4 flex items-center gap-3 flex-wrap">
        <Autocomplete
          value={prefix}
          onChange={setPrefix}
          placeholder="prefix to watch, e.g. /registry/"
          ariaLabel="Watch prefix"
          monospace
          className="flex-1 min-w-[240px]"
          query={async (input, limit) => {
            if (!cluster) return [];
            const keys = await api.suggestKeys(cluster, input, limit);
            const seen = new Set<string>();
            const out: { value: string; hint?: string }[] = [];
            const cutAt = input.lastIndexOf("/") + 1;
            for (const k of keys) {
              const slash = k.indexOf("/", cutAt);
              const v = slash >= 0 ? k.slice(0, slash + 1) : k;
              if (seen.has(v)) continue;
              seen.add(v);
              out.push({ value: v, hint: slash >= 0 ? "folder" : "key" });
            }
            return out;
          }}
        />
        <button onClick={() => setPaused((p) => !p)} className="btn">
          {paused ? <Play className="w-4 h-4" /> : <Pause className="w-4 h-4" />}
          {paused ? "Resume" : "Pause"}
        </button>
        <button onClick={() => setEvents([])} className="btn">
          <Trash2 className="w-4 h-4" /> Clear
        </button>
      </div>

      <div className="flex items-center gap-1.5 text-xs flex-wrap">
        <span className="muted mr-1">filter:</span>
        <FilterPill active={opFilter === "all"} onClick={() => setOpFilter("all")}>
          all <span className="opacity-60">{events.length}</span>
        </FilterPill>
        <FilterPill
          active={opFilter === "put"}
          tone="accent"
          onClick={() => setOpFilter("put")}
        >
          PUT <span className="opacity-60">{counts.put}</span>
        </FilterPill>
        <FilterPill
          active={opFilter === "delete"}
          tone="danger"
          onClick={() => setOpFilter("delete")}
        >
          DELETE <span className="opacity-60">{counts.del}</span>
        </FilterPill>
        {paused && <span className="pill !text-warn !border-warn/40 ml-1">paused</span>}
      </div>

      <div className="panel max-h-[calc(100vh-320px)] overflow-y-auto">
        {filtered.length === 0 ? (
          <div className="p-8 text-center muted text-sm">
            {events.length === 0
              ? "Waiting for events…"
              : "No events match the current filter."}
          </div>
        ) : (
          <ul className="divide-y" style={{ borderColor: "rgb(var(--line))" }}>
            {filtered.map((e, i) => {
              const isOpen = expanded === i;
              const binary = e.value ? looksBinary(e.value) : false;
              return (
                <li key={i} className="row-hover">
                  <button
                    onClick={() => setExpanded(isOpen ? null : i)}
                    className="w-full grid grid-cols-[64px_84px_1fr] items-center gap-3 px-3 py-2 text-sm text-left"
                  >
                    <span
                      className={cn(
                        "pill justify-center !rounded-md !text-[10px] uppercase tracking-wider",
                        e.type === "PUT" ? "text-accent-500 border-accent-500/40" : "text-danger border-danger/40",
                      )}
                    >
                      {e.type}
                    </span>
                    <span className="text-xs muted font-mono truncate">r{e.revision}</span>
                    <div className="min-w-0">
                      <div className="font-mono text-[13px] truncate" title={e.key}>
                        {e.key}
                      </div>
                      {e.preview ? (
                        <PreviewLine p={e.preview} fallbackSize={e.value?.length} />
                      ) : e.value && !binary ? (
                        <div className="text-[11px] muted mt-0.5 font-mono truncate">
                          {e.value.replace(/[\n\r\t]+/g, " ").slice(0, 200)}
                        </div>
                      ) : e.value ? (
                        <div className="text-[11px] muted mt-0.5">
                          binary · {e.value.length} B
                        </div>
                      ) : null}
                    </div>
                  </button>
                  {isOpen && e.value && (
                    <div className="px-3 pb-3">
                      <div className="text-[10px] uppercase tracking-wider muted mb-1">value</div>
                      <pre className="panel-2 p-3 rounded-md text-[12px] font-mono whitespace-pre-wrap break-all max-h-72 overflow-auto">
                        {binary ? hexPreview(e.value, 1024) : e.value}
                      </pre>
                      {e.prevValue && (
                        <>
                          <div className="text-[10px] uppercase tracking-wider muted mt-3 mb-1">previous value</div>
                          <pre className="panel-2 p-3 rounded-md text-[12px] font-mono whitespace-pre-wrap break-all max-h-48 overflow-auto">
                            {looksBinary(e.prevValue)
                              ? hexPreview(e.prevValue, 1024)
                              : e.prevValue}
                          </pre>
                        </>
                      )}
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </div>
    </div>
  );
}

function FilterPill({
  active,
  tone,
  onClick,
  children,
}: {
  active: boolean;
  tone?: "accent" | "danger";
  onClick: () => void;
  children: React.ReactNode;
}) {
  return (
    <button
      onClick={onClick}
      aria-pressed={active}
      className={cn(
        "pill cursor-pointer",
        active && tone === "accent" && "!text-accent-500 !border-accent-500/50",
        active && tone === "danger" && "!text-danger !border-danger/50",
        active && !tone && "!text-current !border-current/30",
      )}
    >
      {children}
    </button>
  );
}

function PreviewLine({
  p,
  fallbackSize,
}: {
  p: NonNullable<WatchEvent["preview"]>;
  fallbackSize?: number;
}) {
  return (
    <div className="text-[11px] mt-0.5 flex items-center gap-1.5 flex-wrap font-mono">
      <span className="pill !text-[9px] !text-accent-500 !border-accent-500/40 uppercase tracking-wider">
        {p.format}
      </span>
      {(p.apiVersion || p.kind) && (
        <span style={{ color: "rgb(var(--fg))" }}>
          {p.apiVersion}
          {p.apiVersion && p.kind && <span className="muted"> · </span>}
          {p.kind}
        </span>
      )}
      {(p.namespace || p.name) && (
        <span className="muted">
          {p.namespace && <>{p.namespace} <span className="opacity-40">/</span> </>}
          <span style={{ color: "rgb(var(--fg))" }}>{p.name}</span>
        </span>
      )}
      {fallbackSize != null && (
        <span className="muted ml-auto opacity-60">{fallbackSize} B</span>
      )}
    </div>
  );
}

function hexPreview(s: string, max: number): string {
  const n = Math.min(s.length, max);
  const out: string[] = [];
  for (let i = 0; i < n; i += 16) {
    const slice = s.slice(i, i + 16);
    const hex: string[] = [];
    const ascii: string[] = [];
    for (let j = 0; j < slice.length; j++) {
      const c = slice.charCodeAt(j) & 0xff;
      hex.push(c.toString(16).padStart(2, "0"));
      ascii.push(c >= 32 && c < 127 ? slice[j] : ".");
    }
    out.push(
      `${i.toString(16).padStart(8, "0")}  ${hex.join(" ").padEnd(47, " ")}  |${ascii.join("")}|`,
    );
  }
  if (s.length > max) out.push(`… (${s.length - max} more bytes)`);
  return out.join("\n");
}
