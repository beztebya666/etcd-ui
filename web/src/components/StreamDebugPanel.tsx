// WS / SSE debug panel for Settings → "Live transport".
//
// Surfaces the rolling buffer maintained by lib/streamStats:
//   - aggregates (counts at a glance)
//   - last N events as a tail (for ticket attachments)
//   - copy-to-clipboard JSON dump
//   - clear button
//
// Collapsed by default — the typical user never opens it. When something
// breaks ("WS keeps dropping every 30s through corporate proxy"), the
// operator drops the tail into the bug report and we're off to the races.

import { useEffect, useMemo, useState } from "react";
import { aggregates, clear, snapshot } from "../lib/streamStats";
import { Activity, Copy, Trash2, ChevronDown } from "lucide-react";
import { toast } from "./Toast";
import { copyToClipboard } from "../lib/clipboard";
import { cn } from "../lib/cn";

export function StreamDebugPanel() {
  const [open, setOpen] = useState(false);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    if (!open) return;
    // Re-render every 2s while open so the panel reflects live updates
    // without subscribing to the bus. Aggregates are cheap to recompute.
    const t = window.setInterval(() => setTick((n) => n + 1), 2000);
    return () => window.clearInterval(t);
  }, [open]);

  const agg = useMemo(aggregates, [tick, open]);
  const events = useMemo(() => (open ? snapshot().slice().reverse() : []), [tick, open]);

  const copy = async () => {
    const dump = {
      generatedAt: new Date().toISOString(),
      userAgent: navigator.userAgent,
      url: window.location.href,
      aggregates: aggregates(),
      events: snapshot(),
    };
    if (await copyToClipboard(JSON.stringify(dump, null, 2))) {
      toast.success("Diagnostic dump copied to clipboard");
    } else {
      toast.error("Clipboard not available — open devtools and inspect localStorage");
    }
  };

  return (
    <section className="panel p-5">
      <button
        onClick={() => setOpen((v) => !v)}
        className="flex items-center gap-2 w-full text-left"
        aria-expanded={open}
      >
        <Activity className="w-4 h-4 text-accent-500" />
        <div className="text-sm font-medium">Live transport</div>
        <Stat label="opens" value={agg.opens} />
        <Stat label="closes" value={agg.closes} />
        <Stat label="reconnects" value={agg.reconnects} />
        <Stat label="SSE fallback" value={agg.fallbacks} warn={agg.fallbacks > 0} />
        <ChevronDown
          className={cn("w-4 h-4 muted ml-auto transition-transform", open && "rotate-180")}
        />
      </button>

      {open && (
        <>
          <p className="muted text-xs mt-3 max-w-2xl">
            Rolling log of every WS/SSE lifecycle event in this tab. Lives in
            <code className="kbd mx-1">localStorage</code>, capped at 200 entries.
            Attach the dump to bug reports when streams keep flapping.
          </p>

          <div className="mt-3 flex items-center gap-2">
            <button onClick={copy} className="btn">
              <Copy className="w-4 h-4" /> Copy diagnostic JSON
            </button>
            <button
              onClick={() => {
                clear();
                setTick((n) => n + 1);
              }}
              className="btn btn-ghost text-danger"
            >
              <Trash2 className="w-4 h-4" /> Clear
            </button>
            {agg.oldest && (
              <span className="text-xs muted ml-auto">
                window starts {new Date(agg.oldest).toLocaleString()}
              </span>
            )}
          </div>

          <div
            className="mt-3 panel-2 rounded-lg max-h-[260px] overflow-auto font-mono text-[11px]"
            style={{ border: "1px solid rgb(var(--line))" }}
          >
            {events.length === 0 ? (
              <div className="p-4 muted text-center">No events yet — open a page with a live stream (Watch, Audit, Dashboard alerts).</div>
            ) : (
              <ul className="divide-y" style={{ borderColor: "rgb(var(--line))" }}>
                {events.map((e, i) => (
                  <li key={i} className="px-3 py-1.5 grid grid-cols-[64px_64px_70px_1fr] gap-2">
                    <span className="muted">{new Date(e.t).toLocaleTimeString()}</span>
                    <KindBadge kind={e.kind} />
                    <span className="muted">{e.transport ?? "—"}</span>
                    <span className="truncate">
                      {e.reason ?? ""}
                      {e.backoffMs ? ` · backoff ${e.backoffMs}ms` : ""}
                    </span>
                  </li>
                ))}
              </ul>
            )}
          </div>
        </>
      )}
    </section>
  );
}

function Stat({ label, value, warn }: { label: string; value: number; warn?: boolean }) {
  return (
    <span
      className={cn(
        "ml-3 inline-flex items-center gap-1 text-xs font-mono",
        warn && value > 0 ? "text-warn" : "muted",
      )}
    >
      <span style={{ color: warn && value > 0 ? "rgb(var(--warn, 234 179 8))" : "rgb(var(--fg))" }}>
        {value}
      </span>
      <span className="muted">{label}</span>
    </span>
  );
}

function KindBadge({ kind }: { kind: string }) {
  const tone =
    kind === "open"
      ? "text-accent-500"
      : kind === "close"
        ? "text-danger"
        : kind === "transport"
          ? "text-warn"
          : "muted";
  return <span className={cn("font-medium", tone)}>{kind}</span>;
}
