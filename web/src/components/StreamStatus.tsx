// Tiny pill in the top-right corner of Shell showing the worst current
// streaming transport, plus a countdown when any stream is in backoff:
//
//   no streams         → hidden
//   all WS             → green   "WS · 2"
//   any SSE only       → amber   "SSE · 1 (proxy)"
//   connecting / open  → spinner "connecting · 1"
//   reconnect in N s   → clock   "reconnect in 4s"   (countdown)
//
// Tooltip lists count per state + the soonest reconnect timestamp.

import { useEffect, useState } from "react";
import { subscribeStreamStatus, type StreamStatusEntry } from "../lib/stream";
import { Radio, RotateCw, AlertTriangle, Clock } from "lucide-react";

export function StreamStatus() {
  const [snap, setSnap] = useState<Map<string, StreamStatusEntry>>(new Map());
  const [now, setNow] = useState(Date.now());

  useEffect(() => subscribeStreamStatus(setSnap), []);

  // 1Hz ticker just for the countdown text. Only this component re-renders.
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(t);
  }, []);

  if (snap.size === 0) return null;

  let ws = 0,
    sse = 0,
    connecting = 0;
  let soonestReconnect = 0;
  for (const v of snap.values()) {
    if (v.transport === "ws") ws++;
    else if (v.transport === "sse") sse++;
    else if (v.transport === "connecting") {
      connecting++;
      if (v.nextAttemptAt > 0 && (soonestReconnect === 0 || v.nextAttemptAt < soonestReconnect)) {
        soonestReconnect = v.nextAttemptAt;
      }
    }
  }

  let label: string;
  let tone: "ok" | "warn" | "neutral";
  let icon: React.ReactNode;

  if (connecting > 0) {
    if (soonestReconnect > 0) {
      const secs = Math.max(0, Math.ceil((soonestReconnect - now) / 1000));
      label = secs > 0 ? `reconnect in ${secs}s` : "reconnecting…";
      icon = <Clock className="w-3.5 h-3.5" />;
    } else {
      label = `connecting · ${connecting}`;
      icon = <RotateCw className="w-3.5 h-3.5 animate-spin" />;
    }
    tone = "neutral";
  } else if (sse > 0 && ws === 0) {
    label = `SSE · ${sse}`;
    tone = "warn";
    icon = <AlertTriangle className="w-3.5 h-3.5" />;
  } else {
    label = `WS · ${ws}${sse ? ` (+${sse} SSE)` : ""}`;
    tone = "ok";
    icon = <Radio className="w-3.5 h-3.5" />;
  }

  const toneClass =
    tone === "ok"
      ? "text-accent-500 bg-accent/10 border-accent-500/30"
      : tone === "warn"
        ? "text-warn bg-warn/10 border-warn/30"
        : "muted bg-transparent border-white/10";

  // Native title tooltips render in a proportional font — column-aligning
  // with spaces results in wonky gaps. One space per row, browser wraps.
  const detail =
    `WS streams: ${ws}\n` +
    `SSE fallback: ${sse}\n` +
    `Connecting / reconnecting: ${connecting}\n` +
    (soonestReconnect > 0
      ? `Next attempt at: ${new Date(soonestReconnect).toLocaleTimeString()}\n`
      : "") +
    "\nWS is preferred — SSE means a proxy in the path doesn't allow upgrades.\n" +
    "Reconnect uses exponential backoff capped at 30s.";

  return (
    <span
      className={
        // Match ClusterPicker proportions (h-9, px-3, rounded-lg) so the
        // topbar reads as a single row of equal-height controls.
        "inline-flex items-center gap-2 h-9 px-3 rounded-lg text-xs font-mono border select-none " +
        toneClass
      }
      title={detail}
      aria-label={`Stream transport: ${label}`}
    >
      {icon}
      <span>{label}</span>
    </span>
  );
}
