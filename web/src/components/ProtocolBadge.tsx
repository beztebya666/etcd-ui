// Tiny pill that surfaces the API surface a cluster speaks. etcd v3 is gRPC
// over HTTP/2 (modern, default); etcd v2 is REST/JSON over HTTP/1.1
// (legacy — only Kubernetes < 1.6 / CoreOS-era stacks). The badge lets
// operators tell at a glance which protocol the etcd-ui gateway is using
// to talk to that cluster — relevant because most v3-only features
// (txn, lease attach, watch fragments, snapshot v3) are unavailable on v2.

import { cn } from "../lib/cn";

export function ProtocolBadge({
  api,
  serverVersion,
  className,
}: {
  api?: "v2" | "v3";
  serverVersion?: string;
  className?: string;
}) {
  if (!api) return null;
  const isV3 = api === "v3";
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 text-[10px] uppercase tracking-wider font-semibold rounded px-1.5 py-0.5",
        isV3
          ? "bg-warn/15 text-warn border border-warn/40"
          : "bg-accent/15 text-accent-500 border border-accent-500/40",
        className,
      )}
      title={
        isV3
          ? `etcd v3 — gRPC over HTTP/2${serverVersion ? ` (server ${serverVersion})` : ""}`
          : `etcd v2 — REST/JSON over HTTP/1.1${serverVersion ? ` (server ${serverVersion})` : ""}`
      }
    >
      {isV3 ? "gRPC" : "HTTP"}
      <span className="opacity-60 normal-case font-mono">{api}</span>
    </span>
  );
}
