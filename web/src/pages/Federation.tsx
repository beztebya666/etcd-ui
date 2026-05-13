// Federation hub view. Lists every remote etcd-ui peer this hub is
// configured to aggregate, with per-peer health and a compact cluster
// roster. The endpoint is `null`-on-404 when ETCD_UI_PEERS isn't set, in
// which case we render an explainer empty state.

import { useQuery } from "@tanstack/react-query";
import { api } from "../lib/api";
import {
  Globe,
  ServerCrash,
  CheckCircle2,
  AlertTriangle,
  Inbox,
  Shield,
  ExternalLink,
} from "lucide-react";
import { cn } from "../lib/cn";
import { parsePrincipal } from "./Permissions";

type Health = "healthy" | "degraded" | "down" | "empty";

const TONE: Record<Health, { color: string; label: string; Icon: any }> = {
  healthy:  { color: "text-accent-500", label: "healthy",  Icon: CheckCircle2 },
  degraded: { color: "text-warn",       label: "degraded", Icon: AlertTriangle },
  down:     { color: "text-danger",     label: "down",     Icon: ServerCrash },
  empty:    { color: "muted",           label: "empty",    Icon: Inbox },
};

type ACLRule = { user: string; cluster: string; prefix?: string; access: "read" | "write" | "admin" };

export function FederationPage() {
  const peersQ = useQuery({
    queryKey: ["federation-peers"],
    queryFn: api.federationPeers,
    refetchInterval: 10_000,
    // 404 when hub isn't configured — render the empty state instead of
    // bouncing to /login.
    retry: false,
  });
  // ACL rules so we can show per-peer policy attribution inline. Tolerate
  // the no-ACL case (returns `{loaded:false}`) by treating rules as empty.
  const aclQ = useQuery({
    queryKey: ["acl"],
    queryFn: api.acl,
    retry: false,
  });
  const rules: ACLRule[] = aclQ.data?.rules ?? [];

  if (peersQ.isError) {
    return (
      <div className="space-y-4">
        <Header />
        <div className="panel p-12 text-center">
          <Globe className="w-10 h-10 mx-auto mb-3 muted" />
          <div className="font-medium">Federation hub not configured</div>
          <p className="muted text-sm mt-2 max-w-md mx-auto">
            Set <code className="kbd">ETCD_UI_PEERS=https://peer1,…</code> to
            aggregate clusters from remote etcd-ui instances. See{" "}
            <a className="underline" href="/docs/CONNECT.md">docs/CONNECT.md</a>.
          </p>
        </div>
      </div>
    );
  }

  const peers = peersQ.data ?? [];
  const counts = {
    healthy:  peers.filter((p) => p.health === "healthy").length,
    degraded: peers.filter((p) => p.health === "degraded").length,
    down:     peers.filter((p) => p.health === "down").length,
    empty:    peers.filter((p) => p.health === "empty").length,
  };

  return (
    <div className="space-y-5">
      <Header />
      <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
        <SummaryCard kind="healthy"  count={counts.healthy} />
        <SummaryCard kind="degraded" count={counts.degraded} />
        <SummaryCard kind="down"     count={counts.down} />
        <SummaryCard kind="empty"    count={counts.empty} />
      </div>

      {peers.length === 0 ? (
        <div className="panel p-12 text-center text-sm muted">
          No peers reachable yet — first poll in progress.
        </div>
      ) : (
        <ul className="grid grid-cols-1 lg:grid-cols-2 gap-3">
          {peers.map((p) => (
            <PeerCard key={p.id} peer={p} rules={rules} aclLoaded={!!aclQ.data?.loaded} />
          ))}
        </ul>
      )}
    </div>
  );
}

function Header() {
  return (
    <div>
      <h1 className="text-2xl font-semibold tracking-tight flex items-center gap-2">
        <Globe className="w-5 h-5 text-accent-500" /> Federation
      </h1>
      <p className="muted mt-1">
        Remote etcd-ui peers this hub aggregates. Cluster IDs are
        namespaced <code className="kbd">peer/cluster</code> elsewhere in the UI.
      </p>
    </div>
  );
}

function SummaryCard({ kind, count }: { kind: Health; count: number }) {
  const t = TONE[kind];
  return (
    <div className="panel p-3 flex items-center gap-2">
      <div
        className={cn(
          "w-8 h-8 rounded-md flex items-center justify-center shrink-0",
          kind === "healthy"  && "bg-accent/15",
          kind === "degraded" && "bg-warn/15",
          kind === "down"     && "bg-danger/15",
          kind === "empty"    && "bg-white/[0.04]",
        )}
      >
        <t.Icon className={cn("w-4 h-4", t.color)} />
      </div>
      <div className="min-w-0">
        <div className="text-lg font-semibold leading-none tabular-nums">{count}</div>
        <div className="text-xs muted mt-0.5">{t.label}</div>
      </div>
    </div>
  );
}

function PeerCard({
  peer,
  rules,
  aclLoaded,
}: {
  peer: NonNullable<ReturnType<typeof api.federationPeers> extends Promise<infer T> ? T : never>[number];
  rules: ACLRule[];
  aclLoaded: boolean;
}) {
  const t = TONE[peer.health];
  // Rules where the principal is either this peer (`system:peer:<id>`) or
  // any user attributed via this peer (`peer:<id>/<user>`). This is what
  // determines what the peer is allowed to do at THIS hub — independent
  // of whatever the remote hub's own ACL says.
  const peerRules = rules.filter((r) => {
    const p = parsePrincipal(r.user);
    return p.kind !== "local" && p.peer === peer.id;
  });
  return (
    <li className="panel p-4">
      <div className="flex items-start gap-2">
        <t.Icon className={cn("w-4 h-4 mt-0.5 shrink-0", t.color)} />
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 flex-wrap">
            <span className="font-medium truncate">{peer.id}</span>
            <span className={cn("pill !text-[10px]", t.color)}>{t.label}</span>
          </div>
          <div className="text-xs muted font-mono truncate" title={peer.url}>
            {peer.url}
          </div>
        </div>
      </div>

      {peer.error && (
        <div className="mt-3 text-xs text-danger font-mono break-all p-2 rounded-md"
             style={{ background: "rgb(var(--panel-2))" }}>
          {peer.error}
        </div>
      )}

      <div className="mt-3 flex items-center gap-3 text-xs">
        <Counter label="clusters" value={peer.clustersTotal} />
        <Counter label="healthy"  value={peer.clustersHealthy} tone="accent" />
        <Counter label="unhealthy" value={peer.clustersUnhealthy} tone="danger" />
        <span className="ml-auto muted">
          checked {new Date(peer.lastChecked).toLocaleTimeString()}
        </span>
      </div>

      {peer.clusters && peer.clusters.length > 0 && (
        <details className="mt-3">
          <summary className="cursor-pointer text-xs muted">
            {peer.clusters.length} cluster{peer.clusters.length === 1 ? "" : "s"}
          </summary>
          <ul className="mt-2 space-y-1">
            {peer.clusters.map((c) => (
              <li
                key={c.id}
                className="grid grid-cols-[auto_1fr_auto] gap-2 items-center text-xs font-mono row-hover px-2 py-1 rounded-md"
              >
                <span
                  className={cn(
                    "w-1.5 h-1.5 rounded-full",
                    c.healthy ? "bg-accent-500" : "bg-danger",
                  )}
                />
                <span className="truncate" title={c.id}>{c.name}</span>
                <span className="muted">{c.memberCount} mem</span>
              </li>
            ))}
          </ul>
        </details>
      )}

      <div className="mt-3 pt-3" style={{ borderTop: "1px solid rgb(var(--line))" }}>
        <div className="flex items-center gap-2 text-xs">
          <Shield className="w-3.5 h-3.5 muted" />
          <span className="muted">policy at this hub:</span>
          {!aclLoaded ? (
            <span className="muted">no ACL loaded — peer has full access</span>
          ) : peerRules.length === 0 ? (
            <span className="text-warn">no rules — peer is denied by default</span>
          ) : (
            <span>
              <span className="font-semibold tabular-nums">{peerRules.length}</span>{" "}
              <span className="muted">rule{peerRules.length === 1 ? "" : "s"}</span>
            </span>
          )}
          {aclLoaded && (
            <a
              href={`/permissions?peer=${encodeURIComponent(peer.id)}`}
              className="ml-auto inline-flex items-center gap-1 text-xs text-accent-500 hover:underline"
              title="Open Permissions filtered to this peer"
            >
              manage <ExternalLink className="w-3 h-3" />
            </a>
          )}
        </div>
        {peerRules.length > 0 && (
          <ul className="mt-2 space-y-1">
            {peerRules.slice(0, 6).map((r, i) => (
              <li
                key={i}
                className="grid grid-cols-[auto_1fr_auto] gap-2 items-center text-[11px] font-mono px-2 py-1 rounded-md"
                style={{ background: "rgb(var(--panel-2))" }}
              >
                <span className={cn(
                  "px-1.5 py-0.5 rounded text-[10px] font-medium",
                  r.access === "read"  && "bg-accent/15 text-accent-500",
                  r.access === "write" && "bg-warn/15 text-warn",
                  r.access === "admin" && "bg-danger/15 text-danger",
                )}>{r.access}</span>
                <span className="truncate" title={`${r.cluster}${r.prefix ? " " + r.prefix : ""}`}>
                  {r.cluster === "*" ? "any cluster" : r.cluster}
                  {r.prefix ? <span className="muted"> · {r.prefix}</span> : null}
                </span>
                <span className="muted truncate" title={r.user}>
                  {(() => {
                    const p = parsePrincipal(r.user);
                    return p.kind === "peer-user" ? p.user : "peer";
                  })()}
                </span>
              </li>
            ))}
            {peerRules.length > 6 && (
              <li className="text-[11px] muted px-2">
                +{peerRules.length - 6} more — click "manage" to view all
              </li>
            )}
          </ul>
        )}
      </div>
    </li>
  );
}

function Counter({
  label,
  value,
  tone,
}: {
  label: string;
  value: number;
  tone?: "accent" | "danger";
}) {
  return (
    <span className="inline-flex items-center gap-1">
      <span
        className={cn(
          "font-semibold tabular-nums",
          tone === "accent" && value > 0 && "text-accent-500",
          tone === "danger" && value > 0 && "text-danger",
        )}
      >
        {value}
      </span>
      <span className="muted">{label}</span>
    </span>
  );
}
