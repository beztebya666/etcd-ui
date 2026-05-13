// Subscribes to /api/alerts/stream (SSE) and pops a Toast + (optional)
// browser Notification on every event. Mounted once at the App root.
//
// User control:
//  - Toast appears unconditionally — it's in-app, can't spam.
//  - Browser Notifications require explicit permission grant. We surface a
//    "Enable desktop notifications" button on first event if perm is "default".

import { useEffect, useRef, useState } from "react";
import { toast } from "./Toast";
import { openStream } from "../lib/stream";

type AlertEvent = {
  time: string;
  kind: "unhealthy" | "recovered" | "alarm" | "leader-flip";
  cluster: string;
  detail?: string;
};

const ICON_FOR_KIND: Record<AlertEvent["kind"], string> = {
  unhealthy: "⚠️",
  recovered: "✅",
  alarm: "🚨",
  "leader-flip": "🔁",
};

export function AlertWatcher() {
  const streamRef = useRef<{ close: () => void } | null>(null);
  const [permission, setPermission] = useState<NotificationPermission>(
    typeof Notification !== "undefined" ? Notification.permission : "default",
  );

  useEffect(() => {
    const s = openStream("/api/alerts/stream", {
      onMessage: (raw) => {
        try {
          const data: AlertEvent = JSON.parse(raw);
          handle(data, permission);
        } catch {
          /* ignore malformed frames */
        }
      },
      onError: (err) => {
        if (import.meta.env.DEV) console.debug("alerts stream error", err);
      },
    });
    streamRef.current = s;
    return () => {
      s.close();
      streamRef.current = null;
    };
  }, [permission]);

  // First-event nudge: if the browser can show notifications and we haven't
  // asked yet, surface an opt-in toast. Stays out of the way otherwise.
  if (typeof Notification === "undefined" || permission !== "default") return null;
  return (
    <button
      onClick={async () => {
        const p = await Notification.requestPermission();
        setPermission(p);
      }}
      className="fixed bottom-4 right-4 z-40 pill"
      title="Browser will show a desktop alert when a cluster goes unhealthy"
    >
      🔔 Enable desktop alerts
    </button>
  );
}

function handle(ev: AlertEvent, permission: NotificationPermission) {
  const icon = ICON_FOR_KIND[ev.kind] ?? "ℹ️";
  const headline = `${icon}  ${ev.cluster} — ${ev.kind}`;
  const body = ev.detail ?? "";

  switch (ev.kind) {
    case "unhealthy":
    case "alarm":
      toast.error(`${headline}\n${body}`);
      break;
    case "recovered":
      toast.success(`${headline}\n${body}`);
      break;
    default:
      toast.info(`${headline}\n${body}`);
  }

  if (permission === "granted") {
    try {
      new Notification(`etcd-ui — ${ev.cluster}`, {
        body: `${ev.kind}: ${body}`,
        tag: `${ev.cluster}:${ev.kind}`, // collapses repeats
        silent: ev.kind === "recovered",
      });
    } catch {
      /* some browsers throw if called from a non-secure context */
    }
  }
}
