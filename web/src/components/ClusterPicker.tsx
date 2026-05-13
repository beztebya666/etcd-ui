import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import { useStore } from "../lib/store";
import { ChevronsUpDown, Check, Pin, PinOff } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { cn } from "../lib/cn";

export function ClusterPicker() {
  const { data, isLoading } = useQuery({
    queryKey: ["clusters"],
    queryFn: api.clusters,
    refetchInterval: 5_000,
  });
  const { selectedCluster, setSelectedCluster, pinnedClusters, togglePin, reorderPinned } = useStore();
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);

  // Sort: pinned first (in pin order), then healthy, then alphabetical.
  const sorted = useMemo(() => {
    if (!data) return [];
    const pinned = pinnedClusters
      .map((id) => data.find((c) => c.id === id))
      .filter((x): x is NonNullable<typeof x> => !!x);
    const rest = data.filter((c) => !pinnedClusters.includes(c.id));
    rest.sort((a, b) => (a.name || a.id).localeCompare(b.name || b.id));
    return [...pinned, ...rest];
  }, [data, pinnedClusters]);

  // auto-select first cluster on first load
  useEffect(() => {
    if (!selectedCluster && sorted[0]) setSelectedCluster(sorted[0].id);
  }, [sorted, selectedCluster, setSelectedCluster]);

  // dismiss on outside click / escape
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (rootRef.current && !rootRef.current.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    window.addEventListener("mousedown", onDown);
    window.addEventListener("keydown", onKey);
    return () => {
      window.removeEventListener("mousedown", onDown);
      window.removeEventListener("keydown", onKey);
    };
  }, [open]);

  const current = sorted.find((c) => c.id === selectedCluster) ?? sorted[0];

  return (
    <div className="relative" ref={rootRef}>
      <button
        onClick={() => setOpen((v) => !v)}
        className="surface flex items-center gap-2.5 h-9 px-3 rounded-lg text-sm min-w-[240px] max-w-[320px] cursor-pointer"
      >
        <StatusDot healthy={current?.healthy} />
        <span className="flex-1 text-left truncate font-medium">
          {current?.name ?? (isLoading ? "loading…" : "no clusters")}
        </span>
        {current && <span className="muted text-[11px] shrink-0">{current.source}</span>}
        <ChevronsUpDown className="w-3.5 h-3.5 muted shrink-0" />
      </button>

      {open && (
        <div
          className="absolute right-0 mt-2 w-[360px] rounded-xl p-1 z-30 shadow-elev animate-in"
          style={{ background: "rgb(var(--panel))", border: "1px solid rgb(var(--line))" }}
        >
          {sorted.length ? (
            sorted.map((c) => {
              const active = c.id === current?.id;
              const isPinned = pinnedClusters.includes(c.id);
              const pinIndex = pinnedClusters.indexOf(c.id);
              return (
                <div
                  key={c.id}
                  draggable={isPinned}
                  onDragStart={(e) => {
                    if (!isPinned) return;
                    e.dataTransfer.setData("text/etcd-ui-pin-index", String(pinIndex));
                    e.dataTransfer.effectAllowed = "move";
                  }}
                  onDragOver={(e) => {
                    if (isPinned) e.preventDefault();
                  }}
                  onDrop={(e) => {
                    const from = Number(e.dataTransfer.getData("text/etcd-ui-pin-index"));
                    if (isPinned && Number.isFinite(from) && from !== pinIndex) {
                      reorderPinned(from, pinIndex);
                    }
                  }}
                  className={cn(
                    "group flex items-start gap-2.5 px-2.5 py-2 rounded-lg text-sm transition",
                    active ? "soft-active" : "soft-hover",
                    isPinned && "cursor-grab active:cursor-grabbing",
                  )}
                  title={isPinned ? "Drag to reorder" : undefined}
                >
                  <StatusDot healthy={c.healthy} className="mt-1" />
                  <button
                    onClick={() => {
                      setSelectedCluster(c.id);
                      setOpen(false);
                    }}
                    className="flex-1 min-w-0 text-left"
                  >
                    <div className="flex items-center gap-2">
                      <span className="font-medium truncate">{c.name}</span>
                      <span className="pill shrink-0">{c.source}</span>
                    </div>
                    <div className="text-xs muted truncate mt-0.5">{c.endpoints.join(", ")}</div>
                  </button>
                  <button
                    onClick={(e) => {
                      e.stopPropagation();
                      togglePin(c.id);
                    }}
                    className={cn(
                      "p-1 rounded muted hover:text-current opacity-0 group-hover:opacity-100 transition",
                      isPinned && "opacity-100 text-accent-500",
                    )}
                    title={isPinned ? "Unpin" : "Pin"}
                  >
                    {isPinned ? <PinOff className="w-3.5 h-3.5" /> : <Pin className="w-3.5 h-3.5" />}
                  </button>
                  {active && <Check className="w-4 h-4 text-accent-500 mt-1 shrink-0" />}
                </div>
              );
            })
          ) : (
            <div className="px-3 py-4 text-sm muted">No clusters detected. Add one in Settings.</div>
          )}
        </div>
      )}
    </div>
  );
}

function StatusDot({ healthy, className }: { healthy?: boolean; className?: string }) {
  return (
    <span
      className={cn(
        "w-2 h-2 rounded-full shrink-0",
        healthy ? "bg-accent-500" : "bg-warn",
        healthy ? "animate-pulseDot" : "",
        className,
      )}
      aria-hidden
    />
  );
}
